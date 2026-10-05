package recording

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

type Interval struct {
	Start int64 `json:"start_ms"`
	End   int64 `json:"end_ms"`
}
type Rules struct {
	Version   int        `json:"version"`
	Literals  []string   `json:"literals"`
	Intervals []Interval `json:"intervals"`
	Detected  bool       `json:"detected,omitempty"`
}
type Finding struct {
	Kind    string `json:"kind"`
	At      int64  `json:"at_ms"`
	Field   string `json:"field"`
	Preview string `json:"preview"`
}
type span struct {
	start, end    int
	visible, keep bool
	plain         int
}

var detectors = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"token", regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{16,}|AKIA[A-Z0-9]{16})`)},
	{"credential", regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?token|password|secret|token)[ \t]*[:=][ \t]*["']?([^\s"']{6,})`)},
	{"private key", regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9 ]*PRIVATE KEY)-----.*?-----END (?:[A-Z0-9 ]*PRIVATE KEY)-----`)},
}

// Parse escape strings as a stream so payloads split between PTY reads stay hidden
func textSpans(raw string) ([]span, string) {
	var out []span
	var plain strings.Builder
	for i := 0; i < len(raw); {
		start := i
		r, n := utf8.DecodeRuneInString(raw[i:])
		i += n
		if r == 0x1b || r == 0x9b || r == 0x9d || r == 0x90 || r == 0x9e || r == 0x9f {
			kind := r
			if r == 0x1b {
				if i >= len(raw) {
					out = append(out, span{start: start, end: i})
					break
				}
				kind = rune(raw[i])
				i++
			}
			keep := false
			switch kind {
			case '[', 0x9b:
				for i < len(raw) {
					b := raw[i]
					i++
					if b >= 0x40 && b <= 0x7e {
						keep = strings.ContainsRune("@ABCDEFGHJKLMPSTXZ`abdfhlmrsu", rune(b))
						break
					}
				}
			case '(', ')', '*', '+':
				if i < len(raw) {
					keep = raw[i] == '0' || raw[i] == 'B'
					i++
				}
			case ']', 'P', '^', '_', 0x9d, 0x90, 0x9e, 0x9f:
				for i < len(raw) {
					if raw[i] == 7 {
						i++
						break
					}
					if raw[i] == 0x1b && i+1 < len(raw) && raw[i+1] == '\\' {
						i += 2
						break
					}
					rr, nn := utf8.DecodeRuneInString(raw[i:])
					i += nn
					if rr == 0x9c {
						break
					}
				}
			default:
				keep = strings.ContainsRune("78DEM c", kind)
			}
			out = append(out, span{start: start, end: i, keep: keep})
			continue
		}
		visible := r >= 32 && r != 127 && !(r >= 0x80 && r < 0xa0)
		if visible {
			for i < len(raw) {
				rr, nn := utf8.DecodeRuneInString(raw[i:])
				if rr < 32 || rr == 127 || (rr >= 0x80 && rr < 0xa0) {
					break
				}
				i += nn
			}
		}
		p := plain.Len()
		if visible || r == '\n' || r == '\r' || r == '\t' {
			plain.WriteString(raw[start:i])
		}
		out = append(out, span{start: start, end: i, visible: visible, keep: visible || r == '\n' || r == '\r' || r == '\t' || r == '\b', plain: p})
	}
	return out, plain.String()
}

func rawOutput(r *Recording) (string, []int, []int64) {
	var raw strings.Builder
	offsets := make([]int, len(r.Lines))
	times := make([]int64, len(r.Lines))
	var t int64
	for i, e := range r.Lines {
		offsets[i] = raw.Len()
		t += e.Time
		times[i] = t
		raw.WriteString(strings.Join(e.Lines, ""))
	}
	return raw.String(), offsets, times
}
func matches(text string) []struct {
	start, end int
	kind       string
} {
	var found []struct {
		start, end int
		kind       string
	}
	for _, d := range detectors {
		for _, m := range d.pattern.FindAllStringSubmatchIndex(text, -1) {
			start, end := m[0], m[1]
			if len(m) >= 4 && m[2] >= 0 {
				start, end = m[2], m[3]
			}
			if strings.Trim(text[start:end], "*") == "" {
				continue
			}
			found = append(found, struct {
				start, end int
				kind       string
			}{start, end, d.kind})
		}
	}
	return found
}

