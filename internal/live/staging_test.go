package live

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestStagingDelivery(t *testing.T) {
	endpoint := os.Getenv("TBT_STAGING_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TBT_STAGING_ENDPOINT to test real SFU delivery")
	}
	for _, scenario := range []struct {
		name    string
		viewers int
		relay   bool
	}{{"100 viewers and publisher replacement", 100, false}, {"TURN-only delivery", 1, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			started := time.Now()
			result, err := probe(ctx, endpoint, scenario.viewers, scenario.relay, true, func(stage string, count int) {
				t.Logf("%s: %d (%s)", stage, count, time.Since(started).Round(time.Second))
			})
			if err != nil {
				t.Logf("Partial probe result: %+v", result)
				t.Fatal(err)
			}
			t.Logf("Verified %d viewers; delivery %d ms; relay=%t; replacement=%t; retries=%d", result.Viewers, result.DeliveryMS, result.RelayOnly, result.Replacement, result.Retries)
		})
	}
}

func TestStagingExpiry(t *testing.T) {
	endpoint := os.Getenv("TBT_STAGING_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TBT_STAGING_ENDPOINT to test deployed expiry")
	}
	api := API{Origin: endpoint, HTTP: &http.Client{Timeout: 10 * time.Second}}
	var room struct {
		ID   string `json:"roomId"`
		Host string `json:"hostCapability"`
	}
	if err := api.Call(t.Context(), "", map[string]any{"ttlSeconds": 1}, &room); err != nil {
		t.Fatal(err)
	}
	api.Room = room.ID
	api.Capability = room.Host
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = api.Call(ctx, "end", nil, nil)
	}()
	time.Sleep(1500 * time.Millisecond)
	var apiErr *APIError
	err := api.Call(t.Context(), "join", map[string]any{"generation": "11111111111111111111111111111111"}, nil)
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusGone {
		t.Fatalf("expired room was not rejected: %v", err)
	}
}

func TestStagingHostControls(t *testing.T) {
	endpoint := os.Getenv("TBT_STAGING_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TBT_STAGING_ENDPOINT to test deployed host controls")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	host := API{Origin: endpoint, HTTP: &http.Client{Timeout: 15 * time.Second}}
	var room struct {
		ID         string `json:"roomId"`
		Host       string `json:"hostCapability"`
		Viewer     string `json:"viewerCapability"`
		Management string `json:"managementCapability"`
	}
	if err := host.Call(ctx, "", map[string]any{"ttlSeconds": 180}, &room); err != nil {
		t.Fatal(err)
	}
	host.Room, host.Capability = room.ID, room.Host
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = host.Call(cleanup, "end", nil, nil)
	}()
	if room.Management == "" {
		t.Fatal("deploy the compatible Worker before testing host controls")
	}
	manager, viewer := host, host
	manager.Capability, viewer.Capability = room.Management, room.Viewer
	assertCode := func(err error, code string) {
		t.Helper()
		var problem *APIError
		if !errors.As(err, &problem) || problem.Code != code {
			t.Fatalf("expected %s, got %v", code, err)
		}
	}
	assertCode(manager.Call(ctx, "join", map[string]any{"generation": "11111111111111111111111111111111"}, nil), "AUTH")
	publisher, err := connectRole(ctx, host, "11111111111111111111111111111111", false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.close()
	var admitted struct {
		Capability string `json:"capability"`
	}
	if err := viewer.Call(ctx, "join", nil, &admitted); err != nil {
		t.Fatal(err)
	}
	change := map[string]any{"revision": 0, "paused": true, "locked": true}
	for i := 0; i < 2; i++ {
		if err := manager.Call(ctx, "settings", change, nil); err != nil {
			t.Fatal(err)
		}
	}
	for {
		select {
		case control := <-publisher.controls:
			if control.Revision != 1 {
				continue
			}
			if !control.Paused {
				t.Fatal("publisher received incorrect pause state")
			}
		case <-ctx.Done():
			t.Fatal("publisher did not receive the pause control")
		}
		break
	}
	if err := publisher.api.Call(ctx, "ack", map[string]any{"revision": 1}, nil); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Revision     int  `json:"revision"`
		Acknowledged int  `json:"acknowledged"`
		Paused       bool `json:"paused"`
		Locked       bool `json:"locked"`
	}
	if err := manager.Call(ctx, "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	if status.Revision != 1 || status.Acknowledged != 1 || !status.Paused || !status.Locked {
		t.Fatalf("unexpected controls: %+v", status)
	}
	assertCode(viewer.Call(ctx, "join", nil, nil), "ROOM_LOCKED")
	if err := viewer.Call(ctx, "join", map[string]any{"resumeCapability": admitted.Capability}, nil); err != nil {
		t.Fatalf("locked viewer reconnect: %v", err)
	}
	if err := manager.Call(ctx, "settings", map[string]any{"revision": 1, "paused": false, "locked": false}, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := manager.Call(ctx, "end", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	assertCode(viewer.Call(ctx, "join", nil, nil), "ROOM_EXPIRED")
}
