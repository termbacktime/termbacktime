package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/buildinfo"
	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/updates"
)

type options struct {
	configPath, token, endpoint, dataDir string
	open                                 bool
	checkUpdate                          bool
	updates                              *updates.Checker
}

func (o *options) library() (library.Library, error) {
	root, err := config.DataDir(o.dataDir)
	return library.Library{Root: root}, err
}

func (o *options) credentials() (config.Config, error) {
	c, err := config.Load(o.configPath)
	if err != nil {
		return nil, err
	}
	token := os.Getenv("TERMBACKTIME_TOKEN")
	if o.token != "" {
		token = o.token
	}
	if token != "" {
		c.Set("token", token)
	}
	return c, nil
}

func NewRoot() *cobra.Command {
	return newRootWithUpdates(updates.New())
}

func newRootWithUpdates(checker *updates.Checker) *cobra.Command {
	o := &options{updates: checker}
	root := &cobra.Command{
		Use:           "termbacktime",
		Short:         "Record, replay and share your terminal",
		Version:       buildinfo.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}
	root.RunE = func(c *cobra.Command, _ []string) error {
		if o.checkUpdate {
			return o.checkUpdates(c, true)
		}
		return c.Help()
	}
	root.SetVersionTemplate("termbacktime {{.Version}}\n")
	root.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		for p := c; p != nil; p = p.Parent() {
			if p.Name() == "completion" || p.Name() == "__complete" || p.Name() == "__completeNoDesc" {
				return nil
			}
		}
		if c == root {
			return nil
		}
		if c.Name() != "doctor" || o.checkUpdate {
			if err := o.checkUpdates(c, false); err != nil && o.checkUpdate {
				fmt.Fprintln(c.ErrOrStderr(), "Update check:", err)
			}
		}
		if root.PersistentFlags().Changed("config") {
			if o.configPath == "" {
				return fmt.Errorf("--config requires a filename")
			}
			var err error
			o.configPath, err = config.ExpandPath(o.configPath)
			return err
		}
		root, err := config.DataDir(o.dataDir)
		if err != nil {
			return err
		}
		o.configPath = filepath.Join(root, "termbacktime.json")
		if c.Name() == "doctor" {
			return nil
		}
		if err := config.Migrate(o.configPath); err != nil {
			return err
		}
		if c.Name() == "auth" {
			return config.EnsurePrivateDir(root)
		}
		return nil
	}
	root.PersistentFlags().StringVar(&o.dataDir, "data-dir", "", "data directory (or TERMBACKTIME_DATA_DIR; default ~/termbacktime)")
	root.PersistentFlags().StringVar(&o.configPath, "config", "", "configuration file (default <data-dir>/termbacktime.json)")
	root.PersistentFlags().StringVar(&o.token, "token", "", "GitHub token (prefer TERMBACKTIME_TOKEN)")
	root.PersistentFlags().StringVar(&o.endpoint, "endpoint", config.SiteURL(), "website and live API origin (or SITE_URL)")
	root.PersistentFlags().BoolVar(&o.open, "open", false, "open resulting URL in browser")
	root.PersistentFlags().BoolVar(&o.checkUpdate, "check-update", false, "check GitHub releases now, bypassing the 24-hour cache")
	root.AddCommand(newRecord(o), newPlay(o), newAuth(o), newLive(o), newCompletion())
	root.AddCommand(newList(o), newInfo(o), newImport(o), newRecover(o))
	root.AddCommand(newQueue(o), newStorage(o), newHistory(o))
	root.AddCommand(newUpload(o), newScan(o), newRedact(o))
	root.AddCommand(newExport(o))
	root.AddCommand(newDoctor(o))
	root.AddCommand(newManage(o))
	return root
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := NewRoot().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		var child *exec.ExitError
		if errors.As(err, &child) {
			if code := child.ExitCode(); code >= 0 {
				os.Exit(code)
			}
			if status, ok := child.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				os.Exit(128 + int(status.Signal()))
			}
		}
		os.Exit(1)
	}
}
