// Package cmd contains all CLI commands for the namecom CLI.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
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
	jq        string
	fields    []string
}

var gf globalFlags

// rootLongBody is the root help below its title line, which Execute rebuilds
// with the resolved version. The exit codes are the table exitCode implements.
const rootLongBody = `Manage domains, DNS records, email forwarding, URL forwarding, transfers, and more.

Quick start:
  namecom auth login              # configure credentials
  namecom domain list             # list your domains
  namecom dns list example.com    # manage DNS records
  namecom domain register foo.com # register a new domain

Exit codes:
  0  success
  1  API or other runtime error, a prompt declined or cancelled, or a name
     'domain check --exit-status' found unavailable
  2  usage error: a bad command, flag, argument or value
  3  authentication: credentials missing, failing or rejected, or access denied
  4  not found
  5  rate limited
  6  write outcome unknown: a change failed in a way that may have gone through`

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
	if checksForUpdates(os.Args[1:]) {
		go func() { updateCh <- update.Check(Version) }()
	}

	// Classify cobra's own flag-parse failures (unknown flag, bad value) as
	// usage errors so they exit 2 rather than collapsing into the generic 1.
	// Applies to every subcommand, not just root. An unknown flag also gets
	// a did-you-mean and the usage line.
	rootCmd.SetFlagErrorFunc(cmdutil.FlagError)

	if code := run(); code != 0 {
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

// run executes the root command and returns its exit code, having reported
// the outcome on stderr: the error, or on success any warnings JSON and YAML
// modes kept back (#240). Execute is this plus os.Exit, so tests call run.
func run() int {
	err := suggestFor(cmdutil.ClassifyCobraUsage(rootCmd.Execute()))
	// --fields and --jq print the command's output once it has returned,
	// failed or not: `dns sync` prints a document when a change fails. The
	// command's own error is the one reported.
	if ferr := endFilter(); err == nil {
		err = ferr
	}
	if err != nil {
		return reportError(errorOutput(), err)
	}
	if resolvedOut != nil {
		resolvedOut.FlushWarnings()
	}
	return 0
}

// rootSuggestFor maps words typed in place of a top-level command to the
// command meant, when the two are spelled nothing alike and cobra's
// edit-distance suggestions cannot find it (#237). Cobra's own SuggestFor
// field can only name a direct subcommand, and two of these are deeper.
var rootSuggestFor = map[string]string{
	"records":   "dns",
	"record":    "dns",
	"redirect":  "url",
	"redirects": "url",
	"forward":   "url",
	"login":     "auth login",
	"logout":    "auth logout",
	"whoami":    "auth status",
	"env":       "help environment",
}

// suggestFor adds rootSuggestFor's answer to an unknown top-level command
// error, ahead of any suggestion cobra found itself.
func suggestFor(err error) error {
	u, ok := errors.AsType[*cmdutil.UnknownCommandError](err)
	if !ok || u.Path != rootCmd.CommandPath() {
		return err
	}
	target, ok := rootSuggestFor[strings.ToLower(u.Word)]
	if !ok {
		return err
	}
	suggestions := []string{target}
	for _, s := range u.Suggestions {
		if s = strings.TrimPrefix(s, u.Path+" "); s != target {
			suggestions = append(suggestions, s)
		}
	}
	return cmdutil.UnknownCommand(u.Word, u.Path, suggestions)
}

// checksForUpdates reports whether an invocation with these arguments looks
// for a newer release. Shell completion does not: it runs on every TAB, and
// in a package manager's sandbox at install time (the Homebrew formula
// generates its completion scripts with `namecom completion <shell>`), where
// a request to GitHub and a cache write are both out of place.
func checksForUpdates(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return false
	}
	return true
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
	pf.BoolVar(&gf.sandbox, "sandbox", false, "use sandbox API (api.dev.name.com) (env: NAMECOM_SANDBOX)")
	pf.StringVarP(&gf.output, "output", "o", "", "output format: table, json, yaml, tsv (default: table in TTY, json otherwise)")
	pf.BoolVarP(&gf.quiet, "quiet", "q", false, "script output, whatever --output says: lists print one ID/name per line, creates the new ID, other writes nothing")
	pf.BoolVar(&gf.noHeader, "no-header", false, "omit header row from table and TSV output")
	pf.StringSliceVar(&gf.fields, "fields", nil, "keep only these keys of each list item, or of the object, in this order (see 'namecom help formatting')")
	pf.StringVar(&gf.jq, "jq", "", "filter the JSON output with a jq expression; strings print unquoted (see 'namecom help formatting')")
	pf.BoolVar(&gf.wide, "wide", false, "keep every table column even if it overflows the terminal")
	pf.StringVar(&gf.color, "color", "auto", "colorize output: auto, always, never (env: NO_COLOR, CLICOLOR_FORCE)")
	pf.DurationVar(&gf.timeout, "timeout", 30*time.Second, "total time budget for one API call, retries included")
	pf.BoolVar(&gf.debug, "debug", false, "log HTTP requests/responses to stderr (token redacted)")
	pf.StringVar(&gf.debugFile, "debug-file", "", "log HTTP requests/responses to this file instead of stderr")
	pf.BoolVarP(&gf.yes, "yes", "y", false, "skip confirmation prompts")
	pf.BoolVar(&gf.dryRun, "dry-run", false, "for write operations, print the request instead of sending it (reads are unaffected)")
	pf.StringVar(&gf.idempKey, "idempotency-key", "", "pin every write in this invocation to one idempotency key (default: a fresh key per write)")
	pf.StringVar(&gf.baseURL, "base-url", "", "override the API base URL (for local stubs and proxies; credentials are sent to whatever you name) (env: NAMECOM_BASE_URL)")

	// Root help lists these under headings, not as one block of 19 (#237).
	// Unlisted flags, the ones nearly every write uses, come first.
	for section, names := range map[string][]string{
		"Output":      {"output", "fields", "jq", "quiet", "no-header", "wide", "color"},
		"Credentials": {"profile", "username", "token", "sandbox"},
		"Advanced":    {"timeout", "debug", "debug-file", "idempotency-key", "base-url"},
	} {
		for _, name := range names {
			_ = pf.SetAnnotation(name, cmdutil.FlagSection, []string{section})
		}
	}

	// Flag values the shell can offer; without these, TAB after -o, --color
	// or --profile completed filenames (#187).
	_ = rootCmd.RegisterFlagCompletionFunc("output",
		cobra.FixedCompletions([]string{"table", "json", "yaml", "tsv"}, cobra.ShellCompDirectiveNoFileComp))
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
	out, filter, err := buildOutputConfig()
	if err != nil {
		return err
	}
	if filter != nil {
		out.BeginFilter(filter)
	}
	cmd.SetContext(context.WithValue(cmd.Context(), cmdutil.KeyOutput, out))
	// Remember it for Execute's error path. That path ran before this config
	// existed and fell back to output.DefaultConfig(), which decides format by
	// TTY detection alone — so `-o json` in a terminal printed a plain-text
	// error instead of the documented JSON envelope, and `-o table` in a pipe
	// printed the envelope anyway. --color was ignored for errors entirely.
	resolvedOut = out
	// No client yet for this command; initClient sets it if one is built.
	resolvedClient = nil
	return nil
}

