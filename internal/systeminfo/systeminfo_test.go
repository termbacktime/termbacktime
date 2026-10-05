package systeminfo

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestBoundedStringsRemoveControlsWithoutSplittingUnicode(t *testing.T) {
	value := bounded("\x00hello\n\t\x7f" + strings.Repeat("世界", 100))
	if !strings.HasPrefix(value, "hello") || len(value) > 512 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\n\t\x7f") {
		t.Fatal(value)
	}
}

func TestCollectionUsesExistingParentAndAllowlistedSnapshot(t *testing.T) {
	root := t.TempDir()
	if path := existingParent(filepath.Join(root, "missing", "recording.tbt")); path != root {
		t.Fatal("filesystem probe did not find existing parent", path)
	}
	result := collect(t.Context(), root, time.Second, []probe{
		func(_ context.Context, path string) func(*recording.SystemInfo) {
			if path != root {
				t.Errorf("probe path %q", path)
			}
			return func(s *recording.SystemInfo) { s.CPUModel = "test CPU"; s.LogicalCores = 4; s.RAMBytes = 1024 }
		},
	})
	if result.OS != runtime.GOOS || result.Arch != runtime.GOARCH || result.ObservedAt <= 0 || result.CPUModel != "test CPU" {
		t.Fatal(result)
	}
	data, err := json.Marshal(result)
	if err != nil || strings.Contains(string(data), root) {
		t.Fatal(string(data), err)
	}
	actual := Collect(t.Context(), root)
	if actual == nil || actual.ObservedAt <= 0 || (&recording.Metadata{Version: 1, CaptureSystem: actual}).Validate() != nil {
		t.Fatal(actual)
	}
}

func TestTimeoutAndParentCancellationDoNotWaitForUncooperativeProbe(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		if canceled {
			cancel()
		}
		started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
		start := time.Now()
		result := collect(ctx, t.TempDir(), 20*time.Millisecond, []probe{
			func(context.Context, string) func(*recording.SystemInfo) {
				close(started)
				<-release
				close(finished)
				return func(s *recording.SystemInfo) { s.CPUModel = "late" }
			},
		})
		cancel()
		if time.Since(start) > time.Second || result.CPUModel != "" {
			t.Fatal(result)
		}
		<-started
		close(release)
		<-finished
		if result.CPUModel != "" {
			t.Fatal("late probe mutated result")
		}
	}
}
