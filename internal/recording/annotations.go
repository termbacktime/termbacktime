package recording

import "fmt"

// CommandEvent is optional so existing readers still replay the output stream
type CommandEvent struct {
	ID       string   `json:"i"`
	Phase    string   `json:"p"`
	Text     string   `json:"t,omitempty"`
	ExitCode *int     `json:"e,omitempty"`
	Partial  bool     `json:"r,omitempty"`
	CWD      string   `json:"w,omitempty"`
	Argv     []string `json:"a,omitempty"`
}

// ExecutionContext describes command capture, independently of computer metadata.
type ExecutionContext struct {
	Shell   string `json:"h"`
	Version string `json:"v,omitempty"`
	CWD     string `json:"w,omitempty"`
}

func (x *ExecutionContext) Validate() error {
	if x == nil {
		return nil
	}
	if len(x.Shell) > 128 || len(x.Version) > 128 || len(x.CWD) > 16384 {
		return fmt.Errorf("invalid execution context")
	}
	return nil
}

type Callout struct {
	ID       string `json:"i"`
	At       int64  `json:"a"`
	Text     string `json:"t"`
	Duration int64  `json:"d"`
	Pause    bool   `json:"p,omitempty"`
}

type CommandChapter struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	At       int64    `json:"at_ms"`
	End      int64    `json:"end_ms"`
	ExitCode *int     `json:"exit_code,omitempty"`
	Partial  bool     `json:"partial,omitempty"`
	CWD      string   `json:"cwd,omitempty"`
	Argv     []string `json:"argv,omitempty"`
}

func (c *CommandEvent) Validate() error {
	if c == nil {
		return nil
	}
	if !ValidID(c.ID) || (c.Phase != "start" && c.Phase != "end") || len(c.Text) > 16384 || (c.ExitCode != nil && (*c.ExitCode < 0 || *c.ExitCode > 255)) {
		return fmt.Errorf("invalid command metadata")
	}
	if c.Phase == "start" && c.ExitCode != nil {
		return fmt.Errorf("start event cannot have exit status")
	}
	if len(c.CWD) > 16384 || len(c.Argv) > 4096 {
		return fmt.Errorf("invalid command context")
	}
	n := 0
	for _, arg := range c.Argv {
		n += len(arg)
	}
	if n > 16384 {
		return fmt.Errorf("command arguments exceed 16 KiB")
	}
	return nil
}

func Commands(r *Recording) []CommandChapter {
	out := []CommandChapter{}
	active := map[string]int{}
	partial := map[string]bool{}
	var at int64
	duration := Duration(r)
	for _, e := range r.Lines {
		at += e.Time
		c := e.Lifecycle
		if c == nil {
			continue
		}
		if c.Phase == "start" {
			active[c.ID] = len(out)
			partial[c.ID] = c.Partial || (c.Text == "" && len(c.Argv) == 0)
			out = append(out, CommandChapter{ID: c.ID, Text: c.Text, CWD: c.CWD, Argv: c.Argv, At: at, End: duration, Partial: true})
		} else if i, ok := active[c.ID]; ok {
			out[i].End, out[i].ExitCode, out[i].Partial = at, c.ExitCode, c.Partial || partial[c.ID] || c.ExitCode == nil
			delete(active, c.ID)
		} else {
			out = append(out, CommandChapter{ID: c.ID, Text: c.Text, CWD: c.CWD, Argv: c.Argv, At: at, End: at, ExitCode: c.ExitCode, Partial: true})
		}
	}
	return out
}

func validateCallouts(r *Recording, duration int64) error {
	if len(r.Callouts) > 10000 {
		return fmt.Errorf("too many callouts")
	}
	seen := map[string]bool{}
	for _, c := range r.Callouts {
		if !ValidID(c.ID) || seen[c.ID] || c.At < 0 || c.At > duration || c.Duration < 1 || c.Duration > 60000 || len(c.Text) == 0 || len(c.Text) > 16384 {
			return fmt.Errorf("invalid recording callout")
		}
		seen[c.ID] = true
	}
	return nil
}
