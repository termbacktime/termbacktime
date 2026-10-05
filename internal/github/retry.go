package github

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

type HTTPError struct {
	Status    int
	RetryAt   time.Time
	RequestID string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("GitHub returned HTTP %d", e.Status) }
func (e *HTTPError) Retryable() bool {
	return e.Status == 429 || e.Status >= 500 || (e.Status == 403 && !e.RetryAt.IsZero())
}
func retryAt(header http.Header, now time.Time) time.Time {
	if value := header.Get("Retry-After"); value != "" {
		if n, err := strconv.Atoi(value); err == nil && n >= 0 {
			return now.Add(time.Duration(n) * time.Second)
		}
		if t, err := http.ParseTime(value); err == nil {
			return t
		}
	}
	if header.Get("X-RateLimit-Remaining") == "0" {
		if n, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return time.Unix(n, 0)
		}
	}
	return time.Time{}
}

type deadlineBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *deadlineBody) Close() error { defer b.cancel(); return b.ReadCloser.Close() }
func (c *Client) send(req *http.Request) (*http.Response, error) {
	timeout := c.HTTP.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	req = req.Clone(ctx)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "termbacktime")
	if c.Token != "" && req.URL.Host == "api.github.com" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	safe := req.Method == http.MethodGet || req.Method == http.MethodHead
	for attempt := 0; ; attempt++ {
		response, err := c.HTTP.Do(req)
		var failure *HTTPError
		if err == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
			response.Body = &deadlineBody{response.Body, cancel}
			return response, nil
		}
		if err == nil {
			failure = &HTTPError{Status: response.StatusCode, RetryAt: retryAt(response.Header, time.Now()), RequestID: response.Header.Get("X-GitHub-Request-Id")}
			response.Body.Close()
			err = failure
		}
		if !safe || attempt >= 2 || (failure != nil && !failure.Retryable()) || ctx.Err() != nil {
			cancel()
			return nil, err
		}
		delay := time.Duration(250*(1<<attempt)+rand.IntN(150)) * time.Millisecond
		if failure != nil && !failure.RetryAt.IsZero() {
			delay = max(delay, time.Until(failure.RetryAt))
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			cancel()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
