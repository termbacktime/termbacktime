package recording

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termbacktime/termbacktime/internal/buildinfo"
	"golang.org/x/sys/unix"
)

func TestCLIMetadataInspectionAndRedaction(t *testing.T) {
	info := CurrentInfo()
	if info.CLI != buildinfo.Tag() || info.OS == "" || info.Arch == "" || info.Go == "" {
		t.Fatalf("incomplete capture metadata: %+v", info)
	}
	info.UploadCLI = "v1.3.0"
	r := &Recording{Info: info, Sizes: []int{80, 24}, Lines: []Event{{Lines: []string{"public output"}}}}
	path := filepath.Join(t.TempDir(), "recording.json")
	if err := Save(path, r); err != nil {
		t.Fatal(err)
	}
	summary, err := Inspect(path)
	if err != nil || summary.Info != info {
		t.Fatalf("inspection lost versions: %+v, %v", summary, err)
	}
	cleaned, err := Redact(r, Rules{Version: 1})
	if err != nil || cleaned.Info != info {
		t.Fatalf("redaction lost versions: %+v, %v", cleaned, err)
	}
	r.Info.CLI = "token=syntheticsecret"
	r.Info.UploadCLI = "password=syntheticpassword"
	if len(Scan(r)) != 2 {
		t.Fatal("new metadata fields bypass secret scanning")
	}
	cleaned, err = Redact(r, Rules{Version: 1, Detected: true})
	if err != nil || len(Scan(cleaned)) != 0 || strings.Contains(cleaned.Info.CLI, "syntheticsecret") || strings.Contains(cleaned.Info.UploadCLI, "syntheticpassword") {
		t.Fatal("new metadata fields bypass redaction", err)
	}
	if r.Info.CLI != "token=syntheticsecret" {
		t.Fatal("redaction modified source metadata")
	}
}

type cancelInspectionContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelInspectionContext) Err() error {
	c.checks--
	if c.checks == 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestInspectionCancellationStopsScanningAndReleasesFile(t *testing.T) {
	for _, journal := range []bool{false, true} {
		t.Run(map[bool]string{false: "archive", true: "journal"}[journal], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recording.tbt")
			w, err := NewWriter(path, Recording{Sizes: []int{80, 24}})
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			for n := 0; n < 10; n++ {
				if err := w.Append(Event{Lines: []string{"event"}}); err != nil {
					t.Fatal(err)
				}
			}
			if journal {
				err = w.Close()
				path += ".partial"
			} else {
				err = w.Finish()
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			checked := &cancelInspectionContext{Context: ctx, cancel: cancel, checks: 4}
			if _, err = InspectContext(checked, path); !errors.Is(err, context.Canceled) {
				t.Fatal("inspection continued after cancellation", err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
				t.Fatal("canceled inspection retained its file lock", err)
			}
			if _, err := InspectContext(ctx, path+".missing"); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled inspection accessed another file", err)
			}
		})
	}
}
