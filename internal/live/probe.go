package live

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/termbacktime/termbacktime/internal/config"
)

type ProbeResult struct {
	Viewers             int   `json:"viewers"`
	RelayOnly           bool  `json:"relay_only"`
	DeliveryMS          int64 `json:"delivery_ms"`
	LateJoin            bool  `json:"late_join"`
	Replacement         bool  `json:"publisher_replacement"`
	Retries             int   `json:"negotiation_retries"`
	ViewerCountVerified bool  `json:"viewer_count_verified"`
}

// Probe transmits only a synthetic payload and verifies actual decrypted delivery
func Probe(ctx context.Context, endpoint string, viewers int, relayOnly, replacement bool) (ProbeResult, error) {
	return probe(ctx, endpoint, viewers, relayOnly, replacement, nil)
}

func probe(ctx context.Context, endpoint string, viewers int, relayOnly, replacement bool, progress func(string, int)) (ProbeResult, error) {
	report := func(stage string, count int) {
		if progress != nil {
			progress(stage, count)
		}
	}
	result := ProbeResult{Viewers: viewers, RelayOnly: relayOnly}
	if viewers < 1 || viewers > 100 {
		return result, fmt.Errorf("viewer count must be 1 through 100")
	}
	origin, err := config.ValidateSiteURL(endpoint)
	if err != nil {
		return result, err
	}
	host := API{Origin: origin, HTTP: &http.Client{Timeout: 30 * time.Second}}
	var room struct {
		ID         string `json:"roomId"`
		Host       string `json:"hostCapability"`
		Viewer     string `json:"viewerCapability"`
		Management string `json:"managementCapability"`
	}
	if err := host.Call(ctx, "", nil, &room); err != nil {
		return result, err
	}
	host.Room = room.ID
	host.Capability = room.Host
	defer func() {
		end, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = host.Call(end, "end", nil, nil)
	}()
	key := make([]byte, 32)
	defer clear(key)
	if _, err := rand.Read(key); err != nil {
		return result, err
	}
	sealer, err := NewSealer(key, room.ID)
	if err != nil {
		return result, err
	}

	var retryMu sync.Mutex
	dial := func(parent context.Context, role API, generation string, viewer bool) (*connection, error) {
		var last error
		for attempt := 0; attempt < 3; attempt++ {
			setup, stop := context.WithTimeout(parent, 45*time.Second)
			c, err := connectRole(setup, role, generation, viewer, relayOnly)
			stop()
			if err == nil {
				return c, nil
			}
			last = err
			var problem *APIError
			if parent.Err() != nil || (errors.As(err, &problem) && (problem.Status == 403 || problem.Status == 410 || problem.Code == "ROOM_FULL")) {
				break
			}
			if attempt < 2 {
				retryMu.Lock()
				result.Retries++
				retryMu.Unlock()
				select {
				case <-parent.Done():
					return nil, parent.Err()
				case <-time.After(time.Second):
				}
			}
		}
		return nil, last
	}
	publisher, err := dial(ctx, host, sealer.Generation(), false)
	if err != nil {
		return result, fmt.Errorf("publisher negotiation: %w", err)
	}
	report("publisher connected", 1)
	defer func() {
		if publisher != nil {
			publisher.close()
		}
	}()
	payload := []byte(`{"type":"snapshot","cols":80,"rows":24,"screen":"TermBackTime synthetic connection check","cursor":[0,0],"visible":true}`)
	var subscribers []*connection
	closeSubscribers := func() {
		var wg sync.WaitGroup
		slots := make(chan struct{}, 6)
		for _, c := range subscribers {
			slots <- struct{}{}
			wg.Go(func() { defer func() { <-slots }(); c.close() })
		}
		wg.Wait()
		subscribers = nil
	}
	defer closeSubscribers()
	round := func(count int) (time.Duration, error) {
		seen := make([]chan struct{}, count)

		result.ViewerCountVerified = false
		generation := sealer.Generation()
		for i := range seen {
			seen[i] = make(chan struct{}, 1)
		}
		connected := 0
		for batch := 0; batch < count; batch += 4 {
			batchCtx, stop := context.WithCancel(ctx)
			type joined struct {
				index int
				c     *connection
				err   error
			}
			n := min(4, count-batch)
			results := make(chan joined, n)
			for i := batch; i < batch+n; i++ {
				go func(index int) {
					viewer := host
					viewer.Capability = room.Viewer
					c, err := dial(batchCtx, viewer, generation, true)
					results <- joined{index, c, err}
				}(i)
			}
			var failure error
			for range n {
				item := <-results
				if item.err != nil {
					if failure == nil {
						failure = fmt.Errorf("viewer %d negotiation: %w", item.index+1, item.err)
					}
					stop()
					continue
				}
				c := item.c
				subscribers = append(subscribers, c)
				connected++
				if connected%10 == 0 || connected == count {
					report("viewers connected", connected)
				}
				notification := seen[item.index]
				c.dc.OnMessage(func(message webrtc.DataChannelMessage) {
					data := message.Data
					if len(data) < HeaderSize+16 || string(data[:4]) != "TBT1" || hex.EncodeToString(data[4:20]) != generation || binary.BigEndian.Uint16(data[28:30]) != 0 || binary.BigEndian.Uint16(data[30:32]) != 1 {
						return
					}
					derived, err := hkdf.Key(sha256.New, key, data[4:20], "termbacktime/live/v1/"+room.ID, 32)
					if err != nil {
						return
					}
					block, _ := aes.NewCipher(derived)
					clear(derived)
					aead, _ := cipher.NewGCM(block)
					nonce := make([]byte, 12)
					copy(nonce[4:], data[20:28])
					aad := append(append([]byte{}, data[:HeaderSize]...), room.ID...)
					plain, err := aead.Open(nil, nonce, data[HeaderSize:], aad)
					if err == nil && bytes.Equal(plain, payload) {
						select {
						case notification <- struct{}{}:
						default:
						}
					}
					clear(plain)
				})
			}
			stop()
			if failure != nil {
				return 0, failure
			}
		}
		start := time.Now()
		chunks, err := sealer.Seal(payload)
		if err != nil {
			return 0, err
		}
		for _, chunk := range chunks {
			if err := publisher.dc.Send(chunk); err != nil {
				return 0, err
			}
		}
		deadline := time.NewTimer(30 * time.Second)
		defer deadline.Stop()
		for _, ch := range seen {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-deadline.C:
				return 0, fmt.Errorf("encrypted delivery timed out")
			case <-ch:
			}
		}
		report("encrypted delivery verified", count)

		delivery := time.Since(start)
		result.DeliveryMS = delivery.Milliseconds()
		if room.Management != "" {
			manager := host
			manager.Capability = room.Management
			deadline := time.Now().Add(5 * time.Second)
			for {
				var status RoomStatus
				if err := manager.Call(ctx, "status", nil, &status); err != nil {
					return 0, fmt.Errorf("management status: %w", err)
				}
				if status.Viewers == count {
					result.ViewerCountVerified = true
					report("management viewer count verified", status.Viewers)
					break
				}
				if time.Now().After(deadline) {
					return 0, fmt.Errorf("management viewer count: got %d, want %d", status.Viewers, count)
				}
				select {
				case <-ctx.Done():
					return 0, ctx.Err()
				case <-time.After(200 * time.Millisecond):
				}
			}
		}
		return delivery, nil
	}
	if viewers > 1 {
		if _, err := round(1); err != nil {
			return result, err
		}
		subscribers[0].close()
		subscribers = nil
		result.LateJoin = true
	}
	duration, err := round(viewers)
	if err != nil {
		return result, err
	}
	result.DeliveryMS = duration.Milliseconds()
	if replacement {
		closeSubscribers()
		publisher.close()
		sealer, err = NewSealer(key, room.ID)
		if err != nil {
			return result, err
		}
		publisher, err = dial(ctx, host, sealer.Generation(), false)
		if err != nil {
			return result, fmt.Errorf("replacement publisher negotiation: %w", err)
		}
		report("replacement publisher connected", 1)
		if _, err := round(viewers); err != nil {
			return result, fmt.Errorf("replacement delivery: %w", err)
		}
		result.Replacement = true
	}
	return result, nil
}
