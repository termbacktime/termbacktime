package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/dashboard"
	"github.com/termbacktime/termbacktime/internal/live"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/systeminfo"
	"github.com/termbacktime/termbacktime/internal/terminal"
)

func newLive(o *options) *cobra.Command { return liveCommand(o, live.Run) }
func liveCommand(o *options, run func(context.Context, live.Options) error) *cobra.Command {
	var shell, output, title string
	var save, noMetadata, noDashboard bool
	var interval, ttl time.Duration
	c := &cobra.Command{Use: "live [-- command args...]", Short: "Share an encrypted, read-only terminal", RunE: func(c *cobra.Command, args []string) (result error) {
		dashboardMode := !noDashboard && interactiveUpload(c)
		if interval < 0 || ttl < time.Second {
			return fmt.Errorf("invalid sync interval or room lifetime")
		}
		var commandArgs []string
		if len(args) > 0 {
			shell = args[0]
			commandArgs = args[1:]
		}
		var display terminal.Presentation
		var dash *dashboard.Dashboard
		cols, rows := terminal.Size(stdout())
		if dashboardMode {
			dash = dashboard.New(stdout(), "", dashboard.LiveMode, openURL)
			display = dash
			cols, rows = dash.Geometry()
		}
		report := func(message string) {
			if dash != nil {
				dash.Set(func(s *dashboard.State) { s.Status = message })
			} else {
				fmt.Fprintln(c.ErrOrStderr(), message)
			}
		}
		var capture *recording.Capture
		var failed bool
		started := time.Now().Unix()
		if save || c.Flags().Changed("output") {
			lib, err := o.library()
			if err != nil {
				return err
			}
			if err = lib.Init(); err != nil {
				return err
			}
			output, err = lib.Output(output)
			if err != nil {
				return err
			}
			if dash != nil {
				dash.Set(func(s *dashboard.State) { s.Path = output })
			}
			r := recording.Recording{ID: recording.NewID(), Title: title, Started: started, Sizes: []int{cols, rows}, Info: recording.CurrentInfo()}
			if !noMetadata {
				r.Metadata = &recording.Metadata{Version: 1, CaptureSystem: systeminfo.Collect(c.Context(), filepath.Dir(output))}
			}
			writer, err := recording.NewWriterWithInterval(output, r, interval)
			if err != nil {
				return err
			}
			defer writer.Close()
			defer watchRecordingErrors(c, writer, report)()
			capture = recording.NewCapture(writer)
			if _, err := lib.Register(output + ".partial"); err != nil {
				return err
			}
			defer func() {
				if err := capture.Finish(); err != nil {
					result = errors.Join(result, fmt.Errorf("recording failed; recover %s.partial: %w", output, err))
					return
				}
				if _, err := lib.Register(output); err != nil {
					result = errors.Join(result, err)
				}
				fmt.Fprintln(c.ErrOrStderr(), "Saved", output)
			}()
		}
		return run(c.Context(), live.Options{Endpoint: o.endpoint, Shell: shell, Args: commandArgs, TTL: ttl, Title: title, Started: started, Cols: cols, Rows: rows, Presentation: display,
			OnReady: func(link string) {
				if dash != nil {
					dash.Set(func(s *dashboard.State) { s.Viewer = link; s.Status = "Sharing" })
				} else {
					fmt.Fprintln(c.OutOrStdout(), "Read-only live link:", link)
				}
				if o.open && !dashboardMode {
					_ = openURL(link)
				}
			},
			OnHostReady: func(link string) {
				if dash != nil {
					dash.Set(func(s *dashboard.State) { s.Host = link })
				} else {
					fmt.Fprintln(c.OutOrStdout(), "Private host controls:", link)
				}
			},
			OnStatus:      report,
			OnRoomStatus:  roomReporter(dash),
			OnDiagnostics: diagnosticsReporter(dash),
			OnEvent: func(e terminal.Event) error {
				if capture == nil || failed {
					return nil
				}
				err := capture.Event(e)
				failed = err != nil
				return err
			},
		})
	}}
	c.Flags().BoolVar(&noDashboard, "no-dashboard", false, "disable the automatic terminal dashboard")
	c.Flags().StringVar(&shell, "shell", "", "shell executable")
	c.Flags().BoolVar(&noMetadata, "no-metadata", false, "disable optional local computer metadata")
	c.Flags().BoolVar(&save, "record", false, "save this live session in your recording library")
	c.Flags().StringVarP(&output, "output", "o", "", "record to this filename (enables recording)")
	c.Flags().StringVar(&title, "title", "Live terminal recording", "live session and recording title")
	c.Flags().DurationVar(&interval, "sync-interval", 0, "disk sync interval; zero syncs every event")
	c.Flags().DurationVar(&ttl, "ttl", 8*time.Hour, "room lifetime, bounded by the deployment maximum")
	return c
}

func roomReporter(dash *dashboard.Dashboard) func(live.RoomStatus, error) {
	if dash == nil {
		return nil
	}
	return func(state live.RoomStatus, err error) {
		dash.Set(func(s *dashboard.State) {
			if err != nil {
				s.Room = "Room status unavailable (stale)"
				return
			}
			mode := "sharing"
			if state.Paused {
				mode = "paused"
			}
			if state.Revision > state.Acknowledged {
				mode += " · pending publisher acknowledgement"
			}
			s.Room = fmt.Sprintf("%d viewers · %s · expires %s", state.Viewers, mode, time.UnixMilli(state.Expires).Local().Format(time.TimeOnly))
		})
	}
}

func diagnosticsReporter(dash *dashboard.Dashboard) func(live.Diagnostics) {
	if dash == nil {
		return nil
	}
	return func(d live.Diagnostics) { dash.Set(func(s *dashboard.State) { s.Diagnostics = d }) }
}
