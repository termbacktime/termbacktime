package live

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNegotiationFailuresReleaseAdmission(t *testing.T) {
	for _, stage := range []string{"join", "ice", "establish", "invalid transport"} {
		t.Run(stage, func(t *testing.T) {
			var closes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				operation := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				if operation == "close" {
					closes.Add(1)
					w.Write([]byte("{}"))
					return
				}
				if operation == stage {
					w.WriteHeader(403)
					w.Write([]byte(`{"error":{"code":"AUTH","message":"refused"}}`))
					return
				}
				switch operation {
				case "join":
					json.NewEncoder(w).Encode(map[string]string{"capability": "member"})
				case "ice":
					w.Write([]byte(`{"iceServers":[]}`))
				case "establish":
					w.Write([]byte(`{"sessionDescription":{"type":"answer","sdp":"invalid"}}`))
				default:
					t.Errorf("unexpected operation %s", operation)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := connect(ctx, API{Origin: server.URL, Room: "room", Capability: "host", HTTP: server.Client()}, strings.Repeat("a", 32)); err == nil {
				t.Fatal("invalid negotiation accepted")
			}
			want := int32(1)
			if stage == "join" {
				want = 0
			}
			if closes.Load() != want {
				t.Fatal("admission leak", closes.Load())
			}
		})
	}
}

func TestProbeValidationAndFailedPublisherEndsRoom(t *testing.T) {
	for _, viewers := range []int{0, 101} {
		if _, err := Probe(t.Context(), "https://example.com", viewers, false, false); err == nil {
			t.Fatal(viewers)
		}
	}
	if _, err := Probe(t.Context(), "http://remote.example", 1, false, false); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
	var ended atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/rooms" {
			w.Write([]byte(`{"roomId":"room","hostCapability":"host","viewerCapability":"viewer","managementCapability":"management"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/end") {
			ended.Add(1)
			w.Write([]byte("{}"))
			return
		}
		w.WriteHeader(403)
		w.Write([]byte(`{"error":{"code":"AUTH","message":"refused"}}`))
	}))
	defer server.Close()
	if _, err := Probe(t.Context(), server.URL, 1, false, false); err == nil || ended.Load() != 1 {
		t.Fatal(err, ended.Load())
	}
}

func TestLiveEncryptionRejectsInvalidKeysAndFrameBounds(t *testing.T) {
	if _, err := NewSealer(make([]byte, 31), "room"); err == nil {
		t.Fatal("invalid key accepted")
	}
	sealer, err := NewSealer(make([]byte, 32), "room")
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{nil, make([]byte, MaxFrame+1)} {
		if _, err := sealer.Seal(payload); err == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	sealer.sequence = 1<<53 - 1
	if _, err := sealer.Seal([]byte("hello")); err == nil {
		t.Fatal("sequence exhausted without rejection")
	}
}
