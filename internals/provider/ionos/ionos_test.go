package ionos

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"goci-dns/internals/provider"
	"goci-dns/internals/utils"
)

const newIP = "203.0.113.7"

// fakeIONOS serves the given zones and records every PUT body (keyed by
// record ID) and the API key used for each zone.
type fakeIONOS struct {
	zones map[string][]map[string]any // zone ID -> records

	mu      sync.Mutex
	updates map[string]map[string]any
	keys    map[string]string // zone ID -> X-API-Key
}

func newFake(zones map[string][]map[string]any) *fakeIONOS {
	return &fakeIONOS{zones: zones, updates: map[string]map[string]any{}, keys: map[string]string{}}
}

func (f *fakeIONOS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/zones":
		json.NewEncoder(w).Encode([]map[string]any{{"id": "zone", "name": "example.org", "type": "NATIVE"}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/zones/"):
		zone := strings.TrimPrefix(r.URL.Path, "/v1/zones/")
		records, ok := f.zones[zone]
		if !ok {
			http.Error(w, `[{"code":"NOT_FOUND"}]`, http.StatusNotFound)
			return
		}
		f.mu.Lock()
		f.keys[zone] = r.Header.Get("X-API-Key")
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"id": zone, "name": "example.org", "type": "NATIVE", "records": records,
		})
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/records/"):
		_, id, _ := strings.Cut(r.URL.Path, "/records/")
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
	fake := newFake(map[string][]map[string]any{"zone": {
		record("old", "A", "198.51.100.1", 600),
		record("same", "A", newIP, 3600),
		record("aaaa", "AAAA", "2001:db8::1", 3600),
	}})
	srv := httptest.NewServer(fake)
	defer srv.Close()

	cfg := &utils.Config{IONOS: utils.IONOSConfig{
		IONOSApiUrl: srv.URL,
		Zones: []utils.ZoneEntry{{
			ZoneID:     "zone",
			RecordIDs:  []string{"old", "same", "aaaa", "missing"},
			Credential: "key",
		}},
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

func TestUpdateDNSMultipleZones(t *testing.T) {
	fake := newFake(map[string][]map[string]any{
		"z1": {record("r1", "A", "198.51.100.1", 3600)},
		"z2": {record("r2", "A", "198.51.100.1", 3600)},
	})
	srv := httptest.NewServer(fake)
	defer srv.Close()

	cfg := &utils.Config{IONOS: utils.IONOSConfig{
		IONOSApiUrl: srv.URL,
		Zones: []utils.ZoneEntry{
			{ZoneID: "broken", RecordIDs: []string{"x"}, Credential: "key-a"},
			{ZoneID: "z1", RecordIDs: []string{"r1"}, Credential: "key-a"},
			{ZoneID: "z2", RecordIDs: []string{"r2"}, Credential: "key-b"},
		},
	}}

	err := UpdateDNS(cfg, newIP)
	if err == nil || !strings.Contains(err.Error(), "zone broken") {
		t.Fatalf("err = %v, want an error for zone broken", err)
	}
	// The failing zone must not stop the others.
	for _, id := range []string{"r1", "r2"} {
		if u, ok := fake.updates[id]; !ok || u["content"] != newIP {
			t.Errorf("%s update = %v (found=%v)", id, u, ok)
		}
	}
	if want := map[string]string{"z1": "key-a", "z2": "key-b"}; !reflect.DeepEqual(fake.keys, want) {
		t.Errorf("keys = %v, want %v", fake.keys, want)
	}
}

func TestListZonesAndARecords(t *testing.T) {
	fake := newFake(map[string][]map[string]any{"zone": {
		record("a1", "A", "198.51.100.1", 3600),
		record("aaaa", "AAAA", "2001:db8::1", 3600),
	}})
	srv := httptest.NewServer(fake)
	defer srv.Close()

	zones, err := ListZones(context.Background(), srv.URL, "key")
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if want := []provider.Zone{{ID: "zone", Name: "example.org"}}; !reflect.DeepEqual(zones, want) {
		t.Errorf("zones = %+v, want %+v", zones, want)
	}

	records, err := ListARecords(context.Background(), srv.URL, "key", "zone")
	if err != nil {
		t.Fatalf("ListARecords: %v", err)
	}
	if want := []provider.Record{{ID: "a1", Name: "a1.example.org", Content: "198.51.100.1"}}; !reflect.DeepEqual(records, want) {
		t.Errorf("records = %+v, want %+v", records, want)
	}
}
