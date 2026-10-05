//go:build unix

package scripts

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}
func cliRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func environment(overrides map[string]string) []string {
	values := map[string]string{}
	for _, item := range os.Environ() {
		key, value, _ := strings.Cut(item, "=")
		values[key] = value
	}
	for key, value := range overrides {
		values[key] = value
	}
	var result []string
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

type installerFixture struct {
	dir, tools, release, bin, version, archive string
	env                                        map[string]string
}

func newInstaller(t *testing.T, version string) *installerFixture {
	t.Helper()
	f := &installerFixture{dir: t.TempDir(), version: version}
	f.tools, f.release, f.bin = filepath.Join(f.dir, "tools"), filepath.Join(f.dir, "release"), filepath.Join(f.dir, "bin with spaces")
	for _, path := range []string{f.tools, f.release, f.bin} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.archive = "termbacktime_" + version + "_linux_amd64.tar.gz"
	f.pack(t, false)
	writeFile(t, filepath.Join(f.release, "page-1"), fmt.Sprintf("[{\"id\":42,\"tag_name\":%q,\"draft\":false}]", version), 0600)
	writeFile(t, filepath.Join(f.tools, "uname"), "#!/bin/sh\ncase \"$1\" in -s) echo Linux;; -m) echo x86_64;; esac\n", 0700)
	writeFile(t, filepath.Join(f.tools, "getconf"), "#!/bin/sh\necho 64\n", 0700)
	writeFile(t, filepath.Join(f.tools, "curl"), `#!/bin/sh
set -eu
output=
headers=
while [ "$#" -gt 0 ]; do
 case "$1" in
 --output) output=$2; shift 2 ;;
 --dump-header) headers=$2; shift 2 ;;
 https://*) url=$1; shift ;;
 *) shift ;;
 esac
done
: > "$headers"
printf '%s\n' "$url" >> "$TEST_REQUESTS"
[ "${TEST_FAIL_DOWNLOAD:-}" != 1 ] || exit 22
case "$url" in
 https://api.github.com/repos/termbacktime/termbacktime/releases\?per_page=1\&page=*)
  [ "${TEST_FAIL_DISCOVERY:-}" != 1 ] || exit 22
  page=${url##*=}
  cp "$TEST_RELEASE/page-$page" "$output"
  if [ -f "$TEST_RELEASE/headers-$page" ]; then cp "$TEST_RELEASE/headers-$page" "$headers"; fi ;;
 https://github.com/termbacktime/termbacktime/releases/download/*) cp "$TEST_RELEASE/${url##*/}" "$output" ;;
 *) exit 22 ;;
esac
`, 0700)
	f.env = map[string]string{"PATH": f.tools + string(os.PathListSeparator) + os.Getenv("PATH"), "TEST_RELEASE": f.release, "TEST_REQUESTS": filepath.Join(f.dir, "requests"), "TMPDIR": f.dir, "SITE_URL": "", "TERMBACKTIME_SITE_URL": ""}
	return f
}
func (f *installerFixture) pack(t *testing.T, extra bool) {
	t.Helper()
	file, err := os.Create(filepath.Join(f.release, f.archive))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	archive := tar.NewWriter(gz)
	content := fmt.Sprintf("#!/bin/sh\nprintf 'termbacktime %s revision=test (go1.27.1)\\n'\n", f.version)
	for name, data := range map[string]string{"termbacktime": content, "extra": "unexpected"} {
		if name == "extra" && !extra {
			continue
		}
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range []error{archive.Close(), gz.Close(), file.Close()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(f.release, f.archive))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.release, "SHA256SUMS"), fmt.Sprintf("%x  %s\n", sha256.Sum256(data), f.archive), 0600)
}
func (f *installerFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	command := exec.Command("sh", append([]string{filepath.Join(cliRoot(t), "install.sh"), "--bin-dir", f.bin}, args...)...)
	command.Env = environment(f.env)
	output, err := command.CombinedOutput()
	matches, globErr := filepath.Glob(filepath.Join(f.dir, "termbacktime.*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatal("temporary files leaked", matches, globErr)
	}
	return string(output), err
}
func TestInstallerInstallationAndExplicitVersions(t *testing.T) {
	f := newInstaller(t, "v1.0.0")
	if output, err := f.run(t); err != nil || !strings.Contains(output, "Installed v1.0.0") {
		t.Fatal(output, err)
	}
	data, err := os.ReadFile(filepath.Join(f.bin, "termbacktime"))
	if err != nil || !strings.Contains(string(data), "revision=test") {
		t.Fatal(string(data), err)
	}
	requests, _ := os.ReadFile(f.env["TEST_REQUESTS"])
	want := "https://api.github.com/repos/termbacktime/termbacktime/releases?per_page=1&page=1\nhttps://github.com/termbacktime/termbacktime/releases/download/v1.0.0/" + f.archive + "\nhttps://github.com/termbacktime/termbacktime/releases/download/v1.0.0/SHA256SUMS\n"
	if string(requests) != want {
		t.Fatal(string(requests))
	}
	writeFile(t, filepath.Join(f.bin, "termbacktime"), "old", 0700)
	f.env["TEST_FAIL_DISCOVERY"] = "1"
	if output, err := f.run(t, "--version", "v1.0.0"); err != nil {
		t.Fatal(output, err)
	}
	pre := newInstaller(t, "v1.1.0-rc.1")
	pre.env["TEST_FAIL_DISCOVERY"] = "1"
	if output, err := pre.run(t, "--version", pre.version); err != nil || !strings.Contains(output, pre.version) {
		t.Fatal(output, err)
	}
	requests, _ = os.ReadFile(pre.env["TEST_REQUESTS"])
	if strings.Count(string(requests), "\n") != 2 || strings.Contains(string(requests), "api.github.com") {
		t.Fatal(string(requests))
	}
}
func TestInstallerFailuresPreserveExistingBinary(t *testing.T) {
	for _, scenario := range []string{"checksum", "download", "discovery", "archive", "symlink", "version", "positional"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInstaller(t, "v1.0.0")
			target := filepath.Join(f.bin, "termbacktime")
			writeFile(t, target, "preserve me", 0700)
			var args []string
			switch scenario {
			case "checksum":
				writeFile(t, filepath.Join(f.release, "SHA256SUMS"), strings.Repeat("0", 64)+"  "+f.archive+"\n", 0600)
			case "download":
				f.env["TEST_FAIL_DOWNLOAD"] = "1"
			case "discovery":
				f.env["TEST_FAIL_DISCOVERY"] = "1"
			case "archive":
				f.pack(t, true)
			case "symlink":
				if err := os.Rename(target, filepath.Join(f.dir, "original")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(f.dir, "original"), target); err != nil {
					t.Fatal(err)
				}
			case "version":
				args = []string{"--version", "../../bad"}
			case "positional":
				args = []string{"1.19.0"}
			}
			if output, err := f.run(t, args...); err == nil {
				t.Fatal("accepted invalid installation", output)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "preserve me" {
				t.Fatal(string(data), err)
			}
		})
	}
}
func TestInstallerPlatforms(t *testing.T) {
	for _, host := range []struct{ os, machine, bits, platform, arch string }{
		{"Darwin", "x86_64", "64", "darwin", "amd64"}, {"Darwin", "arm64", "64", "darwin", "arm64"},
		{"Linux", "x86_64", "64", "linux", "amd64"}, {"Linux", "x86_64", "32", "linux", "386"},
		{"Linux", "aarch64", "64", "linux", "arm64"}, {"Linux", "armv6l", "32", "linux", "armv6"}, {"Linux", "armv7l", "32", "linux", "armv7"},
		{"FreeBSD", "amd64", "64", "freebsd", "amd64"}, {"FreeBSD", "i386", "32", "freebsd", "386"},
	} {
		t.Run(host.platform+"/"+host.arch, func(t *testing.T) {
			f := newInstaller(t, "v1.0.0")
			f.archive = "termbacktime_" + f.version + "_" + host.platform + "_" + host.arch + ".tar.gz"
			f.pack(t, false)
			writeFile(t, filepath.Join(f.tools, "uname"), fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -s) echo %s;; -m) echo %s;; esac\n", host.os, host.machine), 0700)
			writeFile(t, filepath.Join(f.tools, "getconf"), "#!/bin/sh\necho "+host.bits+"\n", 0700)
			writeFile(t, filepath.Join(f.tools, "sysctl"), "#!/bin/sh\necho 0\n", 0700)
			if output, err := f.run(t); err != nil {
				t.Fatal(output, err)
			}
		})
	}
}
func TestInstallerReleaseDiscoveryValidationAndBounds(t *testing.T) {
	for _, payload := range []string{
		`{"message":"API rate limit exceeded"}`, `[{"tag_name":"../../bad"}]`,
		`[{"tag_name":"v1.0.0","tag_name":"v1.1.0"}]`, `[{"tag_name":"v1.0.0"},{"tag_name":"v1.1.0"}]`,
		`[{"tag_name":"v1.0.0-rc.01"}]`, "[]", "not JSON",
	} {
		f := newInstaller(t, "v1.0.0")
		target := filepath.Join(f.bin, "termbacktime")
		writeFile(t, target, "keep this binary", 0700)
		writeFile(t, filepath.Join(f.release, "page-1"), payload, 0600)
		output, err := f.run(t)
		if err == nil || (!strings.Contains(output, "Invalid GitHub release response") && !strings.Contains(output, "No published semantic-version release")) {
			t.Fatal(payload, output, err)
		}
		data, _ := os.ReadFile(target)
		if string(data) != "keep this binary" {
			t.Fatal(string(data))
		}
		requests, _ := os.ReadFile(f.env["TEST_REQUESTS"])
		if strings.Count(string(requests), "\n") != 1 {
			t.Fatal(string(requests))
		}
	}
	f := newInstaller(t, "v1.1.0-rc.1")
	writeFile(t, filepath.Join(f.release, "page-2"), `[{"tag_name":"v1.1.0-rc.1","prerelease":true}]`, 0600)
	writeFile(t, filepath.Join(f.release, "page-1"), `[{"tag_name":"v2.0.0","draft":true}]`, 0600)
	writeFile(t, filepath.Join(f.release, "headers-1"), "Link: <https://api.github.com/repos/termbacktime/termbacktime/releases?per_page=1&page=2>; rel=\"next\"\n", 0600)
	if output, err := f.run(t); err != nil || !strings.Contains(output, f.version) {
		t.Fatal(output, err)
	}
	writeFile(t, filepath.Join(f.release, "page-1"), strings.Repeat(" ", 1<<20+1), 0600)
	if output, err := f.run(t); err == nil || !strings.Contains(output, "exceeds 1 MiB") {
		t.Fatal(output, err)
	}
	for page := 1; page <= 20; page++ {
		writeFile(t, filepath.Join(f.release, fmt.Sprintf("page-%d", page)), `[{"tag_name":"unsupported"}]`, 0600)
		writeFile(t, filepath.Join(f.release, fmt.Sprintf("headers-%d", page)), fmt.Sprintf("Link: <https://api.github.com/repos/termbacktime/termbacktime/releases?per_page=1&page=%d>; rel=\"next\"\n", page+1), 0600)
	}
	writeFile(t, f.env["TEST_REQUESTS"], "", 0600)
	if output, err := f.run(t); err == nil || !strings.Contains(output, "within 20 pages") {
		t.Fatal(output, err)
	}
	requests, _ := os.ReadFile(f.env["TEST_REQUESTS"])
	if strings.Count(string(requests), "\n") != 20 {
		t.Fatal(string(requests))
	}
}
