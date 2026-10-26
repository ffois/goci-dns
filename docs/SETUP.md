# Setup guide

This guide explains how to get everything `config.ini` needs:

- a **Cloudflare API token**, **Zone ID(s)** and **DNS record IDs**
- an **IONOS API key**, **Zone ID(s)** and **DNS record IDs**

You only need the sections for the providers you enable.

> **Note:** goci-dns only updates existing **A records**. Create the record(s) in your provider's dashboard first, for example `home.example.org → 1.2.3.4`. Any IP will do, goci-dns overwrites it on its first run.

## Option 1: the setup wizard (recommended)

The wizard asks for your token or API key, lists your zones and A records, lets you pick the ones to keep updated (one or more zones), and writes `config.ini`. When it asks *"Add zones that need another API token/key?"*, answer yes to add zones from another account: their credential is saved as a per-zone override.

```bash
./goci-dns setup                 # writes ./config.ini
./goci-dns setup /etc/goci-dns/config.ini
```

Controls: `↑`/`↓` to move, `space` or `x` to toggle an item in a list, `/` to filter, `enter` to confirm, `shift+tab` to go back, `ctrl+c` to abort.

The file is written with `0600` permissions because it contains credentials.

You still need a token or API key first: see [Cloudflare API token](#1-create-an-api-token) or [IONOS API key](#1-create-an-api-key) below. The wizard finds the IDs for you.

> The wizard needs an interactive terminal. With Docker, run the binary on your machine (or in a throwaway `golang` container) to generate `config.ini`, then mount the file as described in the README.

## Option 2: by hand

### Cloudflare

#### 1. Create an API token

1. Open the Cloudflare dashboard → **My Profile** → **API Tokens** (<https://dash.cloudflare.com/profile/api-tokens>).
2. Click **Create Token** and pick the **Edit zone DNS** template.
3. Under **Zone Resources**, select the zone(s) whose records goci-dns should update: **Specific zone** for one, add more rows for several, or **All zones from an account**. Zones in another account need another token, set in `CF_ZONE_TOKENS`.
4. Optionally restrict it with **Client IP Address Filtering** and a **TTL**.
5. Click **Continue to summary** → **Create Token** and copy the token. **It is shown only once.**

Check that it works:

```bash
curl -s https://api.cloudflare.com/client/v4/user/tokens/verify \
  -H "Authorization: Bearer $CF_TOKEN"
# → "status": "active"
```

Put it in `CF_TOKEN`.

> Use an API **token**, not the Global API Key. A token is scoped to the zones you pick and can only edit DNS.

#### 2. Find the Zone ID (and Account ID)

1. In the dashboard, open **Domains** and select your domain.
2. On its **Overview** page, scroll down to the **API** section.
3. Copy the **Zone ID**: it goes into `CF_ZONES` (step 3). The **Account ID** shown next to it goes into `CF_ACCOUNT_ID` (optional).

Or, with the token:

```bash
curl -s "https://api.cloudflare.com/client/v4/zones" \
  -H "Authorization: Bearer $CF_TOKEN" | jq -r '.result[] | "\(.id)  \(.name)"'
```

#### 3. Find the DNS record IDs

The dashboard does not show record IDs, so ask the API for the zone's A records:

```bash
curl -s "https://api.cloudflare.com/client/v4/zones/$ZONE_ID/dns_records?type=A&per_page=100" \
  -H "Authorization: Bearer $CF_TOKEN" | jq -r '.result[] | "\(.id)  \(.name) → \(.content)"'
```

Example output:

```
372e67954025e0ba6aaa6d586b9e0b59  home.example.org → 198.51.100.1
9a7806061c88ada191ed06f989cc3dac  vpn.example.org → 198.51.100.1
```

Put the zone and its record IDs in `CF_ZONES` as `<zone id>:<record id>,<record id>`, and separate zones with `;` (no space before it). Repeat the command for each zone:

```ini
CF_ZONES = 023e105f4ecef8ad9ca31a8372d0c353:372e67954025e0ba6aaa6d586b9e0b59,9a7806061c88ada191ed06f989cc3dac;5b6c9e1f0a7d4e2b8c3f6a1d9e0b7c42:0f1e2d3c4b5a69788796a5b4c3d2e1f0
# only for zones that need a different token:
CF_ZONE_TOKENS = 5b6c9e1f0a7d4e2b8c3f6a1d9e0b7c42:another-token
```

### IONOS

This uses the IONOS **Hosting** DNS API (`api.hosting.ionos.com`), for domains whose DNS is managed by IONOS. It is not the IONOS **Cloud** DNS API, which uses different credentials.

#### 1. Create an API key

1. Sign in at the IONOS developer portal: <https://developer.hosting.ionos.com>.
2. Follow **Getting started** (<https://developer.hosting.ionos.com/docs/getstarted>) to create an API key.
3. The key has two parts, a **public prefix** and a **secret**. **The secret is shown only once.**

goci-dns expects them joined with a dot:

```ini
IONOS_API_KEY = <public prefix>.<secret>
```

Leave `IONOS_API_URL` at its default `https://api.hosting.ionos.com/dns`.

#### 2. Find the Zone ID

```bash
curl -s https://api.hosting.ionos.com/dns/v1/zones \
  -H "X-API-Key: $IONOS_API_KEY" | jq -r '.[] | "\(.id)  \(.name)"'
```

Note the ID of each domain to update.

#### 3. Find the DNS record IDs

```bash
curl -s "https://api.hosting.ionos.com/dns/v1/zones/$ZONE_ID?recordType=A" \
  -H "X-API-Key: $IONOS_API_KEY" | jq -r '.records[] | "\(.id)  \(.name) → \(.content)"'
```

Put them in `IONOS_ZONES`, same format as `CF_ZONES`:

```ini
IONOS_ZONES = <zone id>:<record id>,<record id>;<zone id>:<record id>
# only for zones that need a different API key:
IONOS_ZONE_API_KEYS = <zone id>:<prefix>.<secret>
```

> Configs that use the old `CF_ZONE_ID` + `CF_DNS_ENTRIES_ID` (or `IONOS_ZONE_ID` + `IONOS_DNS_ENTRIES_ID`) keep working: that zone is added to the list.

## How goci-dns finds your public IP

Pick one method in `[GoCI]`:

- **DNS lookup (default):** set `DOMAIN_TO_RESOLVE` to a hostname that already points at your current public IP, such as a DDNS name kept up to date by your router. Do **not** use one of the records goci-dns updates: it would never notice an IP change.
- **HTTP discovery (opt-in):** set `USE_IP_DISCOVERY = true`. goci-dns asks `IP_DISCOVERY_URL` (default `https://api.ipify.org`) for your public IPv4 and ignores `DOMAIN_TO_RESOLVE`.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `record not found in zone – skipping` | Wrong record ID, or the ID belongs to another zone. List the records again (step 3). |
| `record type is AAAA, not A – skipping` | The ID points to a non-A record. Only A records are supported. |
| Cloudflare `401`/`403` | Token expired or revoked, or not scoped to this zone. Recreate it with the **Edit zone DNS** template. |
| Cloudflare `7003 Could not route` | A zone ID in `CF_ZONES` is wrong. |
| `remove the space before ';'` | ` ;` starts an ini comment. Write `zone1:rec1;zone2:rec2`. |
| `expected <zone id>:<record id>...` | An entry of `CF_ZONES`/`IONOS_ZONES` has no `:`. |
| `zone X is not configured` | `CF_ZONE_TOKENS`/`IONOS_ZONE_API_KEYS` names a zone missing from `CF_ZONES`/`IONOS_ZONES`. |
| `zone X: …` errors, other zones updated | That zone failed (wrong ID, or the token cannot access it); the others were still updated. |
| IONOS `401` | `IONOS_API_KEY` must be `<prefix>.<secret>`, joined with a dot. |
| Wizard: "no zones are visible" | The credentials cannot list zones. Choose **Enter the Zone ID(s) manually** and paste them from the dashboard. |

### Debug logs

Set `LOG_LEVEL=DEBUG` at runtime, or build a binary that defaults to DEBUG:

```bash
go build -ldflags "-X goci-dns/internals/logger.DefaultLevel=DEBUG" -o build/goci-dns-debug .
./build/goci-dns-debug setup /tmp/test.ini
```

The wizard logs only to the file (`<user config dir>/goci-dns/logs/app.log`, e.g. `~/Library/Application Support/goci-dns/logs/app.log` on macOS, `~/.config/goci-dns/logs/app.log` on Linux) and prints its path at start. Tokens and API keys are never logged.
