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

	"goci-dns/internals/logger"
	"goci-dns/internals/utils"

	cloudflare "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
)

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
func updateCFDNS(cfg *utils.Config, newIP string) error {
	client := cloudflare.NewClient(option.WithAPIToken(cfg.CF.CFToken))
	ctx := context.Background()

	for _, recordID := range cfg.CF.CFDNSEntryIDs {
		recordID = strings.TrimSpace(recordID)
		if recordID == "" {
			continue
		}

		// GET – retrieve the existing record to obtain its name.
		existing, err := client.DNS.Records.Get(ctx, recordID, dns.RecordGetParams{
			ZoneID: cloudflare.F(cfg.CF.CFZoneID),
		})
		if err != nil {
			return fmt.Errorf("get record %q: %w", recordID, err)
		}

		recordName := existing.Name

		logLine := fmt.Sprintf("[%s] current name=%q content=%s → new content=%s",
			recordID, recordName, existing.Content, newIP)
		logger.Info(logLine)

		// PUT – overwrite with the new IP, keeping the same name.
		_, err = client.DNS.Records.Update(ctx, recordID, dns.RecordUpdateParams{
			ZoneID: cloudflare.F(cfg.CF.CFZoneID),
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

		logger.Info(fmt.Sprintf("[%s] success: updated to %s", recordID, newIP))
	}

	return nil
}

// Core logic

func check(cfg *utils.Config, statePath string) error {
	// 1. Resolve the current public IP via DNS lookup (same as pinging the domain).
	currentIP, err := resolveIP(cfg.GoCI.DomainToResolve)
	if err != nil {
		return fmt.Errorf("resolve IP: %w", err)
	}
	logger.Info(fmt.Sprintf("Resolved %q → %s", cfg.GoCI.DomainToResolve, currentIP))

	// 2. Load the last saved IP.
	state, err := loadIPState(statePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// 3. Decide whether an update is required.
	ipChanged := state.IP != currentIP
	if !ipChanged && !cfg.GoCI.IgnoreOldIP {
		logger.Info(fmt.Sprintf("IP unchanged (%s) – nothing to do.", currentIP))
		return nil
	}

	if ipChanged {
		logger.Info(fmt.Sprintf("IP changed: %q → %q", state.IP, currentIP))
	} else {
		logger.Info(fmt.Sprintf("IGNORE_OLD_IP=true – forcing update regardless of IP match."))
	}

	// 4. Determine the target IP to push to Cloudflare.
	targetIP := currentIP
	if cfg.GoCI.OverrideNewIP && cfg.GoCI.NewIP != "" {
		targetIP = cfg.GoCI.NewIP
		logger.Info(fmt.Sprintf("OVERRIDE_NEW_IP=true – using configured IP %s instead of resolved %s",
			targetIP, currentIP))
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
	logger.Info(fmt.Sprintf("State saved: ip=%s updatedAt=%s", state.IP, state.UpdatedAt.Format(time.RFC3339)))

	interval := time.Duration(cfg.GoCI.TTLMinutesCheckDNSEntries) * time.Minute
	logger.Info(fmt.Sprintf("Current run done! New run scheduled in: %s", interval))

	return nil
}

func main() {
	if err := logger.InitLogger("goci-dns"); err != nil {
		log.Println("Error initializing logger:", err)
	}
	defer logger.CloseLogger()

	// Usage: goci-dns [config.ini [ip_state.json]]
	configPath := "config.ini"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	statePath := filepath.Join(filepath.Dir(configPath), "ip_state.json")
	if len(os.Args) > 2 {
		statePath = os.Args[2]
	}

	cfg, err := utils.LoadConfig(configPath)
	if err != nil {
		logger.Fatal(fmt.Sprintf("Config error: %v", err))
	}

	logger.Info(fmt.Sprintf("goci-dns started – monitoring %q, %d Cloudflare record(s)",
		cfg.GoCI.DomainToResolve, len(cfg.CF.CFDNSEntryIDs)))

	if cfg.GoCI.TTLMinutesCheckDNSEntries > 0 {
		// Daemon mode (systemd service)
		// Run immediately, then repeat every TTLMinutesCheckDNSEntries minutes.
		interval := time.Duration(cfg.GoCI.TTLMinutesCheckDNSEntries) * time.Minute
		logger.Info(fmt.Sprintf("Daemon mode: checking every %s", interval))

		runCheck := func() {
			if err := check(cfg, statePath); err != nil {
				logger.Info(fmt.Sprintf("ERROR: %v", err))
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
		logger.Info(fmt.Sprintf("One-shot mode (TTL_MINUTES_CHECK_DNS_ENTRIES=0)."))
		if err := check(cfg, statePath); err != nil {
			logger.Fatal(fmt.Sprintf("ERROR: %v", err))
		}
	}
}