func Scan(r *Recording) []Finding {
	found := []Finding{}
	raw, offsets, times := rawOutput(r)
	spans, plain := textSpans(raw)
	visible := make([]span, 0, len(spans))
	for _, s := range spans {
		if s.visible {
			visible = append(visible, s)
		}
	}
	for _, m := range matches(plain) {
		at := int64(0)
		j := sort.Search(len(visible), func(i int) bool { return visible[i].plain > m.start }) - 1
		if j >= 0 {
			s := visible[j]
			if s.visible && m.start >= s.plain && m.start < s.plain+s.end-s.start {
				rawPos := s.start + m.start - s.plain
				i := sort.Search(len(offsets), func(i int) bool { return offsets[i] > rawPos }) - 1
				if i >= 0 {
					at = times[i]
				}
			}
		}
		found = append(found, Finding{m.kind, at, "output", "[redacted]"})
	}
	check := func(text, field string, at int64) {
		_, plain := textSpans(text)
		for _, m := range matches(plain) {
			found = append(found, Finding{m.kind, at, field, "[redacted]"})
		}
	}
	check(r.Title, "title", 0)
	check(r.Info.Arch, "metadata", 0)
	check(r.Info.OS, "metadata", 0)
	check(r.Info.Go, "metadata", 0)
	check(r.Info.CLI, "metadata", 0)
	check(r.Info.UploadCLI, "metadata", 0)
	r.Metadata.Texts(func(value, _ string) { check(value, "metadata", 0) })
	for _, m := range r.Markers {
		check(m.Label, "marker label", m.At)
		check(m.Note, "marker note", m.At)
	}
	var at int64
	for _, e := range r.Lines {
		at += e.Time
		if e.Lifecycle != nil {
			check(e.Lifecycle.Text, "command", at)
		}
	}
	for _, c := range r.Callouts {
		check(c.Text, "callout", c.At)
	}
	return found
}

func mergeRanges(ranges [][2]int) [][2]int {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i][0] < ranges[j][0] })
	out := ranges[:0]
	for _, r := range ranges {
		if len(out) > 0 && r[0] <= out[len(out)-1][1] {
			out[len(out)-1][1] = max(out[len(out)-1][1], r[1])
		} else {
			out = append(out, r)
		}
	}
	return out
}

func (rules *Rules) Validate(duration int64) error {
	if rules.Version != 1 {
		return fmt.Errorf("redaction rules require version 1")
	}
	if len(rules.Literals) > 10000 || len(rules.Intervals) > 10000 {
		return fmt.Errorf("too many redaction rules")
	}
	for _, s := range rules.Literals {
		if s == "" || len(s) > 65536 {
			return fmt.Errorf("invalid redaction literal")
		}
	}
	sort.Slice(rules.Intervals, func(i, j int) bool { return rules.Intervals[i].Start < rules.Intervals[j].Start })
	merged := []Interval{}
	for _, v := range rules.Intervals {
		if v.Start < 0 || v.End <= v.Start || v.End > duration {
			return fmt.Errorf("invalid redaction interval")
		}
		if len(merged) > 0 && v.Start <= merged[len(merged)-1].End {
			merged[len(merged)-1].End = max(v.End, merged[len(merged)-1].End)
		} else {
			merged = append(merged, v)
		}
	}
	rules.Intervals = merged
	return nil
}

func cleanText(raw string, rules Rules) string {
	spans, plain := textSpans(raw)
	ranges := [][2]int{}
	for _, literal := range rules.Literals {
		offset := 0
		for offset < len(plain) {
			i := strings.Index(plain[offset:], literal)
			if i < 0 {
				break
			}
			start := offset + i
			ranges = append(ranges, [2]int{start, start + len(literal)})
			offset = start + len(literal)
		}
	}
	if rules.Detected {
		for _, m := range matches(plain) {
			ranges = append(ranges, [2]int{m.start, m.end})
		}
	}
	ranges = mergeRanges(ranges)
	matchIndex := 0
	var out strings.Builder
	for _, s := range spans {
		if !s.keep {
			continue
		}
		if !s.visible {
			out.WriteString(raw[s.start:s.end])
			continue
		}
		for i := s.start; i < s.end; {
			r, n := utf8.DecodeRuneInString(raw[i:s.end])
			p := s.plain + i - s.start
			for matchIndex < len(ranges) && ranges[matchIndex][1] <= p {
				matchIndex++
			}
			mask := matchIndex < len(ranges) && ranges[matchIndex][0] <= p
			if mask {
				out.WriteString(strings.Repeat("*", ansi.StringWidth(string(r))))
			} else {
				out.WriteString(raw[i : i+n])
			}
			i += n
		}
	}
	return out.String()
}

