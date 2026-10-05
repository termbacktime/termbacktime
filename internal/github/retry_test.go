package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSafeReadsRetryButCreationDoesNot(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			client := New("token")
			attempts := 0
			client.HTTP.Transport = uploadTransport(func(req *http.Request) (*http.Response, error) {
				attempts++
				status := 503
				if attempts == 3 {
					status = 200
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
			})
			req, _ := http.NewRequestWithContext(context.Background(), method, "https://api.github.com/gists", nil)
			res, err := client.send(req)
			if method == "POST" {
				var h *HTTPError
				if !errors.As(err, &h) || h.Status != 503 || attempts != 1 {
					t.Fatal(attempts, err)
				}
			} else {
				if err != nil || attempts != 3 {
					t.Fatal(attempts, err)
				}
				res.Body.Close()
			}
		})
	}
}
func TestRateLimitRespectsOverallDeadline(t *testing.T) {
	client := New("token")
	attempts := 0
	client.HTTP.Transport = uploadTransport(func(req *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"60"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/gists", nil)
	_, err := client.send(req)
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
		t.Fatal(attempts, err)
	}
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	if got := retryAt(http.Header{"Retry-After": []string{reset.UTC().Format(http.TimeFormat)}}, time.Now()); !got.Equal(reset) {
		t.Fatal(got, reset)
	}
}
