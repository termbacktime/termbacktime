package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/storage"
)

func newStorage(o *options) *cobra.Command {
	root := &cobra.Command{Use: "storage", Short: "Inspect disk usage, preview cleanup, and pin recordings"}
	var usageJSON bool
	usage := &cobra.Command{Use: "usage", Short: "Report managed storage and indexed external file sizes", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		lib, err := o.library()
		if err != nil {
			return err
		}
		report, err := (storage.Store{Root: lib.Root}).Snapshot(c.Context())
		if err != nil {
			return err
		}
		if usageJSON {
			return json.NewEncoder(c.OutOrStdout()).Encode(report)
		}
		for _, kind := range []string{"recordings", "exports", "queue", "protected", "external"} {
			t := report.Totals[kind]
			fmt.Fprintf(c.OutOrStdout(), "%s: %d files, %d bytes\n", kind, t.Files, t.Bytes)
		}
		fmt.Fprintln(c.OutOrStdout(), "External sizes are from the library index. Cleanup previews are available with storage clean.")
		return nil
	}}
	usage.Flags().BoolVar(&usageJSON, "json", false, "print machine-readable usage without sharing keys")
	root.AddCommand(usage)
	var kind, age, size string
	var apply, yes, jsonOutput bool
	clean := &cobra.Command{Use: "clean", Short: "Preview eligible managed files; --apply explicitly enables deletion", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if yes && !apply {
			return fmt.Errorf("--yes requires --apply")
		}
		older, err := storage.ParseAge(age)
		if err != nil {
			return err
		}
		minimum, err := storage.ParseSize(size)
		if err != nil {
			return err
		}
		filter := storage.Filter{Kind: kind, OlderThan: older, MinSize: minimum}
		if err := filter.Validate(); err != nil {
			return err
		}
		lib, err := o.library()
		if err != nil {
			return err
		}
		store := storage.Store{Root: lib.Root}
		report, err := store.Snapshot(c.Context())
		if err != nil {
			return err
		}
		selected := report.Select(filter, time.Now())
		var reclaim int64
		for _, item := range selected {
			reclaim += item.Bytes
		}
		if !jsonOutput {
			for _, item := range selected {
				fmt.Fprintf(c.OutOrStdout(), "%s %d bytes %q\n", item.Kind, item.Bytes, item.Path)
			}
			fmt.Fprintf(c.OutOrStdout(), "%d items, %d reclaimable bytes\n", len(selected), reclaim)
		}
		if !apply {
			if jsonOutput {
				return json.NewEncoder(c.OutOrStdout()).Encode(struct {
					Items []storage.Item `json:"items"`
					Bytes int64          `json:"reclaimable_bytes"`
				}{selected, reclaim})
			}
			fmt.Fprintln(c.OutOrStdout(), "Preview only. Use --apply to confirm deletion.")
			return nil
		}
		if len(selected) > 0 && !yes {
			if !interactiveUpload(c) {
				return fmt.Errorf("noninteractive cleanup requires --apply --yes")
			}
			if jsonOutput {
				for _, item := range selected {
					fmt.Fprintf(c.ErrOrStderr(), "%s %d bytes %q\n", item.Kind, item.Bytes, item.Path)
				}
			}
			fmt.Fprintf(c.ErrOrStderr(), "Permanently delete these %d items (%d bytes)? Type delete: ", len(selected), reclaim)
			line, err := bufio.NewReaderSize(c.InOrStdin(), 4096).ReadString('\n')
			if err != nil {
				return err
			}
			if strings.TrimSpace(line) != "delete" {
				return fmt.Errorf("cleanup canceled")
			}
		}
		results, err := store.Apply(c.Context(), selected)
		if jsonOutput {
			if e := json.NewEncoder(c.OutOrStdout()).Encode(results); e != nil {
				return e
			}
		} else {
			for _, r := range results {
				if r.Deleted {
					fmt.Fprintf(c.OutOrStdout(), "Deleted %q\n", r.Path)
				} else {
					fmt.Fprintf(c.ErrOrStderr(), "Kept %q: %s\n", r.Path, r.Error)
				}
			}
		}
		if err != nil {
			return err
		}
		for _, r := range results {
			if !r.Deleted {
				return fmt.Errorf("some items were retained; refresh the cleanup preview")
			}
		}
		return nil
	}}
	clean.Flags().StringVar(&kind, "kind", "all", "recordings, exports, queue, or all")
	clean.Flags().StringVar(&age, "older-than", "0s", "minimum age since modification (for example 30d or 48h)")
	clean.Flags().StringVar(&size, "min-size", "0", "minimum item size in bytes or KiB/MiB/GiB")
	clean.Flags().BoolVar(&apply, "apply", false, "apply the reviewed cleanup preview")
	clean.Flags().BoolVar(&yes, "yes", false, "confirm deletion without prompting (requires --apply)")
	clean.Flags().BoolVar(&jsonOutput, "json", false, "print machine-readable preview or results")
	root.AddCommand(clean)
	for _, action := range []string{"pin", "unpin"} {
		root.AddCommand(&cobra.Command{Use: action + " <id|path>", Short: action + " a local recording", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
			lib, err := o.library()
			if err != nil {
				return err
			}
			path, err := lib.Resolve(args[0])
			if err != nil {
				return err
			}
			entry, err := lib.Register(path)
			if err != nil {
				return err
			}
			if err := lib.SetPinned(entry.ID, action == "pin"); err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), action, entry.ID)
			return nil
		}})
	}
	return root
}
