package live

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRoomPollingKeepsManagementCapabilityPrivateAndStops(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "status", true: "expired"}[failure], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/api/v1/rooms/room/status" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer manager" {
					t.Error("management capability or route changed", r.URL)
				}
				if failure {
					w.WriteHeader(http.StatusGone)
					json.NewEncoder(w).Encode(map[string]string{"code": "EXPIRED", "error": "room ended"})
					return
				}
				json.NewEncoder(w).Encode(RoomStatus{Viewers: 2, Paused: true, Locked: true, Revision: 3, Acknowledged: 2})
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			pollRoom(ctx, API{Origin: server.URL, Room: "room", Capability: "manager", HTTP: server.Client()}, func(status RoomStatus, err error) {
				defer cancel()
				if failure {
					if err == nil {
						t.Error("lost expiry error")
					}
				} else if err != nil || status.Viewers != 2 || !status.Paused || !status.Locked || status.Revision != 3 || status.Acknowledged != 2 {
					t.Error(status, err)
				}
			})
			if calls.Load() != 1 {
				t.Fatal("poll continued after cancellation", calls.Load())
			}
		})
	}
}

func TestRoomPollingCancellationDuringRequestDoesNotNotify(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pollRoom(ctx, API{Origin: server.URL, Room: "room", HTTP: server.Client()}, func(RoomStatus, error) {
			t.Error("notified after cancellation")
		})
	}()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("polling did not cancel")
	}
}
