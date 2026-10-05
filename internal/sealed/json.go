package sealed

import "github.com/termbacktime/termbacktime/internal/jsonkeys"

// New output uses single-letter keys; existing descriptive recordings remain readable
func (value *Manifest) UnmarshalJSON(data []byte) error {
	type wire Manifest
	return jsonkeys.Unmarshal(data, (*wire)(value), map[string]string{"format": "f", "id": "i", "bytes": "b", "chunk_size": "s", "chunks": "c", "prefix": "n", "parts": "p"})
}
