//go:build unix

package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenLinkPassesCompleteURLAsOneArgumentAndPropagatesBrowserFailure(t *testing.T) {
	bin := t.TempDir()
	args := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("PATH", bin)
	t.Setenv("TBT_OPEN_TEST_ARGS", args)
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$#\" \"$@\" > \"$TBT_OPEN_TEST_ARGS\"\nexit \"${TBT_OPEN_TEST_STATUS:-0}\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := "https://play.example/p/" + strings.Repeat("a", 32) + "#k=private-key&title=a%20b"
	if err := (&manageBackend{}).OpenLink(link); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(args)
	if err != nil || string(data) != "1\n"+link+"\n" {
		t.Fatal(string(data), err)
	}
	t.Setenv("TBT_OPEN_TEST_STATUS", "7")
	var exit *exec.ExitError
	if err := openURL(link); !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatal(err)
	}
}
