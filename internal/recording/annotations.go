package recording

import "fmt"

// CommandEvent is optional so existing readers still replay the output stream
type CommandEvent struct {
	ID       string `json:"i"`
	Phase    string `json:"p"`
	Text     string `json:"t,omitempty"`
	ExitCode *int   `json:"e,omitempty"`
	Partial  bool   `json:"r,omitempty"`
}

type Callout struct {
	ID       string `json:"i"`
	At       int64  `json:"a"`
	Text     string `json:"t"`
	Duration int64  `json:"d"`
	Pause    bool   `json:"p,omitempty"`
}

type CommandChapter struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	At       int64  `json:"at_ms"`
	End      int64  `json:"end_ms"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Partial  bool   `json:"partial,omitempty"`
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
			partial[c.ID] = c.Partial
			out = append(out, CommandChapter{ID: c.ID, Text: c.Text, At: at, End: duration, Partial: true})
		} else if i, ok := active[c.ID]; ok {
			out[i].End, out[i].ExitCode, out[i].Partial = at, c.ExitCode, c.Partial || partial[c.ID]
			delete(active, c.ID)
		} else {
			out = append(out, CommandChapter{ID: c.ID, Text: c.Text, At: at, End: at, ExitCode: c.ExitCode, Partial: true})
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
