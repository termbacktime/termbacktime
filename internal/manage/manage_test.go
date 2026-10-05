package manage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sealed"
)

type fakeStore struct {
	items   []Item
	deleted []string
	fail    bool
}

func (s *fakeStore) List(context.Context, bool, int) ([]Item, int, error) {
	return append([]Item(nil), s.items...), 0, nil
}
func (s *fakeStore) Inspect(_ context.Context, i Item) (string, error) {
	return "Metadata for " + i.Title(), nil
}
func (s *fakeStore) Delete(_ context.Context, i Item) error {
	if s.fail {
		return fmt.Errorf("denied")
	}
	s.deleted = append(s.deleted, i.ID())
	for n, item := range s.items {
		if item.ID() == i.ID() {
			s.items = append(s.items[:n], s.items[n+1:]...)
			break
		}
	}
	return nil
}
func (s *fakeStore) Action(context.Context, Item, string) (string, error) { return "Complete", nil }
func (s *fakeStore) Import(string) (string, error)                        { return "Imported", nil }
func press(m *model, key string) tea.Cmd {
	k := tea.KeyPressMsg{}
	switch key {
	case "enter":
		k.Code = tea.KeyEnter
	case "esc":
		k.Code = tea.KeyEscape
	case "down":
		k.Code = tea.KeyDown
	case "tab":
		k.Code = tea.KeyTab
	case "up":
		k.Code = tea.KeyUp
	case "end":
		k.Code = tea.KeyEnd
	default:
		k.Code = []rune(key)[0]
	}
	_, cmd := m.update(k)
	return cmd
}
func sampleItems() []Item {
	return []Item{{Local: &library.Entry{ID: strings.Repeat("a", 32), Title: "Alpha 世界", Path: "/tmp/alpha.json", Status: "ready"}}, {Local: &library.Entry{ID: strings.Repeat("b", 32), Title: "Beta", Path: "/tmp/beta.json", Status: "ready"}}}
}
func readyModel(t *testing.T) (*model, *fakeStore) {
	t.Helper()
	s := &fakeStore{items: sampleItems()}
	m := newModel(t.Context(), s, false, nil)
	m.Update(m.Init()())
	return m, s
}

func TestManagerConfirmationSelectionAndCancellation(t *testing.T) {
	m, s := readyModel(t)
	press(m, "d")
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	if m.mode != "delete" || !strings.Contains(ansi.Strip(m.View().Content), "alpha.json") {
		t.Fatal("missing deletion target")
	}
	if cmd := press(m, "enter"); cmd != nil {
		t.Fatal("default Enter deletes")
	}
	press(m, "down")
	if m.selected != 0 {
		t.Fatal("confirmation changed selection")
	}
	press(m, "esc")
	if len(s.deleted) != 0 {
		t.Fatal("cancel deleted")
	}
	press(m, "down")
	press(m, "d")
	m.input.SetValue("delete")
	cmd := press(m, "enter")
	if cmd == nil {
		t.Fatal("confirmed deletion missing")
	}
	m.Update(cmd())
	if len(s.deleted) != 1 || s.deleted[0] != strings.Repeat("b", 32) {
		t.Fatal(s.deleted)
	}
	// A failed deletion leaves the row available and makes the failure visible.
	m.loading = false
	s.fail = true
	press(m, "d")
	m.input.SetValue("delete")
	m.Update(press(m, "enter")())
	if !strings.Contains(m.status, "denied") || len(m.items) != 1 {
		t.Fatal(m.status)
	}
}
func TestManagerSearchStaleRequestsAndPagedGists(t *testing.T) {
	m, _ := readyModel(t)
	old := m.selectItem(false)
	press(m, "down")
	m.Update(old())
	if strings.Contains(m.details, "Metadata for Alpha") {
		t.Fatal("stale details replaced selection")
	}
	press(m, "/")
	m.Update(tea.PasteMsg{Content: "世界"})
	press(m, "enter")
	if len(m.visible) != 1 || m.current().Title() != "Alpha 世界" {
		t.Fatal("search failed")
	}
	oldList := m.refresh(false)
	newList := press(m, "2")
	m.Update(oldList())
	if len(m.items) != 0 {
		t.Fatal("stale local list replaced GitHub")
	}
	msg := newList().(listMsg)
	msg.items = nil
	msg.next = 2
	m.Update(msg)
	if !strings.Contains(ansi.Strip(m.View().Content), "Press n") || press(m, "n") == nil {
		t.Fatal("empty filtered page cannot continue")
	}
}
func TestManagerResponsiveLayoutAndUntrustedMetadata(t *testing.T) {
	m, _ := readyModel(t)
	m.items[0].Local.Title = "\x1b]52;c;c2VjcmV0\aHello\x1b[2J世界\u202e"
	m.details = "\x1b]8;;https://hidden.invalid\x1b\\Long metadata\x1b]8;;\x1b\\\n" + strings.Repeat("Description 世界\n", 70)
	for _, size := range [][2]int{{40, 16}, {60, 24}, {80, 24}, {120, 36}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, focus := range []bool{false, true} {
			m.detailFocus = focus
			view := m.View().Content
			if strings.Contains(view, "52;") || strings.Contains(view, "hidden.invalid") || strings.Contains(view, "\u202e") {
				t.Fatal("terminal payload leaked")
			}
			lines := strings.Split(view, "\n")
			if len(lines) != size[1] {
				t.Fatalf("height=%d, want %d", len(lines), size[1])
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("line wider than %d: %q", size[0], line)
				}
			}
		}
	}
	press(m, "end")
	end := m.detailOffset
	press(m, "up")
	if m.detailOffset != end-1 {
		t.Fatal("cannot scroll back from end")
	}
	press(m, "d")
	m.confirm.Local.Path = "/" + strings.Repeat("long/", 200) + "recording.json"
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	if !strings.Contains(ansi.Strip(m.View().Content), "PgUp/PgDn") {
		t.Fatal("long deletion target cannot be reviewed")
	}
}

