//go:build unix

package scripts

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/recording"
)

func runTool(t *testing.T, directory string, env map[string]string, binary string, args ...string) string {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Dir, command.Env = directory, environment(env)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", binary, args, err, output)
	}
	return string(output)
}

func TestTaggedAndCheckoutGoInstall(t *testing.T) {
	const module, version = "github.com/termbacktime/termbacktime", "v1.0.0"
	root, temporary := cliRoot(t), t.TempDir()
	proxy := filepath.Join(temporary, "proxy", filepath.FromSlash(module), "@v")
	if err := os.MkdirAll(proxy, 0700); err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(proxy, version+".mod"), string(mod), 0600)
	writeFile(t, filepath.Join(proxy, "list"), version+"\n", 0600)
	writeFile(t, filepath.Join(proxy, version+".info"), "{\"Version\":\""+version+"\",\"Time\":\"2026-09-18T00:00:00Z\"}", 0600)
	file, err := os.Create(filepath.Join(proxy, version+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative != "." && relative != "cmd" && relative != "internal" && !strings.HasPrefix(relative, "cmd"+string(os.PathSeparator)) && !strings.HasPrefix(relative, "internal"+string(os.PathSeparator)) {
				return filepath.SkipDir
			}
			return nil
		}
		if relative != "go.mod" && relative != "go.sum" && relative != "main.go" && !strings.HasPrefix(relative, "cmd"+string(os.PathSeparator)) && !strings.HasPrefix(relative, "internal"+string(os.PathSeparator)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		output, err := archive.Create(module + "@" + version + "/" + filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		_, err = output.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	dependencyCache := strings.TrimSpace(runTool(t, root, nil, "go", "env", "GOMODCACHE"))
	// Use a fresh module cache so a previous run's synthetic tag cannot mask
	// checkout changes. Dependency archives already downloaded to run this
	// suite form a second local proxy; neither proxy can access the network.
	env := map[string]string{
		"GOBIN":      filepath.Join(temporary, "bin"),
		"GOMODCACHE": filepath.Join(temporary, "modules"),
		"GOPROXY":    "file://" + filepath.ToSlash(filepath.Join(temporary, "proxy")) + ",file://" + filepath.ToSlash(filepath.Join(dependencyCache, "cache", "download")),
		"GOSUMDB":    "off", "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "-modcacherw",
	}
	runTool(t, temporary, env, "go", "install", module+"@"+version)
	binary := filepath.Join(env["GOBIN"], "termbacktime")
	output := runTool(t, temporary, env, binary, "--version")
	if !strings.HasPrefix(output, "termbacktime "+version+" revision=") {
		t.Fatal(output)
	}
	runTool(t, temporary, env, binary, "--help")
	path := filepath.Join(temporary, "recording.tbt")
	options := []string{"--data-dir", filepath.Join(temporary, "data"), "--config", filepath.Join(temporary, "config.json")}
	runTool(t, temporary, env, binary, append(append([]string{}, options...), "record", "--no-metadata", "--output", path, "--", "/bin/sh", "-c", "printf hello")...)
	record, err := recording.Load(path)
	if err != nil || record.Info.CLI != version {
		t.Fatal(record, err)
	}
	for _, format := range []string{"txt", "md"} {
		transcript := filepath.Join(temporary, "transcript."+format)
		runTool(t, temporary, env, binary, append(append([]string{}, options...), "export", path, "--format", format, "--output", transcript)...)
		data, err := os.ReadFile(transcript)
		if err != nil || !bytes.Contains(data, []byte("hello")) {
			t.Fatal(string(data), err)
		}
	}
	runTool(t, root, env, "go", "install", ".")
	checkoutVersion := runTool(t, temporary, env, binary, "--version")
	if !strings.HasPrefix(checkoutVersion, "termbacktime dev revision=") {
		t.Fatal(checkoutVersion)
	}
}

func TestCIRecordFailureArtifacts(t *testing.T) {
	root, build := cliRoot(t), t.TempDir()
	binary := filepath.Join(build, "termbacktime")
	runTool(t, root, nil, "go", "build", "-o", binary, ".")
	for _, test := range []struct {
		name, command, scan, status string
		diagnostic                  bool
	}{
		{"success", "printf 'password=verysecretvalue\\n'; exit 0", "", "0", false},
		{"failure", "printf 'password=verysecretvalue\\n'; exit 42", "", "42", false},
		{"signal", "printf 'password=verysecretvalue\\n'; kill -TERM $$", "", "143", false},
		{"scan error", "printf 'password=verysecretvalue\\n'; exit 7", "error", "7", true},
		{"scan malformed", "printf hello; exit 7", "malformed", "7", true},
		{"scan empty", "printf hello; exit 7", "empty", "7", true},
		{"scan noncanonical", "printf hello; exit 7", "whitespace", "7", true},
		{"scan missing newline", "printf hello; exit 7", "no-newline", "7", true},
		{"scan extra newline", "printf hello; exit 7", "extra-newline", "7", true},
		{"scan null byte", "printf hello; exit 7", "null-byte", "7", true},
		{"scan remaining secrets", "printf hello; exit 7", "findings", "7", true},
		{"redaction error", "printf hello; exit 7", "redact", "7", true},
		{"build error", "printf hello; exit 7", "build", "1", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			tools := filepath.Join(dir, "tools")
			if err := os.Mkdir(tools, 0700); err != nil {
				t.Fatal(err)
			}
			wrapper := filepath.Join(dir, "wrapper")
			writeFile(t, wrapper, `#!/bin/sh
for arg in "$@"; do
 if [ "$arg" = scan ]; then
  case "$TEST_SCAN" in
   error) exit 2;;
   malformed) echo '{}'; exit 0;;
   empty) exit 0;;
   whitespace) echo '[ ]'; exit 0;;
   no-newline) printf '[]'; exit 0;;
   extra-newline) printf '[]\n\n'; exit 0;;
   null-byte) printf '[]\000\n'; exit 0;;
   findings) echo '[{"kind":"secret"}]'; exit 0;;
  esac
 fi
 if [ "$arg" = redact ] && [ "$TEST_SCAN" = redact ]; then exit 2; fi
done
exec "$TBT_TEST_BINARY" "$@"
`, 0700)
			writeFile(t, filepath.Join(tools, "go"), `#!/bin/sh
[ "$TEST_SCAN" != build ] || exit 1
[ "$1" = build ] && [ "$2" = -o ] || exit 1
cp "$TBT_TEST_WRAPPER" "$3"
chmod 700 "$3"
`, 0700)
			outputs := filepath.Join(dir, "outputs")
			env := map[string]string{"PATH": tools + string(os.PathListSeparator) + os.Getenv("PATH"), "RUNNER_TEMP": dir, "GITHUB_OUTPUT": outputs, "TBT_ACTION_PATH": root, "TBT_DIRECTORY": root, "TBT_SHELL": "/bin/sh", "TBT_COMMAND": test.command, "TEST_SCAN": test.scan, "TBT_TEST_BINARY": binary, "TBT_TEST_WRAPPER": wrapper}
			runTool(t, root, env, "bash", filepath.Join(root, "scripts", "ci-record.sh"))
			data, err := os.ReadFile(outputs)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				key, value, _ := strings.Cut(line, "=")
				values[key] = value
			}
			if values["exit-code"] != test.status {
				t.Fatal(values)
			}
			raw, err := filepath.Glob(filepath.Join(dir, "tbt-raw.*"))
			if err != nil || len(raw) != 0 {
				t.Fatal("raw capture leaked", raw, err)
			}
			entries, err := os.ReadDir(values["artifacts"])
			if err != nil {
				t.Fatal(err)
			}
			if test.status == "0" {
				if len(entries) != 0 {
					t.Fatal(entries)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatal(entries)
			}
			if test.diagnostic {
				if entries[0].Name() != "diagnostic.txt" {
					t.Fatal(entries)
				}
				data, err := os.ReadFile(filepath.Join(values["artifacts"], "diagnostic.txt"))
				if err != nil || bytes.Contains(data, []byte("verysecretvalue")) {
					t.Fatal(string(data), err)
				}
				return
			}
			if entries[0].Name() != "terminal-recording.tbt" {
				t.Fatal(entries)
			}
			record, err := recording.Load(filepath.Join(values["artifacts"], entries[0].Name()))
			if err != nil {
				t.Fatal(err)
			}
			data, err = json.Marshal(record)
			if err != nil || bytes.Contains(data, []byte("verysecretvalue")) || !bytes.Contains(data, []byte("*")) || len(recording.Scan(record)) != 0 {
				t.Fatal(string(data), err)
			}
		})
	}
}

func TestBuildRunnerEmbedsIndependentOrigins(t *testing.T) {
	root, dir := cliRoot(t), t.TempDir()
	runner := filepath.Join(dir, "build-go")
	runTool(t, root, nil, "go", "build", "-o", runner, "./scripts/build-go")
	tools := filepath.Join(dir, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tools, "go"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TBT_BUILD_ARGS\"\n", 0700)
	for _, mode := range []string{"dev", "prod"} {
		env := map[string]string{"PATH": tools + string(os.PathListSeparator) + os.Getenv("PATH"), "SITE_URL": "https://production.example", "SITE_URL_DEV": "https://development.example", "TBT_BUILD_ARGS": filepath.Join(dir, "args")}
		runTool(t, dir, env, runner, "build", mode)
		args, err := os.ReadFile(env["TBT_BUILD_ARGS"])
		if err != nil {
			t.Fatal(err)
		}
		want, name := "https://production.example", "builds/termbacktime"
		if mode == "dev" {
			want, name = "https://development.example", "builds/termbacktime-dev"
		}
		if !strings.Contains(string(args), "EmbeddedSiteURL="+want+"\n") || !strings.Contains(string(args), name+"\n") {
			t.Fatal(string(args))
		}
		if output := runTool(t, dir, env, runner, "site-url", mode); strings.TrimSpace(output) != want {
			t.Fatal(output)
		}
	}
	for _, args := range [][]string{{}, {"build", "typo"}, {"site-url", "prod", "extra"}, {"build", "prod"}} {
		command := exec.Command(runner, args...)
		command.Dir = dir
		command.Env = environment(map[string]string{"SITE_URL": "", "SITE_URL_DEV": ""})
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatal(fmt.Sprint(args), string(output))
		}
	}
}