// buildOutputConfig applies the parsed output flags to the default config,
// and returns the --fields/--jq filter they ask for, or nil.
func buildOutputConfig() (*output.Config, *output.Filter, error) {
	out := output.DefaultConfig()
	// Bad --output/--color values are invocation mistakes, not runtime failures:
	// classify them so they exit 2 like any other usage error.
	if gf.output != "" {
		f, err := output.ParseFormat(gf.output)
		if err != nil {
			return nil, nil, cmdutil.NewUsageError(err)
		}
		out.Format = f
	}
	filter, err := buildFilter(out)
	if err != nil {
		return nil, nil, cmdutil.NewUsageError(err)
	}
	if gf.color != "auto" {
		cm, err := output.ParseColorMode(gf.color)
		if err != nil {
			return nil, nil, cmdutil.NewUsageError(err)
		}
		out.Color = cm
	}
	out.ApplyColorProfile()
	out.QuietMode = gf.quiet
	out.NoHeader = gf.noHeader
	out.Wide = gf.wide
	return out, filter, nil
}

// buildFilter checks --fields and --jq against the other output flags and
// returns the filter they ask for, or nil when neither is set (#241). --jq
// sets out's format to JSON when -o did not name one. Everything that can be
// wrong with them on the command line is caught here, before the command
// runs, so a mistyped expression never follows a write it was meant to filter.
func buildFilter(out *output.Config) (*output.Filter, error) {
	if gf.jq == "" && len(gf.fields) == 0 {
		return nil, nil
	}
	// -q prints one chosen value per line; --fields and --jq choose
	// something else. Neither can win without surprising someone.
	if gf.quiet {
		names := "--jq"
		if len(gf.fields) > 0 {
			names = "--fields"
		}
		return nil, fmt.Errorf("--quiet cannot be combined with %s: -q prints one ID or name per line; use --jq to pick a value instead", names)
	}
	f := &output.Filter{}
	if len(gf.fields) > 0 {
		fields, err := output.ParseFields(gf.fields)
		if err != nil {
			return nil, err
		}
		f.Fields = fields
	}
	if gf.jq != "" {
		// jq reads JSON and prints JSON, so another -o is a contradiction,
		// as it is in gh. Without -o it is JSON, in a terminal too.
		if gf.output != "" && out.Format != output.FormatJSON {
			return nil, fmt.Errorf("--jq filters JSON, so it cannot be combined with -o %s; leave out -o, or use --fields with -o %s", out.Format, out.Format)
		}
		out.Format = output.FormatJSON
		code, err := output.CompileJQ(gf.jq)
		if err != nil {
			return nil, err
		}
		f.JQ = code
	}
	return f, nil
}

