package ionos

import (
	"context"
	"fmt"

	"goci-dns/internals/provider"

	ionosapi "github.com/ffois/go-ionos"
)

func newClient(apiURL, apiKey string) *ionosapi.Client {
	return ionosapi.NewClient(
		ionosapi.WithAPIKey(apiKey),
		ionosapi.WithBaseURL(apiURL),
		ionosapi.WithTimeout(requestTimeout),
	)
}

// ListZones returns every zone the API key can see.
func ListZones(ctx context.Context, apiURL, apiKey string) ([]provider.Zone, error) {
	zones, err := newClient(apiURL, apiKey).Zones.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	out := make([]provider.Zone, 0, len(zones))
	for _, z := range zones {
		out = append(out, provider.Zone{ID: deref(z.Id), Name: deref(z.Name)})
	}
	return out, nil
}

// ListARecords returns every A record in the zone.
func ListARecords(ctx context.Context, apiURL, apiKey, zoneID string) ([]provider.Record, error) {
	zone, err := newClient(apiURL, apiKey).Zones.Get(ctx, zoneID, nil)
	if err != nil {
		return nil, fmt.Errorf("get zone: %w", err)
	}
	var out []provider.Record
	if zone.Records == nil {
		return out, nil
	}
	for _, r := range *zone.Records {
		if r.Type == nil || *r.Type != ionosapi.A {
			continue
		}
		out = append(out, provider.Record{ID: deref(r.Id), Name: deref(r.Name), Content: deref(r.Content)})
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
