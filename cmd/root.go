// Package cmd contains all CLI commands for the namecom CLI.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/patramsey/namecom-cli/cmd/apicmd"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	configcmd "github.com/patramsey/namecom-cli/cmd/config"
	"github.com/patramsey/namecom-cli/cmd/dns"
	"github.com/patramsey/namecom-cli/cmd/dnssec"
	"github.com/patramsey/namecom-cli/cmd/domain"
	"github.com/patramsey/namecom-cli/cmd/email"
	"github.com/patramsey/namecom-cli/cmd/order"
	"github.com/patramsey/namecom-cli/cmd/transfer"
	urlcmd "github.com/patramsey/namecom-cli/cmd/url"
	"github.com/patramsey/namecom-cli/cmd/vanity"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/patramsey/namecom-cli/internal/update"
	"github.com/spf13/cobra"
)

// Use the shared context keys from cmdutil so subpackages can retrieve values
// without importing cmd (which would create a cycle).

// Version is set at build time via -ldflags "-X main.version=x.y.z".
var Version = "dev"

// globalFlags holds the parsed values of all root-level persistent flags.
type globalFlags struct {
	profile   string
	username  string
	token     string
	sandbox   bool
	output    string
	quiet     bool
	noHeader  bool
	color     string
	timeout   time.Duration
	debug     bool
	debugFile string
	yes       bool
	dryRun    bool
	idempKey  string
}

var gf globalFlags

// rootCmd is the top-level `namecom` command. It configures the API client and
// output renderer and stashes them on the context for every subcommand.
var rootCmd = &cobra.Command{
	Use:   "namecom",
	Short: "CLI for the name.com Core API",
	Long: `namecom — CLI for the name.com Core API

Manage domains, DNS records, email forwarding, URL redirects, transfers, and more.

Quick start:
  namecom auth login              # configure credentials
  namecom domain list             # list your domains
  namecom dns list example.com    # manage DNS records
  namecom domain register foo.com # register a new domain

Run 'namecom <command> --help' for details on any command.`,
	SilenceUsage:      true,
	SilenceErrors:     true,
	Version:           Version,
	PersistentPreRunE: persistentPreRunE,
}

// Execute is the entry point called from main.
func Execute() {
	// Start version check in background before the command runs, so there's
	// a chance the network round-trip completes by the time we're done.
	updateCh := make(chan string, 1)
	go func() { updateCh <- update.Check(Version) }()

	if err := rootCmd.Execute(); err != nil {
		cfg := output.DefaultConfig()
		cfg.Error(err)
		code := exitCode(err)
		if code == 3 {
			cfg.Hint("Run 'namecom auth status' to check your credentials, or 'namecom auth login' to reconfigure")
		}
		os.Exit(code)
	}

	// Show update notification if the goroutine finished in time.
	if output.IsStderrTTY() {
		select {
		case msg := <-updateCh:
			if msg != "" {
				fmt.Fprintln(os.Stderr, "\n"+output.DefaultConfig().Dim(msg))
			}
		default:
			// Check not done yet — don't block.
		}
	}
}

