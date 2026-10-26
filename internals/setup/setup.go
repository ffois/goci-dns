// Package setup implements the interactive `goci-dns setup` wizard that
// creates a config.ini by asking for credentials and letting the user pick
// zones and A records straight from the provider APIs.
package setup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"goci-dns/internals/logger"
	"goci-dns/internals/provider"
	"goci-dns/internals/provider/cf"
	"goci-dns/internals/provider/ionos"
	"goci-dns/internals/utils"

	"github.com/charmbracelet/huh"
)

// apiTimeout bounds every API call made by the wizard.
const apiTimeout = 30 * time.Second

// ErrAborted is returned when the user cancels the wizard.
var ErrAborted = errors.New("setup aborted")

// Run walks the user through creating a config file and writes it.
// defaultPath is proposed as the output path.
func Run(defaultPath string) error {
	logger.Debug("[setup] started", "default_path", defaultPath)

	cfg := &utils.Config{}
	cfg.GoCI.IPDiscoveryURL = utils.DefaultIPDiscoveryURL
	cfg.IONOS.IONOSApiUrl = utils.DefaultIONOSApiURL

	err := askGeneral(cfg)
	if err == nil && cfg.GoCI.EnableCloudflare {
		err = askCloudflare(cfg)
	}
	if err == nil && cfg.GoCI.EnableIONOS {
		err = askIONOS(cfg)
	}
	if err != nil {
		return wrapAbort(err)
	}

	if valid, err := utils.ValidateConfig(cfg); !valid {
		logger.Debug("[setup] generated config is invalid", "error", err.Error())
		return fmt.Errorf("generated config is invalid: %w", err)
	}

	path, err := askOutputPath(defaultPath)
	if err != nil {
		return wrapAbort(err)
	}
	if err := writeConfig(cfg, path); err != nil {
		logger.Debug("[setup] write failed", "path", path, "error", err.Error())
		return err
	}
	logger.Debug("[setup] config written", "path", path)

	fmt.Printf("\nConfig written to %s\n\nRun goci-dns with:\n  goci-dns %s\n", path, path)
	return nil
}

func wrapAbort(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		logger.Debug("[setup] aborted by user")
		return ErrAborted
	}
	logger.Debug("[setup] failed", "error", err.Error())
	return err
}

// ---------------------------------------------------------------------------
// [GoCI]
// ---------------------------------------------------------------------------

func askGeneral(cfg *utils.Config) error {
	ipSource := "dns"
	mode := "daemon"
	minutes := "5"
	var providers []string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("goci-dns setup").
				Description("This wizard creates a config.ini.\nSee docs/SETUP.md for how to get API tokens and keys.\nPress Ctrl+C at any time to abort."),
			huh.NewSelect[string]().
				Title("How should goci-dns find your public IP?").
				Options(
					huh.NewOption("Resolve a hostname via DNS (DOMAIN_TO_RESOLVE)", "dns"),
					huh.NewOption("Ask an HTTP service such as api.ipify.org (USE_IP_DISCOVERY)", "http"),
				).
				Value(&ipSource),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Hostname to resolve").
				Description("Must point at your current public IP (e.g. your router's DDNS name).\nIt must NOT be one of the records goci-dns updates.").
				Value(&cfg.GoCI.DomainToResolve).
				Validate(notEmpty),
		).WithHideFunc(func() bool { return ipSource != "dns" }),
		huh.NewGroup(
			huh.NewInput().
				Title("IP discovery URL").
				Description("HTTP(S) service that returns your public IP as plain text.").
				Value(&cfg.GoCI.IPDiscoveryURL).
				Validate(httpURL),
		).WithHideFunc(func() bool { return ipSource != "http" }),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Run mode").
				Options(
					huh.NewOption("Daemon: keep running, check every N minutes (systemd service, Docker)", "daemon"),
					huh.NewOption("One-shot: check once and exit (cron, systemd timer)", "oneshot"),
				).
				Value(&mode),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Check interval (minutes)").
				Value(&minutes).
				Validate(positiveInt),
		).WithHideFunc(func() bool { return mode != "daemon" }),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("DNS providers to update").
				Description("Space to toggle, Enter to confirm.").
				Options(
					huh.NewOption("Cloudflare", "cloudflare"),
					huh.NewOption("IONOS", "ionos"),
				).
				Value(&providers).
				Validate(func(v []string) error {
					if len(v) == 0 {
						return errors.New("select at least one provider")
					}
					return nil
				}),
		),
	)
	if err := form.Run(); err != nil {
		return err
	}

	cfg.GoCI.UseIPDiscovery = ipSource == "http"
	if cfg.GoCI.UseIPDiscovery {
		cfg.GoCI.DomainToResolve = ""
	}
	if mode == "daemon" {
		cfg.GoCI.TTLMinutesCheckDNSEntries, _ = strconv.Atoi(strings.TrimSpace(minutes))
	}
	for _, p := range providers {
		switch p {
		case "cloudflare":
			cfg.GoCI.EnableCloudflare = true
		case "ionos":
			cfg.GoCI.EnableIONOS = true
		}
	}
	logger.Debug("[setup] general answers",
		"ip_source", ipSource,
		"domain_to_resolve", cfg.GoCI.DomainToResolve,
		"ip_discovery_url", cfg.GoCI.IPDiscoveryURL,
		"mode", mode,
		"ttl_minutes", cfg.GoCI.TTLMinutesCheckDNSEntries,
		"providers", providers,
	)
	return nil
}

