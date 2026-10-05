package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/config"
)

func TestDoctorReportsPermissionsAndCompatibilityWithoutLeakingCredentials(t *testing.T) {
	h := newCommandHarness(t)
	cfg := config.Config{}
	cfg.Set("token", "saved-private-github-token")
	if err := cfg.Save(h.config); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.data, "recordings"), 0700); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v1/config" || r.Method != "GET" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected compatibility request", r.Method, r.URL, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"recordingFormats":["tbt-recording-v1","tbt-encrypted-v1"]}`))
	}))
	defer server.Close()
	for _, flags := range [][]string{nil, {"--json"}} {
		args := append([]string{"doctor", "--endpoint", server.URL}, flags...)
		out, diagnostic, err := h.run(t.Context(), args...)
		var report struct {
			Data        string            `json:"data_dir"`
			Config      string            `json:"config_path"`
			Token       bool              `json:"github_token_configured"`
			Encrypted   bool              `json:"encrypted_uploads_supported"`
			Permissions map[string]string `json:"permissions"`
		}
		if err != nil || diagnostic != "" || json.Unmarshal([]byte(out), &report) != nil || report.Data != h.data || report.Config != h.config || !report.Token || !report.Encrypted {
			t.Fatal(out, diagnostic, err)
		}
		if report.Permissions[filepath.Join(h.data, "recordings")] != "0700" || report.Permissions[filepath.Join(h.data, "shares")] != "not created" {
			t.Fatal(report.Permissions)
		}
		if strings.Contains(out, "saved-private-github-token") {
			t.Fatal("doctor exposed credentials", out)
		}
	}
	if requests != 2 {
		t.Fatal(requests)
	}
	if _, err := os.Stat(filepath.Join(h.data, "shares")); !os.IsNotExist(err) {
		t.Fatal("doctor created storage", err)
	}
}

func TestDoctorReportsInvalidConfigurationAndCompatibilityFailures(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"unavailable", `{}`, 503}, {"invalid response", `{`, 200}, {"unsupported", `{"recordingFormats":[]}`, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newCommandHarness(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			out, _, err := h.run(t.Context(), "doctor", "--json", "--endpoint", server.URL)
			var report map[string]any
			if err != nil || json.Unmarshal([]byte(out), &report) != nil || report["compatibility_error"] == nil || report["encrypted_uploads_supported"] != nil {
				t.Fatal(out, err)
			}
		})
	}
	h := newCommandHarness(t)
	if err := os.WriteFile(h.config, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, err := h.run(t.Context(), "doctor", "--json", "--endpoint", "http://remote.example")
	var report map[string]any
	if err != nil || json.Unmarshal([]byte(out), &report) != nil || report["configuration_error"] == nil || report["endpoint_error"] == nil {
		t.Fatal(out, err)
	}
	if out, _, err := h.run(t.Context(), "doctor", "--relay-only", "--endpoint", "invalid"); err == nil || !strings.Contains(err.Error(), "requires --live") || out != "" {
		t.Fatal(out, err)
	}
}

func TestDoctorLiveFailureAndCancellationKeepMachineReadableDiagnostics(t *testing.T) {
	h := newCommandHarness(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"recordingFormats":["tbt-recording-v1","tbt-encrypted-v1"]}`))
			return
		}
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":"local probe unavailable"}`))
	}))
	defer server.Close()
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		if canceled {
			cancel()
		}
		out, _, err := h.run(ctx, "doctor", "--json", "--live", "--relay-only", "--endpoint", server.URL)
		cancel()
		var report map[string]any
		if err == nil || json.Unmarshal([]byte(out), &report) != nil || report["live_error"] == nil || report["live"] != nil {
			t.Fatal(out, err)
		}
	}
}
