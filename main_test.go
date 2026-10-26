package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadIPStateMissingFile(t *testing.T) {
	s, err := loadIPState(filepath.Join(t.TempDir(), "ip_state.json"))
	if err != nil {
		t.Fatalf("loadIPState: %v", err)
	}
	if s.IP != "" || !s.UpdatedAt.IsZero() {
		t.Fatalf("expected empty state, got %+v", s)
	}
}

func TestLoadIPStateInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ip_state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadIPState(path); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestIPStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ip_state.json")
	want := &IPState{IP: "203.0.113.7", UpdatedAt: time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)}

	if err := saveIPState(path, want); err != nil {
		t.Fatalf("saveIPState: %v", err)
	}
	got, err := loadIPState(path)
	if err != nil {
		t.Fatalf("loadIPState: %v", err)
	}
	if got.IP != want.IP || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestFetchPublicIP(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr bool
	}{
		{"plain ip", http.StatusOK, "203.0.113.7", "203.0.113.7", false},
		{"trailing newline", http.StatusOK, "203.0.113.7\n", "203.0.113.7", false},
		{"ipv6 rejected", http.StatusOK, "2001:db8::1", "", true},
		{"garbage", http.StatusOK, "<html>", "", true},
		{"server error", http.StatusInternalServerError, "203.0.113.7", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			got, err := fetchPublicIP(srv.URL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("fetchPublicIP = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