// ---------------------------------------------------------------------------
// [CF]
// ---------------------------------------------------------------------------

func askCloudflare(cfg *utils.Config) error {
	z, err := askZones(zoneSource{
		name:     "Cloudflare",
		credName: "API token",
		askCredential: func(round int) (string, error) {
			var token string
			err := huh.NewInput().
				Title("Cloudflare API token").
				Description("Needs Zone → DNS → Edit on the zone(s) (template \"Edit zone DNS\").\nDashboard → My Profile → API Tokens → Create Token.").
				EchoMode(huh.EchoModePassword).
				Value(&token).
				Validate(notEmpty).
				Run()
			return token, err
		},
		listZones: cf.ListZones,
		listRecords: func(ctx context.Context, token, zoneID string) ([]provider.Record, error) {
			return cf.ListARecords(ctx, token, zoneID)
		},
	})
	if err != nil {
		return err
	}
	cfg.CF.CFToken = z.defaultCred
	cfg.CF.CFAccountID = z.accountID
	cfg.CF.CFZones = strings.Join(z.zones, ";")
	cfg.CF.CFZoneTokens = strings.Join(z.overrides, ";")
	return nil
}

// ---------------------------------------------------------------------------
// [IONOS]
// ---------------------------------------------------------------------------

func askIONOS(cfg *utils.Config) error {
	z, err := askZones(zoneSource{
		name:     "IONOS",
		credName: "API key",
		askCredential: func(round int) (string, error) {
			var key string
			fields := []huh.Field{
				huh.NewInput().
					Title("IONOS API key").
					Description("Format: <public prefix>.<secret>.\nCreate it at https://developer.hosting.ionos.com (\"Manage API keys\").").
					EchoMode(huh.EchoModePassword).
					Value(&key).
					Validate(notEmpty),
			}
			if round == 0 {
				// The API URL is shared by every IONOS zone: ask it once.
				fields = append(fields, huh.NewInput().
					Title("IONOS DNS API URL").
					Description("Leave the default unless you know you need another endpoint.").
					Value(&cfg.IONOS.IONOSApiUrl).
					Validate(httpURL))
			}
			err := huh.NewForm(huh.NewGroup(fields...)).Run()
			cfg.IONOS.IONOSApiUrl = strings.TrimSpace(cfg.IONOS.IONOSApiUrl)
			return key, err
		},
		listZones: func(ctx context.Context, key string) ([]provider.Zone, error) {
			return ionos.ListZones(ctx, cfg.IONOS.IONOSApiUrl, key)
		},
		listRecords: func(ctx context.Context, key, zoneID string) ([]provider.Record, error) {
			return ionos.ListARecords(ctx, cfg.IONOS.IONOSApiUrl, key, zoneID)
		},
	})
	if err != nil {
		return err
	}
	cfg.IONOS.IONOSApiKey = z.defaultCred
	cfg.IONOS.IONOSZones = strings.Join(z.zones, ";")
	cfg.IONOS.IONOSZoneApiKeys = strings.Join(z.overrides, ";")
	return nil
}

// ---------------------------------------------------------------------------
// Shared steps
// ---------------------------------------------------------------------------

