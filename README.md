# goci-dns

`goci-dns` is a small Go service that keeps Cloudflare and IONOS A records in sync with your current public IP.

It resolves a configured hostname, compares the result with the last known IP in `ip_state.json`, and updates one or more Cloudflare and/or IONOS DNS records when needed.

## Features

- Supports one-shot mode (for cron/systemd timer usage)
- Supports daemon mode (periodic checks in a long-running service)
- Can force updates even if IP has not changed
- Can optionally push a fixed override IP instead of the resolved one
- Updates multiple Cloudflare DNS record IDs in one run
- Updates multiple IONOS DNS record IDs in one run
- Cloudflare and IONOS can be enabled independently or together

## Requirements

- Go 1.26+ (if building from source)
- At least one provider enabled (Cloudflare, IONOS, or both)
- For Cloudflare: an API token with **Zone - DNS - Edit** permission, plus the Zone ID and DNS Record ID(s)
- For IONOS: an API key with DNS edit permission, plus the Zone ID and DNS Record ID(s)

## Configuration

Copy `config.example.ini` to `config.ini` and edit it. The file is split into sections.

### `[GoCI]` (general)

- `TTL_MINUTES_CHECK_DNS_ENTRIES`
  - `0` = one-shot mode (run once and exit)
  - `>0` = daemon mode (run every N minutes)
- `IGNORE_OLD_IP` = force update even if IP is unchanged
- `DOMAIN_TO_RESOLVE` = hostname to resolve for your current public IP
- `OVERRIDE_NEW_IP` / `NEW_IP` = optional static IP override
- `ENABLE_CLOUDFLARE` = enable Cloudflare DNS updates (`true`/`false`)
- `ENABLE_IONOS` = enable IONOS DNS updates (`true`/`false`)

At least one provider must be enabled.

### `[CF]` (Cloudflare)

- `CF_TOKEN` = Cloudflare API token
- `CF_ZONE_ID` = Cloudflare zone ID
- `CF_ACCOUNT_ID` = Cloudflare account ID (reserved for future use)
- `CF_DNS_ENTRIES_ID` = comma-separated record IDs (for example `id1,id2`)

### `[IONOS]` (IONOS)

- `IONOS_API_URL` = IONOS DNS API base URL (default `https://api.hosting.ionos.com/dns`)
- `IONOS_API_KEY` = IONOS API key with DNS edit permission
- `IONOS_ZONE_ID` = IONOS zone ID
- `IONOS_DNS_ENTRIES_ID` = comma-separated record IDs (for example `id1,id2`)

## Build

```bash
go build -o goci-dns .
```

## Run

With default paths:

```bash
./goci-dns
```

With explicit config and state paths:

```bash
./goci-dns /etc/goci-dns/config.ini /etc/goci-dns/ip_state.json
```

## Scheduling options

### Cron / one-shot mode

Set `TTL_MINUTES_CHECK_DNS_ENTRIES = 0`, then schedule runs using `crontab.example`.

### systemd / daemon mode

Set `TTL_MINUTES_CHECK_DNS_ENTRIES > 0`, then install `goci-dns.service`.

The repository includes:

- `goci-dns.service` (service unit example)
- `crontab.example` (cron line example)

## State file

`ip_state.json` stores the last resolved IP and timestamp, so `goci-dns` can avoid unnecessary updates.

## Third-party modules and licenses

Directly used non-stdlib Go modules in this project:

- `github.com/cloudflare/cloudflare-go/v7` (`v7.6.0`) - Apache License 2.0
- `github.com/ffois/go-ionos` (`v1.0.0`) - MIT License
- `gopkg.in/ini.v1` (`v1.67.3`) - Apache License 2.0
- `gopkg.in/natefinch/lumberjack.v2` (`v2.2.1`) - MIT License

Note: versions can change over time; `go.mod` is the source of truth.

## License

See [LICENSE](LICENSE).
