package live

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestSessionMetadataSurvivesSnapshotsAndResizing(t *testing.T) {
	s := NewScreen(80, 24)
	defer s.Close()
	info := recording.CurrentInfo()
	s.SetSession("Shared terminal", 1789826400, info)
	first, _ := s.Frame(true)
	s.Write("\x1b]2;changed terminal title\x07output")
	s.Resize(100, 30)
	frame, changed := s.Frame(false)
	if !changed || frame.Session == nil || frame.Session.Title != "Shared terminal" || frame.Session.Info != info || frame.Session.Started != 1789826400 {
		t.Fatalf("lost session metadata: %+v", frame)
	}
	data, err := json.Marshal(frame)
	var decoded ScreenFrame
	if err != nil || json.Unmarshal(data, &decoded) != nil || decoded.Session == nil || *decoded.Session != *frame.Session {
		t.Fatal("session metadata does not round trip in the encrypted frame payload")
	}
	s.SetSession(strings.Repeat("界", 300), 1789826401, info)
	frame, _ = s.Frame(true)
	if len([]rune(frame.Session.Title)) != 256 || first.Session.Title != "Shared terminal" {
		t.Fatal("metadata must be bounded and previous snapshots must remain immutable")
	}
}

func TestScreenDropsScrollbackAndTracksMetadata(t *testing.T) {
	s := NewScreen(20, 3)
	defer s.Close()
	if err := s.Write("old secret\r\nline two\r\nline three\r\ncurrent 世界\x1b]9;4;1;42\x07\x1b]2;title\x07\x1b[?25l"); err != nil {
		t.Fatal(err)
	}
	frame, changed := s.Frame(true)
	if !changed || frame.Type != "snapshot" || strings.Contains(frame.Screen, "old secret") {
		t.Fatalf("unexpected current screen: %#v", frame)
	}
	if frame.Progress != [2]int{1, 42} || frame.Visible || frame.Title != "title" {
		t.Fatalf("lost terminal metadata: %#v", frame)
	}
	if !strings.Contains(frame.Screen, "\r\n") {
		t.Fatal("screen rows must reset the column before advancing to the next line")
	}
	if _, changed = s.Frame(false); changed {
		t.Fatal("unchanged screen should not produce an update")
	}
	if err := s.Write("\x1b[?1049h\x1b[Halt screen"); err != nil {
		t.Fatal(err)
	}
	s.Resize(30, 5)
	frame, changed = s.Frame(false)
	if !changed || !frame.Alternate || frame.Cols != 30 || frame.Rows != 5 {
		t.Fatalf("lost alternate screen or resize: %#v", frame)
	}
	if frame.Type != "update" || strings.Contains(frame.Screen, "line two") {
		t.Fatalf("unexpected alternate screen: %#v", frame)
	}
}
