package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
)

func newQueue(o *options) *cobra.Command {
	var jsonOutput bool
	root := &cobra.Command{Use: "queue", Short: "Manage durable uploads with one explicit foreground runner"}
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		lib, err := o.library()
		if err != nil {
			return err
		}
		jobs, err := (uploadqueue.Store{Root: lib.Root}).List()
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(jobs)
		}
		for i, job := range jobs {
			fmt.Fprintf(c.OutOrStdout(), "%d. %s · %s · %d/%d bytes · %s\n", i+1, job.ID, job.State, job.Sent, job.Total, job.Result)
		}
		return nil
	}}
	list.Flags().BoolVar(&jsonOutput, "json", false, "print machine-readable jobs without private encryption keys")
	root.AddCommand(list)
	root.AddCommand(&cobra.Command{Use: "run", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		lib, err := o.library()
		if err != nil {
			return err
		}
		client, err := o.recordingUploader()
		if err != nil {
			return err
		}
		previous := map[string]string{}
		return (uploadqueue.Store{Root: lib.Root}).Run(c.Context(), client, func(job uploadqueue.Job) {
			if previous[job.ID] != job.State {
				fmt.Fprintf(c.OutOrStdout(), "%s · %s · %s\n", job.ID, job.State, job.Result)
				previous[job.ID] = job.State
			}
			if job.Error != "" {
				fmt.Fprintln(c.ErrOrStderr(), job.Error)
			}
		})
	}})
	for _, action := range []string{"retry", "cancel"} {
		root.AddCommand(&cobra.Command{Use: action + " <job-id>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
			lib, err := o.library()
			if err != nil {
				return err
			}
			store := uploadqueue.Store{Root: lib.Root}
			if action == "retry" {
				err = store.Retry(args[0])
			} else {
				err = store.Cancel(args[0])
			}
			if err == nil {
				fmt.Fprintln(c.OutOrStdout(), action, args[0])
			}
			return err
		}})
	}
	return root
}
