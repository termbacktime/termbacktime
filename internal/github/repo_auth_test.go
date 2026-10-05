package github

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRepositoryDeviceStartErrorsIdentifyConfigurationFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		invalid bool
		message string
	}{
		{"deleted app", 404, `{}`, true, "Client ID is unavailable"},
		{"invalid client", 200, `{"error":"incorrect_client_credentials"}`, true, "Client ID is unavailable"},
		{"disabled flow", 200, `{"error":"device_flow_disabled"}`, false, "enable Device Flow"},
		{"server failure", 503, `{}`, false, "HTTP 503"},
		{"untrusted description", 200, `{"error":"unknown-error","error_description":"sensitive-body"}`, false, "GitHub rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New("")
			calls := 0
			c.HTTP.Transport = uploadTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/login/device/code" {
					t.Fatal(r.URL)
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			_, err := c.AuthorizeRepo(t.Context(), RepoApp{ID: 3, ClientID: "client", Slug: "app"}, func(string, string) { t.Fatal("displayed code after rejection") })
			var start *DeviceAuthorizationError
			if !errors.As(err, &start) || start.InvalidClient != tc.invalid || !strings.Contains(err.Error(), tc.message) || strings.Contains(err.Error(), "sensitive-body") || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
}
