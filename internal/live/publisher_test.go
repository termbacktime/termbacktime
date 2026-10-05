package live

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/termbacktime/termbacktime/internal/recording"
	terminalpkg "github.com/termbacktime/termbacktime/internal/terminal"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

func testSFU(t *testing.T, ctx context.Context, controls <-chan string) (API, <-chan []byte) {
	t.Helper()
	sfu, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sfu.Close() })

	transportID, terminalID := uint16(0), uint16(2)
	events, err := sfu.CreateDataChannel("server-events", &webrtc.DataChannelInit{ID: &transportID})
	if err != nil {
		t.Fatal(err)
	}
	acknowledged := make(chan struct{})
	events.OnOpen(func() { close(acknowledged) })
	yes := true
	terminal, err := sfu.CreateDataChannel("terminal-v1", &webrtc.DataChannelInit{
		Negotiated: &yes, Ordered: &yes, ID: &terminalID,
	})
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan []byte, 100)
	terminal.OnMessage(func(message webrtc.DataChannelMessage) {
		received <- append([]byte(nil), message.Data...)
	})
	sfu.OnDataChannel(func(channel *webrtc.DataChannel) {
		t.Errorf("publisher unexpectedly opened an in-band channel: %s", channel.Label())
	})

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var response any = struct{}{}
		switch strings.TrimPrefix(r.URL.Path, "/api/v1/rooms/test-room/") {
		case "/api/v1/rooms":
			response = map[string]any{"roomId": "test-room", "hostCapability": "host-capability", "viewerCapability": "viewer-capability", "managementCapability": "management-capability", "shareUrl": server.URL + "/live/test-room"}
		case "join":
			response = map[string]string{"capability": "member-capability"}
		case "ice":
			response = map[string]any{"iceServers": []any{}}
		case "establish":
			offer, err := sfu.CreateOffer(nil)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			gathered := webrtc.GatheringCompletePromise(sfu)
			if err := sfu.SetLocalDescription(offer); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			select {
			case <-ctx.Done():
				http.Error(w, "ICE gathering timed out", 504)
				return
			case <-gathered:
			}
			response = negotiation{
				Description: *sfu.LocalDescription(),
				Channel:     channelResult{ID: &transportID},
				Renegotiate: true,
			}
		case "answer":
			var answer negotiation
			if err := json.NewDecoder(r.Body).Decode(&answer); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if err := sfu.SetRemoteDescription(answer.Description); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		case "channel":
			// Cloudflare's in-band transport requires the client to acknowledge its Open message
			select {
			case <-acknowledged:
			case <-ctx.Done():
				http.Error(w, "server-events DCEP Open was never acknowledged", 504)
				return
			}
			response = map[string]any{"dataChannels": []channelResult{{ID: &terminalID}}}
		case "ticket":
			response = map[string]string{"ticket": "test-ticket"}
		case "control":
			upgrader := websocket.Upgrader{Subprotocols: []string{"tbt"}}
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			closed := make(chan struct{})
			go func() {
				defer close(closed)
				for {
					if _, _, err := ws.ReadMessage(); err != nil {
						return
					}
				}
			}()
			for {
				select {
				case <-closed:
					return
				case <-ctx.Done():
					return
				case message := <-controls:
					if err := ws.WriteJSON(map[string]any{"type": message}); err != nil {
						return
					}
				}
			}
		case "ready", "close", "end":
		default:
			http.Error(w, "unexpected operation", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	api := API{Origin: server.URL, Room: "test-room", Capability: "host-capability", HTTP: server.Client()}
	return api, received
}

// Exercise the SFU handshake over real local SCTP so DCEP collisions cannot hide behind API mocks
func TestPublisherAcceptsServerEventsBeforePublishing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	api, received := testSFU(t, ctx, nil)
	terminalID := uint16(2)
	connection, err := connect(ctx, api, strings.Repeat("1", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.close()
	if !connection.dc.Negotiated() || !connection.dc.Ordered() || *connection.dc.ID() != terminalID {
		t.Fatal("terminal channel lost its negotiated reliable ordered configuration")
	}
	payload := []byte("encrypted terminal fixture")
	if err := connection.dc.Send(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-received:
		if !bytes.Equal(message, payload) {
			t.Fatalf("unexpected terminal payload: %q", message)
		}
	case <-ctx.Done():
		t.Fatal("negotiated terminal channel did not deliver data")
	}
}

func TestSharingEndKeepsShellAndRecordingRunning(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	control := make(chan string, 1)
	api, _ := testSFU(t, ctx, control)
	path := filepath.Join(t.TempDir(), "live.json")
	writer, err := recording.NewWriter(path, recording.Recording{Sizes: []int{80, 24}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	capture := recording.NewCapture(writer)
	sent := false
	statuses := make(chan string, 10)
	err = Run(ctx, Options{Endpoint: api.Origin, Shell: "/bin/sh", Args: []string{"-c", "printf before; sleep 0.5; printf after"}, TTL: time.Hour, OnEvent: func(e terminalpkg.Event) error {
		if !sent && strings.Contains(e.Text, "before") {
			sent = true
			control <- "ended"
		}
		return capture.Event(e)
	}, OnStatus: func(s string) { statuses <- s }})
	if err != nil {
		t.Fatal(err)
	}
	if err = capture.Finish(); err != nil {
		t.Fatal(err)
	}
	r, err := recording.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, e := range r.Lines {
		text.WriteString(strings.Join(e.Lines, ""))
	}
	if !strings.Contains(text.String(), "beforeafter") {
		t.Fatal("shell or recording stopped with sharing")
	}
	select {
	case status := <-statuses:
		if !strings.Contains(status, "continue") {
			t.Fatal(status)
		}
	default:
		t.Fatal("sharing did not end before shell")
	}
	if _, err := os.Stat(path + ".partial"); !os.IsNotExist(err) {
		t.Fatal("journal was not finalized")
	}
}