// endFilter prints the output --fields or --jq filtered, if either was
// given. A filter that does not fit the output is a usage error, except
// after a write, which output.Config.EndFilter reports as a warning.
func endFilter() error {
	if resolvedOut == nil {
		return nil
	}
	err := resolvedOut.EndFilter(resolvedClient.SentWrite())
	if _, ok := errors.AsType[*output.FilterError](err); ok {
		return cmdutil.NewUsageError(err)
	}
	return err
}

// resolvedOut is the output config built by initOutputContext, retained so the
// top-level error handler can honor --output/--color. Nil when the command
// failed before PersistentPreRunE ran; see errorOutput.
var resolvedOut *output.Config

// resolvedClient is the API client initClient built, retained so the error
// handler can ask whether the failure was a write whose outcome is unknown
// (#243). Nil when the command built none.
var resolvedClient *api.Client

// errorOutput returns the config the top-level error handler renders with.
//
// Cobra validates the argument count before PersistentPreRunE, so for
// `namecom domain get a b -o yaml` resolvedOut was still nil and the error
// came out in the TTY-detected default format, ignoring -o. The flags are
// parsed by then, so they are applied here directly. Only when they cannot
// be — a malformed flag, or a bad --output value — does the default apply.
//
// When cobra fails before it parses flags at all — an unknown top-level
// command, or an unknown flag ahead of -o — gf.output is still empty, and
// `-o table` in a pipe got the JSON envelope (#247). The arguments are
// scanned for --output then.
func errorOutput() *output.Config {
	if resolvedOut != nil {
		return resolvedOut
	}
	if gf.output == "" {
		gf.output = scanOutputFlag(os.Args[1:])
	}
	if out, _, err := buildOutputConfig(); err == nil {
		return out
	}
	// The flags do not go together — `--jq … -o table`, `-q --fields` — but
	// a valid -o and --color still say how to show that (#291). Only a bad
	// value for one of them leaves its default.
	out := output.DefaultConfig()
	if f, err := output.ParseFormat(gf.output); gf.output != "" && err == nil {
		out.Format = f
	}
	if cm, err := output.ParseColorMode(gf.color); gf.color != "auto" && err == nil {
		out.Color = cm
	}
	out.ApplyColorProfile()
	return out
}

// scanOutputFlag returns the value of the last -o/--output in args, in any
// of the forms pflag accepts, or "" when there is none. Arguments after "--"
// are not flags.
func scanOutputFlag(args []string) string {
	val := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return val
		case a == "-o" || a == "--output":
			if i+1 < len(args) {
				val = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--output="):
			val = strings.TrimPrefix(a, "--output=")
		case strings.HasPrefix(a, "-o") && !strings.HasPrefix(a, "--"):
			val = strings.TrimPrefix(strings.TrimPrefix(a, "-o"), "=")
		}
	}
	return val
}

