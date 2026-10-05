package live

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const ChunkSize = 12 * 1024
const MaxFrame = 2 << 20
const HeaderSize = 32

// Wire: TBT1 | generation[16] | sequence uint64 | chunk uint16 | count uint16
// Header + room ID are authenticated. Nonce: chunk uint32 | sequence uint64
type Sealer struct {
	room       string
	generation [16]byte
	sequence   uint64
	aead       cipher.AEAD
}

func NewSealer(root []byte, room string) (*Sealer, error) {
	if len(root) != 32 {
		return nil, fmt.Errorf("key must contain 32 bytes")
	}
	s := &Sealer{room: room}
	if _, err := rand.Read(s.generation[:]); err != nil {
		return nil, err
	}
	key, err := hkdf.Key(sha256.New, root, s.generation[:], "termbacktime/live/v1/"+room, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	s.aead, err = cipher.NewGCM(block)
	return s, err
}

func (s *Sealer) Generation() string { return hex.EncodeToString(s.generation[:]) }
func (s *Sealer) Seal(payload []byte) ([][]byte, error) {
	if len(payload) == 0 || len(payload) > MaxFrame {
		return nil, fmt.Errorf("invalid frame size")
	}
	if s.sequence >= 1<<53-1 {
		return nil, fmt.Errorf("generation exhausted")
	}
	s.sequence++
	count := (len(payload) + ChunkSize - 1) / ChunkSize
	out := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		h := make([]byte, HeaderSize)
		copy(h, "TBT1")
		copy(h[4:20], s.generation[:])
		binary.BigEndian.PutUint64(h[20:28], s.sequence)
		binary.BigEndian.PutUint16(h[28:30], uint16(i))
		binary.BigEndian.PutUint16(h[30:32], uint16(count))
		nonce := make([]byte, 12)
		binary.BigEndian.PutUint32(nonce, uint32(i))
		binary.BigEndian.PutUint64(nonce[4:], s.sequence)
		aad := append(append([]byte{}, h...), s.room...)
		out = append(out, s.aead.Seal(h, nonce, payload[i*ChunkSize:min((i+1)*ChunkSize, len(payload))], aad))
	}
	return out, nil
}
