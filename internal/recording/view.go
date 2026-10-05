package recording

import (
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"io"
	"strings"

	"github.com/charmbracelet/x/vt"
)

type TranscriptEntry struct {
	At   int64  `json:"at_ms"`
	Text string `json:"text"`
	Kind string `json:"kind"`
}

// Observer owns a noninteractive emulator; replies are drained and never reach a shell
func newObserver(size []int) (*vt.Emulator, func()) {
	em := vt.NewEmulator(size[0], size[1])
	em.SetScrollbackSize(1000)
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.Copy(io.Discard, em) }()
	return em, func() { _ = em.InputPipe().(io.Closer).Close(); <-done; _ = em.Close() }
}

func rowText(em *vt.Emulator, row int, history bool) string {
	var b strings.Builder
	for x := 0; x < em.Width(); x++ {
		cell := em.CellAt(x, row)
		if history {
			cell = em.ScrollbackCellAt(x, row)
		}
		if cell == nil {
			b.WriteByte(' ')
			continue
		}
		if cell.Width == 0 {
			continue
		}
		if cell.Content == "" {
			b.WriteByte(' ')
		} else {
			b.WriteString(cell.Content)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// WalkTranscript commits normal lines as they scroll and collapses edits in pending rows
func WalkTranscript(ctx context.Context, r *Recording, visit func(TranscriptEntry) error) error {
	if err := r.Validate(); err != nil {
		return err
	}
	em, close := newObserver(r.Sizes)
	defer close()
	pending := map[int]TranscriptEntry{}
	top := 0
	var at int64
	alternate := false
	lastScreen := ""
	flush := func() error {
		for y := top; y < top+em.Height(); y++ {
			if p, ok := pending[y]; ok && p.Text != "" {
				if err := visit(p); err != nil {
					return err
				}
			}
		}
		pending = map[int]TranscriptEntry{}
		top = 0
		return nil
	}
	var observerErr error
	beforeClear := func() {
		if observerErr == nil {
			observerErr = flush()
		}
	}
	em.RegisterCsiHandler('J', func(params ansi.Params) bool {
		value, _, _ := params.Param(0, 0)
		if value == 2 || value == 3 {
			beforeClear()
		}
		return false
	})
	em.RegisterEscHandler('c', func() bool { beforeClear(); return false })
	for _, event := range r.Lines {
		if err := ctx.Err(); err != nil {
			return err
		}
		at += event.Time
		if event.Command == "s" {
			if err := flush(); err != nil {
				return err
			}
			em.Resize(event.Sizes[0], event.Sizes[1])
		}
		text := strings.Join(event.Lines, "")
		// Process line boundaries individually so a single large PTY event cannot overflow history
		for len(text) > 0 {
			n := strings.IndexByte(text, '\n') + 1
			if n == 0 {
				n = len(text)
			}
			n = min(n, max(4, em.Width()*4))
			part := text[:n]
			text = text[n:]
			if _, err := em.WriteString(part); err != nil {
				return err
			}
			if observerErr != nil {
				return observerErr
			}
			if em.IsAltScreen() != alternate {
				if err := flush(); err != nil {
					return err
				}
				alternate = em.IsAltScreen()
				lastScreen = ""
			}
			if alternate {
				continue
			}
			for y := 0; y < em.ScrollbackLen(); y++ {
				line := rowText(em, y, true)
				p, ok := pending[top]
				if !ok {
					p = TranscriptEntry{At: at, Kind: "line"}
				}
				p.Text = line
				if line != "" {
					if err := visit(p); err != nil {
						return err
					}
				}
				delete(pending, top)
				top++
			}
			em.ClearScrollback()
			for y := 0; y < em.Height(); y++ {
				line := rowText(em, y, false)
				index := top + y
				p, ok := pending[index]
				if !ok || p.Text == "" {
					p = TranscriptEntry{At: at, Kind: "line"}
				}
				p.Text = line
				pending[index] = p
			}
		}
		if alternate {
			screen := strings.TrimRight(em.String(), "\n ")
			if screen != "" && screen != lastScreen {
				if err := visit(TranscriptEntry{At: at, Text: screen, Kind: "screen"}); err != nil {
					return err
				}
				lastScreen = screen
			}
		}
	}
	if !alternate {
		return flush()
	}
	return nil
}

func WriteTranscript(ctx context.Context, w io.Writer, r *Recording, markdown bool) error {
	return WalkTranscript(ctx, r, func(e TranscriptEntry) error {
		if markdown {
			fence := "```"
			for strings.Contains(e.Text, fence) {
				fence += "`"
			}
			_, err := fmt.Fprintf(w, "**%.3fs**\n\n%s text\n%s\n%s\n\n", float64(e.At)/1000, fence, e.Text, fence)
			return err
		}
		_, err := fmt.Fprintf(w, "[%.3fs] %s\n", float64(e.At)/1000, e.Text)
		return err
	})
}
