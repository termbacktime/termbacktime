package recording

import (
	"fmt"
	"strings"
	"time"
)

// Metadata contains only optional, reviewable information. No device identifiers or paths.
type Metadata struct {
	Version       int         `json:"v"`
	Description   string      `json:"d,omitempty"`
	CaptureSystem *SystemInfo `json:"c,omitempty"`
	UploadSystem  *SystemInfo `json:"u,omitempty"`
}

type SystemInfo struct {
	ObservedAt     int64  `json:"t,omitempty"`
	OS             string `json:"o,omitempty"`
	OSVersion      string `json:"v,omitempty"`
	Arch           string `json:"a,omitempty"`
	CPUModel       string `json:"c,omitempty"`
	PhysicalCores  int    `json:"p,omitempty"`
	LogicalCores   int    `json:"l,omitempty"`
	RAMBytes       uint64 `json:"r,omitempty"`
	DiskTotalBytes uint64 `json:"d,omitempty"`
}

func (m *Metadata) Validate() error {
	if m == nil {
		return nil
	}
	if m.Version != 1 || len(m.Description) > 16384 {
		return fmt.Errorf("invalid recording metadata")
	}
	for _, s := range []*SystemInfo{m.CaptureSystem, m.UploadSystem} {
		if s == nil {
			continue
		}
		if s.ObservedAt < 0 || s.ObservedAt > 9007199254740991 || s.PhysicalCores < 0 || s.PhysicalCores > 65536 || s.LogicalCores < 0 || s.LogicalCores > 65536 || s.RAMBytes > 9007199254740991 || s.DiskTotalBytes > 9007199254740991 {
			return fmt.Errorf("invalid computer metadata")
		}
		for _, value := range []string{s.OS, s.OSVersion, s.Arch, s.CPUModel} {
			if len(value) > 512 {
				return fmt.Errorf("computer metadata is too large")
			}
		}
	}
	return nil
}

func (m *Metadata) Clone() *Metadata {
	if m == nil {
		return nil
	}
	out := *m
	if m.CaptureSystem != nil {
		s := *m.CaptureSystem
		out.CaptureSystem = &s
	}
	if m.UploadSystem != nil {
		s := *m.UploadSystem
		out.UploadSystem = &s
	}
	return &out
}

func (m *Metadata) Texts(visit func(string, string)) {
	if m == nil {
		return
	}
	visit(m.Description, "description")
	for name, s := range map[string]*SystemInfo{"capture computer": m.CaptureSystem, "upload computer": m.UploadSystem} {
		if s == nil {
			continue
		}
		for _, value := range []string{s.OS, s.OSVersion, s.Arch, s.CPUModel} {
			visit(value, name)
		}
	}
}

func (m *Metadata) Clean(rules Rules) *Metadata {
	out := m.Clone()
	if out == nil {
		return nil
	}
	out.Description = cleanText(out.Description, rules)
	for _, s := range []*SystemInfo{out.CaptureSystem, out.UploadSystem} {
		if s == nil {
			continue
		}
		s.OS = cleanText(s.OS, rules)
		s.OSVersion = cleanText(s.OSVersion, rules)
		s.Arch = cleanText(s.Arch, rules)
		s.CPUModel = cleanText(s.CPUModel, rules)
	}
	return out
}

// A single selection is applied to both snapshots and duplicate legacy platform fields.
type MetadataFields struct {
	OS   bool `json:"os"`
	CPU  bool `json:"cpu"`
	RAM  bool `json:"ram"`
	Disk bool `json:"disk"`
}

func AllMetadataFields() MetadataFields { return MetadataFields{true, true, true, true} }
func (r *Recording) SelectMetadata(fields MetadataFields) {
	r.Metadata = r.Metadata.Clone()
	if !fields.OS {
		r.Info.OS = ""
		r.Info.Arch = ""
	}
	if r.Metadata == nil {
		return
	}
	for _, s := range []*SystemInfo{r.Metadata.CaptureSystem, r.Metadata.UploadSystem} {
		if s == nil {
			continue
		}
		if !fields.OS {
			s.OS = ""
			s.OSVersion = ""
			s.Arch = ""
		}
		if !fields.CPU {
			s.CPUModel = ""
			s.PhysicalCores = 0
			s.LogicalCores = 0
		}
		if !fields.RAM {
			s.RAMBytes = 0
		}
		if !fields.Disk {
			s.DiskTotalBytes = 0
		}
	}
}

// Escape user prose so Markdown cannot load remote images, embed HTML or conceal link targets.
func markdownText(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r < 32 && r != '\n' && r != '\t' || r == 127 {
			continue
		}
		if strings.ContainsRune("\\`*_{}[]()#+-.!|<>", r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

func MetadataMarkdown(r *Recording) string {
	if r.Metadata == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", markdownText(r.Title))
	if r.Metadata.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", markdownText(r.Metadata.Description))
	}
	b.WriteString("## Recording\n\n")
	if r.Started > 0 {
		fmt.Fprintf(&b, "- Recorded: %s\n", time.Unix(r.Started, 0).UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "- Duration: %.1f seconds\n- Terminal: %d × %d\n", float64(Duration(r))/1000, r.Sizes[0], r.Sizes[1])
	for _, row := range [][2]string{{"Recorded with", r.Info.CLI}, {"Uploaded with", r.Info.UploadCLI}} {
		if row[1] != "" {
			fmt.Fprintf(&b, "- %s: TermBackTime %s\n", row[0], markdownText(row[1]))
		}
	}
	for _, group := range []struct {
		name string
		info *SystemInfo
	}{{"Capture computer", r.Metadata.CaptureSystem}, {"Upload computer", r.Metadata.UploadSystem}} {
		s := group.info
		if s == nil {
			continue
		}
		b.WriteString("\n## " + group.name + "\n\n")
		if s.OS != "" || s.Arch != "" {
			fmt.Fprintf(&b, "- Platform: %s\n", markdownText(strings.TrimSpace(s.OS+" "+s.OSVersion+" "+s.Arch)))
		}
		if s.CPUModel != "" {
			fmt.Fprintf(&b, "- CPU: %s\n", markdownText(s.CPUModel))
		}
		if s.PhysicalCores > 0 {
			fmt.Fprintf(&b, "- Physical cores: %d\n", s.PhysicalCores)
		}
		if s.LogicalCores > 0 {
			fmt.Fprintf(&b, "- Logical cores: %d\n", s.LogicalCores)
		}
		if s.RAMBytes > 0 {
			fmt.Fprintf(&b, "- Total RAM: %.1f GiB\n", float64(s.RAMBytes)/(1<<30))
		}
		if s.DiskTotalBytes > 0 {
			fmt.Fprintf(&b, "- Filesystem capacity: %.1f GiB\n", float64(s.DiskTotalBytes)/(1<<30))
		}
	}
	b.WriteString("\nGenerated by TermBackTime from reviewed recording metadata.\n")
	return b.String()
}
