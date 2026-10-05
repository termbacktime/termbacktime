package live

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlRequestsSendObjectsAndKeepCapabilitiesOutOfURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/rooms/test-room/ice" || r.URL.RawQuery != "" {
			t.Error("invalid control URL", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer capability" {
			t.Error("missing capability")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body == nil {
			t.Error("body must be an object")
		}
		_, _ = w.Write([]byte(`{"iceServers":[]}`))
	}))
	defer server.Close()
	api := API{Origin: server.URL, Room: "test-room", Capability: "capability", HTTP: server.Client()}
	if err := api.Call(context.Background(), "ice", nil, nil); err != nil {
		t.Fatal(err)
	}
}
