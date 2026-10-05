package github

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type availabilityTransport func(*http.Request) (*http.Response, error)

func (f availabilityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestAvailabilityUsesOnlyHEADAndClassifiesFailures(t *testing.T) {
	for code, want := range map[int]string{200: "Available", 401: "Authentication failed", 404: "Inaccessible or missing", 403: "Rate limited or access denied"} {
		client := New("synthetic")
		client.HTTP = &http.Client{Transport: availabilityTransport(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodHead {
				t.Fatal("downloaded recording contents")
			}
			return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
		got, err := client.CheckAvailability(t.Context(), strings.Repeat("a", 32))
		if err != nil || got != want {
			t.Fatal(code, got, err)
		}
	}
}