func Redact(r *Recording, rules Rules) (*Recording, error) {
	if err := rules.Validate(Duration(r)); err != nil {
		return nil, err
	}
	// Match across events and ANSI colors, then map cleaned bytes to their source events
	raw, offsets, _ := rawOutput(r)
	spans, plain := textSpans(raw)
	ranges := [][2]int{}
	for _, literal := range rules.Literals {
		for offset := 0; offset < len(plain); {
			i := strings.Index(plain[offset:], literal)
			if i < 0 {
				break
			}
			a := offset + i
			ranges = append(ranges, [2]int{a, a + len(literal)})
			offset = a + len(literal)
		}
	}
	if rules.Detected {
		for _, m := range matches(plain) {
			ranges = append(ranges, [2]int{m.start, m.end})
		}
	}
	texts := make([]strings.Builder, len(r.Lines))
	ranges = mergeRanges(ranges)
	matchIndex := 0
	owner := func(pos int) int {
		return max(0, sort.Search(len(offsets), func(i int) bool { return offsets[i] > pos })-1)
	}
	for _, s := range spans {
		if !s.keep {
			continue
		}
		if !s.visible {
			if len(texts) > 0 {
				texts[owner(s.end-1)].WriteString(raw[s.start:s.end])
			}
			continue
		}
		for i := s.start; i < s.end; {
			rr, n := utf8.DecodeRuneInString(raw[i:s.end])
			p := s.plain + i - s.start
			for matchIndex < len(ranges) && ranges[matchIndex][1] <= p {
				matchIndex++
			}
			masked := matchIndex < len(ranges) && ranges[matchIndex][0] <= p
			if masked {
				texts[owner(i)].WriteString(strings.Repeat("*", ansi.StringWidth(string(rr))))
			} else {
				texts[owner(i)].WriteString(raw[i : i+n])
			}
			i += n
		}
	}
	info := Info{Arch: cleanText(r.Info.Arch, rules), OS: cleanText(r.Info.OS, rules), Go: cleanText(r.Info.Go, rules), CLI: cleanText(r.Info.CLI, rules), UploadCLI: cleanText(r.Info.UploadCLI, rules)}
	out := &Recording{ID: NewID(), Info: info, Started: r.Started, Title: cleanText(r.Title, rules), Sizes: append([]int(nil), r.Sizes...)}
	out.Metadata = r.Metadata.Clean(rules)
	mapTime := func(t int64) (int64, bool) {
		var removed int64
		for _, v := range rules.Intervals {
			if t >= v.Start && t < v.End {
				return 0, false
			}
			if t >= v.End {
				removed += v.End - v.Start
			}
		}
		return t - removed, true
	}
	var original, last int64
	cutCommands := map[string]bool{}
	for _, chapter := range Commands(r) {
		for _, cut := range rules.Intervals {
			if chapter.At < cut.End && chapter.End > cut.Start {
				cutCommands[chapter.ID] = true
				break
			}
		}
	}
	nextCut := 0
	size := append([]int(nil), r.Sizes...)
	emit := func(at int64, e Event) { e.Time = at - last; last = at; out.Lines = append(out.Lines, e) }
	for i, e := range r.Lines {
		original += e.Time
		for nextCut < len(rules.Intervals) && rules.Intervals[nextCut].End <= original {
			at, _ := mapTime(rules.Intervals[nextCut].End)
			emit(at, Event{Lines: []string{"\x1bc"}})
			emit(at, Event{Command: "s", Sizes: append([]int(nil), size...)})
			nextCut++
		}
		if e.Command == "s" {
			size = append([]int(nil), e.Sizes...)
		}
		at, keep := mapTime(original)
		if !keep {
			continue
		}
		e.Lines = nil
		if e.Lifecycle != nil {
			c := *e.Lifecycle
			c.Text = cleanText(c.Text, rules)
			c.Partial = c.Partial || cutCommands[c.ID]
			e.Lifecycle = &c
		}
		if texts[i].Len() > 0 {
			e.Lines = []string{texts[i].String()}
		}
		emit(at, e)
	}
	for nextCut < len(rules.Intervals) {
		at, _ := mapTime(rules.Intervals[nextCut].End)
		emit(at, Event{Lines: []string{"\x1bc"}})
		nextCut++
	}
	for _, m := range r.Markers {
		at, keep := mapTime(m.At)
		if keep {
			m.At = at
			m.Label = cleanText(m.Label, rules)
			m.Note = cleanText(m.Note, rules)
			out.Markers = append(out.Markers, m)
		}
	}
	for _, c := range r.Callouts {
		if at, keep := mapTime(c.At); keep {
			c.At = at
			c.Text = cleanText(c.Text, rules)
			if c.Text != "" {
				out.Callouts = append(out.Callouts, c)
			}
		}
	}
	markPartialCommands(out)
	return out, out.Validate()
}

// A removed lifecycle boundary must never turn a partial command into a complete one
func markPartialCommands(r *Recording) {
	starts, ends := map[string]bool{}, map[string]bool{}
	for _, e := range r.Lines {
		if c := e.Lifecycle; c != nil {
			if c.Phase == "start" {
				starts[c.ID] = true
			} else {
				ends[c.ID] = true
			}
		}
	}
	for i := range r.Lines {
		if c := r.Lines[i].Lifecycle; c != nil && (!starts[c.ID] || !ends[c.ID]) {
			copy := *c
			copy.Partial = true
			r.Lines[i].Lifecycle = &copy
		}
	}
}
