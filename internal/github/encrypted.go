package github

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/termbacktime/termbacktime/internal/buildinfo"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sealed"
)

func (c *Client) CheckEncryption(ctx context.Context) error {
	if c.SiteURL == "" {
		return fmt.Errorf("encrypted uploads require a playback endpoint; set SITE_URL or use --no-encrypt")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.SiteURL, "/")+"/api/v1/config", nil)
	if err != nil {
		return err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("playback endpoint unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("playback endpoint unavailable (HTTP %d)", res.StatusCode)
	}
	b, err := recording.ReadBounded(res.Body, 16384)
	if err != nil {
		return err
	}
	var data struct {
		Formats []string `json:"recordingFormats"`
	}
	if json.Unmarshal(b, &data) != nil {
		return fmt.Errorf("invalid playback configuration")
	}
	encrypted, archive := false, false
	for _, v := range data.Formats {
		encrypted = encrypted || v == sealed.Format
		archive = archive || v == recording.Format
	}
	if encrypted && archive {
		return nil
	}
	return fmt.Errorf("update the website before uploading encrypted recordings")
}

func (c *Client) UploadEncrypted(ctx context.Context, path string, options ...UploadOptions) (string, error) {
	settings := uploadOptions(options)
	if c.Token == "" {
		return "", fmt.Errorf("upload requires authentication; run termbacktime auth")
	}
	dir, err := os.MkdirTemp("", "termbacktime-encrypted-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	_, key, files, err := sealed.EncryptFile(ctx, path, dir)
	if err != nil {
		return "", err
	}
	if settings.Metadata != "" {
		p := filepath.Join(dir, PlaybackFilename)
		if err := os.WriteFile(p, []byte(settings.Metadata), 0600); err != nil {
			return "", err
		}
		files[PlaybackFilename] = p
	}
	payload, err := os.CreateTemp(dir, "request-*")
	if err != nil {
		return "", err
	}
	defer payload.Close()
	w := bufio.NewWriterSize(payload, 65536)
	if _, err := fmt.Fprintf(w, `{"description":"Encrypted terminal recording","public":%t,"files":{`, settings.Public); err != nil {
		return "", err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		if i > 0 {
			if err := w.WriteByte(','); err != nil {
				return "", err
			}
		}
		if _, err := fmt.Fprintf(w, `%q:{"content":`, name); err != nil {
			return "", err
		}
		f, err := os.Open(files[name])
		if err != nil {
			return "", err
		}
		err = writeJSONString(w, contextReader{ctx, f})
		f.Close()
		if err != nil {
			return "", err
		}
		if err := w.WriteByte('}'); err != nil {
			return "", err
		}
	}
	if _, err := w.WriteString("}}"); err != nil {
		return "", err
	}
	if err := w.Flush(); err != nil {
		return "", err
	}
	size, err := payload.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", err
	}
	if _, err := payload.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	reader := &uploadProgress{source: payload, report: func(sent int64) {
		if report := uploadOptions(options).Progress; report != nil {
			report(sent, size)
		}
	}}
	defer func() { reader.mu.Lock(); reader.report = nil; reader.mu.Unlock() }()
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.github.com/gists", reader)
	if err != nil {
		return "", err
	}
	req.ContentLength = size
	res, err := c.send(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	id, err := readUploadedID(res.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(c.SiteURL, "/") + "/p/" + id + "#k=" + key, nil
}

type gistFile struct {
	Content   string `json:"content"`
	RawURL    string `json:"raw_url"`
	Truncated bool   `json:"truncated"`
}

func (c *Client) fileBytes(ctx context.Context, f gistFile, max int64) ([]byte, error) {
	if !f.Truncated {
		if int64(len(f.Content)) > max {
			return nil, fmt.Errorf("Gist file is too large")
		}
		return []byte(f.Content), nil
	}
	u, err := url.Parse(f.RawURL)
	if err != nil || u.Scheme != "https" || u.Host != "gist.githubusercontent.com" || u.User != nil {
		return nil, fmt.Errorf("invalid Gist download URL")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	res, err := c.send(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return recording.ReadBounded(res.Body, max)
}
func (c *Client) loadEncrypted(ctx context.Context, input string, files map[string]gistFile) (*recording.Recording, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	u, err := url.Parse(input)
	if err != nil {
		return nil, err
	}
	fragment, _ := url.ParseQuery(u.Fragment)
	key := fragment.Get("k")
	if key == "" {
		return nil, fmt.Errorf("encrypted recording requires its complete sharing link")
	}
	b, err := c.fileBytes(ctx, files[sealed.Filename], 16384)
	if err != nil {
		return nil, err
	}
	var m sealed.Manifest
	if json.Unmarshal(b, &m) != nil {
		return nil, fmt.Errorf("invalid encrypted manifest")
	}
	plain, err := sealed.Decrypt(ctx, m, key, func(name string) ([]byte, error) {
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("missing encrypted Gist part")
		}
		return c.fileBytes(ctx, f, sealed.MaxPartBytes)
	})
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	return recording.Decode(bytes.NewReader(plain))
}

// UploadSource stamps the upload version in an isolated copy, preserving capture provenance.
func UploadSource(path, title string, prepare ...func(*recording.Recording) error) (string, func(), error) {
	r, err := recording.Load(path)
	if err != nil {
		return "", nil, err
	}
	if title != "" {
		r.Title = title
	}
	r.Info.UploadCLI = buildinfo.Tag()
	for _, fn := range prepare {
		if err := fn(r); err != nil {
			return "", nil, err
		}
	}
	dir, err := os.MkdirTemp("", "termbacktime-metadata-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	out := filepath.Join(dir, "recording.tbt")
	if err := recording.Publish(out, func(w io.Writer) error { return recording.EncodeSmall(w, r) }); err != nil {
		cleanup()
		return "", nil, err
	}
	return out, cleanup, nil
}
