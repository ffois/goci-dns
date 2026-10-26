package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"goci-dns/internals/utils"
)

func TestWriteConfigRoundTrip(t *testing.T) {
	cfg := &utils.Config{
		GoCI: utils.GoCIConfig{
			TTLMinutesCheckDNSEntries: 5,
			UseIPDiscovery:            true,
			IPDiscoveryURL:            utils.DefaultIPDiscoveryURL,
			EnableCloudflare:          true,
			EnableIONOS:               true,
		},
		CF: utils.CFConfig{
			CFToken:      "cf-token",
			CFAccountID:  "cf-account",
			CFZones:      "z1:r1,r2;z2:r3",
			CFZoneTokens: "z2:other-token",
		},
		IONOS: utils.IONOSConfig{
			IONOSApiUrl: utils.DefaultIONOSApiURL,
			IONOSApiKey: "prefix.secret",
			IONOSZones:  "ionos-zone:i1",
		},
	}

	path := filepath.Join(t.TempDir(), "config.ini")
	if err := writeConfig(cfg, path); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}

	got, err := utils.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig on generated file: %v", err)
	}
	if got.GoCI != cfg.GoCI {
		t.Errorf("GoCI = %+v, want %+v", got.GoCI, cfg.GoCI)
	}
	wantCF := []utils.ZoneEntry{
		{ZoneID: "z1", RecordIDs: []string{"r1", "r2"}, Credential: "cf-token"},
		{ZoneID: "z2", RecordIDs: []string{"r3"}, Credential: "other-token"},
	}
	if got.CF.CFAccountID != "cf-account" || !reflect.DeepEqual(got.CF.Zones, wantCF) {
		t.Errorf("CF = %+v", got.CF)
	}
	wantIONOS := []utils.ZoneEntry{{ZoneID: "ionos-zone", RecordIDs: []string{"i1"}, Credential: "prefix.secret"}}
	if !reflect.DeepEqual(got.IONOS.Zones, wantIONOS) {
		t.Errorf("IONOS = %+v", got.IONOS)
	}
}

func TestRenderOmitsDisabledProviders(t *testing.T) {
	cfg := &utils.Config{GoCI: utils.GoCIConfig{DomainToResolve: "home.example.org", EnableCloudflare: true}}
	f := render(cfg)
	if f.HasSection("IONOS") {
		t.Error("IONOS section written although IONOS is disabled")
	}
	if !f.HasSection("CF") {
		t.Error("CF section missing")
	}
}
