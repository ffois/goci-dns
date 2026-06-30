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
	ionos "github.com/ffois/go-ionos"
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

	currentRecords, err := client.DNS.Records.List(ctx, dns.RecordListParams{
		ZoneID: cloudflare.F(cfg.CF.CFZoneID),
	})
	if err != nil {
		return fmt.Errorf("list records: %w", err)
	}

	for _, recordID := range cfg.CF.CFDNSEntryIDs {
		recordID = strings.TrimSpace(recordID)
		if recordID == "" {
			continue
		}

		var existing *dns.RecordResponse
		for _, r := range currentRecords.Result {
			if r.ID == recordID {
				existing = &r
				break
			}
		}
		if existing == nil {
			logger.Info(fmt.Sprintf("[%s] record not found in zone – skipping", recordID))
			continue
		}
		recordName := existing.Name

		logger.Info(fmt.Sprintf("[%s] current name=%q content=%s → new content=%s", recordID, recordName, existing.Content, newIP))

		if existing.Content == newIP {
			logger.Info(fmt.Sprintf("[%s] content already matches new IP %s – skipping update", recordID, newIP))
			continue
		}

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

// updateIONOSDNS iterates over every DNS record ID in cfg and updates the record content.
func updateIONOSDNS(cfg *utils.Config, newIP string) error {
	ctx := context.Background()

	client := ionos.NewClient(
		ionos.WithAPIKey(cfg.IONOS.IONOSApiKey),
		ionos.WithBaseURL(cfg.IONOS.IONOSApiUrl),
	)

	zone, err := client.Zones.Get(ctx, cfg.IONOS.IONOSZoneID, nil)
	if err != nil {
		return fmt.Errorf("get ionos zone: %w", err)
	}

	records := zone.Records
	if records == nil {
		return fmt.Errorf("ionos zone has no records payload")
	}

	for _, recordID := range cfg.IONOS.IONOSEntryIDs {
		recordID = strings.TrimSpace(recordID)
		if recordID == "" {
			logger.Warn(fmt.Sprintf("[IONOS:%s] record ID is empty - skipping", recordID))
			continue
		}

		var existing *ionos.RecordResponse
		for i := range *records {
			r := &(*records)[i]
			if r.Id != nil && *r.Id == recordID {
				existing = r
				break
			}
		}
		if existing == nil {
			logger.Warn(fmt.Sprintf("[IONOS:%s] record not found in zone - skipping", recordID))
			continue
		}

		recordName := ""
		if existing.Name != nil {
			recordName = *existing.Name
		}
		currentContent := ""
		if existing.Content != nil {
			currentContent = *existing.Content
		}

		logger.Info(fmt.Sprintf("[IONOS:%s] current name=%q content=%s -> new content=%s", recordID, recordName, currentContent, newIP))

		if currentContent == newIP {
			logger.Info(fmt.Sprintf("[IONOS:%s] content already matches new IP %s - skipping update", recordID, newIP))
			continue
		}

		newContent := newIP
		updateBody := ionos.UpdateRecordJSONRequestBody{
			Content:  &newContent,
			Disabled: existing.Disabled,
			Prio:     existing.Prio,
			Ttl:      existing.Ttl,
		}

		if _, err := client.Records.Update(ctx, cfg.IONOS.IONOSZoneID, recordID, updateBody); err != nil {
			return fmt.Errorf("update ionos record %q: %w", recordID, err)
		}

		logger.Info(fmt.Sprintf("[IONOS:%s] success: updated to %s", recordID, newIP))
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

	if cfg.GoCI.EnableCloudflare {
		if err := updateCFDNS(cfg, targetIP); err != nil {
			return fmt.Errorf("cloudflare update: %w", err)
		}
	}
	if cfg.GoCI.EnableIONOS {
		if err := updateIONOSDNS(cfg, targetIP); err != nil {
			return fmt.Errorf("ionos update: %w", err)
		}
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

	if cfg.GoCI.EnableCloudflare {
		logger.Info("Cloudflare DNS entries enabled")
	}
	if cfg.GoCI.EnableIONOS {
		logger.Info("IONOS DNS entries enabled")
	}

	logger.Info(fmt.Sprintf("goci-dns started – monitoring %q, %d Cloudflare record(s) | %d IONOS record(s)",
		cfg.GoCI.DomainToResolve, len(cfg.CF.CFDNSEntryIDs), len(cfg.IONOS.IONOSEntryIDs)))

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
