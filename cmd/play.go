package cmd

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/history"
	"github.com/termbacktime/termbacktime/internal/playback"
	"github.com/termbacktime/termbacktime/internal/recording"
	"golang.org/x/term"
)

func newPlay(o *options) *cobra.Command {
	var speed float64
	var idleLimit time.Duration
	var noInteractive, noHistory bool
	c := &cobra.Command{Use: "play <id|file.tbt|file.partial|recording-link|github-user/recording-id>", Short: "Play a recording with optional interactive controls", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if math.IsNaN(speed) || math.IsInf(speed, 0) || speed <= 0 || speed > 100 {
			return fmt.Errorf("speed must be greater than zero and at most 100")
		}
		if idleLimit < 0 {
			return fmt.Errorf("idle limit must not be negative")
		}
		source, local, err := localOrRemote(o, args[0])
		if err != nil {
			return err
		}
		var r *recording.Recording
		if local {
			r, err = recording.OpenPlayback(source)
		} else {
			cfg, e := o.credentials()
			if e != nil {
				return e
			}
			r, err = gh.New(cfg.String("token")).Load(c.Context(), source)
		}
		if err != nil {
			return err
		}
		defer r.Close()
		original, _ := recording.Times(r, idleLimit.Milliseconds())
		duration := original[len(original)-1]
		position := float64(0)
		explicitStart := false
		if u, e := url.Parse(source); e == nil {
			q, _ := url.ParseQuery(u.Fragment)
			explicitStart = q.Has("t")
			if t, e := strconv.ParseFloat(q.Get("t"), 64); e == nil && !math.IsNaN(t) && !math.IsInf(t, 0) {
				position = math.Max(0, math.Min(float64(duration), t*1000))
			}
		}
		input, inputOK := c.InOrStdin().(*os.File)
		output, outputOK := c.OutOrStdout().(*os.File)
		interactive := !noInteractive && inputOK && outputOK && term.IsTerminal(int(input.Fd())) && term.IsTerminal(int(output.Fd()))
		if interactive {
			options := playback.HistoryOptions{ExplicitStart: explicitStart}
			if !noHistory {
				lib, e := o.library()
				if e != nil {
					return e
				}
				options.Store = &history.Store{Root: lib.Root}
			}
			return playback.RunWithHistory(c.Context(), input, output, r, speed, idleLimit, position, options)
		}
		width, height := r.Sizes[0], r.Sizes[1]
		if outputOK && term.IsTerminal(int(output.Fd())) {
			width, height, _ = term.GetSize(int(output.Fd()))
		}
		return playback.Render(c.Context(), c.OutOrStdout(), r, speed, idleLimit, position, width, height)
	}}
	c.Flags().Float64Var(&speed, "speed", 1, "playback speed multiplier")
	c.Flags().DurationVar(&idleLimit, "idle-limit", 0, "maximum idle pause; zero preserves original timing")
	c.Flags().BoolVar(&noHistory, "no-history", false, "do not read or save playback history for this invocation")
	c.Flags().BoolVar(&noInteractive, "no-interactive", false, "disable keyboard playback controls")
	return c
}
