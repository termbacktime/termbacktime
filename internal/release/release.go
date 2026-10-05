// Package release builds the CLI's platform archives and checksum manifest.
package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/termbacktime/termbacktime/internal/buildenv"
)

type target struct{ os, arch, arm string }

var targets = []target{
	{"darwin", "amd64", ""},
	{"darwin", "arm64", ""},
	{"linux", "amd64", ""},
	{"linux", "386", ""},
	{"linux", "arm64", ""},
	{"linux", "arm", "6"},
	{"linux", "arm", "7"},
	{"freebsd", "amd64", ""},
	{"freebsd", "386", ""},
}

var versionPattern = regexp.MustCompile(`^v1\.\d+\.\d+(-[A-Za-z0-9.-]+)?$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

type builder struct {
	env     []string
	targets []target
	run     func(context.Context, string, []string, string, ...string) ([]byte, error)
}

// Build packages all release targets in root/builds/version. An empty version
// reads VERSION; production origins use the shared shell-over-.env resolver.
// This builds local files only. Publishing remains a separate workflow step.
func Build(ctx context.Context, root, version string, output io.Writer) error {
	return build(ctx, root, version, output, builder{env: os.Environ(), targets: targets, run: runCommand})
}

func build(ctx context.Context, root, version string, output io.Writer, b builder) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	site, err := buildenv.SiteURL(root, "prod", func(key string) (string, bool) {
		for i := len(b.env) - 1; i >= 0; i-- {
			if value, found := strings.CutPrefix(b.env[i], key+"="); found {
				return value, true
			}
		}
		return "", false
	})
	if err != nil {
		return err
	}
	if version == "" {
		data, err := os.ReadFile(filepath.Join(root, "VERSION"))
		if err != nil {
			return fmt.Errorf("read release version: %w", err)
		}
		version = strings.TrimSpace(string(data))
	}
	if !versionPattern.MatchString(version) {
		return fmt.Errorf("expected an immutable v1 semantic version tag")
	}
	data, err := b.run(ctx, root, b.env, "git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve release revision: %w", err)
	}
	revision := strings.TrimSpace(string(data))
	if !revisionPattern.MatchString(revision) {
		return fmt.Errorf("invalid Git revision")
	}
	out := filepath.Join(root, "builds", version)
	if err := os.MkdirAll(out, 0755); err != nil {
		return fmt.Errorf("create release directory: %w", err)
	}
	staging, err := os.MkdirTemp("", "tbt-release-*")
	if err != nil {
		return fmt.Errorf("create release staging directory: %w", err)
	}
	defer func() { result = errors.Join(result, os.RemoveAll(staging)) }()
	if output == nil {
		output = io.Discard
	}
	ldflags := "-s -w" +
		" -X github.com/termbacktime/termbacktime/internal/buildinfo.Version=" + version +
		" -X github.com/termbacktime/termbacktime/internal/buildinfo.Revision=" + revision +
		" -X github.com/termbacktime/termbacktime/internal/config.EmbeddedSiteURL=" + site
	var checksums strings.Builder
	for _, platform := range b.targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		binary := filepath.Join(staging, "termbacktime")
		if err := os.Remove(binary); err != nil && !os.IsNotExist(err) {
			return err
		}
		data, err := b.run(ctx, root, targetEnvironment(b.env, platform), "go", "build", "-trimpath", "-ldflags", ldflags, "-o", binary, ".")
		if err != nil {
			return fmt.Errorf("build %s/%s: %w", platform.os, platform.archName(), err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := output.Write(data); err != nil {
			return err
		}
		name := fmt.Sprintf("termbacktime_%s_%s_%s.tar.gz", version, platform.os, platform.archName())
		digest, err := archiveBinary(filepath.Join(out, name), binary)
		if err != nil {
			return fmt.Errorf("package %s: %w", name, err)
		}
		fmt.Fprintf(&checksums, "%x  %s\n", digest, name)
		if _, err := fmt.Fprintln(output, name); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publishFile(filepath.Join(out, "SHA256SUMS"), func(w io.Writer) error {
		_, err := io.WriteString(w, checksums.String())
		return err
	})
}

func (t target) archName() string {
	if t.arm != "" {
		return "armv" + t.arm
	}
	return t.arch
}

func targetEnvironment(base []string, t target) []string {
	env := make([]string, 0, len(base)+4)
	for _, value := range base {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "CGO_ENABLED", "GOOS", "GOARCH", "GOARM":
		default:
			env = append(env, value)
		}
	}
	env = append(env, "CGO_ENABLED=0", "GOOS="+t.os, "GOARCH="+t.arch)
	if t.arm != "" {
		env = append(env, "GOARM="+t.arm)
	}
	return env
}

func runCommand(ctx context.Context, root string, env []string, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir, command.Env = root, env
	command.WaitDelay = 2 * time.Second
	data, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: %w\n%s", name, err, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func archiveBinary(path, binary string) ([]byte, error) {
	file, err := os.Open(binary)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() == 0 {
		return nil, fmt.Errorf("build did not produce a regular, nonempty binary")
	}
	digest := sha256.New()
	err = publishFile(path, func(w io.Writer) error {
		compressed := gzip.NewWriter(io.MultiWriter(w, digest))
		archive := tar.NewWriter(compressed)
		if err := archive.WriteHeader(&tar.Header{Name: "termbacktime", Mode: 0755, Size: stat.Size(), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := io.CopyN(archive, file, stat.Size()); err != nil {
			return err
		}
		return errors.Join(archive.Close(), compressed.Close())
	})
	if err != nil {
		return nil, err
	}
	return digest.Sum(nil), nil
}

// Rename complete files into place so interrupted writes never replace an
// existing archive or checksum manifest with an incomplete file.
func publishFile(path string, write func(io.Writer) error) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".tbt-release-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	if err := write(file); err != nil {
		return err
	}
	if err := file.Chmod(0644); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
