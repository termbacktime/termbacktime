//go:build unix

package scripts

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseRunnerPackagesAllTargetsWithoutNodeOrTar(t *testing.T) {
	root, dir := cliRoot(t), t.TempDir()
	runner := filepath.Join(dir, "release")
	runTool(t, root, nil, "go", "build", "-o", runner, "./scripts/release")
	tools, temporary := filepath.Join(dir, "tools"), filepath.Join(dir, "tmp")
	for _, path := range []string{tools, temporary} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(tools, "git"), "#!/bin/sh\nprintf '0123456789abcdef0123456789abcdef01234567\\n'\n", 0700)
	writeFile(t, filepath.Join(tools, "go"), `#!/bin/sh
printf '%s/%s/%s/%s\n' "$GOOS" "$GOARCH" "$GOARM" "$CGO_ENABLED" >> "$TBT_RELEASE_CALLS"
printf '%s\n' "$@" >> "$TBT_RELEASE_ARGS"
while [ "$#" -gt 0 ]; do
 if [ "$1" = -o ]; then shift; binary=$1; fi
 shift
done
printf '%s/%s/%s\n' "$GOOS" "$GOARCH" "$GOARM" > "$binary"
`, 0700)
	writeFile(t, filepath.Join(dir, "VERSION"), "v1.0.0\n", 0600)
	writeFile(t, filepath.Join(dir, ".env"), "SITE_URL=https://file.example\nSITE_URL_DEV=http://localhost:8787\n", 0600)
	env := map[string]string{
		"PATH": tools, "TMPDIR": temporary, "SITE_URL": "https://shell.example/", "GOARM": "5",
		"TBT_RELEASE_CALLS": filepath.Join(dir, "calls"), "TBT_RELEASE_ARGS": filepath.Join(dir, "args"),
	}
	for _, test := range []struct {
		version string
		args    []string
	}{
		{"v1.0.0", nil},
		{"v1.9.7-rc.1", []string{"v1.9.7-rc.1"}},
	} {
		t.Run(test.version, func(t *testing.T) {
			writeFile(t, env["TBT_RELEASE_CALLS"], "", 0600)
			writeFile(t, env["TBT_RELEASE_ARGS"], "", 0600)
			output := runTool(t, dir, env, runner, test.args...)
			calls, err := os.ReadFile(env["TBT_RELEASE_CALLS"])
			want := "darwin/amd64//0\ndarwin/arm64//0\nlinux/amd64//0\nlinux/386//0\nlinux/arm64//0\nlinux/arm/6/0\nlinux/arm/7/0\nfreebsd/amd64//0\nfreebsd/386//0\n"
			if err != nil || string(calls) != want {
				t.Fatal("incorrect platform selection", string(calls), err)
			}
			args, err := os.ReadFile(env["TBT_RELEASE_ARGS"])
			if err != nil || strings.Count(string(args), "buildinfo.Version="+test.version) != 9 || strings.Count(string(args), "config.EmbeddedSiteURL=https://shell.example\n") != 9 {
				t.Fatal("release runner lost metadata or environment selection", string(args), err)
			}
			out := filepath.Join(dir, "builds", test.version)
			manifest, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(string(manifest), "\n"), "\n")
			if len(lines) != 9 || len(strings.Split(strings.TrimSpace(output), "\n")) != 9 {
				t.Fatal(string(manifest), output)
			}
			for _, line := range lines {
				digest, name, ok := strings.Cut(line, "  ")
				data, err := os.ReadFile(filepath.Join(out, name))
				if !ok || err != nil || digest != fmt.Sprintf("%x", sha256.Sum256(data)) || !strings.HasPrefix(name, "termbacktime_"+test.version+"_") || !strings.HasSuffix(name, ".tar.gz") {
					t.Fatal("invalid archive checksum", line, err)
				}
			}
			if entries, err := os.ReadDir(temporary); err != nil || len(entries) != 0 {
				t.Fatal("release runner leaked staging", entries, err)
			}
		})
	}
	for _, args := range [][]string{{"v2.0.0"}, {"v1.0.0", "extra"}} {
		c := exec.Command(runner, args...)
		c.Dir, c.Env = dir, environment(env)
		if data, err := c.CombinedOutput(); err == nil || len(data) == 0 {
			t.Fatal("invalid runner arguments succeeded", args, string(data))
		}
	}
}
