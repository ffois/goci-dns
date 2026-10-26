// Package cf updates Cloudflare DNS records.
package cf

import (
	"context"
	"fmt"
	"strings"

	"goci-dns/internals/logger"
	"goci-dns/internals/utils"

	cloudflare "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
)

// UpdateDNS iterates over every DNS record ID in cfg and PUTs the new IP.
// It first lists all zone records (across every page) so it can preserve the record name.
func UpdateDNS(cfg *utils.Config, newIP string) error {
	client := cloudflare.NewClient(option.WithAPIToken(cfg.CF.CFToken))
	ctx := context.Background()

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

	for _, recordID := range cfg.CF.CFDNSEntryIDs {
		recordID = strings.TrimSpace(recordID)
		if recordID == "" {
			continue
		}

		existing, ok := currentRecords[recordID]
		if !ok {
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
		_, err := client.DNS.Records.Update(ctx, recordID, dns.RecordUpdateParams{
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
