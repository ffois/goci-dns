// Package provider holds the types shared by the DNS provider packages.
package provider

// Zone is a DNS zone as exposed by a provider.
type Zone struct {
	ID        string
	Name      string
	AccountID string // Cloudflare only
}

// Record is an A record as exposed by a provider.
type Record struct {
	ID      string
	Name    string
	Content string
}
