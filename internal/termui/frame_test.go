package termui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestViewportClipsANSIAndWideTextAndErasesOldRows(t *testing.T) {
	for _, test := range []struct {
		input         string
		width, height int
		want          []string
	}{
		{"\x1b[31m世界abc\x1b[0m\r\nsecond\nthird", 4, 2, []string{"世界", "seco"}},
		{"x", 3, 3, []string{"x  ", "   ", "   "}},
		{"long", 0, 0, []string{"l"}},
	} {
		output := Fit(test.input, test.width, test.height)
		lines := strings.Split(output, "\n")
		if len(lines) != len(test.want) {
			t.Fatal(output)
		}
		for i, line := range lines {
			if ansi.Strip(line) != test.want[i] || !strings.Contains(line, "\x1b[0m") {
				t.Fatal(output)
			}
		}
	}
}
