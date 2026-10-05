// Package sealed implements the authenticated, multipart recording container
package sealed

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/termbacktime/termbacktime/internal/recording"
)

const Format = "tbt-encrypted-v1"
const Filename = "terminal-recording.encrypted.json"
const ChunkSize = 65536
const ChunksPerPart = 40
const MaxPartBytes = 4 << 20

type Manifest struct {
	Format    string   `json:"f"`
	ID        string   `json:"i"`
	Bytes     int64    `json:"b"`
	ChunkSize int      `json:"s"`
	Chunks    int      `json:"c"`
	Prefix    string   `json:"n"`
	Parts     []string `json:"p"`
}

func PartName(i int) string { return fmt.Sprintf("terminal-recording.part-%04d.tbt", i) }
func (m Manifest) Validate() error {
	if m.Format != Format || !recording.ValidID(m.ID) || m.Bytes < 1 || m.Bytes > recording.MaxBytes || m.ChunkSize != ChunkSize || m.Chunks != int((m.Bytes+ChunkSize-1)/ChunkSize) || len(m.Parts) != (m.Chunks+ChunksPerPart-1)/ChunksPerPart {
		return fmt.Errorf("invalid encrypted recording manifest")
	}
	p, err := base64.RawURLEncoding.DecodeString(m.Prefix)
	if err != nil || len(p) != 8 {
		return fmt.Errorf("invalid encryption prefix")
	}
	for i, name := range m.Parts {
		if name != PartName(i) {
			return fmt.Errorf("invalid encrypted part name")
		}
	}
	return nil
}
func (m Manifest) aad(index int) []byte {
	return []byte(fmt.Sprintf("%s|%s|%d|%d|%d|%d|%s|%d", m.Format, m.ID, m.Bytes, m.ChunkSize, m.Chunks, len(m.Parts), m.Prefix, index))
}
func newCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("a complete sharing link with a 32-byte key is required")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func nonce(prefix []byte, index int) []byte {
	b := make([]byte, 12)
	copy(b, prefix)
	binary.BigEndian.PutUint32(b[8:], uint32(index))
	return b
}

func EncryptFile(ctx context.Context, path, directory string) (Manifest, string, map[string]string, error) {
	m := Manifest{Format: Format, ID: recording.NewID(), ChunkSize: ChunkSize}
	files := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return m, "", nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return m, "", nil, err
	}
	if !st.Mode().IsRegular() || st.Size() < 1 || st.Size() > recording.MaxBytes {
		return m, "", nil, fmt.Errorf("invalid recording size")
	}
	m.Bytes = st.Size()
	m.Chunks = int((m.Bytes + ChunkSize - 1) / ChunkSize)
	for i := 0; i < (m.Chunks+ChunksPerPart-1)/ChunksPerPart; i++ {
		m.Parts = append(m.Parts, PartName(i))
	}
	key := make([]byte, 32)
	defer clear(key)
	prefix := make([]byte, 8)
	if _, err = rand.Read(key); err != nil {
		return m, "", nil, err
	}
	if _, err = rand.Read(prefix); err != nil {
		return m, "", nil, err
	}
	m.Prefix = base64.RawURLEncoding.EncodeToString(prefix)
	aead, _ := newCipher(key)
	buf := make([]byte, ChunkSize)
	defer clear(buf)
	var index int
	for _, name := range m.Parts {
		path := filepath.Join(directory, name)
		part, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return m, "", nil, err
		}
		err = func() error {
			defer part.Close()
			w := bufio.NewWriter(part)
			for i := 0; i < ChunksPerPart && index < m.Chunks; i++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				size := min(int64(ChunkSize), m.Bytes-int64(index)*ChunkSize)
				if _, err := io.ReadFull(f, buf[:size]); err != nil {
					return err
				}
				encrypted := aead.Seal(nil, nonce(prefix, index), buf[:size], m.aad(index))
				if _, err := w.WriteString(base64.RawURLEncoding.EncodeToString(encrypted) + "\n"); err != nil {
					return err
				}
				index++
			}
			return w.Flush()
		}()
		if err != nil {
			return m, "", nil, err
		}
		files[name] = path
	}
	var extra [1]byte
	if n, _ := f.Read(extra[:]); n != 0 {
		return m, "", nil, fmt.Errorf("recording changed during encryption")
	}
	b, _ := json.Marshal(m)
	manifestPath := filepath.Join(directory, Filename)
	if err := os.WriteFile(manifestPath, b, 0600); err != nil {
		return m, "", nil, err
	}
	files[Filename] = manifestPath
	return m, base64.RawURLEncoding.EncodeToString(key), files, nil
}

// Decrypt validates all chunks and total length before the caller decodes a recording
func Decrypt(ctx context.Context, m Manifest, keyText string, part func(string) ([]byte, error)) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	key, err := base64.RawURLEncoding.DecodeString(keyText)
	if err != nil {
		return nil, fmt.Errorf("invalid recording key")
	}
	defer clear(key)
	aead, err := newCipher(key)
	if err != nil {
		return nil, err
	}
	prefix, _ := base64.RawURLEncoding.DecodeString(m.Prefix)
	out := make([]byte, 0, int(m.Bytes))
	valid := false
	defer func() {
		if !valid {
			clear(out)
		}
	}()
	index := 0
	type loadedPart struct {
		data []byte
		err  error
	}
	prefetch := func(name string) <-chan loadedPart {
		result := make(chan loadedPart, 1)
		go func() {
			if err := ctx.Err(); err != nil {
				result <- loadedPart{err: err}
				return
			}
			b, e := part(name)
			result <- loadedPart{b, e}
		}()
		return result
	}
	pending := prefetch(m.Parts[0])
	for partIndex := range m.Parts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var loaded loadedPart
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case loaded = <-pending:
		}
		if loaded.err != nil {
			return nil, loaded.err
		}
		data := loaded.data
		if partIndex+1 < len(m.Parts) {
			pending = prefetch(m.Parts[partIndex+1])
		}
		if len(data) > MaxPartBytes || len(data) == 0 || data[len(data)-1] != '\n' {
			return nil, fmt.Errorf("invalid encrypted part")
		}
		lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		expected := min(ChunksPerPart, m.Chunks-index)
		if len(lines) != expected {
			return nil, fmt.Errorf("missing or extra encrypted chunks")
		}
		for _, line := range lines {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			encrypted, err := base64.RawURLEncoding.DecodeString(line)
			if err != nil {
				return nil, fmt.Errorf("invalid encrypted chunk")
			}
			size := min(int64(ChunkSize), m.Bytes-int64(index)*ChunkSize)
			if int64(len(encrypted)) != size+16 {
				return nil, fmt.Errorf("invalid encrypted chunk length")
			}
			plain, err := aead.Open(nil, nonce(prefix, index), encrypted, m.aad(index))
			if err != nil {
				return nil, fmt.Errorf("recording authentication failed: wrong key or modified data")
			}
			out = append(out, plain...)
			clear(plain)
			index++
		}
	}
	if int64(len(out)) != m.Bytes || index != m.Chunks {
		return nil, fmt.Errorf("truncated encrypted recording")
	}
	valid = true
	return out, nil
}
