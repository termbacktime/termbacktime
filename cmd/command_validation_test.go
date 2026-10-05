package cmd

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestCommandArgumentsAndFlagConflictsFailBeforeProducingOutput(t *testing.T) {
	h := newCommandHarness(t)
	for _, args := range [][]string{
		{"list", "extra"}, {"info"}, {"info", "one", "two"}, {"import"}, {"recover"},
		{"scan"}, {"redact"}, {"upload"}, {"export"}, {"play"}, {"doctor", "extra"},
		{"history", "enable", "extra"}, {"queue", "run", "extra"},
		{"queue", "retry"}, {"queue", "cancel", "one", "two"}, {"storage", "pin"},
		{"storage", "unpin", "one", "two"}, {"completion"}, {"completion", "unknown"},
		{"completion", "bash", "zsh"}, {"--config=", "list"},
		{"auth", "--storage", "gist", "--token-stdin", "--set-token", "a-valid-token"},
		{"record", "--queue"}, {"record", "--upload", "--queue", "--no-save"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, _, err := h.run(t.Context(), args...)
			if err == nil || out != "" {
				t.Fatal(out, err)
			}
		})
	}
}

func TestJSONCommandsPropagateOutputFailures(t *testing.T) {
	h := newCommandHarness(t)
	path, _ := h.recording(&recording.Recording{Sizes: []int{80, 24}})
	failure := errors.New("output pipe closed")
	for _, args := range [][]string{
		{"list", "--json"}, {"info", path}, {"scan", path}, {"queue", "list", "--json"},
		{"storage", "usage", "--json"}, {"storage", "clean", "--json"}, {"doctor", "--json", "--endpoint", "invalid"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			c := h.command(args...)
			c.SetOut(commandWriteFailure{failure})
			if err := c.ExecuteContext(t.Context()); !errors.Is(err, failure) {
				t.Fatal(err)
			}
		})
	}
}

func TestCredentialOverridePrecedenceDoesNotPersistRuntimeSecrets(t *testing.T) {
	h := newCommandHarness(t)
	cfg := config.Config{}
	cfg.Set("token", "saved-token")
	cfg.Set("untouched", "preserved")
	if err := cfg.Save(h.config); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(h.config)
	for _, test := range []struct{ name, environment, flag, want string }{
		{"saved", "", "", "saved-token"}, {"environment", "environment-token", "", "environment-token"},
		{"flag", "environment-token", "flag-token", "flag-token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TERMBACKTIME_TOKEN", test.environment)
			got, err := (&options{configPath: h.config, token: test.flag}).credentials()
			if err != nil || got.String("token") != test.want || got.String("untouched") != "preserved" {
				t.Fatal(got, err)
			}
			after, _ := os.ReadFile(h.config)
			if !bytes.Equal(before, after) {
				t.Fatal("runtime override was persisted")
			}
		})
	}
}