func init() {
	rootCmd.AddGroup(
		&cobra.Group{ID: "domains", Title: "Domain Commands:"},
		&cobra.Group{ID: "account", Title: "Account Commands:"},
		&cobra.Group{ID: "utilities", Title: "Utilities:"},
	)

	domain.Cmd.GroupID = "domains"
	dns.Cmd.GroupID = "domains"
	dnssec.Cmd.GroupID = "domains"
	transfer.Cmd.GroupID = "domains"
	email.Cmd.GroupID = "domains"
	urlcmd.Cmd.GroupID = "domains"
	vanity.Cmd.GroupID = "domains"

	authCmd.GroupID = "account"
	statusCmd.GroupID = "account"
	order.Cmd.GroupID = "account"
	configcmd.Cmd.GroupID = "account"

	apicmd.Cmd.GroupID = "utilities"
	versionCmd.GroupID = "utilities"

	rootCmd.AddCommand(apicmd.Cmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(configcmd.Cmd)
	rootCmd.AddCommand(domain.Cmd)
	rootCmd.AddCommand(dns.Cmd)
	rootCmd.AddCommand(dnssec.Cmd)
	rootCmd.AddCommand(email.Cmd)
	rootCmd.AddCommand(order.Cmd)
	rootCmd.AddCommand(transfer.Cmd)
	rootCmd.AddCommand(urlcmd.Cmd)
	rootCmd.AddCommand(vanity.Cmd)
	rootCmd.InitDefaultCompletionCmd()
	// Assign the auto-generated completion command to the utilities group.
	for _, c := range rootCmd.Commands() {
		if c.Name() == "completion" {
			c.GroupID = "utilities"
			break
		}
	}

	pf := rootCmd.PersistentFlags()
	pf.StringVar(&gf.profile, "profile", "", "credentials profile to use (env: NAMECOM_PROFILE)")
	pf.StringVar(&gf.username, "username", "", "API username (env: NAMECOM_USERNAME)")
	pf.StringVar(&gf.token, "token", "", "API token (env: NAMECOM_TOKEN)")
	pf.BoolVar(&gf.sandbox, "sandbox", false, "use sandbox API (api.dev.name.com)")
	pf.StringVarP(&gf.output, "output", "o", "", "output format: table, json, yaml (default: table in TTY, json otherwise)")
	pf.BoolVarP(&gf.quiet, "quiet", "q", false, "print IDs/names only (one per line)")
	pf.BoolVar(&gf.noHeader, "no-header", false, "omit header row from table output")
	pf.StringVar(&gf.color, "color", "auto", "colorize output: auto, always, never (env: NO_COLOR, CLICOLOR_FORCE)")
	pf.DurationVar(&gf.timeout, "timeout", 30*time.Second, "per-request timeout")
	pf.BoolVar(&gf.debug, "debug", false, "log HTTP requests/responses to stderr (token redacted)")
	pf.StringVar(&gf.debugFile, "debug-file", "", "log HTTP requests/responses to this file instead of stderr")
	pf.BoolVarP(&gf.yes, "yes", "y", false, "skip confirmation prompts")
	pf.BoolVar(&gf.dryRun, "dry-run", false, "print the API request that would be sent without executing it")
	pf.StringVar(&gf.idempKey, "idempotency-key", "", "idempotency key for write operations (auto-generated per invocation if not set)")

	// Apply styled help to every command in the tree.
	cobra.AddTemplateFunc("styleHelp", func() bool { return true }) // trigger late-bind
	rootCmd.SetHelpFunc(styledHelp)
}

func persistentPreRunE(cmd *cobra.Command, _ []string) error {
	if err := initOutputContext(cmd); err != nil {
		return err
	}
	if skipClientInit(cmd) {
		return nil
	}
	return initContext(cmd)
}

// initOutputContext applies --output, --color, --quiet, and --no-header to the
// command context. It runs for every command, including those that skip API
// credential setup (auth, version, etc.).
func initOutputContext(cmd *cobra.Command) error {
	out := output.DefaultConfig()
	if gf.output != "" {
		f, err := output.ParseFormat(gf.output)
		if err != nil {
			return err
		}
		out.Format = f
	}
	if gf.color != "auto" {
		cm, err := output.ParseColorMode(gf.color)
		if err != nil {
			return err
		}
		out.Color = cm
	}
	out.QuietMode = gf.quiet
	out.NoHeader = gf.noHeader
	cmd.SetContext(context.WithValue(cmd.Context(), cmdutil.KeyOutput, out))
	return nil
}

// initContext builds the API client and config file from the resolved
// flags/env and stores them on the command's context. Output config is
// already set by initOutputContext.
func initContext(cmd *cobra.Command) error {
	out := cmdutil.Out(cmd)

	// --- Credentials ---
	sandboxSet := cmd.Flags().Changed("sandbox")
	ov := config.Overrides{
		Profile:    gf.profile,
		Username:   gf.username,
		Token:      gf.token,
		Sandbox:    gf.sandbox,
		SandboxSet: sandboxSet,
	}

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Check that an explicitly requested profile actually exists.
	profileReq := gf.profile
	if profileReq == "" {
		profileReq = os.Getenv("NAMECOM_PROFILE")
	}
	if profileReq != "" && cfgFile != nil {
		if _, ok := cfgFile.Profiles[profileReq]; !ok {
			cfgPath, _ := config.Path()
			names := make([]string, 0, len(cfgFile.Profiles))
			for k := range cfgFile.Profiles {
				names = append(names, k)
			}
			if len(names) > 0 {
				return fmt.Errorf("profile %q not found in %s\n\nAvailable profiles: %s\nRun 'namecom auth login --profile %s' to create it",
					profileReq, cfgPath, strings.Join(names, ", "), profileReq)
			}
			return fmt.Errorf("profile %q not found in %s (no profiles configured)\nRun 'namecom auth login --profile %s' to create it",
				profileReq, cfgPath, profileReq)
		}
	}

	creds, err := config.Resolve(cfgFile, ov)
	if err != nil {
		if errors.Is(err, config.ErrNoCredentials) {
			if output.IsInteractive() {
				return fmt.Errorf("no credentials configured — run 'namecom auth login' to set them up")
			}
			return fmt.Errorf("no credentials configured (set NAMECOM_USERNAME and NAMECOM_TOKEN, or run 'namecom auth login')")
		}
		return err
	}

	out.Sandbox = creds.Sandbox

	// --- API client ---
	apiOpts := api.Options{
		Creds:     creds,
		UserAgent: "namecom-cli/" + Version,
		Timeout:   gf.timeout,
	}
	switch {
	case gf.debugFile != "":
		f, err := os.OpenFile(gf.debugFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("opening debug file: %w", err)
		}
		// File is intentionally left open for the process lifetime.
		apiOpts.DebugLog = f
	case gf.debug:
		apiOpts.DebugLog = os.Stderr
	}
	if apiOpts.DebugLog != nil || output.IsStderrTTY() {
		apiOpts.OnRetry = func(attempt int, delay time.Duration) {
			fmt.Fprintf(os.Stderr, "retrying (attempt %d, waiting %s)…\n", attempt, delay.Round(time.Millisecond))
		}
	}
	apiClient, err := api.New(apiOpts)
	if err != nil {
		return fmt.Errorf("initializing API client: %w", err)
	}

	// Stash everything on the context so subcommands can retrieve them via
	// the helpers below without threading parameters through every call.
	ctx := cmd.Context()
	idempKey := gf.idempKey
	if idempKey == "" {
		idempKey = uuid.New().String()
	}
	ctx = api.ContextWithIdempotencyKey(ctx, idempKey)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, apiClient)
	ctx = context.WithValue(ctx, cmdutil.KeyConfig, cfgFile)
	ctx = context.WithValue(ctx, cmdutil.KeyOverrides, ov)
	cmd.SetContext(ctx)
	return nil
}

// IsYes reports whether --yes / -y was set globally (skip confirmation).
func IsYes() bool { return gf.yes }

// IsDryRun reports whether --dry-run was set globally.
func IsDryRun() bool { return gf.dryRun }

// skipClientInit returns true for commands that don't need API credentials.
func skipClientInit(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "auth", "config", "open", "version":
			return true
		}
	}
	return false
}

// exitCode maps an error to a CLI exit code following the documented table:
//
//	0 success, 1 API/runtime, 2 usage, 3 auth, 4 not-found, 5 rate-limited
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if apiErr, ok := errors.AsType[*api.APIError](err); ok {
		switch apiErr.StatusCode {
		case 401, 403:
			return 3
		case 404:
			return 4
		case 429:
			return 5
		}
		return 1
	}
	return 1
}