// flagOverrides collects the global credential flags as config.Overrides.
func flagOverrides(cmd *cobra.Command) config.Overrides {
	return config.Overrides{
		Profile:    gf.profile,
		Username:   gf.username,
		Token:      gf.token,
		Sandbox:    gf.sandbox,
		SandboxSet: cmd.Flags().Changed("sandbox"),
		BaseURL:    gf.baseURL,
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

	if err := cmdutil.RequireProfile(cfgFile, ov); err != nil {
		return err
	}

	creds, err := config.Resolve(cfgFile, ov)
	if err != nil {
		if classified := cmdutil.CredentialsError(err, ov); classified != nil {
			return classified
		}
		// A credential helper that failed is also an auth problem, not a
		// generic runtime one. The default hint suggests `auth status`, which
		// is no help to someone running it.
		if isAuthStatus(cmd) {
			return cmdutil.NewAuthErrorHint(err, "fix the profile's token_cmd, or run 'namecom auth login' to replace it")
		}
		return cmdutil.NewAuthError(err)
	}

	out.Sandbox = creds.Sandbox
	if !forCompletion && creds.BaseURL == "" && envNoticeTTY() {
		if note := envEndpointNotice(cfgFile, creds, ov); note != "" {
			out.Warn(note)
		}
	}

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
	// --base-url, else NAMECOM_BASE_URL (#246), with the same checks.
	if creds.BaseURL != "" {
		name := config.SourceName(creds.Sources.BaseURL)
		if err := checkBaseURL(name, creds.BaseURL); err != nil {
			return cmdutil.NewUsageError(err)
		}
		apiOpts.BaseURL = creds.BaseURL
		// Not during completion: anything printed there lands mid-prompt.
		if warn := baseURLWarningFor(name, creds.BaseURL); warn != "" && !forCompletion {
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
	// A retry goes into the running spinner's text; a line printed over the
	// spinner landed on top of its frame (#232). With no spinner, it is one
	// line on stderr, for a person watching or a --debug log.
	retryLine := apiOpts.DebugLog != nil || output.IsStderrTTY()
	apiOpts.OnRetry = func(r api.Retry) {
		if !output.SpinnerNote(r.String()) && retryLine {
			fmt.Fprintln(os.Stderr, r.String())
		}
	}
	apiClient, err := api.New(apiOpts)
	if err != nil {
		return fmt.Errorf("initializing API client: %w", err)
	}
	if !forCompletion {
		resolvedClient = apiClient
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

// envNoticeTTY gates envEndpointNotice. Replaceable in tests.
var envNoticeTTY = output.IsStderrTTY

// envEndpointNotice says when NAMECOM_SANDBOX sends a profile's requests to
// the other endpoint, or returns "" when it does not. A `NAMECOM_SANDBOX=1`
// left exported in a shell silently pointed a production profile at the
// sandbox, and the reverse sent sandbox work to production (#225, #239).
//
// Only for a person watching (stderr a terminal): raw text ahead of the JSON
// error envelope would corrupt stderr for a script, and a script that sets the
// variable means it. Not when --sandbox decides, since that was typed on the
// line, or when the profile is not in the config file.
func envEndpointNotice(f *config.File, creds config.Credentials, ov config.Overrides) string {
	if ov.SandboxSet || f == nil {
		return ""
	}
	prof, ok := f.Profiles[creds.Profile]
	if !ok || prof.Sandbox == creds.Sandbox {
		return ""
	}
	return fmt.Sprintf("NAMECOM_SANDBOX=%s overrides profile %q (%s): requests go to %s",
		os.Getenv("NAMECOM_SANDBOX"), creds.Profile, loginEnv(prof.Sandbox), api.DefaultBaseURL(creds.Sandbox))
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

// isAuthStatus reports whether cmd is `namecom auth status`, which builds a
// client through initContext like any API command.
func isAuthStatus(cmd *cobra.Command) bool {
	return cmd.Name() == "status" && cmd.HasParent() && cmd.Parent().Name() == "auth"
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
	// A write that answered 5xx, or failed after it was sent, may have gone
	// through: name the idempotency key it carried, and exit 6 (#243). The
	// client decides from the request method, not from whether the command
	// marked the write (#247).
	err = resolvedClient.OutcomeUnknown(err)
	// The 401 hint mentions the sandbox's separate token only when the
	// request went there; cfg.Sandbox is set from the resolved credentials.
	if apiErr, ok := errors.AsType[*api.APIError](err); ok {
		apiErr.Sandbox = cfg.Sandbox
		apiErr.CredentialSource = credentialSource()
	}
	// A timeout says how long it waited, which is the --timeout budget.
	if netErr, ok := errors.AsType[*api.NetworkError](err); ok && gf.timeout > 0 {
		netErr.Timeout = gf.timeout
	}
	cfg.ErrorWith(err, errorInfo(err))
	return exitCode(err)
}

// errorInfo classifies err for the structured error envelope's "type" and
// "status" (#240), by the rules exitCode uses, so the two always agree.
func errorInfo(err error) output.ErrorInfo {
	err = normalizeError(err)
	var info output.ErrorInfo
	apiErr, isAPI := errors.AsType[*api.APIError](err)
	if isAPI {
		info.Status = apiErr.StatusCode
	}
	if _, ok := errors.AsType[*cmdutil.ConfirmationRequiredError](err); ok {
		info.Type = output.ErrorTypeConfirmationRequired
		return info
	}
	if _, ok := errors.AsType[*cmdutil.UsageError](err); ok {
		info.Type = output.ErrorTypeUsage
		return info
	}
	if _, ok := errors.AsType[*cmdutil.AuthError](err); ok {
		info.Type = output.ErrorTypeAuth
		return info
	}
	if errors.Is(err, cmdutil.ErrAborted) {
		info.Type = output.ErrorTypeAborted
		return info
	}
	if _, ok := errors.AsType[*cmdutil.ConflictError](err); ok {
		info.Type = output.ErrorTypeConflict
		return info
	}
	if _, ok := errors.AsType[*cmdutil.UnavailableError](err); ok {
		info.Type = output.ErrorTypeUnavailable
		return info
	}
	if isAPI {
		switch {
		case apiErr.StatusCode == 401, apiErr.StatusCode == 403:
			info.Type = output.ErrorTypeAuth
		case apiErr.StatusCode == 404:
			info.Type = output.ErrorTypeNotFound
		case apiErr.StatusCode == 429:
			info.Type = output.ErrorTypeRateLimited
		case isConflict(apiErr):
			info.Type = output.ErrorTypeConflict
		default:
			info.Type = output.ErrorTypeAPI
		}
		return info
	}
	if _, ok := errors.AsType[*api.NetworkError](err); ok {
		info.Type = output.ErrorTypeNetwork
		return info
	}
	info.Type = output.ErrorTypeAPI
	return info
}

// isConflict reports whether e says the thing being created already exists,
// or that an idempotency key was reused. The API answers a duplicate DNS
// record with `400 Parameter Value Error (Record already exists)` — seen in
// the sandbox, docs/upstream/core-api-go-withoutretries-ignored.md — not a
// 409; a 409 is how the SDK documents an idempotency-key problem.
func isConflict(e *api.APIError) bool {
	if e.StatusCode == 409 {
		return true
	}
	if e.StatusCode != 400 && e.StatusCode != 422 {
		return false
	}
	text := strings.ToLower(e.Message + " " + e.Details)
	return strings.Contains(text, "already exists")
}

// detailedError attaches structured fields to an error, which the JSON error
// envelope prints as "details". The message is unchanged.
type detailedError struct {
	error
	details any
}

func (e *detailedError) Unwrap() error     { return e.error }
func (e *detailedError) ErrorDetails() any { return e.details }

// exitCode maps an error to a CLI exit code following the documented table:
//
//	0 success, 1 API/runtime, 2 usage, 3 auth, 4 not-found, 5 rate-limited,
//	6 write outcome unknown
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
	// Ahead of the status: a 5xx write is 6, not 1. reportError wraps it.
	if _, ok := errors.AsType[*api.OutcomeUnknownError](err); ok {
		return 6
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

// credentialSource names the flags or variables the credentials came from,
// for a 401's hint, or returns "" when the token came from a profile, where
// `auth login` is the fix. A CI job that set NAMECOM_TOKEN was told to run
// `auth login`, which it cannot (#246).
func credentialSource() string {
	id, err := config.Identity(nil, config.Overrides{Username: gf.username, Token: gf.token})
	if err != nil || id.Sources.Token == "" {
		return ""
	}
	var names []string
	for _, src := range []string{id.Sources.Username, id.Sources.Token} {
		if src != "" {
			names = append(names, config.SourceName(src))
		}
	}
	return strings.Join(names, " and ")
}

// validateBaseURL checks a --base-url value before it is used, so a typo fails
// with an explanation rather than an opaque transport error mid-request.
func validateBaseURL(raw string) error { return checkBaseURL("--base-url", raw) }

// checkBaseURL is validateBaseURL for a value from name, the flag or the
// NAMECOM_BASE_URL variable.
func checkBaseURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid %s %q: %w", name, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid %s %q: must be an absolute http:// or https:// URL", name, raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid %s %q: missing host", name, raw)
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
func baseURLWarning(raw string) string { return baseURLWarningFor("--base-url", raw) }

// baseURLWarningFor is baseURLWarning for a value from name, the flag or the
// NAMECOM_BASE_URL variable.
func baseURLWarningFor(name, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch u.Host {
	case "api.name.com", "api.dev.name.com":
		return ""
	}
	return fmt.Sprintf("%s is set: requests and your API credentials are being sent to %s, not name.com", name, u.Host)
}

// normalizeError converts a Core SDK error into the CLI's *api.APIError before
// it is rendered or classified. Doing it here, once, means a command that
// returns an SDK error unconverted still exits 4 on a 404 — see
// api.NormalizeError for why per-call-site conversion was not enough.
func normalizeError(err error) error { return api.NormalizeError(err) }
