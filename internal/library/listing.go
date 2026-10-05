package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/termbacktime/termbacktime/internal/recording"
)

type ListUpdate struct {
	Entries        []Entry
	Warnings       []string
	Checked, Total int
	Begin, Done    bool
}

// StreamList only persists fully inspected entries. Cached rows are display data.
func (l Library) StreamList(ctx context.Context, emit func(ListUpdate) error) error {
	indexed := map[string]*Entry{}
	paths := map[string]string{}
	if err := emit(ListUpdate{Begin: true}); err != nil {
		return err
	}
	batch := []Entry{}
	last := time.Now()
	checked, total := 0, 0
	flush := func(force bool) error {
		if len(batch) == 0 || (!force && len(batch) < 100 && time.Since(last) < 50*time.Millisecond) {
			return nil
		}
		err := emit(ListUpdate{Entries: batch, Checked: checked, Total: total})
		batch = nil
		last = time.Now()
		return err
	}
	files, err := os.ReadDir(filepath.Join(l.Root, "library"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !f.Type().IsRegular() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		e, err := l.readEntry(strings.TrimSuffix(f.Name(), ".json"))
		if err != nil {
			if err = emit(ListUpdate{Warnings: []string{fmt.Sprintf("Index %s: %v", f.Name(), err)}}); err != nil {
				return err
			}
			continue
		}
		indexed[e.Path], paths[e.Path] = e, e.Path
		batch = append(batch, *e)
		if err := flush(false); err != nil {
			return err
		}
	}
	if err := flush(true); err != nil {
		return err
	}
	files, err = os.ReadDir(filepath.Join(l.Root, "recordings"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !f.Type().IsRegular() || (!strings.HasSuffix(f.Name(), ".tbt") && !strings.HasSuffix(f.Name(), ".json") && !strings.HasSuffix(f.Name(), ".partial")) {
			continue
		}
		path := filepath.Join(l.Root, "recordings", f.Name())
		base := strings.TrimSuffix(path, ".partial")
		if _, ok := paths[base]; !ok {
			batch = append(batch, Entry{ID: pathID(base), Path: base, Title: filepath.Base(base), Status: "checking"})
		}
		if _, ok := paths[base]; !ok || !strings.HasSuffix(path, ".partial") {
			paths[base] = path
		}
		if err := flush(false); err != nil {
			return err
		}
	}
	if err := flush(true); err != nil {
		return err
	}
	enrichment, warnings, err := l.Enrichment(ctx)
	if err != nil {
		return err
	}
	if err := emit(ListUpdate{Warnings: warnings, Total: len(paths)}); err != nil {
		return err
	}
	total = len(paths)
	keys := make([]string, 0, len(paths))
	for path := range paths {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	for n, base := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := paths[base]
		if _, err := os.Stat(base); err == nil {
			path = base
		} else if !strings.HasSuffix(path, ".partial") {
			path = base + ".partial"
		}
		entry, err := l.registerContext(ctx, path, indexed[base])
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			e := Entry{ID: pathID(base), Path: base, Title: filepath.Base(base)}
			if old := indexed[base]; old != nil {
				e = *old
			}
			if errors.Is(err, os.ErrNotExist) {
				e.Status = "missing"
			} else if errors.Is(err, recording.ErrUnsupported) {
				e.Status = "unsupported"
			} else {
				e.Status = "invalid"
			}
			entry = &e
		}
		entry.Checked = true
		entry.Enriched = enrichment.Known
		entry.Pinned = enrichment.Pins[entry.ID]
		entry.UploadCount = enrichment.Uploads[entry.ID]
		checked = n + 1
		batch = append(batch, *entry)
		if err := flush(false); err != nil {
			return err
		}
	}
	if err := flush(true); err != nil {
		return err
	}
	return emit(ListUpdate{Done: true, Checked: len(paths), Total: len(paths)})
}

type Filter struct {
	SortBy, Order    string
	Statuses         []string
	Pinned, Uploaded *bool
}

func (f Filter) Validate() error {
	if f.SortBy != "" && f.SortBy != "date" && f.SortBy != "title" && f.SortBy != "duration" && f.SortBy != "size" {
		return fmt.Errorf("sort must be date, title, duration, or size")
	}
	if f.Order != "" && f.Order != "asc" && f.Order != "desc" {
		return fmt.Errorf("order must be asc or desc")
	}
	for _, status := range f.Statuses {
		switch status {
		case "ready", "recording", "partial", "missing", "unsupported", "invalid":
		default:
			return fmt.Errorf("invalid recording status %q", status)
		}
	}
	return nil
}
func (f Filter) Match(e Entry) bool {
	if len(f.Statuses) > 0 {
		found := false
		for _, s := range f.Statuses {
			found = found || s == e.Status
		}
		if !found {
			return false
		}
	}
	if f.Pinned != nil && (!e.Enriched || e.Pinned != *f.Pinned) {
		return false
	}
	if f.Uploaded != nil && (!e.Enriched || (e.UploadCount > 0) != *f.Uploaded) {
		return false
	}
	return true
}
func Sort(entries []Entry, by, order string) {
	if by == "" {
		by = "date"
	}
	if order == "" {
		order = "desc"
		if by == "title" {
			order = "asc"
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		cmp := 0
		if by == "title" {
			cmp = strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		} else {
			x, y := a.Started, b.Started
			if by == "duration" {
				x, y = a.Duration, b.Duration
			}
			if by == "size" {
				x, y = a.Bytes, b.Bytes
			}
			if x < y {
				cmp = -1
			} else if x > y {
				cmp = 1
			}
		}
		if cmp == 0 {
			return a.ID < b.ID
		}
		if order == "desc" {
			return cmp > 0
		}
		return cmp < 0
	})
}

func (l Library) waitMutationLock(ctx context.Context) (func(), error) {
	for {
		unlock, err := l.MutationLock()
		if err == nil {
			return unlock, nil
		}
		if !strings.Contains(err.Error(), "already in progress") {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}
