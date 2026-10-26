// Package ionos lists and updates IONOS DNS records.
package ionos

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goci-dns/internals/logger"
	"goci-dns/internals/utils"

	ionosapi "github.com/ffois/go-ionos"
)

const (
	// requestTimeout bounds a single IONOS API request.
	requestTimeout = 15 * time.Second
	// runTimeout bounds a whole UpdateDNS call (zone fetch + all updates).
	runTimeout = 2 * time.Minute
)

// UpdateDNS iterates over every DNS record ID in cfg and updates the record content.
func UpdateDNS(cfg *utils.Config, newIP string) error {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	client := newClient(cfg.IONOS.IONOSApiUrl, cfg.IONOS.IONOSApiKey)

	zone, err := client.Zones.Get(ctx, cfg.IONOS.IONOSZoneID, nil)
	if err != nil {
		return fmt.Errorf("get ionos zone: %w", err)
	}

	records := zone.Records
	if records == nil {
		return fmt.Errorf("ionos zone has no records payload")
	}
	logger.Debug("[IONOS] zone records fetched", "zone_id", cfg.IONOS.IONOSZoneID, "count", len(*records))

	for _, recordID := range cfg.IONOS.IONOSEntryIDs {
		recordID = strings.TrimSpace(recordID)
		if recordID == "" {
			logger.Warn(fmt.Sprintf("[IONOS:%s] record ID is empty - skipping", recordID))
			continue
		}

		var existing *ionosapi.RecordResponse
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

		if existing.Type == nil || *existing.Type != ionosapi.A {
			recordType := "unknown"
			if existing.Type != nil {
				recordType = string(*existing.Type)
			}
			logger.Warn(fmt.Sprintf("[IONOS:%s] record type is %s, not A - skipping", recordID, recordType))
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
		updateBody := ionosapi.UpdateRecordJSONRequestBody{
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
