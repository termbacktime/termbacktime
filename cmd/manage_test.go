package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestManageRequiresTerminalAndUsesSourceChooser(t *testing.T) {
	for _, args := range [][]string{nil, {"--source", "gist"}, {"--storage", "repo"}} {
		c := newManage(&options{})
		c.SetIn(strings.NewReader(""))
		c.SetOut(&bytes.Buffer{})
		c.SetArgs(args)
		err := c.Execute()
		if err == nil {
			t.Fatal("accepted noninteractive manager")
		}
		want := "interactive"
		if len(args) > 0 {
			want = "unknown flag"
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatal(err)
		}
	}
}