// zoneSource describes how the wizard talks to one provider.
type zoneSource struct {
	name     string // e.g. "Cloudflare"
	credName string // e.g. "API token"
	// askCredential asks for a token/key; round is 0 for the first one.
	askCredential func(round int) (string, error)
	listZones     func(ctx context.Context, cred string) ([]provider.Zone, error)
	listRecords   func(ctx context.Context, cred, zoneID string) ([]provider.Record, error)
}

// zoneAnswers holds the config values collected by askZones.
type zoneAnswers struct {
	defaultCred string   // first credential, the provider default
	accountID   string   // account of the first zone, if known
	zones       []string // "zone:rec1,rec2" entries
	overrides   []string // "zone:credential" entries for later credentials
}

// askZones asks for a credential, lets the user pick zones and records, and
// repeats while the user wants to add zones with another credential.
func askZones(src zoneSource) (zoneAnswers, error) {
	var ans zoneAnswers
	seen := make(map[string]bool)
	for round := 0; ; round++ {
		var cred string
		zones, err := pickZones(src.name,
			func() error {
				c, err := src.askCredential(round)
				cred = strings.TrimSpace(c)
				return err
			},
			func(ctx context.Context) ([]provider.Zone, error) { return src.listZones(ctx, cred) },
		)
		if err != nil {
			return ans, err
		}
		if round == 0 {
			ans.defaultCred = cred
			ans.accountID = zones[0].AccountID
		}

		for _, zone := range zones {
			if seen[zone.ID] {
				fmt.Printf("%s is already configured – skipping\n", zone.Name)
				continue
			}
			seen[zone.ID] = true
			ids, err := pickRecords(src.name, zone, func(ctx context.Context) ([]provider.Record, error) {
				return src.listRecords(ctx, cred, zone.ID)
			})
			if err != nil {
				return ans, err
			}
			ans.zones = append(ans.zones, zone.ID+":"+strings.Join(ids, ","))
			if round > 0 {
				ans.overrides = append(ans.overrides, zone.ID+":"+cred)
			}
		}

		more := false
		if err := huh.NewConfirm().
			Title(fmt.Sprintf("Add %s zones that need another %s?", src.name, src.credName)).
			Description(fmt.Sprintf("For example zones in another %s account.", src.name)).
			Affirmative("Yes").
			Negative("No, continue").
			Value(&more).
			Run(); err != nil {
			return ans, err
		}
		logger.Debug("[setup] zones collected", "provider", src.name, "round", round, "zones", len(ans.zones), "more", more)
		if !more {
			return ans, nil
		}
	}
}

// pickZones asks for credentials, lists the zones they can see and lets the
// user choose one or more. If listing fails the user can retry with other
// credentials or type the Zone IDs by hand.
func pickZones(name string, askCredentials func() error, list func(context.Context) ([]provider.Zone, error)) ([]provider.Zone, error) {
	for {
		if err := askCredentials(); err != nil {
			return nil, err
		}

		fmt.Printf("Fetching %s zones…\n", name)
		logger.Debug("[setup] listing zones", "provider", name)
		zones, err := withTimeout(list)
		if err == nil && len(zones) > 0 {
			return selectZones(name, zones)
		}

		problem := "no zones are visible with these credentials"
		if err != nil {
			problem = err.Error()
		}
		action, aerr := askFailure(fmt.Sprintf("Could not list %s zones", name), problem, true)
		if aerr != nil {
			return nil, aerr
		}
		logger.Debug("[setup] zone listing failed", "provider", name, "problem", problem, "action", action)
		switch action {
		case "retry":
			continue
		case "manual":
			var raw string
			if err := huh.NewInput().
				Title(name + " Zone ID(s)").
				Description("Separate several IDs with commas.").
				Value(&raw).
				Validate(notEmpty).
				Run(); err != nil {
				return nil, err
			}
			var manual []provider.Zone
			for _, id := range strings.Split(raw, ",") {
				if id = strings.TrimSpace(id); id != "" {
					manual = append(manual, provider.Zone{ID: id, Name: id})
				}
			}
			logger.Debug("[setup] zones entered manually", "provider", name, "zones", manual)
			return manual, nil
		default:
			return nil, huh.ErrUserAborted
		}
	}
}

