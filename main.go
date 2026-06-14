package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"gopkg.in/ini.v1"
)

// Config holds all settings read from config.ini.
type Config struct {
	// TTLMinutesCheckDNSEntries controls the polling interval when running as a
	// long-lived daemon (e.g. systemd service).  Set to 0 to run once and exit
	// (cron-job mode).
	TTLMinutesCheckDNSEntries int

	// IgnoreOldIP forces a Cloudflare update even when the resolved IP matches
	// the saved IP.
	IgnoreOldIP bool

	// DomainToResolve is the hostname that is pinged / resolved to obtain the
	// current public IP (e.g. "i59n5i62jw4mazmm.myfritz.net").
	DomainToResolve string

	// OverrideNewIP, when true, uses NewIP instead of the resolved address when
	// pushing updates to Cloudflare.
	OverrideNewIP bool

	// NewIP is the static IP to push when OverrideNewIP is true.
	NewIP string

	// CFToken is the Cloudflare API token.
	CFToken string

	// CFZoneID is the Cloudflare Zone ID that owns the DNS records.
	CFZoneID string

	// CFAccountID is the Cloudflare Account ID (reserved for future use).
	CFAccountID string

	// CFDNSEntriesID is the list of DNS record IDs to update, split by ";".
	CFDNSEntriesID []string
}

// loadConfig reads config.ini from path and returns a populated Config.
func loadConfig(path string) (*Config, error) {
	cfg, err := ini.LoadSources(ini.LoadOptions{
		IgnoreInlineComment: true,
	}, path)
	if err != nil {
		return nil, fmt.Errorf("load config file %q: %w", path, err)
	}

	sec := cfg.Section("")

	ttlMinutes, err := sec.Key("TTL_MINUTES_CHECK_DNS_ENTRIES").Int()
	if err != nil {
		return nil, fmt.Errorf("TTL_MINUTES_CHECK_DNS_ENTRIES: %w", err)
	}

	ignoreOldIP, err := sec.Key("IGNORE_OLD_IP").Bool()
	if err != nil {
		return nil, fmt.Errorf("IGNORE_OLD_IP: %w", err)
	}

	overrideNewIP, err := sec.Key("OVERRIDE_NEW_IP").Bool()
	if err != nil {
		return nil, fmt.Errorf("OVERRIDE_NEW_IP: %w", err)
	}

	var entries []string
	for _, raw := range strings.Split(sec.Key("CF_DNS_ENTRIES_ID").String(), ";") {
		if e := strings.TrimSpace(raw); e != "" {
			entries = append(entries, e)
		}
	}

	return &Config{
		TTLMinutesCheckDNSEntries: ttlMinutes,
		IgnoreOldIP:               ignoreOldIP,
		DomainToResolve:           strings.TrimSpace(sec.Key("DOMAIN_TO_RESOLVE").String()),
		OverrideNewIP:             overrideNewIP,
		NewIP:                     strings.TrimSpace(sec.Key("NEW_IP").String()),
		CFToken:                   strings.TrimSpace(sec.Key("CF_TOKEN").String()),
		CFZoneID:                  strings.TrimSpace(sec.Key("CF_ZONE_ID").String()),
		CFAccountID:               strings.TrimSpace(sec.Key("CF_ACCOUNT_ID").String()),
		CFDNSEntriesID:            entries,
	}, nil
}

// IPState is persisted to ip_state.json to remember the last known IP.
type IPState struct {
	IP        string    `json:"ip"`
	UpdatedAt time.Time `json:"updated_at"`
}

func loadIPState(path string) (*IPState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &IPState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ip_state.json: %w", err)
	}
	var s IPState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse ip_state.json: %w", err)
	}
	return &s, nil
}

func saveIPState(path string, s *IPState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ip_state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write ip_state.json: %w", err)
	}
	return nil
}

// resolveIP resolves domain to its first IPv4 address using Google's public
// DNS (8.8.8.8), bypassing any local OS cache – the same mechanism a plain
// "ping <domain>" uses to find its target address.
func resolveIP(domain string) (string, error) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, "8.8.8.8:53")
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	addrs, err := resolver.LookupHost(ctx, domain)
	if err != nil {
		return "", fmt.Errorf("lookup %q: %w", domain, err)
	}

	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address found for %q", domain)
}

// Cloudflare stuff funcs

