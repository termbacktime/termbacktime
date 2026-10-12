package recording

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCommandContextRoundTripRecoveryAndRedaction(t *testing.T) {
	status := 0
	id := strings.Repeat("a", 32)
	r := &Recording{Sizes: []int{80, 24}, Execution: &ExecutionContext{Shell: "bash", Version: "5.3", CWD: "/workspace"}, Lines: []Event{{Lifecycle: &CommandEvent{ID: id, Phase: "start", Text: "echo data", CWD: "/workspace/sub", Argv: []string{"echo", "password=secretvalue"}}}, {Time: 5, Lifecycle: &CommandEvent{ID: id, Phase: "end", ExitCode: &status}}}}
	var raw bytes.Buffer
	if err := Encode(&raw, r); err != nil {
		t.Fatal(err)
	}
	got, err := Decode(&raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Execution == nil || got.Execution.CWD != r.Execution.CWD || len(Commands(got)[0].Argv) != 2 {
		t.Fatal("context lost", got)
	}
	// The journal header holds context and event bodies hold command metadata.
	headerRecording := *r
	headerRecording.Lines = nil
	header, _ := json.Marshal(journalHeader{Format: "tbt-journal-v1", Recording: headerRecording})
	journal := append(header, '\n')
	for _, e := range r.Lines {
		b, _ := json.Marshal(e)
		journal = append(journal, append(b, '\n')...)
	}
	recovered, err := Recover(bytes.NewReader(journal))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Execution == nil || recovered.Execution.Version != "5.3" || Commands(recovered)[0].CWD != "/workspace/sub" {
		t.Fatal("recovery lost context", recovered)
	}
	if len(Scan(r)) == 0 {
		t.Fatal("argv omitted from secret scan")
	}
	clean, err := Redact(r, Rules{Version: 1, Detected: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(Scan(clean)) != 0 || !Commands(clean)[0].Partial {
		t.Fatal("redacted command remained complete", clean)
	}
	if r.Lines[0].Lifecycle.Argv[1] != "password=secretvalue" {
		t.Fatal("redaction mutated original")
	}
}
