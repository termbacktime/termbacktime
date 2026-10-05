package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

func writeFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func readArchive(t *testing.T, path string) []byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	header, err := archive.Next()
	if err != nil || header.Name != "termbacktime" || header.Mode != 0755 || header.Typeflag != tar.TypeReg || header.Uid != 0 || header.Gid != 0 {
		t.Fatal("invalid installer archive", header, err)
	}
	data, err := io.ReadAll(archive)
	if err != nil || int64(len(data)) != header.Size {
		t.Fatal("truncated archive", err)
	}
	if _, err := archive.Next(); !errors.Is(err, io.EOF) {
		t.Fatal("unexpected extra archive entry", err)
	}
	if _, err := io.Copy(io.Discard, compressed); err != nil {
		t.Fatal("invalid gzip checksum", err)
	}
	return data
}

func values(env []string) map[string]string {
	out := make(map[string]string)
	for _, value := range env {
		key, value, _ := strings.Cut(value, "=")
		out[key] = value
	}
	return out
}

func binaryArgument(t *testing.T, args []string) string {
	t.Helper()
	if len(args) != 7 || args[0] != "build" || args[1] != "-trimpath" || args[2] != "-ldflags" || args[4] != "-o" || args[6] != "." {
		t.Fatal("unexpected Go build arguments", args)
	}
	return args[5]
}

func TestReleaseTargetsMetadataArchivesAndChecksums(t *testing.T) {
	for _, test := range []struct {
		name, argument, version, site string
		env                           []string
	}{
		{"version file and dotenv", "", "v1.2.3", "https://production.example", nil},
		{"explicit prerelease and shell override", "v1.9.7-rc.1", "v1.9.7-rc.1", "https://shell.example", []string{"SITE_URL=https://shell.example/"}},
		{"explicit empty origin", "v1.0.0", "v1.0.0", "", []string{"SITE_URL="}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, temporary := t.TempDir(), t.TempDir()
			t.Setenv("TMPDIR", temporary)
			writeFixture(t, filepath.Join(root, "VERSION"), " v1.2.3\n")
			writeFixture(t, filepath.Join(root, ".env"), "SITE_URL=https://production.example/\nSITE_URL_DEV=http://localhost:8787\n")
			base := append([]string{"PATH=/tools", "GOOS=windows", "GOARCH=arm", "GOARM=5", "CGO_ENABLED=1", "KEEP=retained"}, test.env...)
			var output bytes.Buffer
			var names []string
			b := builder{env: base, targets: targets, run: func(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
				if dir != root || ctx.Err() != nil {
					t.Fatal("wrong build context or root", dir, ctx.Err())
				}
				if name == "git" {
					if !reflect.DeepEqual(args, []string{"rev-parse", "HEAD"}) || !reflect.DeepEqual(env, base) {
						t.Fatal(args, env)
					}
					return []byte(testRevision + "\n"), nil
				}
				if name != "go" {
					t.Fatal("unexpected external tool", name)
				}
				binary := binaryArgument(t, args)
				for _, flag := range []string{"-s -w", "buildinfo.Version=" + test.version, "buildinfo.Revision=" + testRevision, "config.EmbeddedSiteURL=" + test.site} {
					if !strings.Contains(args[3], flag) {
						t.Fatal("missing release metadata", args[3], flag)
					}
				}
				settings := values(env)
				if settings["CGO_ENABLED"] != "0" || settings["KEEP"] != "retained" || settings["PATH"] != "/tools" {
					t.Fatal(settings)
				}
				for _, key := range []string{"CGO_ENABLED", "GOOS", "GOARCH", "GOARM"} {
					count := 0
					for _, value := range env {
						if strings.HasPrefix(value, key+"=") {
							count++
						}
					}
					want := 1
					if key == "GOARM" && settings["GOARCH"] != "arm" {
						want = 0
					}
					if count != want {
						t.Fatal("inherited platform setting leaked", key, env)
					}
				}
				arch := settings["GOARCH"]
				if arch == "arm" {
					arch = "armv" + settings["GOARM"]
				}
				names = append(names, settings["GOOS"]+"_"+arch)
				writeFixture(t, binary, settings["GOOS"]+"_"+arch)
				return nil, nil
			}}
			if err := build(t.Context(), root, test.argument, &output, b); err != nil {
				t.Fatal(err)
			}
			want := []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_386", "linux_arm64", "linux_armv6", "linux_armv7", "freebsd_amd64", "freebsd_386"}
			if !reflect.DeepEqual(names, want) {
				t.Fatal("release targets changed", names)
			}
			out := filepath.Join(root, "builds", test.version)
			entries, err := os.ReadDir(out)
			if err != nil || len(entries) != 10 {
				t.Fatal("unexpected release artifacts", entries, err)
			}
			manifest, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
			if err != nil {
				t.Fatal(err)
			}
			var expectedManifest, expectedOutput strings.Builder
			for _, platform := range want {
				name := "termbacktime_" + test.version + "_" + platform + ".tar.gz"
				path := filepath.Join(out, name)
				if data := readArchive(t, path); string(data) != platform {
					t.Fatal("archive contains the wrong platform", string(data))
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&expectedManifest, "%x  %s\n", sha256.Sum256(data), name)
				fmt.Fprintln(&expectedOutput, name)
			}
			if string(manifest) != expectedManifest.String() || output.String() != expectedOutput.String() {
				t.Fatal("incorrect checksum manifest or progress output", string(manifest), output.String())
			}
			if entries, err := os.ReadDir(temporary); err != nil || len(entries) != 0 {
				t.Fatal("release staging leaked", entries, err)
			}
		})
	}
}

