package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/dashboard"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/review"
	"github.com/termbacktime/termbacktime/internal/systeminfo"
	"github.com/termbacktime/termbacktime/internal/terminal"
)

func newRecord(o *options) *cobra.Command {
	var shell, output, title string
	var upload, noSave, dashboardMode bool
	var u uploadFlags
	var syncInterval time.Duration
	c := &cobra.Command{
		Use:   "record [-- command args...]",
		Short: "Record a terminal session, optionally upload to GitHub",
		RunE: func(c *cobra.Command, args []string) (resultErr error) {
			if dashboardMode {
				if err := dashboard.Check(os.Stdin, os.Stdout); err != nil {
					return err
				}
			}
			if err := u.validate(c); err != nil {
				return err
			}
			if u.queue && (noSave || !upload) {
				return fmt.Errorf("record --queue requires --upload and cannot use --no-save")
			}
			if noSave && !upload {
				return fmt.Errorf("--no-save requires --upload")
			}
			if noSave && c.Flags().Changed("output") {
				return fmt.Errorf("--no-save cannot be used with --output")
			}

			var uploader *gh.Client
			path := output
			if noSave {
				// Check upload prerequisites before starting a recording that will not be retained
				var err error
				uploader, err = o.recordingUploader()
				if err != nil {
					return err
				}
				u.storage, err = o.uploadStorage(u.storage)
				if err != nil {
					return err
				}
				if !c.Flags().Changed("storage") && !u.noMetadata && interactiveUpload(c) {
					u.storage, err = review.ChooseStorage(c.Context(), c.InOrStdin(), c.OutOrStdout(), u.storage)
					if err != nil {
						return err
					}
					u.rememberStorage = true
				}
				if err = validateRepoVisibility(c, u.storage); err != nil {
					return err
				}
				u.storageLocked = true
				uploader, err = uploader.Backend(c.Context(), u.storage)
				if err != nil {
					return err
				}
				if u.storage == "repo" {
					if err = uploader.CheckRepositorySupport(c.Context()); err != nil {
						return err
					}
				}
				if u.encrypt {
					if err := uploader.CheckEncryption(c.Context()); err != nil {
						return err
					}
				}
				dir, err := os.MkdirTemp("", "termbacktime-upload-*")
				if err != nil {
					return fmt.Errorf("create temporary recording directory: %w", err)
				}
				// Remove the final recording and any unfinished journal on every normal exit path
				defer func() {
					if err := os.RemoveAll(dir); err != nil {
						resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary recording directory %s: %w", dir, err))
					}
				}()
				path = filepath.Join(dir, "recording.tbt")
			} else {
				lib, err := o.library()
				if err != nil {
					return err
				}
				if err = lib.Init(); err != nil {
					return err
				}
				path, err = lib.Output(path)
				if err != nil {
					return err
				}
			}

			cols, rows := terminal.Size(stdout())
			var display terminal.Presentation
			var dash *dashboard.Dashboard
			if dashboardMode {
				dash = dashboard.New(stdout(), path, dashboard.RecordingMode, openURL)
				dash.Set(func(s *dashboard.State) { s.Status = "Recording" })
				display = dash
				cols, rows = dash.Geometry()
			}
			rec := recording.Recording{
				ID:      recording.NewID(),
				Info:    recording.CurrentInfo(),
				Started: time.Now().Unix(),
				Title:   title,
				Sizes:   []int{cols, rows},
			}
			if !u.noMetadata {
				rec.Metadata = &recording.Metadata{Version: 1, Description: u.description, CaptureSystem: systeminfo.Collect(c.Context(), filepath.Dir(path))}
			}
			w, err := recording.NewWriterWithInterval(path, rec, syncInterval)
			if err != nil {
				return err
			}
			defer w.Close()
			if dash != nil {
				defer watchRecordingErrors(c, w, func(message string) { dash.Set(func(s *dashboard.State) { s.Status = message }) })()
			} else {
				defer watchRecordingErrors(c, w)()
			}
			capture := recording.NewCapture(w)
			if !noSave {
				lib, _ := o.library()
				if _, err := lib.Register(path + ".partial"); err != nil {
					return err
				}
			}
			commandArgs := []string(nil)
			if len(args) > 0 {
				shell = args[0]
				commandArgs = args[1:]
			}
			runErr := terminal.Run(c.Context(), terminal.Options{
				Shell: shell,
				Cols:  cols, Rows: rows, Presentation: display,
				Args:    commandArgs,
				OnEvent: capture.Event,
			})
			if err = capture.Finish(); err != nil {
				if noSave {
					return fmt.Errorf("prepare temporary recording for upload: %w", err)
				}
				return fmt.Errorf("save failed; recover %s.partial: %w", path, err)
			}
			if !noSave {
				lib, _ := o.library()
				if _, err := lib.Register(path); err != nil {
					return err
				}
				fmt.Fprintln(c.ErrOrStderr(), "Saved", path)
			}
			// Bare exit inherits the last command's status. Finishing an
			// interactive session normally must not prevent its upload.
			var shellExit *exec.ExitError
			if len(args) == 0 && errors.As(runErr, &shellExit) && shellExit.ExitCode() >= 0 {
				runErr = nil
			}
			if runErr != nil {
				return runErr
			}
			if upload {
				if uploader == nil {
					uploader, err = o.recordingUploader()
					if err != nil {
						return err
					}
				}
				uploadTitle := ""
				if c.Flags().Changed("title") {
					uploadTitle = title
				}
				link, err := o.uploadRecording(c, uploader, path, uploadTitle, u, !noSave)
				if err != nil {
					if noSave {
						return fmt.Errorf("upload failed: %w", err)
					}
					return fmt.Errorf("local recording saved; upload failed: %w", err)
				}
				fmt.Fprintln(c.OutOrStdout(), link)
				if o.open && !u.queue {
					return openURL(link)
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&dashboardMode, "dashboard", false, "show an interactive terminal dashboard (Ctrl+] for controls)")
	c.Flags().StringVar(&shell, "shell", "", "shell executable")
	c.Flags().StringVarP(&output, "output", "o", "", "local recording filename")
	c.Flags().StringVar(&title, "title", "Terminal recording", "recording title")
	c.Flags().BoolVar(&upload, "upload", false, "upload to the selected GitHub storage after recording")
	c.Flags().BoolVar(&noSave, "no-save", false, "use temporary files and keep no local recording, even on failure (requires --upload)")
	c.Flags().DurationVar(&syncInterval, "sync-interval", 0, "disk sync interval; zero syncs every event")
	u.flags(c)
	return c
}

func (o *options) recordingUploader() (*gh.Client, error) {
	cfg, err := o.credentials()
	if err != nil {
		return nil, err
	}

	client := gh.New(cfg.String("token"))
	if o.endpoint != "" {
		client.SiteURL, err = config.ValidateSiteURL(o.endpoint)
		if err != nil {
			return nil, err
		}
	}
	o.configureBackends(client)
	return client, nil
}
