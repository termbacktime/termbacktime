// Package storage inventories private data and applies explicit, revalidated cleanup previews.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
	"golang.org/x/sys/unix"
)

type Total struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}
type Item struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind"`
	ID        string    `json:"id,omitempty"`
	Bytes     int64     `json:"bytes"`
	Files     int       `json:"files"`
	Modified  time.Time `json:"modified"`
	Eligible  bool      `json:"eligible"`
	Reason    string    `json:"reason,omitempty"`
	snapshots map[string]os.FileInfo
	directory os.FileInfo
}
type Report struct {
	Totals map[string]Total `json:"totals"`
	Items  []Item           `json:"items"`
}
type Filter struct {
	Kind      string
	OlderThan time.Duration
	MinSize   int64
}
type Result struct {
	Path    string `json:"path"`
	Deleted bool   `json:"deleted"`
	Error   string `json:"error,omitempty"`
}
type Store struct{ Root string }

func ParseSize(value string) (int64, error) {
	value = strings.TrimSpace(value)
	for _, unit := range []struct {
		name  string
		scale int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1000000000}, {"MB", 1000000}, {"KB", 1000}, {"B", 1}} {
		if strings.HasSuffix(value, unit.name) {
			n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(value, unit.name)), 10, 64)
			if err != nil || n < 0 || n > (1<<63-1)/unit.scale {
				return 0, fmt.Errorf("invalid size")
			}
			return n * unit.scale, nil
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("use bytes or an integer KiB/MiB/GiB size")
	}
	return n, nil
}
func ParseAge(value string) (time.Duration, error) {
	if strings.HasSuffix(value, "d") {
		n, err := strconv.ParseInt(strings.TrimSuffix(value, "d"), 10, 64)
		if err != nil || n < 0 || n > 36500 {
			return 0, fmt.Errorf("invalid age")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid age")
	}
	return d, nil
}
func (f Filter) Validate() error {
	if f.Kind != "" && f.Kind != "all" && f.Kind != "recordings" && f.Kind != "exports" && f.Kind != "queue" {
		return fmt.Errorf("kind must be recordings, exports, queue, or all")
	}
	if f.MinSize < 0 || f.OlderThan < 0 {
		return fmt.Errorf("filters must not be negative")
	}
	return nil
}
func (r Report) Select(f Filter, now time.Time) []Item {
	out := []Item{}
	for _, item := range r.Items {
		if item.Eligible && (f.Kind == "" || f.Kind == "all" || item.Kind == f.Kind) && item.Bytes >= f.MinSize && (f.OlderThan == 0 || !item.Modified.After(now.Add(-f.OlderThan))) {
			out = append(out, item)
		}
	}
	return out
}
func openRoot(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// Walk using directory descriptors so a replaced ancestor cannot redirect a read.
func walk(ctx context.Context, dir *os.File, prefix string, files, dirs map[string]os.FileInfo) error {
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := filepath.Join(prefix, e.Name())
		fd, err := unix.Openat(int(dir.Fd()), e.Name(), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			if errors.Is(err, unix.ELOOP) {
				dirs[rel] = nil // Preserve a queue containing a symlink or unsupported entry.
				continue
			}
			return err
		}
		f := os.NewFile(uintptr(fd), rel)
		st, err := f.Stat()
		if err == nil {
			if st.IsDir() {
				dirs[rel] = st
				err = walk(ctx, f, rel, files, dirs)
			} else if st.Mode().IsRegular() {
				files[rel] = st
			} else {
				dirs[rel] = st
			}
		}
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
func openRelative(root *os.File, rel string) (*os.File, error) {
	if filepath.IsAbs(rel) || rel == "." || strings.HasPrefix(rel, "../") || filepath.Clean(rel) != rel {
		return nil, fmt.Errorf("invalid managed path")
	}
	fd, err := unix.Dup(int(root.Fd()))
	if err != nil {
		return nil, err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), rel), nil
}
func read(root *os.File, rel string, limit int64) ([]byte, error) {
	f, err := openRelative(root, rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular managed file")
	}
	return recording.ReadBounded(f, limit)
}
func same(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
func idFor(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:16])
}

func (s Store) Snapshot(ctx context.Context) (Report, error) {
	report := Report{Totals: map[string]Total{"recordings": {}, "exports": {}, "queue": {}, "protected": {}, "external": {}}, Items: []Item{}}
	root, err := openRoot(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	defer root.Close()
	files, dirs := map[string]os.FileInfo{}, map[string]os.FileInfo{}
	if err := walk(ctx, root, "", files, dirs); err != nil {
		return report, err
	}
	indexed := map[string]library.Entry{}
	receipts := map[string]bool{}
	for rel, st := range files {
		first := strings.Split(rel, string(filepath.Separator))[0]
		kind := first
		if kind != "recordings" && kind != "exports" && kind != "queue" {
			kind = "protected"
		}
		total := report.Totals[kind]
		total.Files++
		total.Bytes += st.Size()
		report.Totals[kind] = total
		if first == "library" && strings.HasSuffix(rel, ".json") {
			data, err := read(root, rel, 1<<20)
			if err != nil {
				return report, err
			}
			var e library.Entry
			if json.Unmarshal(data, &e) == nil && filepath.IsAbs(e.Path) && e.ID == idFor(e.Path) {
				indexed[e.Path] = e

			}
		}
		if first == "shares" && strings.HasSuffix(rel, ".json") {
			data, err := read(root, rel, 16384)
			if err != nil {
				continue
			}
			var r library.Receipt
			if json.Unmarshal(data, &r) == nil && r.Encrypted {
				receipts[r.Link] = true
			}
		}
	}
	// Indexed paths outside the managed recordings directory remain external,
	// even if a user explicitly placed one elsewhere under the data root.
	external := map[string]bool{}
	for path, e := range indexed {
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) == 2 && parts[0] == "recordings" {
			continue
		}
		external[path] = true
		total := report.Totals["external"]
		total.Files++
		total.Bytes += max(0, e.Bytes)
		report.Totals["external"] = total
		if st := files[rel]; st != nil {
			kind := parts[0]
			if kind != "recordings" && kind != "exports" && kind != "queue" {
				kind = "protected"
			}
			total := report.Totals[kind]
			total.Files--
			total.Bytes -= st.Size()
			report.Totals[kind] = total
		}
	}
	jobs := map[string]*Item{}
	for rel, st := range files {
		parts := strings.Split(rel, string(filepath.Separator))
		item := Item{Path: filepath.Join(s.Root, rel), Bytes: st.Size(), Files: 1, Modified: st.ModTime(), snapshots: map[string]os.FileInfo{rel: st}}
		switch parts[0] {
		case "recordings":
			item.Kind = "recordings"
			item.Reason = "unsupported or recoverable recording"
			if len(parts) == 2 && strings.HasSuffix(rel, ".tbt") {
				e, ok := indexed[item.Path]
				if !ok {
					e = library.Entry{ID: idFor(item.Path), Path: item.Path, Status: "ready", Bytes: st.Size(), Modified: st.ModTime().UnixNano()}
				}
				if e.Status == "ready" && e.Bytes == st.Size() && e.Modified == st.ModTime().UnixNano() {
					item.ID = e.ID
					pinned, err := (library.Library{Root: s.Root}).Pinned(e.ID)
					if err != nil {
						return report, err
					}
					if pinned {
						item.Reason = "pinned"
					} else {
						f, err := openRelative(root, rel)
						if err != nil {
							return report, err
						}
						source, e := recording.OpenReader(f, st.Size())
						if e == nil {
							e = source.Walk(ctx, func(recording.Event) error { return nil })
							source.Close()
						}
						f.Close()
						if e == nil {
							source.Close()
							item.Eligible = true
							item.Reason = ""
						}
					}
					if index := filepath.Join("library", e.ID+".json"); files[index] != nil {
						item.snapshots[index] = files[index]
					}
				}
			}
			if files[rel+".partial"] != nil {
				item.Eligible = false
				item.Reason = "recovery journal exists"
			}
			report.Items = append(report.Items, item)
		case "exports":
			item.Kind = "exports"
			item.Eligible = true
			if external[item.Path] {
				item.Eligible = false
				item.Reason = "indexed external recording"
			}
			report.Items = append(report.Items, item)
		case "queue":
			if len(parts) < 3 || !recording.ValidID(parts[1]) {
				continue
			}
			job := jobs[parts[1]]
			if job == nil {
				job = &Item{Path: filepath.Join(s.Root, "queue", parts[1]), ID: parts[1], Kind: "queue", snapshots: map[string]os.FileInfo{}, directory: dirs[filepath.Join("queue", parts[1])], Reason: "pending or unresolved upload"}
				if job.directory != nil {
					job.Modified = job.directory.ModTime()
				}
				jobs[parts[1]] = job
			}
			job.snapshots[rel] = st
			job.Bytes += st.Size()
			job.Files++
			if st.ModTime().After(job.Modified) {
				job.Modified = st.ModTime()
			}
		}
	}
	for _, item := range jobs {
		path := filepath.Join("queue", item.ID)
		data, err := read(root, filepath.Join(path, "job.json"), 1<<20)
		var job uploadqueue.Job
		if err == nil && json.Unmarshal(data, &job) == nil && job.ID == item.ID && (job.State == "complete" || job.State == "canceled") {
			item.Eligible = true
			item.Reason = ""
			if job.State == "complete" && job.Encrypted {
				key, e := read(root, filepath.Join(path, "key.txt"), 128)
				link := job.Result + "#k=" + string(key)
				clear(key)
				if e != nil || !receipts[link] {
					item.Eligible = false
					item.Reason = "encrypted sharing receipt unavailable"
				}
			}
			for rel := range item.snapshots {
				if external[filepath.Join(s.Root, rel)] {
					item.Eligible = false
					item.Reason = "indexed external recording"
				}
			}
			for dir := range dirs {
				if strings.HasPrefix(dir, path+string(filepath.Separator)) {
					item.Eligible = false
					item.Reason = "unexpected queue subdirectory"
				}
			}
		}
		report.Items = append(report.Items, *item)
	}
	sort.Slice(report.Items, func(i, j int) bool { return report.Items[i].Path < report.Items[j].Path })
	return report, nil
}

// Apply deletes only entries from the supplied preview. A refreshed inventory may
// remove eligibility but can never add new files to the deletion set.
func (s Store) Apply(ctx context.Context, selected []Item) ([]Result, error) {
	if len(selected) == 0 {
		return []Result{}, ctx.Err()
	}
	checkRoot, err := openRoot(s.Root)
	if err != nil {
		return nil, err
	}
	checkRoot.Close()
	lib := library.Library{Root: s.Root}
	unlock, err := lib.MutationLock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	queue := false
	for _, i := range selected {
		queue = queue || i.Kind == "queue"
	}
	if queue {
		unlock, err := (uploadqueue.Store{Root: s.Root}).CleanupLock()
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	fresh, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	current := map[string]Item{}
	for _, i := range fresh.Items {
		current[i.Path] = i
	}
	root, err := openRoot(s.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	results := []Result{}
	for _, item := range selected {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		r := Result{Path: item.Path}
		now, ok := current[item.Path]
		if !ok || !now.Eligible || !item.Eligible || now.Kind != item.Kind || len(now.snapshots) != len(item.snapshots) {
			r.Error = "item changed or is protected; refresh the preview"
			results = append(results, r)
			continue
		}
		var failure error
		for path, expected := range item.snapshots {
			if !same(expected, now.snapshots[path]) {
				failure = fmt.Errorf("file changed; refresh the preview")
				break
			}
		}
		if failure == nil && item.Kind == "queue" && !same(item.directory, now.directory) {
			failure = fmt.Errorf("queue directory changed")
		}
		if failure == nil {
			paths := make([]string, 0, len(item.snapshots))
			for path := range item.snapshots {
				paths = append(paths, path)
			}
			sort.Slice(paths, func(i, j int) bool {
				if strings.HasPrefix(paths[i], "library/") != strings.HasPrefix(paths[j], "library/") {
					return !strings.HasPrefix(paths[i], "library/")
				}
				return paths[i] < paths[j]
			})
			for _, path := range paths {
				if err := ctx.Err(); err != nil {
					failure = err
					break
				}
				if failure = remove(root, path, item.snapshots[path], false); failure != nil {
					break
				}
			}
			if failure == nil && item.Kind == "queue" {
				failure = remove(root, filepath.Join("queue", item.ID), item.directory, true)
			}
		}
		if failure != nil {
			r.Error = failure.Error()
		} else {
			r.Deleted = true
		}
		results = append(results, r)
	}
	return results, nil
}
func remove(root *os.File, rel string, expected os.FileInfo, directory bool) error {
	parent, base := filepath.Split(rel)
	parent = strings.TrimSuffix(parent, string(filepath.Separator))
	dir := root
	var err error
	if parent != "" {
		dir, err = openRelative(root, parent)
		if err != nil {
			return err
		}
		defer dir.Close()
	}
	fd, err := unix.Openat(int(dir.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), rel)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if directory {
		if expected == nil || !st.IsDir() || !os.SameFile(st, expected) {
			return fmt.Errorf("directory replaced")
		}
		return unix.Unlinkat(int(dir.Fd()), base, unix.AT_REMOVEDIR)
	}
	if !st.Mode().IsRegular() || !same(st, expected) {
		return fmt.Errorf("file changed; refresh the preview")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("file is in use")
	}
	// Opening by name again verifies that the directory entry still denotes this file.
	check, err := unix.Openat(int(dir.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	verify := os.NewFile(uintptr(check), rel)
	actual, err := verify.Stat()
	verify.Close()
	if err != nil {
		return err
	}
	if !same(actual, st) {
		return fmt.Errorf("file replaced")
	}
	return unix.Unlinkat(int(dir.Fd()), base, 0)
}
