// Package cmd contains all CLI commands for the namecom CLI.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/patramsey/namecom-cli/cmd/apicmd"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	configcmd "github.com/patramsey/namecom-cli/cmd/config"
	"github.com/patramsey/namecom-cli/cmd/contact"
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
	wide      bool
	color     string
	timeout   time.Duration
	debug     bool
	debugFile string
	yes       bool
	dryRun    bool
	idempKey  string
	baseURL   string
}

var gf globalFlags

// rootLongBody is the root help below its title line, which Execute rebuilds
// with the resolved version. The exit codes are the table exitCode implements.
const rootLongBody = `Manage domains, DNS records, email forwarding, URL redirects, transfers, and more.

Quick start:
  namecom auth login              # configure credentials
  namecom domain list             # list your domains
  namecom dns list example.com    # manage DNS records
  namecom domain register foo.com # register a new domain

Exit codes:
  0  success
  1  API or other runtime error
  2  usage error: a bad command, flag, argument or value
  3  authentication: credentials missing, failing or rejected, or access denied
  4  not found
  5  rate limited

Run 'namecom <command> --help' for details on any command.`

// rootCmd is the top-level `namecom` command. It configures the API client and
// output renderer and stashes them on the context for every subcommand.
var rootCmd = &cobra.Command{
	// "[command]" in Use so the rendered usage line reads
	// "namecom [command] [flags]". The custom help template builds it from
	// UseLine(), which only appends "[flags]" — so root help read as though the
	// tool took no subcommand at all. cmd.Name() still resolves to "namecom".
	Use:               "namecom [command]",
	Short:             "CLI for the name.com Core API",
	Long:              "namecom — CLI for the name.com Core API\n\n" + rootLongBody,
	SilenceUsage:      true,
	SilenceErrors:     true,
	Version:           Version,
	PersistentPreRunE: persistentPreRunE,
}

// Execute is the entry point called from main.
func Execute() {
	// Resolve the effective version: ldflag value for release builds, module
	// metadata for go install builds, "dev" for local builds.
	Version = resolveVersion()
	rootCmd.Version = Version
	rootCmd.Long = "namecom " + Version + " — CLI for the name.com Core API\n\n" + rootLongBody

	// Start version check in background before the command runs, so there's
	// a chance the network round-trip completes by the time we're done.
	updateCh := make(chan string, 1)
	go func() { updateCh <- update.Check(Version) }()

	// Classify cobra's own flag-parse failures (unknown flag, bad value) as
	// usage errors so they exit 2 rather than collapsing into the generic 1.
	// Applies to every subcommand, not just root.
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return cmdutil.NewUsageError(err)
	})

	if err := cmdutil.ClassifyCobraUsage(rootCmd.Execute()); err != nil {
		os.Exit(reportError(errorOutput(), err))
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
	cobra.OnFinalize(closeDebugLog)
	rootCmd.AddGroup(
		&cobra.Group{ID: "domains", Title: "Domain Commands:"},
		&cobra.Group{ID: "account", Title: "Account Commands:"},
		&cobra.Group{ID: "utilities", Title: "Utilities:"},
	)

	domain.Cmd.GroupID = "domains"
	contact.Cmd.GroupID = "domains"
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
	rootCmd.AddCommand(contact.Cmd)
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
	pf.BoolVarP(&gf.quiet, "quiet", "q", false, "script output, whatever --output says: lists print one ID/name per line, creates the new ID, other writes nothing")
	pf.BoolVar(&gf.noHeader, "no-header", false, "omit header row from table output")
	pf.BoolVar(&gf.wide, "wide", false, "keep every table column even if it overflows the terminal")
	pf.StringVar(&gf.color, "color", "auto", "colorize output: auto, always, never (env: NO_COLOR, CLICOLOR_FORCE)")
	pf.DurationVar(&gf.timeout, "timeout", 30*time.Second, "total time budget for one API call, retries included")
	pf.BoolVar(&gf.debug, "debug", false, "log HTTP requests/responses to stderr (token redacted)")
	pf.StringVar(&gf.debugFile, "debug-file", "", "log HTTP requests/responses to this file instead of stderr")
	pf.BoolVarP(&gf.yes, "yes", "y", false, "skip confirmation prompts")
	pf.BoolVar(&gf.dryRun, "dry-run", false, "for write operations, print the request instead of sending it (reads are unaffected)")
	pf.StringVar(&gf.idempKey, "idempotency-key", "", "pin every write in this invocation to one idempotency key (default: a fresh key per write)")
	pf.StringVar(&gf.baseURL, "base-url", "", "override the API base URL (for local stubs and proxies; credentials are sent to whatever you name)")

	// Flag values the shell can offer; without these, TAB after -o, --color
	// or --profile completed filenames (#187).
	_ = rootCmd.RegisterFlagCompletionFunc("output",
		cobra.FixedCompletions([]string{"table", "json", "yaml"}, cobra.ShellCompDirectiveNoFileComp))
	_ = rootCmd.RegisterFlagCompletionFunc("color",
		cobra.FixedCompletions([]string{"auto", "always", "never"}, cobra.ShellCompDirectiveNoFileComp))
	_ = rootCmd.RegisterFlagCompletionFunc("profile", cmdutil.CompleteProfiles)

	// Cobra adds -h/--help and --version only after it has picked the command
	// to run, but picking it skips flag values by asking whether each flag
	// takes one. An unknown --help was assumed to, so `namecom --help -o json`
	// swallowed -o and ran "json" as a subcommand (#209). Define them up front.
	rootCmd.InitDefaultHelpFlag()
	rootCmd.InitDefaultVersionFlag()

	// Apply styled help to every command in the tree.
	cobra.AddTemplateFunc("styleHelp", func() bool { return true }) // trigger late-bind
	rootCmd.SetHelpFunc(styledHelp)
}

