package recording

import "github.com/termbacktime/termbacktime/internal/jsonkeys"

func recordingFormat(data []byte) (string, error) {
	var envelope struct {
		Format string `json:"f"`
	}
	err := jsonkeys.Unmarshal(data, &envelope, map[string]string{"format": "f"})
	return envelope.Format, err
}

// V1 payloads use single-letter keys; schema aliases also serve inspection and metadata tooling.
func (value *Info) UnmarshalJSON(data []byte) error {
	type wire Info
	return jsonkeys.Unmarshal(data, (*wire)(value), map[string]string{"cli": "c", "upload_cli": "u", "arch": "a", "os": "o", "go": "v"})
}

func (value *Event) UnmarshalJSON(data []byte) error {
	type wire Event
	return jsonkeys.Unmarshal(data, (*wire)(value), map[string]string{"command": "e", "delay_ms": "t", "type": "c", "output": "l", "size": "s"})
}

func (value *Recording) UnmarshalJSON(data []byte) error {
	type wire Recording
	return jsonkeys.Unmarshal(data, (*wire)(value), map[string]string{"metadata": "m", "id": "u", "markers": "a", "callouts": "c", "info": "i", "started_at": "d", "title": "t", "size": "s", "events": "r", "packed": "p"})
}

func (value *journalHeader) UnmarshalJSON(data []byte) error {
	type wire journalHeader
	return jsonkeys.Unmarshal(data, (*wire)(value), map[string]string{"format": "f", "recording": "r"})
}

func (value *Metadata) UnmarshalJSON(data []byte) error {
	type wire Metadata
	return jsonkeys.Unmarshal(data, (*wire)(value), map[string]string{"version": "v", "description": "d", "capture_system": "c", "upload_system": "u"})
}

func (value *SystemInfo) UnmarshalJSON(data []byte) error {
	type wire SystemInfo
	return jsonkeys.Unmarshal(data, (*wire)(value), map[string]string{"observed_at": "t", "os": "o", "os_version": "v", "arch": "a", "cpu_model": "c", "physical_cores": "p", "logical_cores": "l", "ram_bytes": "r", "disk_total_bytes": "d"})
}

func (value *Marker) UnmarshalJSON(data []byte) error {
	type wire Marker
	return jsonkeys.Unmarshal(data, (*wire)(value), map[string]string{"id": "i", "at_ms": "a", "label": "l", "note": "n", "kind": "k"})
}

func (value *CommandEvent) UnmarshalJSON(data []byte) error {
	type wire CommandEvent
	return jsonkeys.Unmarshal(data, (*wire)(value), map[string]string{"id": "i", "phase": "p", "text": "t", "exit_code": "e", "partial": "r"})
}

func (value *Callout) UnmarshalJSON(data []byte) error {
	type wire Callout
	return jsonkeys.Unmarshal(data, (*wire)(value), map[string]string{"id": "i", "at_ms": "a", "text": "t", "duration_ms": "d", "pause": "p"})
}
