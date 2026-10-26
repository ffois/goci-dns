// Package cf lists and updates Cloudflare DNS records.
package cf

import (
	"context"
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
	// runTimeout bounds a whole UpdateDNS call (listing + all updates).
	runTimeout = 2 * time.Minute
)

// UpdateDNS iterates over every DNS record ID in cfg and PUTs the new IP.
// It first lists all zone records (across every page) so it can preserve the record name.
func UpdateDNS(cfg *utils.Config, newIP string) error {
	client := newClient(cfg.CF.CFToken)
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	// Walk every page of the zone so records beyond the first page are found too.
	currentRecords := make(map[string]dns.RecordResponse)
	iter := client.DNS.Records.ListAutoPaging(ctx, dns.RecordListParams{
		ZoneID: cloudflare.F(cfg.CF.CFZoneID),
	})
	for iter.Next() {
		r := iter.Current()
		currentRecords[r.ID] = r
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("list records: %w", err)
	}
	logger.Debug("[CF] zone records listed", "zone_id", cfg.CF.CFZoneID, "count", len(currentRecords))

	for _, recordID := range cfg.CF.CFDNSEntryIDs {
		recordID = strings.TrimSpace(recordID)
		if recordID == "" {
			continue
		}

		existing, ok := currentRecords[recordID]
		if !ok {
			logger.Warn(fmt.Sprintf("[CF:%s] record not found in zone – skipping", recordID))
			continue
		}
		if existing.Type != dns.RecordResponseTypeA {
			logger.Warn(fmt.Sprintf("[CF:%s] record type is %s, not A – skipping", recordID, existing.Type))
			continue
		}
		recordName := existing.Name

		logger.Info(fmt.Sprintf("[CF:%s] current name=%q content=%s → new content=%s", recordID, recordName, existing.Content, newIP))

		if existing.Content == newIP {
			logger.Info(fmt.Sprintf("[CF:%s] content already matches new IP %s – skipping update", recordID, newIP))
			continue
		}

		// PUT – overwrite with the new IP, keeping the same name.
		_, err := client.DNS.Records.Update(ctx, recordID, dns.RecordUpdateParams{
			ZoneID: cloudflare.F(cfg.CF.CFZoneID),
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

		logger.Info(fmt.Sprintf("[CF:%s] success: updated to %s", recordID, newIP))
	}

	return nil
}
