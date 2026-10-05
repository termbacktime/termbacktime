// Package manage provides the interactive recording library, without persisting
// remote recording contents or displaying private sharing capabilities.
package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sharing"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
)

type Item struct {
	Local    *library.Entry
	Gist     *gh.GistSummary
	identity os.FileInfo
	pending  bool
}

func (i Item) ID() string {
	if i.Local != nil {
		return i.Local.ID
	}
	return i.Gist.ID
}
func (i Item) Title() string {
	if i.Local != nil {
		return fallback(i.Local.Title, "Untitled recording")
	}
	return fallback(i.Gist.Description, "Untitled Gist")
}
func (i Item) Subtitle() string {
	if i.Local != nil {
		badges := i.Local.Status
		if i.pending {
			badges = "checking · " + badges
		}
		if i.Local.Enriched {
			if i.Local.Pinned {
				badges += " · pinned"
			}
			if i.Local.UploadCount > 0 {
				badges += fmt.Sprintf(" · %d uploads", i.Local.UploadCount)
			}
		} else {
			badges += " · badges pending"
		}
		return badges + " · " + date(i.Local.Started) + " · " + bytes(i.Local.Bytes)
	}
	visibility := "secret"
	if i.Gist.Public {
		visibility = "public"
	}
	if i.Gist.Encrypted() {
		visibility += " · encrypted"
	}
	return visibility + " · " + i.Gist.Updated.Format("2006-01-02")
}
func (i Item) Path() string {
	if i.Local == nil {
		return ""
	}
	if i.Local.Status == "partial" || i.Local.Status == "recording" {
		return i.Local.Path + ".partial"
	}
	if i.Local.Status == "invalid" || i.Local.Status == "unsupported" {
		if _, err := os.Lstat(i.Local.Path); errors.Is(err, os.ErrNotExist) {
			return i.Local.Path + ".partial"
		}
	}
	return i.Local.Path
}
func (i Item) Deletion() string {
	if i.Local == nil {
		if i.Gist.Storage == sharing.Repo {
			return "Remove this recording from " + i.Gist.Owner.Login + "/TBT-Recordings.\nEarlier copies remain in Git history. Local recordings and receipts are kept."
		}
		return "Permanently delete this entire GitHub Gist, including every companion file.\nhttps://gist.github.com/" + i.ID() + "\nLocal recordings are kept."
	}
	if i.Local.Status == "missing" {
		return "Remove this missing file's library entry:\n" + i.Local.Path + "\nSaved share receipts and GitHub Gists are kept."
	}
	return "Permanently delete this local recording file and its library entry:\n" + i.Path() + "\nThis includes imported files outside the data directory.\nSaved share receipts and GitHub Gists are kept."
}

type Store struct {
	Library library.Library
	GitHub  *gh.Client
}

func (s *Store) List(ctx context.Context, remote bool, page int) ([]Item, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	items := []Item{}
	if remote {
		client, err := s.GitHub.Backend(ctx, sharing.Gist)
		if err != nil {
			return nil, 0, err
		}
		gists, next, err := client.ListRecordings(ctx, page)
		for _, g := range gists {
			items = append(items, Item{Gist: &g})
		}
		return items, next, err
	}
	entries, err := s.Library.ListContext(ctx)
	for _, e := range entries {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		i := Item{Local: &e}
		i.identity, _ = os.Lstat(i.Path())
		items = append(items, i)
	}
	return items, 0, err
}
func (s *Store) Source(i Item) (string, error) {
	if i.Local != nil {
		if i.Local.Status != "ready" {
			return "", fmt.Errorf("recording is %s; recover inactive journals before using this action", i.Local.Status)
		}
		return i.Path(), nil
	}
	if !i.Gist.Encrypted() {
		return i.Gist.Reference().String(), nil
	}
	link, err := s.Library.GistShareLink(i.Gist.Reference().String())
	if err != nil {
		return "", err
	}
	if link == "" {
		return "", fmt.Errorf("encrypted metadata needs a saved share receipt; use play with the original complete link, or inspect the local copy")
	}
	return link, nil
}

