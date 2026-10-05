package manage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestDeleteFailureKeepsListNavigationAndFailureVisible(t *testing.T) {
	m, backend := readyModel(t)
	backend.fail = true
	press(m, "d")
	m.input.SetValue("delete")
	m.update(press(m, "enter")())
	if m.busy || m.mode != "" || !strings.Contains(m.status, "denied") {
		t.Fatal("failure did not return control", m.mode, m.status)
	}
	press(m, "down")
	if m.selected != 1 || len(m.items) != 2 || !strings.Contains(m.status, "denied") {
		t.Fatal("failed deletion trapped navigation or lost its error", m.selected, m.status)
	}
}

func TestConfirmedDeleteRemovesRowBeforeRefreshAndPreservesNeighbor(t *testing.T) {
	m, _ := readyModel(t)
	press(m, "tab")
	press(m, "d")
	m.input.SetValue("delete")
	_, refresh := m.update(press(m, "enter")())
	if len(m.items) != 1 || m.current().Title() != "Beta" || m.detailFocus || refresh == nil {
		t.Fatal("confirmed deletion left stale row or pane focus", len(m.items), m.detailFocus)
	}
}

type canceledDeleteStore struct {
	fakeStore
	started chan struct{}
}

func (s *canceledDeleteStore) Delete(ctx context.Context, _ Item) error {
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestPendingDeleteShowsCancelableOperation(t *testing.T) {
	s := &canceledDeleteStore{fakeStore: fakeStore{items: sampleItems()}, started: make(chan struct{})}
	m := newModel(t.Context(), s, false, nil)
	m.update(m.Init()())
	press(m, "d")
	m.input.SetValue("delete")
	command := press(m, "enter")
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Deleting recording") || !strings.Contains(view, "Esc cancels") || strings.Contains(view, "Recordings 1/2") {
		t.Fatal("pending deletion looks like an unresponsive list", view)
	}
	results := make(chan any, 1)
	go func() { results <- command() }()
	<-s.started
	press(m, "esc")
	select {
	case result := <-results:
		m.update(result)
	case <-time.After(3 * time.Second):
		t.Fatal("deletion did not cancel")
	}
	press(m, "down")
	if m.busy || m.mode != "" || m.selected != 1 || len(m.items) != 2 {
		t.Fatal("cancellation did not restore list navigation")
	}
}

func TestStaleActionAndUploadRepliesDoNotChangeCurrentOperation(t *testing.T) {
	m, _ := readyModel(t)
	old := m.beginAction("Old action", "", func(context.Context) actionMsg { return actionMsg{text: "Old result", details: true} })
	current := m.beginAction("Current action", "", func(context.Context) actionMsg { return actionMsg{text: "Current result"} })
	m.update(old())
	m.update(uploadLinkMsg{epoch: m.upload.epoch})
	if !m.busy || m.mode != "action" || m.detailFocus || m.status != "Current action…" {
		t.Fatal("stale result changed current operation", m.mode, m.status)
	}
	m.update(current())
	if m.busy || m.mode != "" || m.status != "Current result" {
		t.Fatal("current operation did not finish")
	}
}

type inspectingStore struct {
	fakeStore
	started, canceled, release chan struct{}
}

func (s *inspectingStore) Inspect(ctx context.Context, _ Item) (string, error) {
	close(s.started)
	<-ctx.Done()
	close(s.canceled)
	<-s.release
	return "", ctx.Err()
}

func (s *inspectingStore) Delete(ctx context.Context, item Item) error {
	select {
	case <-s.release:
		return s.fakeStore.Delete(ctx, item)
	default:
		return context.DeadlineExceeded // Simulates a file still locked by the canceled inspection.
	}
}

func TestDeleteWaitsForCanceledInspectionToReleaseFile(t *testing.T) {
	s := &inspectingStore{fakeStore: fakeStore{items: sampleItems()}, started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	m := newModel(t.Context(), s, false, nil)
	_, inspect := m.update(m.Init()())
	inspection := make(chan any, 1)
	go func() { inspection <- inspect() }()
	<-s.started
	press(m, "d")
	m.input.SetValue("delete")
	command := press(m, "enter")
	<-s.canceled
	result := make(chan any, 1)
	go func() { result <- command() }()
	select {
	case <-result:
		t.Fatal("deletion ran before inspection released the file")
	case <-time.After(20 * time.Millisecond):
	}
	close(s.release)
	select {
	case msg := <-result:
		m.update(msg)
	case <-time.After(3 * time.Second):
		t.Fatal("delete stayed blocked after inspection ended")
	}
	m.update(<-inspection)
	if len(m.items) != 1 || m.current().Title() != "Beta" || strings.Contains(m.details, "canceled") {
		t.Fatal("delete failed or stale inspection replaced the next item", m.status, m.details)
	}
}
