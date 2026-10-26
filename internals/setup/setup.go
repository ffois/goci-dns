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
	zone, err := pickZone("Cloudflare",
		func() error {
			return huh.NewInput().
				Title("Cloudflare API token").
				Description("Needs Zone → DNS → Edit on the zone (template \"Edit zone DNS\").\nDashboard → My Profile → API Tokens → Create Token.").
				EchoMode(huh.EchoModePassword).
				Value(&cfg.CF.CFToken).
				Validate(notEmpty).
				Run()
		},
		func(ctx context.Context) ([]provider.Zone, error) {
			return cf.ListZones(ctx, strings.TrimSpace(cfg.CF.CFToken))
		},
	)
	if err != nil {
		return err
	}
	cfg.CF.CFToken = strings.TrimSpace(cfg.CF.CFToken)
	cfg.CF.CFZoneID = zone.ID
	cfg.CF.CFAccountID = zone.AccountID

	ids, err := pickRecords("Cloudflare", zone, func(ctx context.Context) ([]provider.Record, error) {
		return cf.ListARecords(ctx, cfg.CF.CFToken, zone.ID)
	})
	if err != nil {
		return err
	}
	cfg.CF.CFDNSEntryIDs = ids
	cfg.CF.CFDNSEntries = strings.Join(ids, ",")
	return nil
}

// ---------------------------------------------------------------------------
// [IONOS]
// ---------------------------------------------------------------------------

func askIONOS(cfg *utils.Config) error {
	zone, err := pickZone("IONOS",
		func() error {
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("IONOS API key").
					Description("Format: <public prefix>.<secret>.\nCreate it at https://developer.hosting.ionos.com (\"Manage API keys\").").
					EchoMode(huh.EchoModePassword).
					Value(&cfg.IONOS.IONOSApiKey).
					Validate(notEmpty),
				huh.NewInput().
					Title("IONOS DNS API URL").
					Description("Leave the default unless you know you need another endpoint.").
					Value(&cfg.IONOS.IONOSApiUrl).
					Validate(httpURL),
			)).Run()
		},
		func(ctx context.Context) ([]provider.Zone, error) {
			return ionos.ListZones(ctx, strings.TrimSpace(cfg.IONOS.IONOSApiUrl), strings.TrimSpace(cfg.IONOS.IONOSApiKey))
		},
	)
	if err != nil {
		return err
	}
	cfg.IONOS.IONOSApiKey = strings.TrimSpace(cfg.IONOS.IONOSApiKey)
	cfg.IONOS.IONOSApiUrl = strings.TrimSpace(cfg.IONOS.IONOSApiUrl)
	cfg.IONOS.IONOSZoneID = zone.ID

	ids, err := pickRecords("IONOS", zone, func(ctx context.Context) ([]provider.Record, error) {
		return ionos.ListARecords(ctx, cfg.IONOS.IONOSApiUrl, cfg.IONOS.IONOSApiKey, zone.ID)
	})
	if err != nil {
		return err
	}
	cfg.IONOS.IONOSEntryIDs = ids
	cfg.IONOS.IONOSEntries = strings.Join(ids, ",")
	return nil
}

// ---------------------------------------------------------------------------
// Shared steps
// ---------------------------------------------------------------------------

// pickZone asks for credentials, lists the zones they can see and lets the
// user choose one. If listing fails the user can retry with other credentials
// or type the Zone ID by hand.
func pickZone(name string, askCredentials func() error, list func(context.Context) ([]provider.Zone, error)) (provider.Zone, error) {
	for {
		if err := askCredentials(); err != nil {
			return provider.Zone{}, err
		}

		fmt.Printf("Fetching %s zones…\n", name)
		logger.Debug("[setup] listing zones", "provider", name)
		zones, err := withTimeout(list)
		if err == nil && len(zones) > 0 {
			return selectZone(name, zones)
		}

		problem := "no zones are visible with these credentials"
		if err != nil {
			problem = err.Error()
		}
		action, aerr := askFailure(fmt.Sprintf("Could not list %s zones", name), problem, true)
		if aerr != nil {
			return provider.Zone{}, aerr
		}
		logger.Debug("[setup] zone listing failed", "provider", name, "problem", problem, "action", action)
		switch action {
		case "retry":
			continue
		case "manual":
			var id string
			if err := huh.NewInput().
				Title(name + " Zone ID").
				Value(&id).
				Validate(notEmpty).
				Run(); err != nil {
				return provider.Zone{}, err
			}
			id = strings.TrimSpace(id)
			logger.Debug("[setup] zone entered manually", "provider", name, "zone_id", id)
			return provider.Zone{ID: id, Name: id}, nil
		default:
			return provider.Zone{}, huh.ErrUserAborted
		}
	}
}

func selectZone(name string, zones []provider.Zone) (provider.Zone, error) {
	if len(zones) == 1 {
		fmt.Printf("Using the only %s zone: %s\n", name, zones[0].Name)
		logger.Debug("[setup] zone selected automatically", "provider", name, "zone", zones[0])
		return zones[0], nil
	}

	options := make([]huh.Option[int], len(zones))
	for i, z := range zones {
		options[i] = huh.NewOption(fmt.Sprintf("%s  (%s)", z.Name, z.ID), i)
	}
	var idx int
	if err := huh.NewSelect[int]().
		Title(name + " zone").
		Options(options...).
		Value(&idx).
		Run(); err != nil {
		return provider.Zone{}, err
	}
	logger.Debug("[setup] zone selected", "provider", name, "zone", zones[idx])
	return zones[idx], nil
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
		options = append(options, huh.NewOption("Enter the Zone ID manually", "manual"))
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
