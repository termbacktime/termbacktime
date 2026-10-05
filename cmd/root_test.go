package cmd

import (
	"bytes"
	"testing"
)

func TestMetadataCommandsNeedNoConfiguration(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"--help"}, {"auth", "--help"}, {"completion", "bash"}, {"completion", "zsh"}, {"completion", "fish"}, {"completion", "powershell"}} {
		root := NewRoot()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append([]string{"--config", "/nonexistent/not-readable"}, args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out.Len() == 0 {
			t.Fatal("no output")
		}
	}
}
