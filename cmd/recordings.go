package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sharing"
	"github.com/termbacktime/termbacktime/internal/upload"
)

func (o *options) uploadRecording(c *cobra.Command, client *gh.Client, path, title string, u uploadFlags, retain bool) (string, error) {
	lib, err := o.library()
	if err != nil {
		return "", err
	}
	u.storage, err = o.uploadStorage(u.storage)
	if err != nil {
		return "", err
	}
	result, err := upload.Run(c.Context(), client, lib, upload.Request{Path: path, Storage: u.storage, Public: u.public, Encrypted: u.encrypt, Queue: u.queue, Retain: retain, FailOnSecrets: u.fail, Review: func(r *recording.Recording, req *upload.Request) error {
		if err := o.prepareUploadReview(c, r, path, title, &u); err != nil {
			return err
		}
		if err := validateRepoVisibility(c, u.storage); err != nil {
			return err
		}
		req.Storage = u.storage
		req.Public = u.public
		req.Encrypted = u.encrypt
		return nil
	}}, nil)
	if result.Warning != "" {
		fmt.Fprintln(c.ErrOrStderr(), result.Warning)
	}
	if err != nil {
		return "", err
	}
	if result.QueueID != "" {
		return "Queued upload " + result.QueueID, nil
	}
	return result.Link, nil
}

func newUpload(o *options) *cobra.Command {
	var title string
	var u uploadFlags
	c := &cobra.Command{Use: "upload <id|path>", Short: "Upload an existing recording to GitHub", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if err := u.validate(c); err != nil {
			return err
		}
		lib, err := o.library()
		if err != nil {
			return err
		}
		path, err := lib.Resolve(args[0])
		if err != nil {
			return err
		}
		client, err := o.recordingUploader()
		if err != nil {
			return err
		}
		link, err := o.uploadRecording(c, client, path, title, u, true)
		if err != nil {
			return err
		}
		fmt.Fprintln(c.OutOrStdout(), link)
		if o.open && !u.queue {
			return openURL(link)
		}
		return nil
	}}
	c.Flags().StringVar(&title, "title", "", "override the uploaded recording's title")
	u.flags(c)
	return c
}

func newScan(o *options) *cobra.Command {
	return &cobra.Command{Use: "scan <id|path>", Short: "Report likely secrets without printing their values", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		lib, err := o.library()
		if err != nil {
			return err
		}
		path, err := lib.Resolve(args[0])
		if err != nil {
			return err
		}
		r, err := recording.Load(path)
		if err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(recording.Scan(r))
	}}
}

func (o *options) saveCopy(c *cobra.Command, r *recording.Recording, output string) error {
	lib, err := o.library()
	if err != nil {
		return err
	}
	path, err := lib.Output(output)
	if err != nil {
		return err
	}
	if err := recording.Save(path, r); err != nil {
		return err
	}
	if _, err := lib.Register(path); err != nil {
		return err
	}
	fmt.Fprintln(c.OutOrStdout(), path)
	return nil
}

func newRedact(o *options) *cobra.Command {
	var rulesPath, output string
	c := &cobra.Command{Use: "redact <id|path> --rules FILE", Short: "Create a cleaned copy using literal and interval redactions", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if rulesPath == "" {
			return fmt.Errorf("--rules is required")
		}
		f, err := os.Open(rulesPath)
		if err != nil {
			return err
		}
		defer f.Close()
		b, err := recording.ReadBounded(f, 1<<20)
		if err != nil {
			return err
		}
		var rules recording.Rules
		if err := json.Unmarshal(b, &rules); err != nil {
			return err
		}
		lib, err := o.library()
		if err != nil {
			return err
		}
		path, err := lib.Resolve(args[0])
		if err != nil {
			return err
		}
		r, err := recording.Load(path)
		if err != nil {
			return err
		}
		clean, err := recording.Redact(r, rules)
		if err != nil {
			return err
		}
		if n := len(recording.Scan(clean)); n > 0 {
			fmt.Fprintf(c.ErrOrStderr(), "Cleaned copy still has %d likely secret(s).\n", n)
		}
		return o.saveCopy(c, clean, output)
	}}
	c.Flags().StringVar(&rulesPath, "rules", "", "version 1 JSON rules file")
	c.Flags().StringVarP(&output, "output", "o", "", "cleaned recording filename")
	return c
}

func localOrRemote(o *options, value string) (string, bool, error) {
	lib, err := o.library()
	if err != nil {
		return "", false, err
	}
	path, e := lib.Resolve(value)
	if e == nil {
		return path, true, nil
	}
	if errors.Is(e, library.ErrMissing) || errors.Is(e, library.ErrAmbiguous) {
		return "", false, e
	}
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		if _, err := sharing.Parse(value); err == nil {
			return value, false, nil
		}
	}
	if _, err := sharing.Parse(value); err == nil {
		return value, false, nil
	}
	return "", false, e
}