func persistentPreRunE(cmd *cobra.Command, _ []string) error {
	if err := initOutputContext(cmd); err != nil {
		return err
	}
	// http.Client reads any timeout <= 0 as "none", so `--timeout -1s` used
	// to remove the budget it looks like it tightens (#187). Zero keeps the
	// API client's default.
	if gf.timeout < 0 {
		return cmdutil.NewUsageError(fmt.Errorf("--timeout must not be negative (got %s)", gf.timeout))
	}
	// Stored before the skip below: `config show --profile x` never builds a
	// client, and when only initContext stored these the flag never reached it.
	cmd.SetContext(context.WithValue(cmd.Context(), cmdutil.KeyOverrides, flagOverrides(cmd)))
	if skipClientInit(cmd) {
		return nil
	}
	// Shell completion builds its client lazily, only when a completion
	// function calls the API. Here the global flags are not parsed yet —
	// __complete disables flag parsing — and building now would also run
	// token_cmd on every TAB, static completions included. See
	// cmdutil.ClientFactory.
	if isCompletionRequest(cmd) {
		cmd.SetContext(context.WithValue(cmd.Context(), cmdutil.KeyClientFactory, cmdutil.ClientFactory(completionClient)))
		return nil
	}
	return initContext(cmd)
}

// isCompletionRequest reports whether cmd is cobra's hidden __complete
// command (or its no-descriptions variant), which the shell runs on TAB.
func isCompletionRequest(cmd *cobra.Command) bool {
	return cmd.Name() == cobra.ShellCompRequestCmd || cmd.Name() == cobra.ShellCompNoDescRequestCmd
}

// completionClient is the cmdutil.ClientFactory for shell completion. cmd is
// the command being completed, whose flags cobra has parsed by now, so the
// globals reflect what was typed on the line.
func completionClient(cmd *cobra.Command) (*api.Client, error) {
	if err := initClient(cmd, true); err != nil {
		return nil, err
	}
	return cmdutil.APIClient(cmd), nil
}

// initOutputContext applies --output, --color, --quiet, --no-header, and --wide to the
// command context. It runs for every command, including those that skip API
// credential setup (auth, version, etc.).
func initOutputContext(cmd *cobra.Command) error {
	out, err := buildOutputConfig()
	if err != nil {
		return err
	}
	cmd.SetContext(context.WithValue(cmd.Context(), cmdutil.KeyOutput, out))
	// Remember it for Execute's error path. That path ran before this config
	// existed and fell back to output.DefaultConfig(), which decides format by
	// TTY detection alone — so `-o json` in a terminal printed a plain-text
	// error instead of the documented JSON envelope, and `-o table` in a pipe
	// printed the envelope anyway. --color was ignored for errors entirely.
	resolvedOut = out
	return nil
}

// buildOutputConfig applies the parsed output flags to the default config.
func buildOutputConfig() (*output.Config, error) {
	out := output.DefaultConfig()
	// Bad --output/--color values are invocation mistakes, not runtime failures:
	// classify them so they exit 2 like any other usage error.
	if gf.output != "" {
		f, err := output.ParseFormat(gf.output)
		if err != nil {
			return nil, cmdutil.NewUsageError(err)
		}
		out.Format = f
	}
	if gf.color != "auto" {
		cm, err := output.ParseColorMode(gf.color)
		if err != nil {
			return nil, cmdutil.NewUsageError(err)
		}
		out.Color = cm
	}
	out.ApplyColorProfile()
	out.QuietMode = gf.quiet
	out.NoHeader = gf.noHeader
	out.Wide = gf.wide
	return out, nil
}

