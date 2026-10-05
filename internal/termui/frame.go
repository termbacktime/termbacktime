package termui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Fit fills the viewport, including blank rows left by a previous screen.
func Fit(content string, width, height int) string {
	width, height = max(1, width), max(1, height)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	rows := make([]string, height)
	for i := range rows {
		if i < len(lines) {
			rows[i] = ansi.Truncate(lines[i], width, "")
		}
		rows[i] += "\x1b[0m" + strings.Repeat(" ", max(0, width-ansi.StringWidth(rows[i])))
	}
	return strings.Join(rows, "\n")
}
