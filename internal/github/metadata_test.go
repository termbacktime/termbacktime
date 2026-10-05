package github

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/termbacktime/termbacktime/internal/buildinfo"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func TestUploadMetadataPreservesOriginalAndLegacyProvenance(t *testing.T) {
	for _, captured := range []string{"", "v1.2.3"} {
		t.Run("capture="+captured, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "original.json")
			r := &recording.Recording{Sizes: []int{80, 24}, Title: "Original", Info: recording.Info{CLI: captured}, Lines: []recording.Event{{Lines: []string{"output"}}}}
			if err := recording.Save(path, r); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			source, cleanup, err := UploadSource(path, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			got, err := recording.Load(source)
			if err != nil || got.Info.CLI != captured || got.Info.UploadCLI != buildinfo.Tag() || got.Title != r.Title || got.Lines[0].Lines[0] != "output" {
				t.Fatalf("incorrect upload metadata: %+v, %v", got, err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) || source == path {
				t.Fatal("upload modified the source")
			}
			cleanup()
			if _, err := os.Stat(filepath.Dir(source)); !os.IsNotExist(err) {
				t.Fatal("temporary metadata was not removed")
			}
		})
	}
}
