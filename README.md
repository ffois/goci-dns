# goci-dns

`goci-dns` is a small Go service that keeps Cloudflare and IONOS A records in sync with your current public IP.

It resolves a configured hostname (or, opt-in, asks an HTTP service such as `api.ipify.org`), compares the result with the last known IP in `ip_state.json`, and updates one or more Cloudflare and/or IONOS DNS records when needed.

## Features

- Supports one-shot mode (for cron/systemd timer usage)
- Supports daemon mode (periodic checks in a long-running service)
- Can force updates even if IP has not changed
- Can optionally push a fixed override IP instead of the resolved one
- Updates multiple zones (domains) and record IDs per provider in one run, with optional per-zone credentials
- Cloudflare and IONOS can be enabled independently or together
- Interactive setup wizard (`goci-dns setup`) that lists your zones and records and writes `config.ini`

## Requirements

- Go 1.27+ (if building from source)
- At least one provider enabled (Cloudflare, IONOS, or both)
- For Cloudflare: an API token with **Zone - DNS - Edit** permission, plus the Zone ID(s) and DNS Record ID(s)
- For IONOS: an API key with DNS edit permission, plus the Zone ID(s) and DNS Record ID(s)

## Configuration

The quickest way is the interactive wizard, which asks for your Cloudflare token and/or IONOS API key, lets you pick zones and A records, and writes `config.ini`:

```bash
./goci-dns setup [config.ini]
```

See **[docs/SETUP.md](docs/SETUP.md)** for how to create the token/API key and how to find Zone IDs and record IDs by hand.

Alternatively, copy `config.example.ini` to `config.ini` and edit it. The file is split into sections.

### `[GoCI]` (general)

- `TTL_MINUTES_CHECK_DNS_ENTRIES`
  - `0` = one-shot mode (run once and exit)
  - `>0` = daemon mode (run every N minutes)
- `IGNORE_OLD_IP` = force update even if IP is unchanged
- `DOMAIN_TO_RESOLVE` = hostname to resolve for your current public IP (ignored when `USE_IP_DISCOVERY=true`)
- `USE_IP_DISCOVERY` = opt-in: get the public IPv4 from an HTTP service instead of resolving `DOMAIN_TO_RESOLVE` (`true`/`false`, default `false`)
- `IP_DISCOVERY_URL` = HTTP(S) service returning the bare IP as text (default `https://api.ipify.org`); the request is always made over IPv4
- `OVERRIDE_NEW_IP` / `NEW_IP` = optional static IP override
- `ENABLE_CLOUDFLARE` = enable Cloudflare DNS updates (`true`/`false`)
- `ENABLE_IONOS` = enable IONOS DNS updates (`true`/`false`)

At least one provider must be enabled.

### `[CF]` (Cloudflare)

- `CF_TOKEN` = Cloudflare API token (default for every zone)
- `CF_ZONES` = zones and their record IDs: `<zone id>:<record id>,<record id>;<zone id>:<record id>`
- `CF_ZONE_TOKENS` = optional per-zone tokens that replace `CF_TOKEN`: `<zone id>:<token>;...`
- `CF_ACCOUNT_ID` = Cloudflare account ID (optional, reserved for future use)

### `[IONOS]` (IONOS)

- `IONOS_API_URL` = IONOS DNS API base URL (default `https://api.hosting.ionos.com/dns`)
- `IONOS_API_KEY` = IONOS API key with DNS edit permission (default for every zone)
- `IONOS_ZONES` = zones and their record IDs, same format as `CF_ZONES`
- `IONOS_ZONE_API_KEYS` = optional per-zone API keys that replace `IONOS_API_KEY`: `<zone id>:<prefix>.<secret>;...`

Example with two Cloudflare zones, the second in another account:

```ini
[CF]
CF_TOKEN       = token-for-account-1
CF_ZONES       = 023e105f4ecef8ad9ca31a8372d0c353:372e67954025e0ba6aaa6d586b9e0b59,9a7806061c88ada191ed06f989cc3dac;5b6c9e1f0a7d4e2b8c3f6a1d9e0b7c42:0f1e2d3c4b5a69788796a5b4c3d2e1f0
CF_ZONE_TOKENS = 5b6c9e1f0a7d4e2b8c3f6a1d9e0b7c42:token-for-account-2
```

Do not put a space before `;`: ` ;` starts an ini comment, so goci-dns rejects it instead of silently dropping zones. A failing zone does not stop the others; the run is reported as failed and retried next time.

The old single-zone keys (`CF_ZONE_ID` + `CF_DNS_ENTRIES_ID`, `IONOS_ZONE_ID` + `IONOS_DNS_ENTRIES_ID`) still work and are added as one more zone, so existing configs need no change.

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

### Docker

The image does not contain `config.ini`: mount it at runtime. `docker-compose.yml` mounts `./config.ini` read-only at `/config/config.ini`, keeps `ip_state.json` in the `data` volume (`/data`) and the rotating log file in the `logs` volume (`/logs`):

```bash
docker compose up -d --build
```

The repository includes:

- `goci-dns.service` (service unit example)
- `crontab.example` (cron line example)

## State file

`ip_state.json` stores the last resolved IP and timestamp, so `goci-dns` can avoid unnecessary updates.

## Third-party modules and licenses

Directly used non-stdlib Go modules in this project:

- `github.com/cloudflare/cloudflare-go/v7` (`v7.13.1`) - Apache License 2.0
- `github.com/ffois/go-ionos` (`v1.0.0`) - MIT License
- `github.com/charmbracelet/huh` (`v1.0.0`) - MIT License
- `gopkg.in/ini.v1` (`v1.67.3`) - Apache License 2.0
- `gopkg.in/natefinch/lumberjack.v2` (`v2.2.1`) - MIT License

Note: versions can change over time; `go.mod` is the source of truth.

## License

See [LICENSE](LICENSE).
