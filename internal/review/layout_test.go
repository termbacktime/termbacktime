package review

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestManagedReviewKeepsFocusedActionsVisibleOnNarrowTerminals(t *testing.T) {
	r := &recording.Recording{Title: "title", Metadata: &recording.Metadata{Version: 1, Description: strings.Repeat("description ", 50), CaptureSystem: &recording.SystemInfo{CPUModel: strings.Repeat("long model ", 20)}}}
	m := New(r, recording.AllMetadataFields(), false, false)
	m.Managed = true
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 16})
	for focus := 0; focus < 11; focus++ {
		text := ansi.Strip(m.View().Content)
		rows := strings.Split(text, "\n")
		if len(rows) != 16 {
			t.Fatal("incorrect review height", len(rows))
		}
		for _, row := range rows {
			if ansi.StringWidth(row) != 40 {
				t.Fatal("review overflow", row)
			}
		}
		if focus == 9 && !strings.Contains(text, "> [ Upload now ]") {
			t.Fatal("upload action hidden", text)
		}
		if focus == 10 && !strings.Contains(text, "> [ Add to queue ]") {
			t.Fatal("queue action hidden", text)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
}
