package setup

import (
	"fmt"
	"os"
	"strconv"

	"goci-dns/internals/utils"

	"gopkg.in/ini.v1"
)

// render builds the ini file for cfg, with a short comment on every key.
// Provider sections are only written when the provider is enabled.
func render(cfg *utils.Config) *ini.File {
	f := ini.Empty()
	set := func(section, key, value, comment string) {
		k := f.Section(section).Key(key)
		k.SetValue(value)
		k.Comment = "# " + comment // match config.example.ini style

	}

	g := cfg.GoCI
	set("GoCI", "TTL_MINUTES_CHECK_DNS_ENTRIES", strconv.Itoa(g.TTLMinutesCheckDNSEntries), "0 = one-shot mode (cron), >0 = daemon mode (check every N minutes)")
	set("GoCI", "IGNORE_OLD_IP", strconv.FormatBool(g.IgnoreOldIP), "If true, push the update even when the IP has not changed")
	set("GoCI", "DOMAIN_TO_RESOLVE", g.DomainToResolve, "Hostname resolved to discover the public IP (ignored when USE_IP_DISCOVERY = true)")
	set("GoCI", "USE_IP_DISCOVERY", strconv.FormatBool(g.UseIPDiscovery), "If true, get the public IPv4 from IP_DISCOVERY_URL instead of DOMAIN_TO_RESOLVE")
	set("GoCI", "IP_DISCOVERY_URL", g.IPDiscoveryURL, "HTTP(S) service returning the public IP as plain text")
	set("GoCI", "OVERRIDE_NEW_IP", strconv.FormatBool(g.OverrideNewIP), "If true, push NEW_IP instead of the discovered address")
	set("GoCI", "NEW_IP", g.NewIP, "Static IP used when OVERRIDE_NEW_IP = true")
	set("GoCI", "ENABLE_CLOUDFLARE", strconv.FormatBool(g.EnableCloudflare), "Enable Cloudflare DNS updates")
	set("GoCI", "ENABLE_IONOS", strconv.FormatBool(g.EnableIONOS), "Enable IONOS DNS updates")

	if g.EnableCloudflare {
		set("CF", "CF_TOKEN", cfg.CF.CFToken, "API token with Zone → DNS → Edit permission")
		set("CF", "CF_ZONE_ID", cfg.CF.CFZoneID, "Zone ID of the domain")
		set("CF", "CF_ACCOUNT_ID", cfg.CF.CFAccountID, "Account ID (optional, reserved for future use)")
		set("CF", "CF_DNS_ENTRIES_ID", cfg.CF.CFDNSEntries, "Comma-separated list of A record IDs to update")
	}

	if g.EnableIONOS {
		set("IONOS", "IONOS_API_URL", cfg.IONOS.IONOSApiUrl, "IONOS DNS API base URL")
		set("IONOS", "IONOS_API_KEY", cfg.IONOS.IONOSApiKey, "API key in the form <public prefix>.<secret>")
		set("IONOS", "IONOS_ZONE_ID", cfg.IONOS.IONOSZoneID, "Zone ID of the domain")
		set("IONOS", "IONOS_DNS_ENTRIES_ID", cfg.IONOS.IONOSEntries, "Comma-separated list of A record IDs to update")
	}

	return f
}

// writeConfig writes cfg to path with 0600 permissions, since the file
// contains API credentials.
func writeConfig(cfg *utils.Config, path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := render(cfg).WriteTo(file); err != nil {
		file.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
