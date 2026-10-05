package recording

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionalMetadataRecoverySelectionAndRedaction(t *testing.T) {
	r := Recording{ID: NewID(), Sizes: []int{80, 24}, Info: Info{OS: "linux", Arch: "amd64", CLI: "v1.0.0"}, Metadata: &Metadata{Version: 1, Description: "token=syntheticsecret", CaptureSystem: &SystemInfo{OS: "linux", Arch: "amd64", CPUModel: "privatecpu", RAMBytes: 16 << 30, DiskTotalBytes: 100 << 30}, UploadSystem: &SystemInfo{CPUModel: "uploadcpu", RAMBytes: 8 << 30}}}
	path := filepath.Join(t.TempDir(), "capture.json")
	w, err := NewWriterWithInterval(path, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Append(Event{Lines: []string{"hello"}}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	recovered := filepath.Join(t.TempDir(), "recovered.json")
	if err = RecoverFile(path+".partial", recovered); err != nil {
		t.Fatal(err)
	}
	got, err := Load(recovered)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.CaptureSystem.CPUModel != "privatecpu" {
		t.Fatal("journal lost metadata")
	}
	if len(Scan(got)) == 0 {
		t.Fatal("metadata not scanned")
	}
	clean, err := Redact(got, Rules{Version: 1, Detected: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clean.Metadata.Description, "syntheticsecret") {
		t.Fatal("metadata not redacted")
	}
	got.SelectMetadata(MetadataFields{})
	b, _ := json.Marshal(got)
	md := MetadataMarkdown(got)
	for _, value := range []string{"privatecpu", "uploadcpu", "linux", "amd64", "17179869184", "107374182400"} {
		if strings.Contains(string(b), value) || strings.Contains(md, value) {
			t.Fatal("unchecked field leaked", value)
		}
	}
	if got.Info.CLI != "v1.0.0" || r.Metadata.CaptureSystem.CPUModel != "privatecpu" {
		t.Fatal("technical provenance or original lost")
	}
}
