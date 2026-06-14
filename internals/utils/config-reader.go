package utils

import (
	"fmt"
	"goci-dns/internals/logger"
	"strings"

	"gopkg.in/ini.v1"
)

// Config represents the structure of config.ini.
type Config struct {
	GoCI GoCIConfig `ini:"GoCI"`
	CF   CFConfig   `ini:"CF"`
}

// GoCIConfig maps the [GoCI] section.
type GoCIConfig struct {
	TTLMinutesCheckDNSEntries int    `ini:"TTL_MINUTES_CHECK_DNS_ENTRIES"`
	IgnoreOldIP               bool   `ini:"IGNORE_OLD_IP"`
	DomainToResolve           string `ini:"DOMAIN_TO_RESOLVE"`
	OverrideNewIP             bool   `ini:"OVERRIDE_NEW_IP"`
	NewIP                     string `ini:"NEW_IP"`
}

// CFConfig maps the [CF] section.
type CFConfig struct {
	CFToken       string   `ini:"CF_TOKEN"`
	CFZoneID      string   `ini:"CF_ZONE_ID"`
	CFAccountID   string   `ini:"CF_ACCOUNT_ID"`
	CFDNSEntries  string   `ini:"CF_DNS_ENTRIES_ID"` // raw value from INI
	CFDNSEntryIDs []string `ini:"-"`                 // parsed IDs from CFDNSEntries
}

// LoadConfig reads the config.ini file at the given path and returns a Config struct
func LoadConfig(path string) (*Config, error) {
	logger.Log("LoadConfig path:", path)
	cfg, err := ini.Load(path)
	if err != nil {
		logger.Log("Fail to read file:", err)
		return nil, err
	}

	config := new(Config)
	if err := cfg.MapTo(config); err != nil {
		logger.Log("Fail to map config:", err)
		return nil, err
	}

	// Keep raw CF_DNS_ENTRIES_ID and expose parsed IDs for easier usage.
	config.CF.CFDNSEntryIDs = parseDNSEntryIDs(config.CF.CFDNSEntries)

	if valid, err := ValidateConfig(config); !valid {
		logger.Warn("Config validation failed:", err)
		return nil, err
	}

	return config, nil
}

func ValidateConfig(config *Config) (bool, error) {
	if config == nil {
		return false, fmt.Errorf("config is nil")
	}

	isDefault := func(v string) bool {
		n := strings.ToLower(strings.TrimSpace(v))
		if n == "" {
			return true
		}
		return strings.HasPrefix(n, "your-") || strings.Contains(n, "replace-me")
	}

	// [GoCI]
	if config.GoCI.TTLMinutesCheckDNSEntries < 0 {
		return false, fmt.Errorf("GoCI.TTL_MINUTES_CHECK_DNS_ENTRIES must be >= 0")
	}
	if isDefault(config.GoCI.DomainToResolve) {
		return false, fmt.Errorf("GoCI.DOMAIN_TO_RESOLVE is empty or default")
	}
	if config.GoCI.OverrideNewIP && isDefault(config.GoCI.NewIP) {
		return false, fmt.Errorf("GoCI.NEW_IP is required when OVERRIDE_NEW_IP=true")
	}

	// [CF]
	if isDefault(config.CF.CFToken) {
		return false, fmt.Errorf("CF.CF_TOKEN is empty or default")
	}
	if isDefault(config.CF.CFZoneID) {
		return false, fmt.Errorf("CF.CF_ZONE_ID is empty or default")
	}
	if isDefault(config.CF.CFAccountID) {
		return false, fmt.Errorf("CF.CF_ACCOUNT_ID is empty or default")
	}
	if isDefault(config.CF.CFDNSEntries) {
		return false, fmt.Errorf("CF.CF_DNS_ENTRIES_ID is empty or default")
	}

	if len(config.CF.CFDNSEntryIDs) == 0 {
		config.CF.CFDNSEntryIDs = parseDNSEntryIDs(config.CF.CFDNSEntries)
	}
	if len(config.CF.CFDNSEntryIDs) == 0 {
		return false, fmt.Errorf("CF.CF_DNS_ENTRIES_ID must contain at least one record id")
	}
	for _, id := range config.CF.CFDNSEntryIDs {
		if isDefault(id) {
			return false, fmt.Errorf("CF.CF_DNS_ENTRIES_ID contains default value")
		}
	}

	return true, nil
}

// parseDNSEntryIDs converts a semicolon-delimited record-id string into []string.
func parseDNSEntryIDs(raw string) []string {
	var entries []string
	for _, part := range strings.Split(raw, ";") {
		if e := strings.TrimSpace(part); e != "" {
			entries = append(entries, e)
		}
	}
	return entries
}