func TestReleaseRejectsInvalidVersionsAndOriginsBeforeBuilding(t *testing.T) {
	for _, version := range []string{"v2.0.0", "1.2.3", "v1.2", "v1.2.3+build", "v1.2.3-", "../v1.2.3", "v1.2.3\n", "v1.2.3 -X injected"} {
		t.Run(version, func(t *testing.T) {
			b := builder{run: func(context.Context, string, []string, string, ...string) ([]byte, error) {
				t.Fatal("invalid version invoked tools")
				return nil, nil
			}}
			if err := build(t.Context(), t.TempDir(), version, nil, b); err == nil || !strings.Contains(err.Error(), "semantic version") {
				t.Fatal(err)
			}
		})
	}
	for _, site := range []string{"http://remote.example", "https://user:pass@example.com", "https://example.com/path", "https://example.com/?key=private"} {
		t.Run(site, func(t *testing.T) {
			b := builder{env: []string{"SITE_URL=" + site}, run: func(context.Context, string, []string, string, ...string) ([]byte, error) {
				t.Fatal("invalid origin invoked tools")
				return nil, nil
			}}
			if err := build(t.Context(), t.TempDir(), "v1.0.0", nil, b); err == nil {
				t.Fatal("invalid production origin was accepted")
			}
		})
	}
	if err := build(t.Context(), t.TempDir(), "", nil, builder{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing VERSION did not fail", err)
	}
}

type failedOutput struct{ err error }

func (w failedOutput) Write([]byte) (int, error) { return 0, w.err }

func TestReleaseFailuresCancellationAndStagingCleanup(t *testing.T) {
	for _, mode := range []string{"git error", "bad revision", "output directory", "build error", "late build error", "missing binary", "empty binary", "directory binary", "archive destination", "manifest destination", "progress output", "cancel before", "cancel build"} {
		t.Run(mode, func(t *testing.T) {
			root, temporary := t.TempDir(), t.TempDir()
			t.Setenv("TMPDIR", temporary)
			out := filepath.Join(root, "builds", "v1.0.0")
			if mode == "output directory" {
				writeFixture(t, filepath.Join(root, "builds"), "keep")
			}
			if mode == "archive destination" || mode == "manifest destination" {
				name := "SHA256SUMS"
				if mode == "archive destination" {
					name = "termbacktime_v1.0.0_darwin_amd64.tar.gz"
				}
				if err := os.MkdirAll(filepath.Join(out, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel before" {
				cancel()
			}
			failure := errors.New("controlled failure")
			calls := 0
			b := builder{targets: targets, run: func(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
				if name == "git" {
					if mode == "git error" {
						return nil, failure
					}
					if mode == "bad revision" {
						return []byte("not-a-commit -X injected"), nil
					}
					return []byte(testRevision), nil
				}
				calls++
				if mode == "build error" || mode == "late build error" && calls == 2 {
					return nil, failure
				}
				binary := binaryArgument(t, args)
				switch mode {
				case "missing binary":
				case "empty binary":
					writeFixture(t, binary, "")
				case "directory binary":
					if err := os.Mkdir(binary, 0700); err != nil {
						t.Fatal(err)
					}
				default:
					writeFixture(t, binary, "fixture binary")
				}
				if mode == "cancel build" {
					cancel()
				}
				return nil, nil
			}}
			var output io.Writer = io.Discard
			if mode == "progress output" {
				output = failedOutput{failure}
			}
			err := build(ctx, root, "v1.0.0", output, b)
			if err == nil {
				t.Fatal("release failure was hidden")
			}
			if strings.HasPrefix(mode, "cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal("release lost cancellation", err)
			}
			if (mode == "git error" || strings.Contains(mode, "build error") || mode == "progress output") && !errors.Is(err, failure) {
				t.Fatal("release lost the failure cause", err)
			}
			if mode != "manifest destination" {
				if _, err := os.Stat(filepath.Join(out, "SHA256SUMS")); err == nil {
					t.Fatal("failed release published checksums", err)
				}
			}
			if entries, err := os.ReadDir(temporary); err != nil || len(entries) != 0 {
				t.Fatal("failed release leaked staging", entries, err)
			}
			partials, err := filepath.Glob(filepath.Join(out, ".tbt-release-*"))
			if err != nil || len(partials) != 0 {
				t.Fatal("failed release leaked partial archives", partials, err)
			}
		})
	}
}

func TestArchiveIsDeterministicAndFailedWritesPreserveExistingFiles(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "binary")
	writeFixture(t, binary, "same binary bytes")
	first, second := filepath.Join(root, "first.tar.gz"), filepath.Join(root, "second.tar.gz")
	digest, err := archiveBinary(first, binary)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(24 * time.Hour)
	if err := os.Chtimes(binary, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	other, err := archiveBinary(second, binary)
	if err != nil || !bytes.Equal(digest, other) {
		t.Fatal("archive depends on filesystem timestamps", err)
	}
	if data := readArchive(t, first); string(data) != "same binary bytes" {
		t.Fatal(string(data))
	}
	path := filepath.Join(root, "manifest")
	writeFixture(t, path, "keep original")
	failure := errors.New("partial write failure")
	err = publishFile(path, func(w io.Writer) error {
		if _, err := io.WriteString(w, "partial replacement"); err != nil {
			t.Fatal(err)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep original" {
		t.Fatal("failed write replaced an existing file", string(data), err)
	}
	if partials, err := filepath.Glob(filepath.Join(root, ".tbt-release-*")); err != nil || len(partials) != 0 {
		t.Fatal(partials, err)
	}
}

func TestReleaseCommandProcess(t *testing.T) {
	switch os.Getenv("TBT_RELEASE_TEST_PROCESS") {
	case "success":
		root, err := os.Getwd()
		if err != nil {
			os.Exit(1)
		}
		fmt.Print(root, "|", os.Getenv("TBT_RELEASE_MARKER"))
		os.Exit(0)
	case "failure":
		fmt.Fprintln(os.Stderr, "controlled compiler diagnostic")
		os.Exit(7)
	case "cancel":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
}

func TestCommandExecutionOutputFailuresAndCancellation(t *testing.T) {
	for _, mode := range []string{"success", "failure", "cancel", "missing"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			timeout := 3 * time.Second
			if mode == "cancel" {
				timeout = 500 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			name, args := os.Args[0], []string{"-test.run=^TestReleaseCommandProcess$"}
			if mode == "missing" {
				name = filepath.Join(root, "missing-tool")
			}
			env := append(os.Environ(), "TBT_RELEASE_TEST_PROCESS="+mode, "TBT_RELEASE_MARKER=retained", "GORACE=atexit_sleep_ms=0")
			data, err := runCommand(ctx, root, env, name, args...)
			switch mode {
			case "success":
				canonical, resolveErr := filepath.EvalSymlinks(root)
				if err != nil || resolveErr != nil || string(data) != canonical+"|retained" {
					t.Fatal(string(data), err)
				}
			case "failure":
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 7 || !strings.Contains(err.Error(), "controlled compiler diagnostic") {
					t.Fatal(err)
				}
			case "cancel":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("compiler cancellation was lost", err)
				}
			case "missing":
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestNativeReleaseBinaryPreservesVersionMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("CLI supports Unix terminals")
	}
	root := t.TempDir()
	// Copy production Go sources only. Local settings, credentials, website
	// assets, and tests cannot enter this isolated release checkout.
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel != "." && rel != "cmd" && rel != "internal" && !strings.HasPrefix(rel, "cmd"+string(os.PathSeparator)) && !strings.HasPrefix(rel, "internal"+string(os.PathSeparator)) {
				return filepath.SkipDir
			}
			return nil
		}
		if rel != "go.mod" && rel != "go.sum" && (!strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go")) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		writeFixture(t, filepath.Join(root, rel), string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "SITE_URL=https://release.example", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOWORK=off")
	b := builder{env: env, targets: []target{{runtime.GOOS, runtime.GOARCH, ""}}, run: func(ctx context.Context, root string, env []string, name string, args ...string) ([]byte, error) {
		if name == "git" {
			return []byte(testRevision), nil
		}
		return runCommand(ctx, root, env, name, args...)
	}}
	if err := build(t.Context(), root, "v1.2.3-rc.1", nil, b); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "builds", "v1.2.3-rc.1", "termbacktime_v1.2.3-rc.1_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz")
	binary := filepath.Join(t.TempDir(), "termbacktime")
	if err := os.WriteFile(binary, readArchive(t, archive), 0700); err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), binary, "--version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(output), "termbacktime v1.2.3-rc.1 revision="+testRevision+" (") {
		t.Fatal("release linker metadata was lost", string(output), err)
	}
}
