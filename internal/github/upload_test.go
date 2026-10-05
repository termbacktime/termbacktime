package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type uploadTransport func(*http.Request) (*http.Response, error)

func (transport uploadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type uploadReadFunc func([]byte) (int, error)

func (read uploadReadFunc) Read(data []byte) (int, error) { return read(data) }

func TestUploadStringEscapingAcrossBoundaries(t *testing.T) {
	var controls strings.Builder
	for value := 0; value < 32; value++ {
		controls.WriteByte(byte(value))
	}
	content := strings.Repeat("a", (32<<10)-1) + "世界 🎥\"\\\n" + controls.String() + "\u2028\u2029"
	source := strings.NewReader(content)
	// Force the UTF-8 decoder to handle characters split across individual reads
	oneByte := uploadReadFunc(func(data []byte) (int, error) { return source.Read(data[:1]) })
	var output bytes.Buffer
	if err := writeUpload(context.Background(), &output, oneByte, "quoted \"title\" 世界"); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Description string `json:"description"`
		Public      bool   `json:"public"`
		Files       map[string]struct {
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if unwrapContent(t, payload.Files[Filename].Content) != content || payload.Description != "quoted \"title\" 世界" || payload.Public {
		t.Fatal("streaming JSON escaping changed the uploaded recording")
	}
}

func TestUploadFileUsesPrivateRequestAndCleansUp(t *testing.T) {
	for _, scenario := range []string{"success", "HTTP failure", "cancelled request", "invalid response"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "recording.json")
			content := `{"i":{"a":"amd64","o":"linux","v":"test"},"d":123,"s":[80,24],"r":[{"l":["fixture"]}]}`
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := New("test-token")
			client.SiteURL = "https://site.example/"
			client.HTTP.Transport = uploadTransport(func(request *http.Request) (*http.Response, error) {
				defer request.Body.Close()
				if request.Header.Get("Authorization") != "Bearer test-token" || request.Header.Get("Content-Type") != "application/json" {
					t.Fatal("missing GitHub request headers")
				}
				matches, err := filepath.Glob(filepath.Join(dir, ".tbt-upload-*"))
				if err != nil || len(matches) != 1 {
					t.Fatal("upload did not spool its request to disk", err)
				}
				info, err := os.Stat(matches[0])
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("temporary upload was not private", err)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil || int64(len(body)) != request.ContentLength {
					t.Fatal("incorrect streamed request length", err)
				}
				var payload struct {
					Files map[string]struct{ Content string } `json:"files"`
				}
				if err := json.Unmarshal(body, &payload); err != nil || unwrapContent(t, payload.Files[Filename].Content) != content {
					t.Fatal("upload changed finalized recording bytes", err)
				}
				if scenario == "cancelled request" {
					cancel()
					return nil, ctx.Err()
				}
				status := http.StatusCreated
				if scenario == "HTTP failure" {
					status = http.StatusServiceUnavailable
				}
				response := `{"id":"` + strings.Repeat("a", 32) + `"}`
				if scenario == "invalid response" {
					response = `{"id":"invalid"}`
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
			})
			link, err := client.UploadFile(ctx, path, "fixture")
			if scenario == "success" {
				if err != nil || link != "https://site.example/p/"+strings.Repeat("a", 32) {
					t.Fatalf("unexpected upload result %q: %v", link, err)
				}
			} else if err == nil {
				t.Fatal("upload ignored a failure")
			}
			if scenario == "cancelled request" && !errors.Is(err, context.Canceled) {
				t.Fatal("upload lost cancellation error", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "recording.json" {
				t.Fatal("upload removed the recording or left its temporary request", err)
			}
		})
	}
}

func TestUploadPreparationCancelsAndPropagatesReadErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := uploadReadFunc(func(data []byte) (int, error) {
		cancel()
		return copy(data, "partial recording"), nil
	})
	if err := writeUpload(ctx, io.Discard, source, "fixture"); !errors.Is(err, context.Canceled) {
		t.Fatal("upload preparation ignored cancellation", err)
	}
	source = uploadReadFunc(func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF })
	if err := writeUpload(context.Background(), io.Discard, source, "fixture"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("upload preparation ignored a read failure", err)
	}
}

func TestUploadedIDDoesNotBufferEchoedRecording(t *testing.T) {
	id := strings.Repeat("b", 32)
	content := `{"owner":{"id":12,"extra":[true,null,{"key":"value"}]},"id":"` + id + `","files":`
	source := io.MultiReader(strings.NewReader(content), uploadReadFunc(func([]byte) (int, error) {
		t.Fatal("upload response reader continued into echoed recording content")
		return 0, io.ErrUnexpectedEOF
	}))
	actual, err := readUploadedID(source)
	if err != nil || actual != id {
		t.Fatalf("unexpected Gist ID %q: %v", actual, err)
	}
	for _, invalid := range []string{
		`[]`,
		`{}`,
		`{"id":12}`,
		`{"id":"invalid"}`,
		`{"owner":{"id":"` + id + `"}}`,
		`{"extra":` + strings.Repeat("[", 65) + strings.Repeat("]", 65) + `,"id":"` + id + `"}`,
		`{"content":"` + strings.Repeat("x", 4<<20) + `","id":"` + id + `"}`,
	} {
		if _, err := readUploadedID(strings.NewReader(invalid)); err == nil {
			t.Fatal("accepted invalid or oversized Gist response")
		}
	}
}

func BenchmarkUploadPreparation(b *testing.B) {
	data := strings.Repeat("terminal output 世界\n", 65536)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if err := writeUpload(b.Context(), io.Discard, strings.NewReader(data), "benchmark"); err != nil {
			b.Fatal(err)
		}
	}
}

func unwrapContent(t *testing.T, text string) string {
	t.Helper()
	var value struct {
		F string
		P string
	}
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	if value.F != "tbt-recording-v1" {
		t.Fatal(value.F)
	}
	b, err := base64.StdEncoding.DecodeString(value.P)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
