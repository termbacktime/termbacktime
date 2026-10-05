package recording

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// WriteWrapped is the text-only transport for a v1 archive stored in a Gist.
func WriteWrapped(w io.Writer, source io.Reader) error {
	out := &recordingOutput{destination: w, remaining: MaxBytes}
	if _, err := fmt.Fprintf(out, `{"f":%q,"p":"`, Format); err != nil {
		return err
	}
	encoded := base64.NewEncoder(base64.StdEncoding, out)
	if _, err := io.Copy(encoded, io.LimitReader(source, MaxBytes+1)); err != nil {
		return err
	}
	if err := encoded.Close(); err != nil {
		return err
	}
	_, err := io.WriteString(out, `"}`)
	return err
}
func ReadWrapped(rd io.Reader) (*Recording, error) {
	data, err := ReadBounded(rd, MaxBytes)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Format string `json:"f"`
		Pack   string `json:"p"`
	}
	if json.Unmarshal(data, &wrapped) != nil || wrapped.Format != Format {
		return nil, ErrUnsupported
	}
	archive, err := base64.StdEncoding.Strict().DecodeString(wrapped.Pack)
	if err != nil {
		return nil, err
	}
	if base64.StdEncoding.EncodeToString(archive) != wrapped.Pack {
		return nil, fmt.Errorf("invalid recording wrapper")
	}
	return decodeArchive(archive)
}

// ValidateSmall is used before work that materializes a complete recording.
func ValidateSmall(r *Recording) error {
	if r.Source != nil {
		return fmt.Errorf("materialize the recording first")
	}
	header := *r
	header.Lines = nil
	data, err := json.Marshal(header)
	if err != nil {
		return err
	}
	size := int64(len(data) + 2)
	if size > MaxBytes {
		return fmt.Errorf("this operation is limited to 64 MiB")
	}
	for _, event := range r.Lines {
		data, err = json.Marshal(event)
		if err != nil {
			return err
		}
		size += int64(len(data) + 1)
		if size > MaxBytes {
			return fmt.Errorf("this operation is limited to 64 MiB")
		}
	}
	return nil
}

// EncodeSmall bounds both expanded data and binary output for transport/export consumers.
func EncodeSmall(w io.Writer, r *Recording) error {
	if err := ValidateSmall(r); err != nil {
		return err
	}
	return Encode(&recordingOutput{destination: w, remaining: MaxBytes}, r)
}
