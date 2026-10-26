// Package cf lists and updates Cloudflare DNS records.
package cf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"goci-dns/internals/logger"
	"goci-dns/internals/utils"

	cloudflare "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/dns"
)

const (
	// requestTimeout bounds a single Cloudflare API request.
	requestTimeout = 15 * time.Second
	// runTimeout bounds the update of one zone (listing + all updates).
	runTimeout = 2 * time.Minute
)

// UpdateDNS updates every configured zone. A failing zone does not stop the
// others; all errors are returned together.
func UpdateDNS(cfg *utils.Config, newIP string) error {
	logger.Debug("[CF] updating zones", "count", len(cfg.CF.Zones))
	var errs []error
	for _, zone := range cfg.CF.Zones {
		if err := updateZone(zone, newIP); err != nil {
			logger.Error(fmt.Sprintf("[CF] zone %s failed", zone.ZoneID), "error", err.Error())
			errs = append(errs, fmt.Errorf("zone %s: %w", zone.ZoneID, err))
		}
	}
	return errors.Join(errs...)
}

// updateZone PUTs the new IP on every record of zone. It first lists all zone
// records (across every page) so it can preserve the record name, TTL and proxied flag.
func updateZone(zone utils.ZoneEntry, newIP string) error {
	client := newClient(zone.Credential)
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	// Walk every page of the zone so records beyond the first page are found too.
	currentRecords := make(map[string]dns.RecordResponse)
	iter := client.DNS.Records.ListAutoPaging(ctx, dns.RecordListParams{
		ZoneID: cloudflare.F(zone.ZoneID),
	})
	for iter.Next() {
		r := iter.Current()
		currentRecords[r.ID] = r
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("list records: %w", err)
	}
	logger.Debug("[CF] zone records listed", "zone_id", zone.ZoneID, "count", len(currentRecords))

	for _, recordID := range zone.RecordIDs {
		recordID = strings.TrimSpace(recordID)
		if recordID == "" {
			continue
		}

		existing, ok := currentRecords[recordID]
		if !ok {
			logger.Warn(fmt.Sprintf("[CF:%s/%s] record not found in zone – skipping", zone.ZoneID, recordID))
			continue
		}
		if existing.Type != dns.RecordResponseTypeA {
			logger.Warn(fmt.Sprintf("[CF:%s/%s] record type is %s, not A – skipping", zone.ZoneID, recordID, existing.Type))
			continue
		}
		recordName := existing.Name

		logger.Info(fmt.Sprintf("[CF:%s/%s] current name=%q content=%s → new content=%s", zone.ZoneID, recordID, recordName, existing.Content, newIP))

		if existing.Content == newIP {
			logger.Info(fmt.Sprintf("[CF:%s/%s] content already matches new IP %s – skipping update", zone.ZoneID, recordID, newIP))
			continue
		}

		// PUT – overwrite with the new IP, keeping the same name.
		_, err := client.DNS.Records.Update(ctx, recordID, dns.RecordUpdateParams{
			ZoneID: cloudflare.F(zone.ZoneID),
			Body: dns.ARecordParam{
				Name:    cloudflare.F(recordName),
				Type:    cloudflare.F(dns.ARecordTypeA),
				TTL:     cloudflare.F(existing.TTL),
				Content: cloudflare.F(newIP),
				Proxied: cloudflare.F(existing.Proxied),
			},
		})
		if err != nil {
			return fmt.Errorf("update record %q: %w", recordID, err)
		}

		logger.Info(fmt.Sprintf("[CF:%s/%s] success: updated to %s", zone.ZoneID, recordID, newIP))
	}

	return nil
}
