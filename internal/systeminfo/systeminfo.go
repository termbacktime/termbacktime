// Package systeminfo collects an allowlisted snapshot, never identifiers or filesystem paths.
package systeminfo

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/termbacktime/termbacktime/internal/recording"
)

type probe func(context.Context, string) func(*recording.SystemInfo)

// Collect returns within two seconds even when a platform probe ignores cancellation.
func Collect(parent context.Context, filesystem string) *recording.SystemInfo {
	return collect(parent, filesystem, 2*time.Second, []probe{
		func(ctx context.Context, _ string) func(*recording.SystemInfo) {
			platform, _, version, _ := host.PlatformInformationWithContext(ctx)
			return func(s *recording.SystemInfo) {
				if platform != "" {
					s.OS = bounded(platform)
				}
				s.OSVersion = bounded(version)
			}
		},
		func(ctx context.Context, _ string) func(*recording.SystemInfo) {
			list, _ := cpu.InfoWithContext(ctx)
			physical, _ := cpu.CountsWithContext(ctx, false)
			logical, _ := cpu.CountsWithContext(ctx, true)
			model := ""
			if len(list) > 0 {
				model = bounded(list[0].ModelName)
			}
			return func(s *recording.SystemInfo) {
				s.CPUModel = model
				s.PhysicalCores = max(0, min(65536, physical))
				s.LogicalCores = max(0, min(65536, logical))
			}
		},
		func(ctx context.Context, _ string) func(*recording.SystemInfo) {
			value, _ := mem.VirtualMemoryWithContext(ctx)
			return func(s *recording.SystemInfo) {
				if value != nil && value.Total <= 9007199254740991 {
					s.RAMBytes = value.Total
				}
			}
		},
		func(ctx context.Context, filesystem string) func(*recording.SystemInfo) {
			filesystem = existingParent(filesystem)
			value, _ := disk.UsageWithContext(ctx, filesystem)
			return func(s *recording.SystemInfo) {
				if value != nil && value.Total <= 9007199254740991 {
					s.DiskTotalBytes = value.Total
				}
			}
		},
	})
}

func collect(parent context.Context, filesystem string, timeout time.Duration, probes []probe) *recording.SystemInfo {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	base := &recording.SystemInfo{ObservedAt: time.Now().Unix(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	// Buffered results allow late probes to finish without modifying the returned snapshot.
	results := make(chan func(*recording.SystemInfo), len(probes))
	for _, lookup := range probes {
		go func() { results <- lookup(ctx, filesystem) }()
	}
	for range probes {
		select {
		case apply := <-results:
			apply(base)
		case <-ctx.Done():
			return base
		}
	}
	return base
}

func existingParent(filesystem string) string {
	for {
		if _, err := os.Stat(filesystem); err == nil {
			break
		}
		next := filepath.Dir(filesystem)
		if next == filesystem {
			break
		}
		filesystem = next
	}
	return filesystem
}

func bounded(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	for len(value) > 512 {
		value = string([]rune(value)[:len([]rune(value))-1])
	}
	return value
}
