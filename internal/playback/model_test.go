package playback

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func key(m *Model, code rune) { m.Update(tea.KeyPressMsg{Code: code}) }
func TestPlaybackNavigationLoopsAndLegacyAnnotations(t *testing.T) {
	id := recording.NewID()
	r := &recording.Recording{Title: "legacy", Sizes: []int{20, 5}, Lines: []recording.Event{
		{Lines: []string{"first"}, Lifecycle: &recording.CommandEvent{ID: id, Phase: "start", Text: "echo first"}},
		{Time: 1000, Lines: []string{"\rsecond"}}, {Time: 1000, Command: "s", Sizes: []int{40, 10}}, {Time: 8000, Lines: []string{"last"}},
	}, Markers: []recording.Marker{{ID: recording.NewID(), At: 1000, Label: "legacy", Kind: "bookmark"}}, Callouts: []recording.Callout{{ID: recording.NewID(), At: 1000, Text: "INVISIBLE", Duration: 5000, Pause: true}}}
	m := New(t.Context(), r, 1, 0, 0)
	defer m.Close()
	m.Init()
	key(m, tea.KeySpace)
	if m.playing {
		t.Fatal("pause")
	}
	key(m, '.')
	if m.index != 2 || m.position != 1000 {
		t.Fatal("next event", m.index, m.position)
	}
	key(m, ',')
	if m.index != 1 || m.position != 0 {
		t.Fatal("previous event")
	}
	key(m, tea.KeyRight)
	if m.position != 5000 {
		t.Fatal("forward five seconds")
	}
	key(m, 'i')
	key(m, tea.KeyRight)
	key(m, 'o')
	key(m, 'l')
	if !m.loop || m.loopStart != 5000 || m.loopEnd != 10000 {
		t.Fatal("loop bounds")
	}
	key(m, '+')
	if m.speed != 2 {
		t.Fatal("speed")
	}
	key(m, '-')
	key(m, tea.KeySpace)
	m.Update(tickMsg{m, m.previous.Add(time.Second)})
	if m.position != 5000 || !m.playing {
		t.Fatal("loop restart")
	}
	key(m, 'l')
	key(m, tea.KeyLeft)
	m.Update(tickMsg{m, m.previous.Add(2 * time.Second)})
	if m.position != 2000 || !m.playing {
		t.Fatal("callout interrupted playback", m.position)
	}
	m.Update(tea.WindowSizeMsg{Width: 35, Height: 14})
	view := m.View().Content
	if strings.Contains(view, "INVISIBLE") || len(strings.Split(view, "\n")) != 14 {
		t.Fatal("view/annotation")
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) != 35 {
			t.Fatal("unbounded view")
		}
	}
	if len(r.Callouts) != 1 || len(r.Markers) != 1 || r.Lines[0].Lifecycle.Text != "echo first" {
		t.Fatal("legacy recording mutated")
	}
	_, cmd := m.Update(tickMsg{m, m.previous.Add(20 * time.Second)})
	if done, ok := cmd().(DoneMsg); !ok || done.Err != nil {
		t.Fatal("completion")
	}
}
func TestPlaybackCancellationAndStaleTicks(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	r := &recording.Recording{Sizes: []int{20, 5}, Lines: []recording.Event{{Time: 1000, Lines: []string{"end"}}}}
	m := New(ctx, r, 1, 0, 0)
	defer m.Close()
	m.Init()
	other := New(t.Context(), r, 1, 0, 0)
	defer other.Close()
	_, cmd := m.Update(tickMsg{other, time.Now().Add(time.Hour)})
	if cmd != nil || m.position != 0 {
		t.Fatal("stale timer applied")
	}
	cancel()
	_, cmd = m.Update(tickMsg{m, m.previous.Add(2 * time.Second)})
	if done, ok := cmd().(DoneMsg); !ok || done.Err != context.Canceled {
		t.Fatal("error not returned")
	}
}

func TestNoninteractivePlaybackNormalizesModesAndIgnoresPauses(t *testing.T) {
	r := &recording.Recording{Sizes: []int{30, 5}, Lines: []recording.Event{{Lines: []string{"\x1b[?1049h\x1b[?2004h\x1b[?1h\x1b[8;55;160t\x1b]52;c;secret\ahello"}}, {Time: 10, Command: "s", Sizes: []int{40, 8}}}, Callouts: []recording.Callout{{ID: recording.NewID(), At: 0, Text: "invisible", Duration: 60000, Pause: true}}}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := Render(ctx, &out, r, 1, 0, 0, 40, 10); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"1049", "2004", "?1h", "55;160t", "52;c", "invisible"} {
		if strings.Contains(out.String(), value) {
			t.Fatal("recorded mode or annotation reached output", value)
		}
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatal("missing rendered recording")
	}
}