// resolvedOut is the output config built by initOutputContext, retained so the
// top-level error handler can honor --output/--color. Nil when the command
// failed before PersistentPreRunE ran; see errorOutput.
var resolvedOut *output.Config

// errorOutput returns the config the top-level error handler renders with.
//
// Cobra validates the argument count before PersistentPreRunE, so for
// `namecom domain get a b -o yaml` resolvedOut was still nil and the error
// came out in the TTY-detected default format, ignoring -o. The flags are
// parsed by then, so they are applied here directly. Only when they cannot
// be — a malformed flag, or a bad --output value — does the default apply.
func errorOutput() *output.Config {
	if resolvedOut != nil {
		return resolvedOut
	}
	if out, err := buildOutputConfig(); err == nil {
		return out
	}
	return output.DefaultConfig()
}

// flagOverrides collects the global credential flags as config.Overrides.
func flagOverrides(cmd *cobra.Command) config.Overrides {
	return config.Overrides{
		Profile:    gf.profile,
		Username:   gf.username,
		Token:      gf.token,
		Sandbox:    gf.sandbox,
		SandboxSet: cmd.Flags().Changed("sandbox"),
	}
}

// initContext builds the API client and config file from the resolved
// flags/env and stores them on the command's context. Output config is
// already set by initOutputContext.
func initContext(cmd *cobra.Command) error { return initClient(cmd, false) }

// initClient is initContext, with forCompletion set when the client answers a
// shell TAB rather than a command.
func initClient(cmd *cobra.Command, forCompletion bool) error {
	out := cmdutil.Out(cmd)

	// --- Credentials ---
	ov := flagOverrides(cmd)

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Check that an explicitly requested profile actually exists. A missing
	// one leaves no usable credentials, so it is an auth error (exit 3), like
	// every other way of having none (#210).
	profileReq := gf.profile
	if profileReq == "" {
		profileReq = os.Getenv("NAMECOM_PROFILE")
	}
	if profileReq != "" && cfgFile != nil {
		if _, ok := cfgFile.Profiles[profileReq]; !ok {
			cfgPath, _ := config.ActivePath()
			names := make([]string, 0, len(cfgFile.Profiles))
			for k := range cfgFile.Profiles {
				names = append(names, k)
			}
			sort.Strings(names)
			if len(names) > 0 {
				return cmdutil.NewAuthError(fmt.Errorf("profile %q not found in %s\n\nAvailable profiles: %s\nRun 'namecom auth login --profile %s' to create it",
					profileReq, cfgPath, strings.Join(names, ", "), profileReq))
			}
			return cmdutil.NewAuthError(fmt.Errorf("profile %q not found in %s (no profiles configured)\nRun 'namecom auth login --profile %s' to create it",
				profileReq, cfgPath, profileReq))
		}
	}

	creds, err := config.Resolve(cfgFile, ov)
	if err != nil {
		if errors.Is(err, config.ErrNoCredentials) {
			// Resolve adds context to this error when it can say something more
			// specific than "nothing is configured" — several profiles exist
			// but none is the default, say. Substituting the generic text threw
			// that away and pointed the user at `auth login`, which overwrites.
			//nolint:errorlint // identity, not chain: the bare sentinel means
			// Resolve had nothing to add, so the friendlier text below applies.
			if err != config.ErrNoCredentials {
				return cmdutil.NewAuthError(err)
			}
			if output.IsInteractive() {
				return cmdutil.NewAuthError(fmt.Errorf("no credentials configured — run 'namecom auth login' to set them up"))
			}
			return cmdutil.NewAuthError(fmt.Errorf("no credentials configured (set NAMECOM_USERNAME and NAMECOM_TOKEN, or run 'namecom auth login')"))
		}
		// A credential helper that failed is also an auth problem, not a
		// generic runtime one.
		return cmdutil.NewAuthError(err)
	}

	out.Sandbox = creds.Sandbox

	// --- API client ---
	apiOpts := api.Options{
		Creds:     creds,
		UserAgent: "namecom-cli/" + Version,
		Timeout:   gf.timeout,
	}
	// A TAB freezes the shell until it returns: one short attempt, and no
	// candidates if the API cannot answer in time. A shorter --timeout wins.
	if forCompletion {
		if apiOpts.Timeout <= 0 || apiOpts.Timeout > cmdutil.CompletionTimeout {
			apiOpts.Timeout = cmdutil.CompletionTimeout
		}
		apiOpts.MaxRetries = -1
	}
	if gf.baseURL != "" {
		if err := validateBaseURL(gf.baseURL); err != nil {
			return cmdutil.NewUsageError(err)
		}
		apiOpts.BaseURL = gf.baseURL
		// Not during completion: anything printed there lands mid-prompt.
		if warn := baseURLWarning(gf.baseURL); warn != "" && !forCompletion {
			out.Warn(warn)
		}
	}
	switch {
	case gf.debugFile != "":
		f, err := os.OpenFile(gf.debugFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("opening debug file: %w", err)
		}
		// The 0600 above applies only to a file this call creates; one that
		// already existed kept its mode, often 0644 (#187). Tighten a regular
		// file only: --debug-file /dev/stderr names a terminal device.
		if fi, err := f.Stat(); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o077 != 0 {
			if err := f.Chmod(0o600); err != nil {
				_ = f.Close()
				return fmt.Errorf("restricting debug file permissions: %w", err)
			}
		}
		closeDebugLog()
		debugLogFile = f
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
	// Only pin a key when the user named one. Left unpinned, the API client
	// mints a fresh key per write, because one invocation can perform many
	// operations — `dns import` posts once per record — and a shared key makes
	// an API that honours it collapse them all onto the first.
	if gf.idempKey != "" {
		ctx = api.ContextWithIdempotencyKey(ctx, gf.idempKey)
	}
	ctx = context.WithValue(ctx, cmdutil.KeyClient, apiClient)
	ctx = context.WithValue(ctx, cmdutil.KeyConfig, cfgFile)
	ctx = context.WithValue(ctx, cmdutil.KeyOverrides, ov)
	cmd.SetContext(ctx)
	return nil
}

// debugLogFile is the open --debug-file, if any. closeDebugLog runs as a cobra
// finalizer, so it is closed when the command returns, on error paths too.
// It used to stay open for the process lifetime, which on Windows locks the
// file until the process exits.
var debugLogFile *os.File

func closeDebugLog() {
	if debugLogFile != nil {
		_ = debugLogFile.Close()
		debugLogFile = nil
	}
}

// skipClientInit returns true for commands that don't need API credentials.
func skipClientInit(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		// completion and help emit static text. Requiring credentials for them
		// was a bootstrap trap: `auth login` ends by suggesting shell
		// completion, and `source <(namecom completion zsh)` in a shell rc broke
		// every new shell until credentials existed.
		case "auth", "config", "open", "version", "completion", "help":
			return true
		}
	}
	return false
}

