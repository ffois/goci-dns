package cf

import (
	"context"
	"encoding/json"
	"fmt"
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

// fakeCF serves a two-page DNS record listing and records every PUT body.
type fakeCF struct {
	pages [][]map[string]any

	mu      sync.Mutex
	updates map[string]map[string]any
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/zones":
		// One zone on page 1, then an empty page to stop auto-paging.
		result := []map[string]any{}
		if p := r.URL.Query().Get("page"); p == "" || p == "1" {
			result = append(result, map[string]any{
				"id": "zone", "name": "example.org",
				"account": map[string]any{"id": "acc", "name": "Account"},
			})
		}
		json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result":      result,
			"result_info": map[string]any{"page": 1, "per_page": 50},
		})
	case r.Method == http.MethodGet && r.URL.Path == "/zones/zone/dns_records":
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		result := []map[string]any{}
		if page >= 1 && page <= len(f.pages) {
			result = f.pages[page-1]
		}
		json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result":      result,
			"result_info": map[string]any{"page": page, "per_page": 2},
		})
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/zones/zone/dns_records/"):
		id := strings.TrimPrefix(r.URL.Path, "/zones/zone/dns_records/")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.updates[id] = body
		f.mu.Unlock()
		body["id"] = id
		json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{}, "result": body,
		})
	default:
		http.Error(w, "unexpected request "+r.Method+" "+r.URL.String(), http.StatusNotFound)
	}
}

func record(id, typ, content string, ttl int, proxied bool) map[string]any {
	return map[string]any{
		"id": id, "name": id + ".example.org", "type": typ,
		"content": content, "ttl": ttl, "proxied": proxied,
	}
}

func TestUpdateDNS(t *testing.T) {
	fake := &fakeCF{
		pages: [][]map[string]any{
			{
				record("page1", "A", "198.51.100.1", 300, true),
				record("same", "A", newIP, 1, false),
			},
			{
				record("page2", "A", "198.51.100.1", 1, false),
				record("aaaa", "AAAA", "2001:db8::1", 1, false),
			},
		},
		updates: map[string]map[string]any{},
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", srv.URL+"/")

	cfg := &utils.Config{CF: utils.CFConfig{
		CFToken:       "token",
		CFZoneID:      "zone",
		CFDNSEntryIDs: []string{"page1", "same", "page2", "aaaa", "missing", " "},
	}}

	if err := UpdateDNS(cfg, newIP); err != nil {
		t.Fatalf("UpdateDNS: %v", err)
	}

	if len(fake.updates) != 2 {
		t.Fatalf("expected updates for page1 and page2 only, got %v", fake.updates)
	}

	// Record on the first page: TTL and proxied must be preserved.
	u, ok := fake.updates["page1"]
	if !ok {
		t.Fatal("page1 was not updated")
	}
	if u["content"] != newIP || u["ttl"] != float64(300) || u["proxied"] != true || u["type"] != "A" {
		t.Errorf("page1 update body = %v", u)
	}

	// Record only reachable through pagination.
	if u, ok := fake.updates["page2"]; !ok || u["content"] != newIP {
		t.Errorf("page2 update = %v (found=%v)", u, ok)
	}
}

func TestListZonesAndARecords(t *testing.T) {
	fake := &fakeCF{
		pages: [][]map[string]any{
			{record("a1", "A", "198.51.100.1", 1, false)},
			{record("aaaa", "AAAA", "2001:db8::1", 1, false), record("a2", "A", "198.51.100.2", 1, false)},
		},
		updates: map[string]map[string]any{},
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", srv.URL+"/")

	zones, err := ListZones(context.Background(), "token")
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if want := []provider.Zone{{ID: "zone", Name: "example.org", AccountID: "acc"}}; !reflect.DeepEqual(zones, want) {
		t.Errorf("zones = %+v, want %+v", zones, want)
	}

	records, err := ListARecords(context.Background(), "token", "zone")
	if err != nil {
		t.Fatalf("ListARecords: %v", err)
	}
	want := []provider.Record{
		{ID: "a1", Name: "a1.example.org", Content: "198.51.100.1"},
		{ID: "a2", Name: "a2.example.org", Content: "198.51.100.2"},
	}
	if !reflect.DeepEqual(records, want) {
		t.Errorf("records = %+v, want %+v", records, want)
	}
}
