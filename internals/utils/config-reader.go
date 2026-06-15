package utils

import (
	"fmt"
	"goci-dns/internals/logger"
	"strings"

	"gopkg.in/ini.v1"
)

// Config represents the structure of config.ini.
type Config struct {
	GoCI  GoCIConfig  `ini:"GoCI"`
	CF    CFConfig    `ini:"CF"`
	IONOS IONOSConfig `ini:"IONOS"`
}

// GoCIConfig maps the [GoCI] section.
type GoCIConfig struct {
	TTLMinutesCheckDNSEntries int    `ini:"TTL_MINUTES_CHECK_DNS_ENTRIES"`
	IgnoreOldIP               bool   `ini:"IGNORE_OLD_IP"`
	DomainToResolve           string `ini:"DOMAIN_TO_RESOLVE"`
	OverrideNewIP             bool   `ini:"OVERRIDE_NEW_IP"`
	NewIP                     string `ini:"NEW_IP"`
	EnableIONOS               bool   `ini:"ENABLE_IONOS"`
	EnableCloudflare          bool   `ini:"ENABLE_CLOUDFLARE"`
}

// CFConfig maps the [CF] section.
type CFConfig struct {
	CFToken       string   `ini:"CF_TOKEN"`
	CFZoneID      string   `ini:"CF_ZONE_ID"`
	CFAccountID   string   `ini:"CF_ACCOUNT_ID"`
	CFDNSEntries  string   `ini:"CF_DNS_ENTRIES_ID"` // raw value from INI
	CFDNSEntryIDs []string `ini:"-"`                 // parsed IDs from CFDNSEntries
}

type IONOSConfig struct {
	IONOSApiUrl   string   `ini:"IONOS_API_URL"`
	IONOSApiKey   string   `ini:"IONOS_API_KEY"`
	IONOSZoneID   string   `ini:"IONOS_ZONE_ID"`
	IONOSEntries  string   `ini:"IONOS_DNS_ENTRIES_ID"` // raw value from INI
	IONOSEntryIDs []string `ini:"-"`                    // parsed IDs from IONOSEntries
}

// LoadConfig reads the config.ini file at the given path and returns a Config struct
func LoadConfig(path string) (*Config, error) {
	logger.Log("LoadConfig path:", path)
	cfg, err := ini.LoadSources(ini.LoadOptions{}, path)
	if err != nil {
		logger.Log("Fail to read file:", err)
		return nil, err
	}

	config := new(Config)
	if err := cfg.MapTo(config); err != nil {
		logger.Log("Fail to map config:", err)
		return nil, err
	}

	if !config.GoCI.EnableIONOS && !config.GoCI.EnableCloudflare {
		logger.Fatal("At least one provider must be enabled: GoCI.EnableIONOS or GoCI.EnableCloudflare")
	}

	if config.GoCI.EnableIONOS {
		config.IONOS.IONOSEntryIDs = parseDNSEntryIDs(config.IONOS.IONOSEntries)
	}
	if config.GoCI.EnableCloudflare {
		config.CF.CFDNSEntryIDs = parseDNSEntryIDs(config.CF.CFDNSEntries)
	}

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
	if config.GoCI.EnableCloudflare {
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
	}

	// [IONOS]
	if config.GoCI.EnableIONOS {
		if isDefault(config.IONOS.IONOSApiUrl) {
			return false, fmt.Errorf("IONOS.IONOS_API_URL is empty or default")
		}
		if isDefault(config.IONOS.IONOSApiKey) {
			return false, fmt.Errorf("IONOS.IONOS_API_KEY is empty or default")
		}
		if isDefault(config.IONOS.IONOSZoneID) {
			return false, fmt.Errorf("IONOS.IONOS_ZONE_ID is empty or default")
		}
		if isDefault(config.IONOS.IONOSEntries) {
			return false, fmt.Errorf("IONOS.IONOS_DNS_ENTRIES_ID is empty or default")
		}
		if len(config.IONOS.IONOSEntryIDs) == 0 {
			config.IONOS.IONOSEntryIDs = parseDNSEntryIDs(config.IONOS.IONOSEntries)
		}
		if len(config.IONOS.IONOSEntryIDs) == 0 {
			return false, fmt.Errorf("IONOS.IONOS_DNS_ENTRIES_ID must contain at least one record id")
		}
		for _, id := range config.IONOS.IONOSEntryIDs {
			if isDefault(id) {
				return false, fmt.Errorf("IONOS.IONOS_DNS_ENTRIES_ID contains default value")
			}
		}
	}

	return true, nil
}

// parseDNSEntryIDs converts a semicolon-delimited record-id string into []string.
func parseDNSEntryIDs(raw string) []string {
	var entries []string
	for _, part := range strings.Split(raw, ",") {
		if e := strings.TrimSpace(part); e != "" {
			entries = append(entries, e)
		}
	}
	return entries
}
