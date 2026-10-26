package ionos

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"goci-dns/internals/utils"
)

const newIP = "203.0.113.7"

// fakeIONOS serves a single zone and records every PUT body.
type fakeIONOS struct {
	records []map[string]any

	mu      sync.Mutex
	updates map[string]map[string]any
}

func (f *fakeIONOS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/zones/zone":
		json.NewEncoder(w).Encode(map[string]any{
			"id": "zone", "name": "example.org", "type": "NATIVE", "records": f.records,
		})
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/zones/zone/records/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/zones/zone/records/")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.updates[id] = body
		f.mu.Unlock()
		body["id"] = id
		json.NewEncoder(w).Encode(body)
	default:
		http.Error(w, "unexpected request "+r.Method+" "+r.URL.String(), http.StatusNotFound)
	}
}

func record(id, typ, content string, ttl int) map[string]any {
	return map[string]any{
		"id": id, "name": id + ".example.org", "type": typ,
		"content": content, "ttl": ttl, "disabled": false, "prio": 0,
	}
}

func TestUpdateDNS(t *testing.T) {
	fake := &fakeIONOS{
		records: []map[string]any{
			record("old", "A", "198.51.100.1", 600),
			record("same", "A", newIP, 3600),
			record("aaaa", "AAAA", "2001:db8::1", 3600),
		},
		updates: map[string]map[string]any{},
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	cfg := &utils.Config{IONOS: utils.IONOSConfig{
		IONOSApiUrl:   srv.URL,
		IONOSApiKey:   "key",
		IONOSZoneID:   "zone",
		IONOSEntryIDs: []string{"old", "same", "aaaa", "missing"},
	}}

	if err := UpdateDNS(cfg, newIP); err != nil {
		t.Fatalf("UpdateDNS: %v", err)
	}

	if len(fake.updates) != 1 {
		t.Fatalf("expected only 'old' to be updated, got %v", fake.updates)
	}
	u, ok := fake.updates["old"]
	if !ok {
		t.Fatal("'old' was not updated")
	}
	if u["content"] != newIP || u["ttl"] != float64(600) {
		t.Errorf("update body = %v", u)
	}
}
