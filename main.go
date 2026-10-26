package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goci-dns/internals/logger"
	"goci-dns/internals/provider/cf"
	"goci-dns/internals/provider/ionos"
	"goci-dns/internals/setup"
	"goci-dns/internals/utils"
)

// Version and BuildDate are set at build time via -ldflags "-X main.Version=… -X main.BuildDate=…".
var (
	Version   = "dev"
	BuildDate = "unknown"
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

// fetchPublicIP asks an HTTP "what is my IP" service (e.g. https://api.ipify.org)
// for the current public IPv4. The request is forced over IPv4 so dual-stack
// services cannot answer with an IPv6 address.
func fetchPublicIP(discoveryURL string) (string, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp4", addr)
			},
		},
	}

	resp, err := client.Get(discoveryURL)
	if err != nil {
		return "", fmt.Errorf("get %q: %w", discoveryURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get %q: unexpected status %s", discoveryURL, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", fmt.Errorf("read %q: %w", discoveryURL, err)
	}

	ip := net.ParseIP(strings.TrimSpace(string(body)))
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("%q did not return an IPv4 address: %q", discoveryURL, strings.TrimSpace(string(body)))
	}
	return ip.String(), nil
}

// currentPublicIP returns the current public IPv4, either from the HTTP
// discovery service (USE_IP_DISCOVERY=true) or by resolving DOMAIN_TO_RESOLVE.
func currentPublicIP(cfg *utils.Config) (string, error) {
	if cfg.GoCI.UseIPDiscovery {
		ip, err := fetchPublicIP(cfg.GoCI.IPDiscoveryURL)
		if err != nil {
			return "", err
		}
		logger.Info(fmt.Sprintf("Discovered public IP via %s → %s", cfg.GoCI.IPDiscoveryURL, ip))
		return ip, nil
	}

	ip, err := resolveIP(cfg.GoCI.DomainToResolve)
	if err != nil {
		return "", err
	}
	logger.Info(fmt.Sprintf("Resolved %q → %s", cfg.GoCI.DomainToResolve, ip))
	return ip, nil
}

// Core logic
func check(cfg *utils.Config, statePath string) error {
	// 1. Determine the current public IP (HTTP discovery or DNS lookup).
	currentIP, err := currentPublicIP(cfg)
	if err != nil {
		return fmt.Errorf("resolve IP: %w", err)
	}

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
		logger.Info("IGNORE_OLD_IP=true – forcing update regardless of IP match.")
	}

	// 4. Determine the target IP to push to the providers.
	targetIP := currentIP
	if cfg.GoCI.OverrideNewIP && cfg.GoCI.NewIP != "" {
		targetIP = cfg.GoCI.NewIP
		logger.Info(fmt.Sprintf("OVERRIDE_NEW_IP=true – using configured IP %s instead of resolved %s",
			targetIP, currentIP))
	}

	// 5. Push the update to every enabled provider.
	if cfg.GoCI.EnableCloudflare {
		if err := cf.UpdateDNS(cfg, targetIP); err != nil {
			return fmt.Errorf("cloudflare update: %w", err)
		}
	}
	if cfg.GoCI.EnableIONOS {
		if err := ionos.UpdateDNS(cfg, targetIP); err != nil {
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

	if cfg.GoCI.TTLMinutesCheckDNSEntries > 0 {
		interval := time.Duration(cfg.GoCI.TTLMinutesCheckDNSEntries) * time.Minute
		logger.Info(fmt.Sprintf("Current run done! New run scheduled in: %s", interval))
	}

	return nil
}

func main() {
	// Usage: goci-dns setup [config.ini]
	// Runs the interactive wizard; the JSON logger is not started so it
	// does not interfere with the terminal UI.
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		runSetup()
		return
	}

	// On error the logger falls back to stdout and reports it itself.
	_ = logger.InitLogger("goci-dns")
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

	logger.Info("goci-dns build", "version", Version, "build_date", BuildDate)
	ipSource := cfg.GoCI.DomainToResolve
	if cfg.GoCI.UseIPDiscovery {
		ipSource = cfg.GoCI.IPDiscoveryURL
	}
	logger.Info(fmt.Sprintf("goci-dns started – monitoring %q, %d Cloudflare record(s) | %d IONOS record(s)",
		ipSource, len(cfg.CF.CFDNSEntryIDs), len(cfg.IONOS.IONOSEntryIDs)))

	if cfg.GoCI.TTLMinutesCheckDNSEntries > 0 {
		// Daemon mode (systemd service)
		// Run immediately, then repeat every TTLMinutesCheckDNSEntries minutes.
		interval := time.Duration(cfg.GoCI.TTLMinutesCheckDNSEntries) * time.Minute
		logger.Info(fmt.Sprintf("Daemon mode: checking every %s", interval))

		runCheck := func() {
			if err := check(cfg, statePath); err != nil {
				logger.Error(fmt.Sprintf("check failed: %v", err))
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
		logger.Info("One-shot mode (TTL_MINUTES_CHECK_DNS_ENTRIES=0).")
		if err := check(cfg, statePath); err != nil {
			logger.Fatal(fmt.Sprintf("check failed: %v", err))
		}
	}
}

func runSetup() {
	configPath := "config.ini"
	if len(os.Args) > 2 {
		configPath = os.Args[2]
	}
	if err := setup.Run(configPath); err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		os.Exit(1)
	}
}
