# goci-dns

`goci-dns` is a small Go service that keeps Cloudflare A records in sync with your current public IP.

It resolves a configured hostname, compares the result with the last known IP in `ip_state.json`, and updates one or more Cloudflare DNS records when needed.

## Features

- Supports one-shot mode (for cron/systemd timer usage)
- Supports daemon mode (periodic checks in a long-running service)
- Can force updates even if IP has not changed
- Can optionally push a fixed override IP instead of the resolved one
- Updates multiple Cloudflare DNS record IDs in one run

## Requirements

- Go 1.26+ (if building from source)
- A Cloudflare API token with **Zone - DNS - Edit** permission
- Cloudflare Zone ID and DNS Record ID(s)

## Configuration

Edit `config.ini`:

- `TTL_MINUTES_CHECK_DNS_ENTRIES`
  - `0` = one-shot mode (run once and exit)
  - `>0` = daemon mode (run every N minutes)
- `IGNORE_OLD_IP` = force update even if IP is unchanged
- `DOMAIN_TO_RESOLVE` = hostname to resolve for your current public IP
- `OVERRIDE_NEW_IP` / `NEW_IP` = optional static IP override
- `CF_TOKEN` = Cloudflare API token
- `CF_ZONE_ID` = Cloudflare zone ID
- `CF_DNS_ENTRIES_ID` = semicolon-separated record IDs (for example `id1;id2`)

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

## License

See [LICENSE](LICENSE).
