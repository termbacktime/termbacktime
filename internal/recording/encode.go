package recording

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const Format = "tbt-recording-v1"
const JournalFormat = "tbt-journal-v1"
const MaxExpandedBytes = 1 << 30
const MaxArchiveBytes = MaxExpandedBytes + (16 << 20)
const MaxManifestBytes = 4 << 20
const ChunkBytes = 1 << 20
const ChunkEvents = 4096

type Chunk struct {
	Count int `json:"n"`
	Bytes int `json:"b"`
}
type Manifest struct {
	Format    string    `json:"f"`
	Recording Recording `json:"r"`
	Count     int       `json:"n"`
	Duration  int64     `json:"d"`
	Bytes     int64     `json:"b"`
	Images    bool      `json:"h"`
	Chunks    []Chunk   `json:"c"`
}

func chunkName(i int) string { return fmt.Sprintf("events/%06d.json", i) }

// Encode writes a v1 archive. Journal finalization uses the same incremental writer.
func Encode(w io.Writer, r *Recording) error {
	if r.Pack != "" || r.Source != nil {
		return fmt.Errorf("materialize the recording before encoding it")
	}
	if err := r.Validate(); err != nil {
		return err
	}
	return writeArchive(w, *r, func(write func(Event) error) error {
		for _, event := range r.Lines {
			if err := write(event); err != nil {
				return err
			}
		}
		return nil
	})
}
func writeArchive(destination io.Writer, header Recording, visit func(func(Event) error) error) error {
	header.Lines, header.Pack, header.Source = nil, "", nil
	metadata, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if len(metadata) > MaxManifestBytes {
		return fmt.Errorf("recording metadata too large")
	}
	m := Manifest{Format: Format, Recording: header, Chunks: []Chunk{}}
	z := zip.NewWriter(&recordingOutput{destination: destination, remaining: MaxArchiveBytes})
	var chunk, timing bytes.Buffer
	chunk.WriteByte('[')
	count := 0
	add := func(name string, data []byte, method uint16) error {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	flush := func() error {
		if count == 0 {
			return nil
		}
		chunk.WriteByte(']')
		if err := add(chunkName(len(m.Chunks)), chunk.Bytes(), zip.Deflate); err != nil {
			return err
		}
		m.Chunks = append(m.Chunks, Chunk{count, chunk.Len()})
		m.Bytes += int64(chunk.Len())
		if m.Bytes+int64(len(metadata)) > MaxExpandedBytes {
			return fmt.Errorf("recording exceeds 1 GiB expanded limit")
		}
		chunk.Reset()
		chunk.WriteByte('[')
		count = 0
		return nil
	}
	err = visit(func(e Event) error {
		if err := validateEvent(e, &m.Duration); err != nil {
			return err
		}
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if len(data) > MaxJournalLineBytes {
			return fmt.Errorf("event exceeds 256 KiB limit")
		}
		if count == ChunkEvents || chunk.Len()+len(data)+2 > ChunkBytes {
			if err := flush(); err != nil {
				return err
			}
		}
		if count > 0 {
			chunk.WriteByte(',')
		}
		chunk.Write(data)
		count++
		m.Count++
		if m.Count > MaxEvents {
			return fmt.Errorf("too many recording events")
		}
		var delta [4]byte
		binary.LittleEndian.PutUint32(delta[:], uint32(e.Time))
		timing.Write(delta[:])
		for _, text := range e.Lines {
			if strings.Contains(text, "\x1bP") || strings.Contains(text, "1337;") {
				m.Images = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err = flush(); err != nil {
		return err
	}
	if err = validateMetadata(&header, m.Duration); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(data) > MaxManifestBytes {
		return fmt.Errorf("manifest exceeds 4 MiB")
	}
	if err = add("timing.bin", timing.Bytes(), zip.Store); err != nil {
		return err
	}
	if err = add("manifest.json", data, zip.Store); err != nil {
		return err
	}
	return z.Close()
}
