package cm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBootstrapWebAPIIncludesWSS(t *testing.T) {
	payload := `{"response":{"serverlist":["1.2.3.4:27017","5.6.7.8:27017"],
		"serverlist_websockets":["cmp1-sgp1.steamserver.net:443","cmp2-dfw2.steamserver.net:443"]}}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer ts.Close()

	// Point the server list at the test server by swapping the hardcoded URL via
	// a custom http.Client redirect isn't possible; instead test parseServerEntry
	// and sorting directly off a JSON decode.
	var api struct {
		Response struct {
			ServerList          []string `json:"serverlist"`
			ServerListWebSocket []string `json:"serverlist_websockets"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(payload), &api); err != nil {
		t.Fatal(err)
	}
	s, ok := parseServerEntry("cmp1-sgp1.steamserver.net:443", true)
	if !ok || !s.WSS || s.Host != "cmp1-sgp1.steamserver.net" || s.Port != 443 {
		t.Fatalf("wss parse got %+v ok=%v", s, ok)
	}

	var combined []Server
	for _, e := range api.Response.ServerList {
		if x, ok := parseServerEntry(e, false); ok {
			combined = append(combined, x)
		}
	}
	for _, e := range api.Response.ServerListWebSocket {
		if x, ok := parseServerEntry(e, true); ok {
			combined = append(combined, x)
		}
	}
	sortServerListWSSFirst(&combined)
	if len(combined) != 4 {
		t.Fatalf("want 4 combined, got %d", len(combined))
	}
	if !combined[0].WSS || !combined[1].WSS {
		t.Errorf("WSS-first sort failed: %+v", combined)
	}
	if combined[2].WSS || combined[3].WSS {
		t.Errorf("raw TCP entries should be at tail: %+v", combined)
	}
}