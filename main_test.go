package main

import (
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
