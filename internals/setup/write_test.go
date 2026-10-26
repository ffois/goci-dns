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
			CFZoneID:     "cf-zone",
			CFAccountID:  "cf-account",
			CFDNSEntries: "r1,r2",
		},
		IONOS: utils.IONOSConfig{
			IONOSApiUrl:  utils.DefaultIONOSApiURL,
			IONOSApiKey:  "prefix.secret",
			IONOSZoneID:  "ionos-zone",
			IONOSEntries: "i1",
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
	if got.CF.CFToken != "cf-token" || got.CF.CFZoneID != "cf-zone" || !reflect.DeepEqual(got.CF.CFDNSEntryIDs, []string{"r1", "r2"}) {
		t.Errorf("CF = %+v", got.CF)
	}
	if got.IONOS.IONOSApiKey != "prefix.secret" || got.IONOS.IONOSZoneID != "ionos-zone" || !reflect.DeepEqual(got.IONOS.IONOSEntryIDs, []string{"i1"}) {
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