func testStore(t *testing.T) (*Store, string, Item) {
	t.Helper()
	s := &Store{Library: library.Library{Root: t.TempDir()}, GitHub: gh.New("")}
	path, _ := s.Library.Output("")
	r := &recording.Recording{ID: recording.NewID(), Title: "Test recording", Sizes: []int{80, 24}, Info: recording.Info{CLI: "v1.0.0"}, Metadata: &recording.Metadata{Version: 1, Description: "Description", CaptureSystem: &recording.SystemInfo{CPUModel: "Fixture CPU", RAMBytes: 16 << 30}}, Lines: []recording.Event{{Time: 10, Lines: []string{"password=private-secret"}}}}
	if err := recording.Save(path, r); err != nil {
		t.Fatal(err)
	}
	items, _, err := s.List(t.Context(), false, 1)
	if err != nil || len(items) != 1 {
		t.Fatal(err, items)
	}
	return s, path, items[0]
}

func TestStoreDeletesUnsupportedJournalAndItsIndex(t *testing.T) {
	s := &Store{Library: library.Library{Root: t.TempDir()}}
	path, err := s.Library.Output("")
	if err != nil {
		t.Fatal(err)
	}
	w, err := recording.NewWriter(path, recording.Recording{Sizes: []int{80, 24}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := s.Library.Register(path + ".partial"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path += ".partial"
	if err := os.WriteFile(path, []byte("{\"format\":\"tbt-journal-v99\",\"recording\":{\"s\":[80,24]}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	items, _, err := s.List(t.Context(), false, 1)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	item := items[0]
	if item.Path() != path || item.identity == nil || item.Local.Status != "unsupported" {
		t.Fatal("unsupported journal lost its deletion target", item)
	}
	if err := s.Delete(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("journal survived deletion", err)
	}
	items, _, err = (&Store{Library: library.Library{Root: s.Library.Root}}).List(t.Context(), false, 1)
	if err != nil || len(items) != 0 {
		t.Fatal("journal reappeared on restart", items, err)
	}
}
func TestStoreInspectScanImportAndRecoveryPreserveSource(t *testing.T) {
	s, path, item := testStore(t)
	before, _ := os.ReadFile(path)
	details, err := s.Inspect(t.Context(), item)
	if err != nil || !strings.Contains(details, "Fixture CPU") || strings.Contains(details, "private-secret") {
		t.Fatal(details, err)
	}
	report, err := s.Action(t.Context(), item, "scan")
	if err != nil || !strings.Contains(report, "1 likely") || strings.Contains(report, "private-secret") {
		t.Fatal(report, err)
	}
	if _, err = s.Action(t.Context(), item, "export"); err == nil {
		t.Fatal("removed export action accepted")
	}
	if _, err := os.Stat(filepath.Join(s.Library.Root, "exports")); !os.IsNotExist(err) {
		t.Fatalf("removed export action created exports: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("source changed")
	}
	if _, err = s.Import(path); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Library.Output("")
	w, err := recording.NewWriter(p, recording.Recording{Sizes: []int{80, 24}})
	if err != nil {
		t.Fatal(err)
	}
	w.Append(recording.Event{Time: 1, Lines: []string{"journal"}})
	w.Close()
	entry, err := s.Library.Register(p + ".partial")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Action(t.Context(), Item{Local: entry}, "recover"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(p + ".partial"); err != nil {
		t.Fatal("journal lost")
	}
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestEncryptedGistMetadataUsesReceiptWithoutDisplayingKey(t *testing.T) {
	s, path, _ := testStore(t)
	id := strings.Repeat("c", 32)
	_, key, files, err := sealed.EncryptFile(t.Context(), path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := "https://test.invalid/p/" + id + "#k=" + key
	if err = s.Library.SaveReceipt(path, link); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{}
	gist := &gh.GistSummary{ID: id}
	// Reuse the API JSON representation so the test covers real encrypted files.
	fileData := map[string]any{}
	for name, path := range files {
		b, _ := os.ReadFile(path)
		fileData[name] = map[string]any{"content": string(b), "size": len(b)}
	}
	data["files"] = fileData
	b, _ := json.Marshal(data)
	if err = json.Unmarshal(b, gist); err != nil {
		t.Fatal(err)
	}
	s.GitHub.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.String(), key) {
			t.Fatal("key sent to server")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})
	i := Item{Gist: gist}
	details, err := s.Inspect(t.Context(), i)
	if err != nil || !strings.Contains(details, "Fixture CPU") {
		t.Fatal(details, err)
	}
	if strings.Contains(i.Details()+details, key) {
		t.Fatal("key displayed")
	}
	s.Library.Root = t.TempDir()
	if _, err = s.Inspect(t.Context(), i); err == nil {
		t.Fatal("missing key accepted")
	}
}
