// Package buildinfo resolves release and ordinary go-install metadata
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var Version, Revision string // Optional release linker overrides

func String() string {
	b, ok := debug.ReadBuildInfo()
	return buildString(b, ok)
}

func buildString(b *debug.BuildInfo, ok bool) string {
	v, r := tag(b, ok), Revision
	if ok {
		for _, s := range b.Settings {
			if s.Key == "vcs.revision" && r == "" {
				r = s.Value
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	if r == "" {
		r = "unknown"
	}
	return fmt.Sprintf("%s revision=%s (%s)", v, r, runtime.Version())
}

// Tag is available for ordinary go install builds as well as release binaries.
func Tag() string {
	b, ok := debug.ReadBuildInfo()
	return tag(b, ok)
}

func tag(b *debug.BuildInfo, ok bool) string {
	if Version != "" {
		return Version
	}
	if ok && b.Main.Version != "" && b.Main.Version != "(devel)" {
		return b.Main.Version
	}
	return "dev"
}
