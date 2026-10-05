package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type probeMember struct {
	pc         *webrtc.PeerConnection
	terminal   *webrtc.DataChannel
	ready      chan struct{}
	viewer     bool
	generation string
}

// A local peer per admission exercises real SDP, SCTP and encrypted delivery.
// The HTTP service forwards opaque publisher frames without knowing their key.
func probeService(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var mu sync.Mutex
	members := map[string]*probeMember{}
	next := 0
	ended := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := strings.TrimPrefix(r.URL.Path, "/api/v1/rooms/room/")
		response := any(struct{}{})
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch action {
		case "/api/v1/rooms":
			response = map[string]string{"roomId": "room", "hostCapability": "host", "viewerCapability": "viewer", "managementCapability": "manager"}
		case "join":
			var body struct{ Generation string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid join", 400)
				return
			}
			pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
			if err != nil {
				t.Error(err)
				http.Error(w, "peer creation", 500)
				return
			}
			transportID, terminalID, yes := uint16(0), uint16(2), true
			events, err := pc.CreateDataChannel("server-events", &webrtc.DataChannelInit{ID: &transportID})
			if err != nil {
				pc.Close()
				t.Error(err)
				http.Error(w, "events channel", 500)
				return
			}
			terminal, err := pc.CreateDataChannel("terminal-v1", &webrtc.DataChannelInit{ID: &terminalID, Negotiated: &yes, Ordered: &yes})
			if err != nil {
				pc.Close()
				t.Error(err)
				http.Error(w, "terminal channel", 500)
				return
			}
			member := &probeMember{pc: pc, terminal: terminal, ready: make(chan struct{}), viewer: token == "viewer", generation: body.Generation}
			events.OnOpen(func() { close(member.ready) })
			if !member.viewer {
				terminal.OnMessage(func(message webrtc.DataChannelMessage) {
					mu.Lock()
					targets := []*webrtc.DataChannel{}
					for _, other := range members {
						if other.viewer && other.generation == member.generation {
							targets = append(targets, other.terminal)
						}
					}
					mu.Unlock()
					for _, target := range targets {
						// Closure can race a departing subscriber; remaining viewers
						// must still receive their own encrypted frames.
						_ = target.Send(message.Data)
					}
				})
			}
			mu.Lock()
			next++
			capability := fmt.Sprintf("member-%d", next)
			members[capability] = member
			mu.Unlock()
			response = map[string]string{"capability": capability}
		case "status":
			if token != "manager" {
				http.Error(w, "wrong management capability", 403)
				return
			}
			mu.Lock()
			count := 0
			for _, member := range members {
				if member.viewer {
					count++
				}
			}
			mu.Unlock()
			response = RoomStatus{Viewers: count}
		case "end":
			ended.Add(1)
		case "control":
			upgrader := websocket.Upgrader{Subprotocols: []string{"tbt"}}
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		default:
			mu.Lock()
			member := members[token]
			if action == "close" {
				delete(members, token)
			}
			mu.Unlock()
			if member == nil {
				http.Error(w, "unknown admission", 403)
				return
			}
			switch action {
			case "ice", "ready":
			case "establish":
				offer, err := member.pc.CreateOffer(nil)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				gathered := webrtc.GatheringCompletePromise(member.pc)
				if err := member.pc.SetLocalDescription(offer); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				select {
				case <-gathered:
				case <-r.Context().Done():
					return
				}
				id := uint16(0)
				response = negotiation{Description: *member.pc.LocalDescription(), Channel: channelResult{ID: &id}}
			case "answer":
				var answer negotiation
				if err := json.NewDecoder(r.Body).Decode(&answer); err != nil {
					http.Error(w, "invalid answer", 400)
					return
				}
				if err := member.pc.SetRemoteDescription(answer.Description); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
			case "channel":
				select {
				case <-member.ready:
				case <-r.Context().Done():
					return
				}
				id := uint16(2)
				response = map[string]any{"dataChannels": []channelResult{{ID: &id}}}
			case "ticket":
				response = map[string]string{"ticket": token}
			case "close":
				member.pc.Close()
			default:
				http.Error(w, "unknown operation", 404)
				return
			}
		}
		json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(func() {
		mu.Lock()
		remaining := []*probeMember{}
		for _, member := range members {
			remaining = append(remaining, member)
		}
		members = map[string]*probeMember{}
		mu.Unlock()
		for _, member := range remaining {
			member.pc.Close()
		}
		server.Close()
	})
	return server.URL, ended
}

func TestProbeLocalEncryptedDeliveryLateJoinAndReplacement(t *testing.T) {
	endpoint, ended := probeService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	stages := []string{}
	result, err := probe(ctx, endpoint, 2, false, true, func(stage string, _ int) { stages = append(stages, stage) })
	if err != nil || result.Viewers != 2 || !result.LateJoin || !result.Replacement || !result.ViewerCountVerified || result.Retries != 0 {
		t.Fatal(result, err)
	}
	if ended.Load() != 1 || !strings.Contains(strings.Join(stages, "\n"), "encrypted delivery verified") {
		t.Fatal("probe did not verify delivery or clean its room", stages, ended.Load())
	}
}

func TestProbeCancelsConnectedViewersAndEndsRoom(t *testing.T) {
	endpoint, ended := probeService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := probe(ctx, endpoint, 1, false, false, func(stage string, _ int) {
		if stage == "viewers connected" {
			cancel()
		}
	})
	if err == nil || ctx.Err() == nil || ended.Load() != 1 {
		t.Fatal("probe lost cancellation or room cleanup", err, ended.Load())
	}
}