// reportError renders err through cfg and returns the exit code for it.
//
// Hints travel with the error, on stderr. An exit-3 hint used to be printed
// separately with cfg.Hint, which writes to stdout — so it landed in
// `namecom … > out.txt` — and repeated the hint the error already carried.
// cmdutil.AuthError now carries its own.
func reportError(cfg *output.Config, err error) int {
	err = normalizeError(err)
	cfg.Error(err)
	return exitCode(err)
}

// exitCode maps an error to a CLI exit code following the documented table:
//
//	0 success, 1 API/runtime, 2 usage, 3 auth, 4 not-found, 5 rate-limited
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	// Normalized here as well as in Execute, so the classification cannot
	// depend on a caller remembering to do it first.
	err = normalizeError(err)
	// Classification set by the failing path itself. Checked before the API
	// error so a wrapped auth failure still reports 3.
	if _, ok := errors.AsType[*cmdutil.UsageError](err); ok {
		return 2
	}
	if _, ok := errors.AsType[*cmdutil.AuthError](err); ok {
		return 3
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

// validateBaseURL checks a --base-url value before it is used, so a typo fails
// with an explanation rather than an opaque transport error mid-request.
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid --base-url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid --base-url %q: must be an absolute http:// or https:// URL", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid --base-url %q: missing host", raw)
	}
	return nil
}

// baseURLWarning returns a caution when --base-url points somewhere other than
// name.com, or the empty string when it does not.
//
// The flag redirects authenticated traffic: the account's Authorization header
// goes to whatever host is named. That is exactly what makes it useful against
// a local stub, and exactly what makes it worth saying out loud — a typo'd or
// pasted value sends a live credential to a third party.
func baseURLWarning(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch u.Host {
	case "api.name.com", "api.dev.name.com":
		return ""
	}
	return fmt.Sprintf("--base-url is set: requests and your API credentials are being sent to %s, not name.com", u.Host)
}

// normalizeError converts a Core SDK error into the CLI's *api.APIError before
// it is rendered or classified. Doing it here, once, means a command that
// returns an SDK error unconverted still exits 4 on a 404 — see
// api.NormalizeError for why per-call-site conversion was not enough.
func normalizeError(err error) error { return api.NormalizeError(err) }