// LoadPlayback leaves large local recordings on disk until events are requested.
func (s *Store) LoadPlayback(ctx context.Context, i Item) (*recording.Recording, error) {
	if i.Local != nil {
		return recording.OpenPlayback(i.Path())
	}
	return s.Load(ctx, i)
}

func (s *Store) Load(ctx context.Context, i Item) (*recording.Recording, error) {
	source, err := s.Source(i)
	if err != nil {
		return nil, err
	}
	if i.Local != nil {
		return recording.Load(source)
	}
	return s.GitHub.Load(ctx, source)
}
func (s *Store) Inspect(ctx context.Context, i Item) (string, error) {
	if i.Local != nil {
		r, err := recording.Preview(ctx, i.Path())
		if err != nil {
			return "", err
		}
		if strings.HasSuffix(i.Path(), ".partial") {
			r.Duration, r.Events = i.Local.Duration, i.Local.Events
			r.Verification = "preview; journal duration cached from library; event count unavailable"
			if i.Local.Events > 0 {
				r.Verification = "preview; journal totals cached from library"
			}
		}
		return describe(&r.Recording, r.Duration, r.Events) + "\n\n" + r.Verification + " · v verifies all events", nil
	}
	r, err := s.Load(ctx, i)
	if err != nil {
		return "", err
	}
	return describe(r, recording.Duration(r), len(r.Lines)), nil
}
func (s *Store) Delete(ctx context.Context, i Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if i.Local != nil {
		return s.Library.Delete(*i.Local, i.identity)
	}
	client, err := s.GitHub.Backend(ctx, i.Gist.Reference().Storage)
	if err != nil {
		return err
	}
	if i.Gist.Storage == sharing.Repo {
		return client.DeleteRepository(ctx, *i.Gist)
	}
	return client.DeleteRecording(ctx, i.ID())
}
func (s *Store) Action(ctx context.Context, i Item, action string) (string, error) {
	queue := uploadqueue.Store{Root: s.Library.Root}
	if action == "queue-list" || action == "queue-run" {
		if action == "queue-run" {
			if s.GitHub == nil {
				return "", fmt.Errorf("authenticate with GitHub first")
			}
			if err := queue.Run(ctx, s.GitHub, func(uploadqueue.Job) {}); err != nil {
				return "", err
			}
		}
		jobs, err := queue.List()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for index, job := range jobs {
			fmt.Fprintf(&b, "%d. %s · %s · %d/%d bytes\n%s\n", index+1, job.ID, job.State, job.Sent, job.Total, job.Error)
		}
		if len(jobs) == 0 {
			b.WriteString("Upload queue is empty")
		}
		return b.String(), nil
	}
	if id, ok := strings.CutPrefix(action, "queue-retry:"); ok {
		return "Retry queued; press U to run", queue.Retry(id)
	}
	if id, ok := strings.CutPrefix(action, "queue-cancel:"); ok {
		return "Cancellation requested", queue.Cancel(id)
	}

	if action == "recover" {
		if i.Local == nil || i.Local.Status != "partial" {
			return "", fmt.Errorf("select an inactive partial journal to recover")
		}
		path, err := s.Library.Output("")
		if err != nil {
			return "", err
		}
		if err = recording.RecoverFile(i.Path(), path); err != nil {
			return "", err
		}
		if _, err = s.Library.Register(path); err != nil {
			return "", err
		}
		return "Recovered to " + path + "\nOriginal journal preserved. Press g to refresh.", nil
	}
	r, err := s.Load(ctx, i)
	if err != nil {
		return "", err
	}
	switch action {
	case "scan":
		findings := recording.Scan(r)
		if len(findings) == 0 {
			return "No likely secrets detected. Heuristic scanning cannot guarantee every secret is found.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d likely secret(s) · values masked\n\n", len(findings))
		for j, f := range findings {
			if j == 100 {
				b.WriteString("More findings omitted; run termbacktime scan for the complete report.\n")
				break
			}
			fmt.Fprintf(&b, "%s · %s · %s\n", time.Duration(f.At)*time.Millisecond, f.Field, f.Kind)
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("unknown action")
}
func (s *Store) Import(path string) (string, error) {
	e, err := s.Library.Register(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	return "Indexed " + e.Path + " without moving it. Press g to refresh.", nil
}

func (i Item) Details() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n\n", i.Title(), i.Subtitle())
	if i.Local != nil {
		fmt.Fprintf(&b, "Location\n%s\n\nLibrary ID\n%s\n\nDuration  %s\nSize      %s\n", i.Path(), i.ID(), time.Duration(i.Local.Duration)*time.Millisecond, bytes(i.Local.Bytes))
	} else {
		g := i.Gist
		kind := "GitHub Gist"
		if g.Storage == sharing.Repo {
			kind = "GitHub repository - TBT-Recordings"
		}
		fmt.Fprintf(&b, kind+"\n%s\nOwner     %s\nCreated   %s\nUpdated   %s\n\nFiles\n", g.ID, g.Owner.Login, g.Created.Format(time.RFC3339), g.Updated.Format(time.RFC3339))
		names := make([]string, 0, len(g.Files))
		for name := range g.Files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "%s · %s\n", name, bytes(g.Files[name].Size))
		}
		b.WriteString("\nEnter: load recording metadata on demand.\n")
		if g.Encrypted() {
			b.WriteString("Decryption uses a saved private share receipt when available. Keys are never displayed.\n")
		}
	}
	return b.String()
}
func describe(r *recording.Recording, duration int64, events int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Recording metadata\nTitle     %s\nRecorded  %s\nDuration  %s\nEvents    %d\nTerminal  %d × %d\n", fallback(r.Title, "Untitled"), date(r.Started), time.Duration(duration)*time.Millisecond, events, r.Sizes[0], r.Sizes[1])
	for _, row := range [][2]string{{"Recording ID", r.ID}, {"Capture CLI", r.Info.CLI}, {"Upload CLI", r.Info.UploadCLI}, {"Platform", strings.TrimSpace(r.Info.OS + " " + r.Info.Arch)}} {
		if row[1] != "" {
			fmt.Fprintf(&b, "%s  %s\n", row[0], row[1])
		}
	}
	if r.Metadata != nil {
		fmt.Fprintf(&b, "\nDescription\n%s\n", fallback(r.Metadata.Description, "None"))
		for _, group := range []struct {
			name string
			s    *recording.SystemInfo
		}{{"Capture computer", r.Metadata.CaptureSystem}, {"Upload computer", r.Metadata.UploadSystem}} {
			if group.s == nil {
				continue
			}
			v := group.s
			fmt.Fprintf(&b, "\n%s\n%s %s %s\nCPU  %s\nCores  %d physical / %d logical\nRAM  %s\nDisk  %s\n", group.name, v.OS, v.OSVersion, v.Arch, fallback(v.CPUModel, "Unavailable"), v.PhysicalCores, v.LogicalCores, bytes(int64(v.RAMBytes)), bytes(int64(v.DiskTotalBytes)))
		}
	}

	return b.String()
}

// Metadata is untrusted terminal text; strip escape sequences and control codes.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && r != '\n') || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, ansi.Strip(s))
}
func fallback(s, empty string) string {
	if strings.TrimSpace(s) == "" {
		return empty
	}
	return s
}
func date(v int64) string {
	if v == 0 {
		return "Unknown"
	}
	return time.Unix(v, 0).Format("2006-01-02 15:04")
}
func bytes(v int64) string {
	if v < 1024 {
		return fmt.Sprintf("%d B", v)
	}
	if v < 1<<20 {
		return fmt.Sprintf("%.1f KiB", float64(v)/1024)
	}
	if v < 1<<30 {
		return fmt.Sprintf("%.1f MiB", float64(v)/(1<<20))
	}
	return fmt.Sprintf("%.1f GiB", float64(v)/(1<<30))
}
