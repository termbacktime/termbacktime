package cmd

import (
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// Split documentation examples without invoking a shell or running the command.
func exampleWords(line string) []string {
	var words []string
	var word strings.Builder
	var quote rune
	escape := false
	for _, r := range line {
		if escape {
			word.WriteRune(r)
			escape = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escape = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if unicode.IsSpace(r) {
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
		} else {
			word.WriteRune(r)
		}
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return words
}
func TestDocumentedCommandExamplesParseWithoutExecution(t *testing.T) {
	paths := []string{"../README.md", "../../README.md", "../../website/README.md", "../../website/web/pages/docs.md"}
	for _, pattern := range []string{"../../website/web/pages/*.html"} {
		more, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, more...)
	}
	codeBlocks := regexp.MustCompile(`(?s)<pre><code>(.*?)</code></pre>`)
	count := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			// The website and workspace README are available in a coordinated
			// checkout, but are not part of a standalone CLI source archive.
			if os.IsNotExist(err) && strings.HasPrefix(path, "../../") {
				continue
			}
			t.Fatal(err)
		}
		text := string(data)
		if filepath.Ext(path) == ".html" {
			// Only literal documented code blocks; never evaluate HTML or shell.
			var blocks []string
			for _, match := range codeBlocks.FindAllStringSubmatch(text, -1) {
				blocks = append(blocks, "```sh\n"+html.UnescapeString(match[1])+"\n```")
			}
			text = strings.Join(blocks, "\n")
		}
		shell := false
		for n, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "```") {
				shell = !shell && (line == "```sh" || line == "```bash" || line == "```shell")
				continue
			}
			line = strings.TrimSpace(line)
			if !shell || !strings.HasPrefix(line, "termbacktime ") {
				continue
			}
			words := exampleWords(line)
			root := NewRoot()
			command, args, err := root.Find(words[1:])
			if err != nil || command == root && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
				t.Fatalf("%s:%d: unknown command %q: %v", path, n+1, line, err)
			}
			if err := command.ParseFlags(args); err != nil {
				t.Fatalf("%s:%d: %s: %v", path, n+1, line, err)
			}
			if command.Args != nil {
				if err := command.Args(command, command.Flags().Args()); err != nil {
					t.Fatalf("%s:%d: %s: %v", path, n+1, line, err)
				}
			}
			count++
		}
	}
	if count < 30 {
		t.Fatal("too few checked examples", count)
	}
}