func selectZones(name string, zones []provider.Zone) ([]provider.Zone, error) {
	if len(zones) == 1 {
		fmt.Printf("Using the only %s zone: %s\n", name, zones[0].Name)
		logger.Debug("[setup] zone selected automatically", "provider", name, "zone", zones[0])
		return zones, nil
	}

	options := make([]huh.Option[int], len(zones))
	for i, z := range zones {
		options[i] = huh.NewOption(fmt.Sprintf("%s  (%s)", z.Name, z.ID), i)
	}
	var picked []int
	if err := huh.NewMultiSelect[int]().
		Title(name + " zones to keep updated").
		Description("Space to toggle, / to filter, Enter to confirm.").
		Options(options...).
		Filterable(true).
		Value(&picked).
		Validate(func(v []int) error {
			if len(v) == 0 {
				return errors.New("select at least one zone")
			}
			return nil
		}).
		Run(); err != nil {
		return nil, err
	}
	selected := make([]provider.Zone, len(picked))
	for i, idx := range picked {
		selected[i] = zones[idx]
	}
	logger.Debug("[setup] zones selected", "provider", name, "zones", selected)
	return selected, nil
}

// pickRecords lists the A records of zone and lets the user choose which
// ones goci-dns should keep updated.
func pickRecords(name string, zone provider.Zone, list func(context.Context) ([]provider.Record, error)) ([]string, error) {
	for {
		fmt.Printf("Fetching A records of %s…\n", zone.Name)
		logger.Debug("[setup] listing A records", "provider", name, "zone_id", zone.ID)
		records, err := withTimeout(list)
		if err == nil && len(records) > 0 {
			return selectRecords(name, records)
		}

		problem := "the zone has no A records: create the record(s) in the " + name + " dashboard first, then retry"
		if err != nil {
			problem = err.Error()
		}
		action, aerr := askFailure(fmt.Sprintf("Could not list %s records", name), problem, false)
		if aerr != nil {
			return nil, aerr
		}
		logger.Debug("[setup] record listing failed", "provider", name, "zone_id", zone.ID, "problem", problem, "action", action)
		if action != "retry" {
			return nil, huh.ErrUserAborted
		}
	}
}

func selectRecords(name string, records []provider.Record) ([]string, error) {
	options := make([]huh.Option[string], len(records))
	for i, r := range records {
		options[i] = huh.NewOption(fmt.Sprintf("%s → %s  (%s)", r.Name, r.Content, r.ID), r.ID)
	}
	var ids []string
	err := huh.NewMultiSelect[string]().
		Title(name + " A records to keep updated").
		Description("Space to toggle, / to filter, Enter to confirm.").
		Options(options...).
		Filterable(true).
		Value(&ids).
		Validate(func(v []string) error {
			if len(v) == 0 {
				return errors.New("select at least one record")
			}
			return nil
		}).
		Run()
	if err == nil {
		logger.Debug("[setup] records selected", "provider", name, "record_ids", ids)
	}
	return ids, err
}

// askFailure explains what went wrong and asks how to continue.
// It returns "retry", "manual" (only if allowManual) or "abort".
func askFailure(title, problem string, allowManual bool) (string, error) {
	options := []huh.Option[string]{huh.NewOption("Try again", "retry")}
	if allowManual {
		options = append(options, huh.NewOption("Enter the Zone ID(s) manually", "manual"))
	}
	options = append(options, huh.NewOption("Abort setup", "abort"))

	action := "retry"
	err := huh.NewSelect[string]().
		Title(title).
		Description(problem).
		Options(options...).
		Value(&action).
		Run()
	return action, err
}

func withTimeout[T any](fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()
	return fn(ctx)
}

// ---------------------------------------------------------------------------
// Output
// ---------------------------------------------------------------------------

func askOutputPath(defaultPath string) (string, error) {
	path := defaultPath
	if err := huh.NewInput().
		Title("Where should the config be written?").
		Value(&path).
		Validate(notEmpty).
		Run(); err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)

	if _, err := os.Stat(path); err == nil {
		overwrite := false
		if err := huh.NewConfirm().
			Title(path + " already exists. Overwrite it?").
			Affirmative("Overwrite").
			Negative("Cancel").
			Value(&overwrite).
			Run(); err != nil {
			return "", err
		}
		logger.Debug("[setup] output file exists", "path", path, "overwrite", overwrite)
		if !overwrite {
			return "", huh.ErrUserAborted
		}
	}
	return path, nil
}

// ---------------------------------------------------------------------------
// Validators
// ---------------------------------------------------------------------------

func notEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	return nil
}

func positiveInt(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return errors.New("enter a whole number greater than 0")
	}
	return nil
}

func httpURL(s string) error {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("enter an http(s) URL")
	}
	return nil
}
