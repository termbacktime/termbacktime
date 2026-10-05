package github

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/termbacktime/termbacktime/internal/recording"
)

// UploadFile streams a finalized recording through a private file containing the Gist request
type UploadOptions struct {
	Public   bool
	Metadata string
	Progress func(sent, total int64)
}

func uploadOptions(options []UploadOptions) UploadOptions {
	if len(options) > 0 {
		return options[0]
	}
	return UploadOptions{}
}

func (c *Client) UploadFile(ctx context.Context, path, title string, options ...UploadOptions) (link string, resultErr error) {
	if c.Token == "" {
		return "", fmt.Errorf("upload requires authentication; run termbacktime auth")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	source, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > recording.MaxBytes {
		return "", fmt.Errorf("invalid or oversized recording file")
	}
	if len(title) > recording.MaxJournalLineBytes {
		return "", fmt.Errorf("recording title is too large")
	}

	// Keep request data alongside the recording so --no-save owns all temporary artifacts
	payload, err := os.CreateTemp(filepath.Dir(path), ".tbt-upload-*")
	if err != nil {
		return "", err
	}
	defer func() {
		payload.Close()
		if err := os.Remove(payload.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary upload %s: %w", payload.Name(), err))
		}
	}()
	if err := writeUpload(ctx, payload, source, title, options...); err != nil {
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
	// An exact content length avoids requiring chunked uploads or an in-memory request body
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
	if c.SiteURL != "" {
		return strings.TrimRight(c.SiteURL, "/") + "/p/" + id, nil
	}
	return "https://gist.github.com/" + id, nil
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

func writeUpload(ctx context.Context, destination io.Writer, source io.Reader, title string, options ...UploadOptions) error {
	settings := uploadOptions(options)
	out := bufio.NewWriterSize(destination, 64<<10)
	description, err := json.Marshal(title)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, `{"description":%s,"public":%t,"files":{%q:{"content":`, description, settings.Public, Filename); err != nil {
		return err
	}
	input := &io.LimitedReader{R: contextReader{ctx, source}, N: recording.MaxBytes + 1}
	if err := writeRecordingJSONString(out, input); err != nil {
		return err
	}
	if input.N == 0 {
		return fmt.Errorf("recording exceeds %d byte limit", recording.MaxBytes)
	}
	if _, err := out.WriteString("}"); err != nil {
		return err
	}
	if settings.Metadata != "" {
		if _, err := fmt.Fprintf(out, `,%q:{"content":`, PlaybackFilename); err != nil {
			return err
		}
		if err := writeJSONString(out, strings.NewReader(settings.Metadata)); err != nil {
			return err
		}
		if err := out.WriteByte('}'); err != nil {
			return err
		}
	}
	if _, err := out.WriteString("}}"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return out.Flush()
}

// Escape a JSON string incrementally while preserving UTF-8 across input buffer boundaries
func writeJSONString(out *bufio.Writer, source io.Reader) error {
	if err := out.WriteByte('"'); err != nil {
		return err
	}

	input := bufio.NewReaderSize(source, 32<<10)
	const digits = "0123456789abcdef"
	for {
		if _, err := input.Peek(1); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		available := input.Buffered()
		data, _ := input.Peek(available)
		n := 0
		for n < len(data) {
			b := data[n]
			if b < ' ' || b == '"' || b == '\\' || b >= utf8.RuneSelf {
				break
			}
			n++
		}
		if n > 0 {
			if _, err := out.Write(data[:n]); err != nil {
				return err
			}
			_, _ = input.Discard(n)
			continue
		}
		r, _, err := input.ReadRune()
		if err != nil {
			return err
		}
		switch {
		case r == '"' || r == '\\':
			if err := out.WriteByte('\\'); err != nil {
				return err
			}
			if err := out.WriteByte(byte(r)); err != nil {
				return err
			}
		case r < 0x20:
			escaped := [6]byte{'\\', 'u', '0', '0', digits[r>>4], digits[r&15]}
			if _, err := out.Write(escaped[:]); err != nil {
				return err
			}
		default:
			if _, err := out.WriteRune(r); err != nil {
				return err
			}
		}
	}
	return out.WriteByte('"')
}

// Stop after the ID so a Gist response cannot load an echoed recording into memory
func readUploadedID(source io.Reader) (string, error) {
	decoder := json.NewDecoder(io.LimitReader(source, 4<<20))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return "", fmt.Errorf("invalid Gist response")
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("invalid or oversized Gist response")
		}
		if key == "id" {
			var id string
			if err := decoder.Decode(&id); err != nil || !idPattern.MatchString(id) {
				return "", fmt.Errorf("invalid Gist response")
			}
			return id, nil
		}
		// Skip unrelated fields without retaining nested objects or arrays
		depth := 0
		for {
			token, err := decoder.Token()
			if err != nil {
				return "", fmt.Errorf("invalid or oversized Gist response")
			}
			if delimiter, ok := token.(json.Delim); ok {
				if delimiter == '{' || delimiter == '[' {
					depth++
				} else {
					depth--
				}
			}
			if depth > 64 {
				return "", fmt.Errorf("Gist response nesting is too deep")
			}
			if depth == 0 {
				break
			}
		}
	}
	return "", fmt.Errorf("Gist response has no recording ID")
}

func writeRecordingJSONString(out *bufio.Writer, source io.Reader) error {
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() { defer close(done); writer.CloseWithError(recording.WriteWrapped(writer, source)) }()
	err := writeJSONString(out, reader)
	reader.CloseWithError(err)
	<-done
	return err
}
