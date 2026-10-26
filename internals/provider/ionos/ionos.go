// Package ionos lists and updates IONOS DNS records.
package ionos

import (
	"context"
	"errors"
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
	// runTimeout bounds the update of one zone (zone fetch + all updates).
	runTimeout = 2 * time.Minute
)

// UpdateDNS updates every configured zone. A failing zone does not stop the
// others; all errors are returned together.
func UpdateDNS(cfg *utils.Config, newIP string) error {
	logger.Debug("[IONOS] updating zones", "count", len(cfg.IONOS.Zones))
	var errs []error
	for _, zone := range cfg.IONOS.Zones {
		if err := updateZone(cfg.IONOS.IONOSApiUrl, zone, newIP); err != nil {
			logger.Error(fmt.Sprintf("[IONOS] zone %s failed", zone.ZoneID), "error", err.Error())
			errs = append(errs, fmt.Errorf("zone %s: %w", zone.ZoneID, err))
		}
	}
	return errors.Join(errs...)
}

// updateZone updates the content of every record of entry.
func updateZone(apiURL string, entry utils.ZoneEntry, newIP string) error {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	client := newClient(apiURL, entry.Credential)

	zone, err := client.Zones.Get(ctx, entry.ZoneID, nil)
	if err != nil {
		return fmt.Errorf("get ionos zone: %w", err)
	}

	records := zone.Records
	if records == nil {
		return fmt.Errorf("ionos zone has no records payload")
	}
	logger.Debug("[IONOS] zone records fetched", "zone_id", entry.ZoneID, "count", len(*records))

	for _, recordID := range entry.RecordIDs {
		recordID = strings.TrimSpace(recordID)
		if recordID == "" {
			logger.Warn(fmt.Sprintf("[IONOS:%s/%s] record ID is empty - skipping", entry.ZoneID, recordID))
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
			logger.Warn(fmt.Sprintf("[IONOS:%s/%s] record not found in zone - skipping", entry.ZoneID, recordID))
			continue
		}

		if existing.Type == nil || *existing.Type != ionosapi.A {
			recordType := "unknown"
			if existing.Type != nil {
				recordType = string(*existing.Type)
			}
			logger.Warn(fmt.Sprintf("[IONOS:%s/%s] record type is %s, not A - skipping", entry.ZoneID, recordID, recordType))
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

		logger.Info(fmt.Sprintf("[IONOS:%s/%s] current name=%q content=%s -> new content=%s", entry.ZoneID, recordID, recordName, currentContent, newIP))

		if currentContent == newIP {
			logger.Info(fmt.Sprintf("[IONOS:%s/%s] content already matches new IP %s - skipping update", entry.ZoneID, recordID, newIP))
			continue
		}

		newContent := newIP
		updateBody := ionosapi.UpdateRecordJSONRequestBody{
			Content:  &newContent,
			Disabled: existing.Disabled,
			Prio:     existing.Prio,
			Ttl:      existing.Ttl,
		}

		if _, err := client.Records.Update(ctx, entry.ZoneID, recordID, updateBody); err != nil {
			return fmt.Errorf("update ionos record %q: %w", recordID, err)
		}

		logger.Info(fmt.Sprintf("[IONOS:%s/%s] success: updated to %s", entry.ZoneID, recordID, newIP))
	}

	return nil
}
