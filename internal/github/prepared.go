package github

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sealed"
)

func JobFilename(id string) string { return "termbacktime-upload-" + id + ".json" }
func (c *Client) Identity(ctx context.Context) (int64, error) {
	b, err := c.request(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return 0, err
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(b, &user) != nil || user.ID <= 0 {
		return 0, fmt.Errorf("could not verify GitHub identity")
	}
	return user.ID, nil
}

// PrepareUpload persists the exact request and encryption key before any creation request is sent.
func PrepareUpload(ctx context.Context, path, dir, jobID, title string, encrypted bool, options UploadOptions) (string, error) {
	files := map[string]string{}
	if !encrypted {
		wrapped := filepath.Join(dir, Filename)
		if err := recording.Publish(wrapped, func(w io.Writer) error {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			return recording.WriteWrapped(w, contextReader{ctx, f})
		}); err != nil {
			return "", err
		}
		files[Filename] = wrapped
	}
	key := ""
	if encrypted {
		_, k, parts, err := sealed.EncryptFile(ctx, path, dir)
		if err != nil {
			return "", err
		}
		key, files = k, parts
		title = "Encrypted terminal recording"
	}
	if options.Metadata != "" {
		p := filepath.Join(dir, PlaybackFilename)
		if err := os.WriteFile(p, []byte(options.Metadata), 0600); err != nil {
			return "", err
		}
		files[PlaybackFilename] = p
	}
	companion := filepath.Join(dir, JobFilename(jobID))
	data, _ := json.Marshal(map[string]string{"format": "tbt-upload-job-v1", "id": jobID})
	if err := os.WriteFile(companion, data, 0600); err != nil {
		return "", err
	}
	files[JobFilename(jobID)] = companion
	err := recording.Publish(filepath.Join(dir, "request.json"), func(destination io.Writer) error {
		w := bufio.NewWriterSize(destination, 65536)
		description, _ := json.Marshal(title)
		if _, err := fmt.Fprintf(w, `{"description":%s,"public":%t,"files":{`, description, options.Public); err != nil {
			return err
		}
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names)
		for i, name := range names {
			if i > 0 {
				if err := w.WriteByte(','); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(w, `%q:{"content":`, name); err != nil {
				return err
			}
			f, err := os.Open(files[name])
			if err != nil {
				return err
			}
			err = writeJSONString(w, contextReader{ctx, f})
			f.Close()
			if err != nil {
				return err
			}
			if err = w.WriteByte('}'); err != nil {
				return err
			}
		}
		if _, err := w.WriteString("}}"); err != nil {
			return err
		}
		return w.Flush()
	})
	return key, err
}

type uploadProgress struct {
	mu     sync.Mutex
	source io.Reader
	sent   int64
	last   time.Time
	report func(int64)
}

func (r *uploadProgress) Read(p []byte) (int, error) {
	n, e := r.source.Read(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent += int64(n)
	if r.report != nil && (time.Since(r.last) > 200*time.Millisecond || e != nil) {
		r.report(r.sent)
		r.last = time.Now()
	}
	return n, e
}
func (c *Client) SendPrepared(ctx context.Context, path string, progress func(int64)) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 6*recording.MaxBytes {
		return "", fmt.Errorf("invalid prepared upload")
	}
	reader := &uploadProgress{source: file, report: progress}
	defer func() { reader.mu.Lock(); reader.report = nil; reader.mu.Unlock() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/gists", reader)
	if err != nil {
		return "", err
	}
	req.ContentLength = info.Size()
	response, err := c.send(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	return readUploadedID(response.Body)
}
func (c *Client) PlaybackLink(id, key string) string {
	origin := strings.TrimRight(c.SiteURL, "/")
	if origin == "" {
		return "https://gist.github.com/" + id
	}
	link := origin + "/p/" + id
	if key != "" {
		link += "#k=" + key
	}
	return link
}

// Reconcile searches companion identifiers, then verifies ownership and content. No POST is retried here.
func (c *Client) Reconcile(ctx context.Context, jobID string, owner int64) ([]string, error) {
	matches := []string{}
	for page := 1; page <= 100; {
		entries, next, err := c.ListRecordings(ctx, page)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if _, ok := entry.Files[JobFilename(jobID)]; !ok {
				continue
			}
			if entry.Owner.ID != owner {
				return nil, fmt.Errorf("upload identifier has conflicting ownership")
			}
			b, err := c.request(ctx, http.MethodGet, "https://api.github.com/gists/"+entry.ID, nil)
			if err != nil {
				return nil, err
			}
			var gist struct {
				Files map[string]gistFile `json:"files"`
				Owner struct {
					ID int64 `json:"id"`
				} `json:"owner"`
			}
			if json.Unmarshal(b, &gist) != nil || gist.Owner.ID != owner {
				return nil, fmt.Errorf("could not verify upload owner")
			}
			data, err := c.fileBytes(ctx, gist.Files[JobFilename(jobID)], 4096)
			if err != nil {
				return nil, err
			}
			var receipt struct{ Format, ID string }
			if json.Unmarshal(data, &receipt) != nil || receipt.Format != "tbt-upload-job-v1" || receipt.ID != jobID {
				return nil, fmt.Errorf("conflicting upload identifier")
			}
			matches = append(matches, entry.ID)
		}
		if next == 0 {
			return matches, nil
		}
		page = next
	}
	return nil, fmt.Errorf("reconciliation exceeded 100 Gist pages; inspect this job manually")
}