// updateCFDNS iterates over every DNS record ID in cfg and PUTs the new IP.
// It first GETs the current record so it can preserve the record name.
func updateCFDNS(cfg *Config, newIP string) error {
	client := cloudflare.NewClient(option.WithAPIToken(cfg.CFToken))
	ctx := context.Background()

	for _, recordID := range cfg.CFDNSEntriesID {
		recordID = strings.TrimSpace(recordID)
		if recordID == "" {
			continue
		}

		// GET – retrieve the existing record to obtain its name.
		existing, err := client.DNS.Records.Get(ctx, recordID, dns.RecordGetParams{
			ZoneID: cloudflare.F(cfg.CFZoneID),
		})
		if err != nil {
			return fmt.Errorf("get record %q: %w", recordID, err)
		}

		recordName := existing.Name
		log.Printf("[%s] current name=%q content=%s → new content=%s",
			recordID, recordName, existing.Content, newIP)

		// PUT – overwrite with the new IP, keeping the same name.
		_, err = client.DNS.Records.Update(ctx, recordID, dns.RecordUpdateParams{
			ZoneID: cloudflare.F(cfg.CFZoneID),
			Body: dns.ARecordParam{
				Name:    cloudflare.F(recordName),
				Type:    cloudflare.F(dns.ARecordTypeA),
				TTL:     cloudflare.F(dns.TTL1), // automatic TTL
				Content: cloudflare.F(newIP),
				Proxied: cloudflare.F(existing.Proxied),
			},
		})
		if err != nil {
			return fmt.Errorf("update record %q: %w", recordID, err)
		}

		log.Printf("[%s] ✓ updated to %s", recordID, newIP)
	}

	return nil
}

// Core logic

func check(cfg *Config, statePath string) error {
	// 1. Resolve the current public IP via DNS lookup (same as pinging the domain).
	currentIP, err := resolveIP(cfg.DomainToResolve)
	if err != nil {
		return fmt.Errorf("resolve IP: %w", err)
	}
	log.Printf("Resolved %q → %s", cfg.DomainToResolve, currentIP)

	// 2. Load the last saved IP.
	state, err := loadIPState(statePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// 3. Decide whether an update is required.
	ipChanged := state.IP != currentIP
	if !ipChanged && !cfg.IgnoreOldIP {
		log.Printf("IP unchanged (%s) – nothing to do.", currentIP)
		return nil
	}

	if ipChanged {
		log.Printf("IP changed: %q → %q", state.IP, currentIP)
	} else {
		log.Printf("IGNORE_OLD_IP=true – forcing update regardless of IP match.")
	}

	// 4. Determine the target IP to push to Cloudflare.
	targetIP := currentIP
	if cfg.OverrideNewIP && cfg.NewIP != "" {
		targetIP = cfg.NewIP
		log.Printf("OVERRIDE_NEW_IP=true – using configured IP %s instead of resolved %s",
			targetIP, currentIP)
	}

	// 5. Push update to Cloudflare.
	if err := updateCFDNS(cfg, targetIP); err != nil {
		return fmt.Errorf("cloudflare update: %w", err)
	}

	// 6. Persist the current (resolved) IP so the next run can detect changes.
	state.IP = currentIP
	state.UpdatedAt = time.Now().UTC()
	if err := saveIPState(statePath, state); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	log.Printf("State saved: ip=%s updatedAt=%s", state.IP, state.UpdatedAt.Format(time.RFC3339))
	return nil
}

func main() {
	// Usage: goci-dns [config.ini [ip_state.json]]
	configPath := "config.ini"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	statePath := filepath.Join(filepath.Dir(configPath), "ip_state.json")
	if len(os.Args) > 2 {
		statePath = os.Args[2]
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("Config error: %v", err)
	}

	log.Printf("goci-dns started – monitoring %q, %d Cloudflare record(s)",
		cfg.DomainToResolve, len(cfg.CFDNSEntriesID))

	if cfg.TTLMinutesCheckDNSEntries > 0 {
		// Daemon mode (systemd service)
		// Run immediately, then repeat every TTLMinutesCheckDNSEntries minutes.
		interval := time.Duration(cfg.TTLMinutesCheckDNSEntries) * time.Minute
		log.Printf("Daemon mode: checking every %s", interval)

		runCheck := func() {
			if err := check(cfg, statePath); err != nil {
				log.Printf("ERROR: %v", err)
			}
		}

		runCheck() // first run right away

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			runCheck()
		}
	} else {
		// OneShot mode (if enabled via cron job and TTL_MINUTES_CHECK_DNS_ENTRIES=0)
		log.Printf("One-shot mode (TTL_MINUTES_CHECK_DNS_ENTRIES=0).")
		if err := check(cfg, statePath); err != nil {
			log.Fatalf("ERROR: %v", err)
		}
	}
}
