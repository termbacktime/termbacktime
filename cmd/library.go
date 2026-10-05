package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func newList(o *options) *cobra.Command {
	var jsonOutput bool
	var title, after, before string
	var filters library.Filter
	var pinned, uploaded bool
	c := &cobra.Command{Use: "list", Short: "List recordings in your library", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		lib, err := o.library()
		if err != nil {
			return err
		}
		if err := filters.Validate(); err != nil {
			return err
		}
		if c.Flags().Changed("pinned") {
			filters.Pinned = &pinned
		}
		if c.Flags().Changed("uploaded") {
			filters.Uploaded = &uploaded
		}
		entries, warnings, err := lib.ListWithWarnings(c.Context())
		for _, warning := range warnings {
			fmt.Fprintln(c.ErrOrStderr(), "Warning:", warning)
		}
		if err != nil {
			return err
		}
		var lower, upper int64
		for _, v := range []struct {
			s string
			p *int64
		}{{after, &lower}, {before, &upper}} {
			if v.s != "" {
				t, err := time.Parse("2006-01-02", v.s)
				if err != nil {
					return fmt.Errorf("dates must use YYYY-MM-DD")
				}
				*v.p = t.Unix()
			}
		}
		filtered := entries[:0]
		for _, e := range entries {
			if filters.Match(e) && strings.Contains(strings.ToLower(e.Title), strings.ToLower(title)) && (lower == 0 || e.Started >= lower) && (upper == 0 || e.Started < upper+86400) {
				filtered = append(filtered, e)
			}
		}
		library.Sort(filtered, filters.SortBy, filters.Order)
		if jsonOutput {
			type row struct {
				library.Entry
				Pinned      *bool `json:"pinned"`
				UploadCount *int  `json:"receipt_count"`
			}
			rows := []row{}
			for _, entry := range filtered {
				r := row{Entry: entry}
				if entry.Enriched {
					r.Pinned = &r.Entry.Pinned
					r.UploadCount = &r.Entry.UploadCount
				}
				rows = append(rows, r)
			}
			return json.NewEncoder(c.OutOrStdout()).Encode(rows)
		}
		w := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tTITLE\tCREATED\tDURATION\tBYTES\tSTATUS\tPINNED\tUPLOADS\tLOCATION")
		for _, e := range filtered {
			pin, uploads := "unknown", "unknown"
			if e.Enriched {
				pin, uploads = fmt.Sprint(e.Pinned), fmt.Sprint(e.UploadCount)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n", e.ID[:12], strings.Map(func(r rune) rune {
				if r < 32 || r == 127 {
					return ' '
				}
				return r
			}, e.Title), time.Unix(e.Started, 0).Format(time.RFC3339), time.Duration(e.Duration)*time.Millisecond, e.Bytes, e.Status, pin, uploads, e.Path)
		}
		return w.Flush()
	}}
	c.Flags().StringVar(&filters.SortBy, "sort", "date", "sort by date, title, duration, or size")
	c.Flags().StringVar(&filters.Order, "order", "", "asc or desc (default: title ascending, others descending)")
	c.Flags().StringSliceVar(&filters.Statuses, "status", nil, "filter statuses: ready, recording, partial, missing, unsupported, invalid")
	c.Flags().BoolVar(&pinned, "pinned", false, "filter pinned recordings; =false selects unpinned")
	c.Flags().BoolVar(&uploaded, "uploaded", false, "filter recordings with saved upload receipts; =false selects those without")
	c.Flags().BoolVar(&jsonOutput, "json", false, "print machine-readable metadata")
	c.Flags().StringVar(&title, "title", "", "filter titles")
	c.Flags().StringVar(&after, "after", "", "created on or after YYYY-MM-DD (UTC)")
	c.Flags().StringVar(&before, "before", "", "created on or before YYYY-MM-DD (UTC)")
	return c
}

func newInfo(o *options) *cobra.Command {
	var share, quick bool
	c := &cobra.Command{Use: "info <id|path>", Short: "Inspect a recording", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		lib, err := o.library()
		if err != nil {
			return err
		}
		path, err := lib.Resolve(args[0])
		if err != nil {
			return err
		}
		if share {
			link, err := lib.ShareLink(path)
			if err != nil {
				return err
			}
			if link == "" {
				return fmt.Errorf("no saved share link")
			}
			fmt.Fprintln(c.OutOrStdout(), link)
			return nil
		}
		var s *recording.Summary
		if quick {
			s, err = recording.Preview(c.Context(), path)
		} else {
			s, err = recording.InspectContext(c.Context(), path)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(s)
	}}
	c.Flags().BoolVar(&quick, "quick", false, "preview metadata without verifying every event chunk")
	c.Flags().BoolVar(&share, "show-share-link", false, "print the private playback link including its key")
	return c
}

func newImport(o *options) *cobra.Command {
	return &cobra.Command{Use: "import <paths...>", Short: "Index existing recordings without moving them", Args: cobra.MinimumNArgs(1), RunE: func(c *cobra.Command, args []string) error {
		lib, err := o.library()
		if err != nil {
			return err
		}
		for _, p := range args {
			e, err := lib.Register(p)
			if err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), e.ID, e.Path)
		}
		return nil
	}}
}

func newRecover(o *options) *cobra.Command {
	var output string
	c := &cobra.Command{Use: "recover <id|path>", Short: "Recover an inactive journal into a new recording", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		lib, err := o.library()
		if err != nil {
			return err
		}
		source, err := lib.Resolve(args[0])
		if err != nil {
			return err
		}
		if !strings.HasSuffix(source, ".partial") {
			return fmt.Errorf("recovery requires a .partial journal")
		}
		if output == "" {
			output = filepath.Join(lib.Root, "recordings", "recovered-"+recording.NewID()+".tbt")
		}
		destination, err := lib.Output(output)
		if err != nil {
			return err
		}
		if err := recording.RecoverFile(source, destination); err != nil {
			return err
		}
		if _, err := lib.Register(destination); err != nil {
			return err
		}
		fmt.Fprintln(c.OutOrStdout(), destination)
		return nil
	}}
	c.Flags().StringVarP(&output, "output", "o", "", "recovered recording filename")
	return c
}
