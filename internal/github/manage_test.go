package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/sealed"
)

const manageID = "0123456789abcdef0123456789abcdef"

func TestListRecordingGistsPagesWithoutDownloadingContents(t *testing.T) {
	c := New("fixture")
	requests := 0
	c.HTTP.Transport = uploadTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != "GET" || r.URL.Host != "api.github.com" || r.URL.Path != "/gists" || r.URL.Query().Get("per_page") != "30" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Fatal("unexpected request", r.URL)
		}
		body := fmt.Sprintf(`[{"id":%q,"files":{"unrelated.txt":{"size":1}}}]`, manageID)
		head := http.Header{}
		if r.URL.Query().Get("page") == "1" {
			head.Set("Link", `<https://api.github.com/gists?page=2>; rel="next"`)
		} else {
			body = fmt.Sprintf(`[{"id":%q,"description":"demo","public":false,"files":{%q:{"size":200}}},{"id":"bad","files":{%q:{}}}]`, manageID, sealed.Filename, Filename)
		}
		return &http.Response{StatusCode: 200, Header: head, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	items, next, err := c.ListRecordings(t.Context(), 1)
	if err != nil || len(items) != 0 || next != 2 {
		t.Fatal(items, next, err)
	}
	items, next, err = c.ListRecordings(t.Context(), next)
	if err != nil || len(items) != 1 || next != 0 || !items[0].Encrypted() || requests != 2 {
		t.Fatal(items, next, err)
	}
}

func TestGistManagementRequiresAuthenticationAndBoundsResponses(t *testing.T) {
	c := New("")
	c.HTTP.Transport = uploadTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unauthenticated request"); return nil, nil })
	if _, _, err := c.ListRecordings(t.Context(), 1); err == nil {
		t.Fatal("anonymous list accepted")
	}
	if err := c.DeleteRecording(t.Context(), manageID); err == nil {
		t.Fatal("anonymous delete accepted")
	}
	c.Token = "fixture"
	c.HTTP.Transport = uploadTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat(" ", (2<<20)+1)))}, nil
	})
	if _, _, err := c.ListRecordings(t.Context(), 1); err == nil {
		t.Fatal("unbounded list")
	}
}

func TestDeleteRechecksOwnershipAndRecordingBeforeWholeGistDeletion(t *testing.T) {
	for _, scenario := range []string{"owned", "other owner", "not recording", "not found", "denied", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			c := New("fixture")
			deleted := false
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			c.HTTP.Transport = uploadTransport(func(r *http.Request) (*http.Response, error) {
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				code, body := 200, `{"id":12}`
				if r.URL.Path == "/gists/"+manageID {
					owner := 12
					if scenario == "other owner" {
						owner = 19
					}
					name := Filename
					if scenario == "not recording" {
						name = "notes.md"
					}
					body = fmt.Sprintf(`{"id":%q,"owner":{"id":%d},"files":{%q:{},"metadata.md":{}}}`, manageID, owner, name)
					if scenario == "not found" {
						code = 404
					}
					if r.Method == "DELETE" {
						deleted = true
						code = 204
						if scenario == "denied" {
							code = 403
						}
					}
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			err := c.DeleteRecording(ctx, manageID)
			if (err == nil) != (scenario == "owned") {
				t.Fatal(err)
			}
			if deleted != (scenario == "owned" || scenario == "denied") {
				t.Fatal("unexpected delete", scenario)
			}
		})
	}
}
