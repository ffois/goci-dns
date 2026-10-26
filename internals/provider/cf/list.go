package cf

import (
	"context"
	"fmt"

	"goci-dns/internals/provider"

	cloudflare "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/zones"
)

func newClient(token string) *cloudflare.Client {
	return cloudflare.NewClient(
		option.WithAPIToken(token),
		option.WithRequestTimeout(requestTimeout),
	)
}

// ListZones returns every zone the token can see.
func ListZones(ctx context.Context, token string) ([]provider.Zone, error) {
	var out []provider.Zone
	iter := newClient(token).Zones.ListAutoPaging(ctx, zones.ZoneListParams{})
	for iter.Next() {
		z := iter.Current()
		out = append(out, provider.Zone{ID: z.ID, Name: z.Name, AccountID: z.Account.ID})
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	return out, nil
}

// ListARecords returns every A record in the zone.
func ListARecords(ctx context.Context, token, zoneID string) ([]provider.Record, error) {
	var out []provider.Record
	iter := newClient(token).DNS.Records.ListAutoPaging(ctx, dns.RecordListParams{
		ZoneID: cloudflare.F(zoneID),
		Type:   cloudflare.F(dns.RecordListParamsTypeA),
	})
	for iter.Next() {
		r := iter.Current()
		if r.Type != dns.RecordResponseTypeA {
			continue
		}
		out = append(out, provider.Record{ID: r.ID, Name: r.Name, Content: r.Content})
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}
	return out, nil
}
