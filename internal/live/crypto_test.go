package live

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestEncryptedChunkRoundTripAndTamper(t *testing.T) {
	root := bytes.Repeat([]byte{7}, 32)
	s, err := NewSealer(root, "room")
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("a"), ChunkSize*2+7)
	chunks, err := s.Seal(payload)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := hkdf.Key(sha256.New, root, s.generation[:], "termbacktime/live/v1/room", 32)
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	var decoded []byte
	for i, c := range chunks {
		nonce := make([]byte, 12)
		binary.BigEndian.PutUint32(nonce, uint32(i))
		copy(nonce[4:], c[20:28])
		aad := append(append([]byte{}, c[:32]...), "room"...)
		p, err := aead.Open(nil, nonce, c[32:], aad)
		if err != nil {
			t.Fatal(err)
		}
		decoded = append(decoded, p...)
		c[len(c)-1] ^= 1
		if _, err = aead.Open(nil, nonce, c[32:], aad); err == nil {
			t.Fatal("accepted tamper")
		}
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("round trip mismatch")
	}
	next, _ := NewSealer(root, "room")
	if s.Generation() == next.Generation() {
		t.Fatal("generation reused")
	}
}

// Export an independent Go-produced fixture for the browser Web Crypto test
func TestBrowserFixture(t *testing.T) {
	destination := os.Getenv("TBT_CRYPTO_FIXTURE")
	if destination == "" {
		t.Skip("fixture generation not requested")
	}
	root := bytes.Repeat([]byte{9}, 32)
	s, _ := NewSealer(root, "interop-room")
	chunks, _ := s.Seal([]byte(`{"type":"snapshot","screen":"hello 世界"}`))
	fixture := map[string]any{"key": hex.EncodeToString(root), "room": "interop-room", "generation": s.Generation(), "frame": hex.EncodeToString(chunks[0])}
	b, _ := json.Marshal(fixture)
	if err := os.WriteFile(destination, b, 0600); err != nil {
		t.Fatal(err)
	}
}
