package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	configcmd "github.com/patramsey/namecom-cli/cmd/config"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage name.com API credentials",
}

// apiSettingsURL is the name.com page where an account creates API tokens.
const apiSettingsURL = "https://www.name.com/account/settings/api"

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Save credentials to a profile, checking them with the API",
	Long: `Ask for your name.com API username and token, check them with the API,
and save them to a profile in the config file.

Create a token at ` + apiSettingsURL + `.
Sandbox credentials are separate from production ones, and the sandbox
username usually ends in -test; log in to the sandbox with --sandbox.

Without a terminal, as in CI, pass --username with either --with-token, which
reads the token from standard input, or --token-cmd, which saves a command
that prints the token each time one is needed (a password manager's CLI, say)
instead of the token itself. Neither asks anything; replacing an existing
profile then needs --yes. A job that only runs commands needs no profile at
all: set NAMECOM_USERNAME and NAMECOM_TOKEN instead.`,
	Example: `  namecom auth login
  namecom auth login --profile staging
  namecom auth login --profile sandbox --sandbox
  echo "$NAMECOM_TOKEN" | namecom auth login --username alice --with-token
  namecom auth login --username alice --token-cmd 'op read op://vault/namecom/token'`,
	Args: cobra.NoArgs,
	RunE: runAuthLogin,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Verify credentials by calling the API hello endpoint",
	Example: `  namecom auth status
  namecom auth status --profile staging`,
	Args: cobra.NoArgs,
	RunE: runAuthStatus,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove credentials for the active profile",
	Example: `  namecom auth logout
  namecom auth logout --profile staging`,
	Args: cobra.NoArgs,
	RunE: runAuthLogout,
}

var loginProfile string
var logoutProfile string

// The non-interactive login flags (#246).
var (
	loginWithToken bool
	loginTokenCmd  string
	loginNoVerify  bool
)

func init() {
	// Both say they replace the global --profile (#237): here it names the
	// profile to write or remove, not the credentials to run with.
	authLoginCmd.Flags().StringVar(&loginProfile, "profile", "default", "profile name to save credentials under (overrides the global --profile)")
	// --username is the global flag. --token is refused rather than used: a
	// token on the command line ends up in shell history and process lists,
	// which is why gh reads it from standard input too.
	authLoginCmd.Flags().BoolVar(&loginWithToken, "with-token", false, "read the token from standard input instead of asking (with --username)")
	authLoginCmd.Flags().StringVar(&loginTokenCmd, "token-cmd", "", "save a command that prints the token, run each time one is needed, instead of the token (with --username)")
	// Without a terminal nobody can answer "save them anyway, unverified?",
	// so this is that answer, given up front: for a profile written before
	// the API or the vault behind --token-cmd can be reached.
	authLoginCmd.Flags().BoolVar(&loginNoVerify, "no-verify", false, "save the credentials without checking them with the API (or running --token-cmd)")
	authLogoutCmd.Flags().StringVar(&logoutProfile, "profile", "", "profile to remove, by default the active one (overrides the global --profile)")
	// logout's local --profile shadows the global one, completion included.
	// login's names a profile that may not exist yet, so it offers none.
	_ = authLogoutCmd.RegisterFlagCompletionFunc("profile", cmdutil.CompleteProfiles)
	cmdutil.GroupCmd(authCmd)
	cmdutil.MarkWrite(authLoginCmd, authLogoutCmd)
	authCmd.AddCommand(authLoginCmd, authStatusCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuthLogin(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	nonInteractive := loginWithToken || loginTokenCmd != ""
	if err := checkLoginFlags(nonInteractive); err != nil {
		return err
	}
	// The credential check below honours --base-url and NAMECOM_BASE_URL, so
	// reject a bad one before the form rather than after it.
	if raw, src := loginBaseURL(); raw != "" {
		if err := checkBaseURL(config.SourceName(src), raw); err != nil {
			return cmdutil.NewUsageError(err)
		}
	}

	// --sandbox answers the sandbox question. The form's answer used to start
	// false and the flag was never read, so `auth login --sandbox` saved a
	// production profile unless the user also said Yes at the prompt.
	sandbox := cmdutil.IsSandbox(cmd)

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	// Ask before replacing a profile's credentials, as gh asks before
	// re-authenticating; login used to overwrite them silently (#239). Asked
	// before the form, so a "no" costs no typing. --yes answers it, and
	// --dry-run writes nothing, so neither asks. Without a terminal it cannot
	// be asked, and the command fails asking for --yes.
	if old, ok := cfgFile.Profiles[loginProfile]; ok && !cmdutil.IsYes(cmd) && !cmdutil.IsDryRun(cmd) {
		detail := loginEnv(old.Sandbox)
		if old.Username != "" {
			detail += " · " + old.Username
		}
		replace, cerr := confirmReplaceProfile(out, false,
			fmt.Sprintf("Profile %q already has credentials. Replace them?", loginProfile), detail)
		if cerr != nil {
			return cerr
		}
		if !replace {
			return fmt.Errorf("%w: profile %q left unchanged", cmdutil.ErrAborted, loginProfile)
		}
	}

	var a loginAnswers
	var verifiedAs string
	var verifyErr error
	if nonInteractive {
		a, verifiedAs, verifyErr, err = loginFromFlags(cmd, sandbox)
	} else {
		if err := offerTokenPage(cmd, sandbox); err != nil {
			return err
		}
		a, verifiedAs, verifyErr, err = loginFromForm(cmd, sandbox)
	}
	if err != nil {
		return err
	}
	switch {
	case verifyErr == nil:
	case cmdutil.IsDryRun(cmd):
		out.Warn(fmt.Sprintf("could not verify the credentials: %v", verifyErr))
	case cmdutil.IsYes(cmd):
		return fmt.Errorf("could not verify the credentials, so they were not saved (--yes never saves unverified credentials; --no-verify does): %w", verifyErr)
	case nonInteractive:
		return fmt.Errorf("could not verify the credentials, so they were not saved (pass --no-verify to save them unchecked): %w", verifyErr)
	default:
		out.Warn(fmt.Sprintf("could not verify the credentials: %v", verifyErr))
		ok, cerr := confirmSaveUnverified(out, false, "Save them anyway, unverified?",
			fmt.Sprintf("%s · profile %s (%s)", loginEnv(a.Sandbox), loginProfile, a.Username))
		if cerr != nil {
			return cerr
		}
		if !ok {
			out.Warn("credentials not saved")
			return nil
		}
	}

	if cfgFile.Profiles == nil {
		cfgFile.Profiles = make(map[string]config.Profile)
	}
	saved := config.Profile{
		Username: a.Username,
		Token:    a.Token,
		Sandbox:  a.Sandbox,
	}
	if loginTokenCmd != "" {
		// The command is saved, never the token it printed for the check.
		saved.Token, saved.TokenCmd = "", loginTokenCmd
	}
	cfgFile.Profiles[loginProfile] = saved
	if cfgFile.Default == "" {
		cfgFile.Default = loginProfile
	}
	if cmdutil.IsDryRun(cmd) {
		// The form still runs, so the preview can say what would be saved —
		// everything but the token.
		configcmd.PreviewChange(out, configcmd.Change{
			Action:       "save_profile",
			Profile:      loginProfile,
			Username:     a.Username,
			Sandbox:      &a.Sandbox,
			UsesTokenCmd: saved.TokenCmd != "",
			Default:      cfgFile.Default,
			Summary:      fmt.Sprintf("save profile %q (username %s, %s)", loginProfile, a.Username, api.DefaultBaseURL(a.Sandbox)),
		})
		return nil
	}
	if err := config.Save(cfgFile); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	path, _ := config.ActivePath()
	if verifiedAs != "" {
		out.Success(fmt.Sprintf("Logged in to %s as %s (profile %s)", loginEnv(a.Sandbox), verifiedAs, loginProfile))
		out.Hint("Credentials saved to " + path)
	} else {
		out.Success(fmt.Sprintf("Credentials saved to %s (profile: %s), unverified", path, loginProfile))
	}
	// Name the profile unless it is the one commands will use: after `auth
	// login --profile work` with another default, a bare 'namecom status'
	// showed the default profile's account (#239).
	if config.ActiveProfile(cfgFile, "") == loginProfile {
		out.Hint("Run 'namecom status' to see your account overview")
	} else {
		out.Hint(fmt.Sprintf("Run 'namecom status --profile %s' to see this account, or 'namecom config use %s' to make it the default",
			loginProfile, loginProfile))
	}
	if !nonInteractive {
		out.Hint("Enable tab completion: run 'namecom completion --help' for shell setup instructions")
	}
	return nil
}

// checkLoginFlags rejects flag combinations login cannot act on, before
// anything is read or asked.
func checkLoginFlags(nonInteractive bool) error {
	if gf.token != "" {
		return cmdutil.NewUsageErrorHint(
			errors.New("auth login does not take --token: a token on the command line is kept in shell history and visible to other processes"),
			"pipe the token to 'namecom auth login --username <name> --with-token' instead")
	}
	if loginWithToken && loginTokenCmd != "" {
		return cmdutil.NewUsageError(errors.New("--with-token and --token-cmd cannot be used together: pick one source for the token"))
	}
	if !nonInteractive {
		if !output.IsInteractive() {
			return cmdutil.NewUsageErrorHint(
				errors.New("auth login needs a terminal to ask for credentials"),
				"pass --username with --with-token (token on standard input) or --token-cmd, or set NAMECOM_USERNAME and NAMECOM_TOKEN instead of saving a profile")
		}
		return nil
	}
	flag := "--with-token"
	if loginTokenCmd != "" {
		flag = "--token-cmd"
	}
	if strings.TrimSpace(gf.username) == "" {
		return cmdutil.NewUsageError(fmt.Errorf("%s needs --username", flag))
	}
	if loginTokenCmd != "" && strings.TrimSpace(loginTokenCmd) == "" {
		return cmdutil.NewUsageError(errors.New("--token-cmd is empty"))
	}
	return nil
}

// loginBaseURL is the base URL the credential check uses: --base-url, else
// NAMECOM_BASE_URL, as API commands choose it.
func loginBaseURL() (raw, source string) {
	return config.BaseURLOverride(config.Overrides{BaseURL: gf.baseURL})
}

// offerTokenPage asks whether to open the API token page before the form, so
// someone without a token need not copy the URL out of the terminal (#272).
// Only a person at a terminal is asked: not under --yes or --dry-run, which
// promise not to ask, nor with -q or a structured -o, whose output is for a
// script. A browser that will not open is a warning with the URL, not a
// failure: the form still follows.
//
// name.com has no separate sandbox token page that we know of, so --sandbox
// opens the same one, with a reminder that its credentials are separate.
func offerTokenPage(cmd *cobra.Command, sandbox bool) error {
	out := cmdutil.Out(cmd)
	if cmdutil.IsYes(cmd) || cmdutil.IsDryRun(cmd) || out.QuietMode ||
		out.Format != output.FormatTable || !output.IsInteractive() {
		return nil
	}
	detail := apiSettingsURL
	if sandbox {
		detail += " · sandbox credentials are separate from production ones"
	}
	open, err := askOpenTokenPage("Open the API token page in your browser?", detail)
	if err != nil {
		return cmdutil.FormError(err)
	}
	if !open {
		return nil
	}
	if err := openBrowser(apiSettingsURL); err != nil {
		out.Warn(fmt.Sprintf("could not open a browser (%v); open %s to create a token", err, apiSettingsURL))
		return nil
	}
	out.Hint("Opening " + apiSettingsURL)
	return nil
}

// askOpenTokenPage asks offerTokenPage's question, defaulting to No. Not
// cmdutil.Confirm: that reports Ctrl-C as "No", and here No means "carry on to
// the form", so a cancel has to come back as huh.ErrUserAborted to end the
// command. Replaceable in tests.
var askOpenTokenPage = func(msg, detail string) (bool, error) {
	var open bool
	err := huh.NewForm(huh.NewGroup(huh.NewConfirm().
		Title(msg).
		Description(detail).
		Affirmative("Yes").
		Negative("No").
		Value(&open))).Run()
	return open, err
}

// loginFromForm asks for the credentials and checks them, offering the form
// again after a rejection. verifyErr is a check that could not be made; err
// ends the command.
func loginFromForm(cmd *cobra.Command, sandbox bool) (a loginAnswers, verifiedAs string, verifyErr, err error) {
	out := cmdutil.Out(cmd)
	a = loginAnswers{Sandbox: sandbox}
	for {
		if err := askLogin(&a, !sandbox); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return a, "", nil, cmdutil.ErrAborted
			}
			return a, "", nil, fmt.Errorf("form: %w", err)
		}
		// A token pasted with a trailing space or newline was saved with it,
		// and every request then failed with a 401 (#229).
		a.Username = strings.TrimSpace(a.Username)
		a.Token = strings.TrimSpace(a.Token)
		if loginNoVerify {
			return a, "", nil, nil
		}

		// Check the credentials before saving them (#229). This runs under
		// --dry-run too: Hello is a read that changes nothing, as the reads
		// behind other commands' previews are, and it lets the preview say
		// whether the save would go ahead. Only the write is skipped.
		verifiedAs, err = verifyLogin(cmd, a)
		if err == nil || !isRejected(err) {
			return a, verifiedAs, err, nil
		}
		rejected := rejectedLoginError(err, a)
		// A rejection used to end the command, and the user retyped
		// everything from the start (#239). Offer the form again, with the
		// username and sandbox answer kept. Not under --yes or --dry-run,
		// which promise not to ask.
		if cmdutil.IsYes(cmd) || cmdutil.IsDryRun(cmd) {
			return a, "", nil, rejected
		}
		out.Warn(rejected.Error())
		again, cerr := confirmRetryLogin(out, false, "Try again?", "")
		if cerr != nil {
			return a, "", nil, cerr
		}
		if !again {
			return a, "", nil, rejected
		}
		a.Token = ""
	}
}

// loginFromFlags takes the credentials from --username and --with-token or
// --token-cmd and checks them as the form's are checked, without asking
// anything: a rejection ends the command. --token-cmd's command is run for
// the check, so a helper that fails is found now rather than on the first
// real command.
func loginFromFlags(cmd *cobra.Command, sandbox bool) (a loginAnswers, verifiedAs string, verifyErr, err error) {
	a = loginAnswers{Username: strings.TrimSpace(gf.username), Sandbox: sandbox}
	if loginWithToken {
		if a.Token, err = readLoginToken(cmd.InOrStdin()); err != nil {
			return a, "", nil, err
		}
	}
	if loginNoVerify {
		return a, "", nil, nil
	}
	if loginTokenCmd != "" {
		tok, terr := config.RunTokenCmd(loginTokenCmd)
		if terr != nil {
			return a, "", nil, cmdutil.NewAuthErrorHint(
				fmt.Errorf("--token-cmd failed, so nothing was saved: %w", terr),
				"fix the command, or pass --no-verify to save it without running it now")
		}
		a.Token = tok
	}
	verifiedAs, err = verifyLogin(cmd, a)
	if err != nil && isRejected(err) {
		return a, "", nil, rejectedLoginError(err, a)
	}
	return a, verifiedAs, err, nil
}

// maxLoginToken bounds what --with-token reads: a token is a few dozen bytes,
// and a mistakenly piped file should not be read whole.
const maxLoginToken = 4096

// readLoginToken reads the token --with-token takes from r, trimmed of the
// newline `echo` adds and any surrounding space (#229).
func readLoginToken(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxLoginToken+1))
	if err != nil {
		return "", fmt.Errorf("reading the token from standard input: %w", err)
	}
	if len(data) > maxLoginToken {
		return "", cmdutil.NewUsageError(fmt.Errorf("standard input holds more than %d bytes; --with-token reads only the token", maxLoginToken))
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", cmdutil.NewUsageErrorHint(errors.New("--with-token read no token from standard input"),
			"pipe it in: echo \"$NAMECOM_TOKEN\" | namecom auth login --username <name> --with-token")
	}
	// Never echo it: the input may be a whole file with the token in it.
	if strings.ContainsAny(tok, "\r\n") {
		return "", cmdutil.NewUsageError(errors.New("standard input holds more than one line; --with-token reads only the token"))
	}
	return tok, nil
}

// confirmSaveUnverified asks whether to save credentials the API could not be
// reached to check. Replaceable in tests.
var confirmSaveUnverified = cmdutil.Confirm

// confirmRetryLogin asks whether to try again after the API rejected the
// credentials. Replaceable in tests.
var confirmRetryLogin = cmdutil.Confirm

// confirmReplaceProfile asks before login overwrites an existing profile.
// Replaceable in tests.
var confirmReplaceProfile = cmdutil.Confirm

// verifyLogin checks a's credentials with the API's Hello endpoint, against
// the endpoint the saved profile will use, and returns the username the API
// reports for them.
func verifyLogin(cmd *cobra.Command, a loginAnswers) (string, error) {
	baseURL, _ := loginBaseURL()
	client, err := api.New(api.Options{
		Creds:     config.Credentials{Username: a.Username, Token: a.Token, Sandbox: a.Sandbox},
		UserAgent: "namecom-cli/" + Version,
		Timeout:   gf.timeout,
		BaseURL:   baseURL,
		// One attempt: someone is waiting at the prompt, and if the API cannot
		// be reached they are asked what to do rather than kept waiting.
		MaxRetries: -1,
	})
	if err != nil {
		return "", err
	}
	stop := cmdutil.Out(cmd).Spin("Checking credentials…")
	resp, err := client.SDK().Hello(cmd.Context())
	stop()
	if err != nil {
		return "", api.FromSDKError(err)
	}
	if resp != nil && resp.Username != "" {
		return resp.Username, nil
	}
	return a.Username, nil
}

// isRejected reports whether err is the API refusing the credentials, as
// opposed to failing to answer.
func isRejected(err error) bool {
	apiErr, ok := errors.AsType[*api.APIError](err)
	return ok && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403)
}

// loginRejectedError is a 401 or 403 from the login credential check. It
// unwraps to the API error, so it still exits 3, but carries its own message
// and hint: the generic ones send the user to `auth login`, the command they
// just ran, and do not say which environment refused them.
type loginRejectedError struct {
	msg string
	err error
}

func (e *loginRejectedError) Error() string { return e.msg }
func (e *loginRejectedError) Unwrap() error { return e.err }
func (e *loginRejectedError) UserHint() string {
	return "check the username and token at " + apiSettingsURL + ", then run 'namecom auth login' again"
}

// rejectedLoginError explains a rejection, including the most common cause:
// sandbox and production have separate credentials, and sandbox usernames
// usually end in -test.
func rejectedLoginError(err error, a loginAnswers) error {
	endpoint, _ := loginBaseURL()
	if endpoint == "" {
		endpoint = api.DefaultBaseURL(a.Sandbox)
	}
	msg := fmt.Sprintf("name.com rejected these credentials for %s (%s); nothing was saved", loginEnv(a.Sandbox), endpoint)
	testUser := strings.HasSuffix(a.Username, "-test")
	switch {
	case testUser && !a.Sandbox:
		msg += ". The username ends in -test, like a sandbox account's, and sandbox credentials work only there: try 'namecom auth login --sandbox'"
	case !testUser && a.Sandbox:
		msg += ". Sandbox credentials are separate from production ones, and the username usually ends in -test"
	}
	return &loginRejectedError{msg: msg, err: err}
}

func loginEnv(sandbox bool) string {
	if sandbox {
		return "sandbox"
	}
	return "production"
}

// loginAnswers holds what the login form collects.
type loginAnswers struct {
	Username string
	Token    string
	Sandbox  bool
}

// tokenFieldDescription says where a token comes from; the form used to
// mention only "the API settings page" without saying where that is (#239).
const tokenFieldDescription = "Create one at " + apiSettingsURL +
	"; sandbox has its own. Kept secret in the config file (chmod 600)"

// askLogin runs the login form, filling a. The sandbox question is asked only
// when askSandbox is set; otherwise a.Sandbox is kept as given. It is
// replaceable in tests: the token field is a password input, which huh reads
// from a terminal even in accessible mode.
var askLogin = func(a *loginAnswers, askSandbox bool) error {
	fields := []huh.Field{
		huh.NewInput().
			Title("Username").
			Description("Your name.com API username, shown at " + apiSettingsURL + " (sandbox usernames usually end in -test)").
			Placeholder("yourname").
			Value(&a.Username).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("username is required")
				}
				return nil
			}),

		huh.NewInput().
			Title("API Token").
			Description(tokenFieldDescription).
			Placeholder("••••••••••••••••").
			EchoMode(huh.EchoModePassword).
			Value(&a.Token).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("token is required")
				}
				return nil
			}),
	}
	if askSandbox {
		fields = append(fields, huh.NewConfirm().
			Title("Use sandbox API?").
			Description("Sends requests to api.dev.name.com instead of api.name.com").
			Value(&a.Sandbox))
	}
	return huh.NewForm(huh.NewGroup(fields...)).Run()
}

func runAuthStatus(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	// auth status needs the client; init it explicitly since PersistentPreRunE
	// is skipped for the auth group.
	if err := initContext(cmd); err != nil {
		// The generic auth hint suggests 'namecom auth status' — this command.
		if ae, ok := errors.AsType[*cmdutil.AuthError](err); ok && ae.Hint == "" {
			ae.Hint = "run 'namecom config list-profiles' to see your profiles, or 'namecom auth login' to add one"
		}
		return err
	}
	client := cmdutil.APIClient(cmd)

	stop := out.Spin("Checking credentials…")
	// Hello lives on the SDK's root client rather than a sub-client — it is the
	// API's credential check, not part of a resource group.
	_, err := client.SDK().Hello(cmd.Context())
	stop()

	// Report the identity the Hello call just used, resolved the same way.
	// initContext already resolved it, so an invalid NAMECOM_SANDBOX cannot
	// reach here; ignoring the error keeps the API error below intact.
	id, _ := config.Identity(cmdutil.CfgFile(cmd), cmdutil.Overrides(cmd))
	cfgPath, _ := config.ActivePath()
	if err != nil {
		// The error hints send users here to see which credentials are in
		// use, so a rejection says which ones were rejected, and where each
		// came from, rather than only "Unauthorized" (#187, #246). Wrapped,
		// so the exit code is unchanged.
		var which []string
		if id.Profile != "" {
			which = append(which, fmt.Sprintf("profile %q", id.Profile))
		}
		which = append(which,
			fmt.Sprintf("username %q from %s", id.Username, id.Sources.Username),
			"token from "+id.Sources.Token,
			"endpoint "+client.BaseURL(),
			"config "+cfgPath)
		// The same values as fields of the envelope's "details", so a script
		// need not parse them out of the message (#240). Sources are sibling
		// keys, as in the successful output.
		details := map[string]string{
			"profile":        id.Profile,
			"username":       id.Username,
			"usernameSource": id.Sources.Username,
			"tokenSource":    id.Sources.Token,
			"endpoint":       client.BaseURL(),
			"endpointSource": id.Sources.Endpoint(),
			"config":         cfgPath,
		}
		if id.Sources.Profile != "" {
			details["profileSource"] = id.Sources.Profile
		}
		return &detailedError{
			error:   fmt.Errorf("%w (%s)", api.FromSDKError(err), strings.Join(which, ", ")),
			details: details,
		}
	}

	env := "production"
	if client.BaseURL() == "https://api.dev.name.com" {
		env = "sandbox"
	}

	renderAuthStatus(out, [][]string{
		{"Profile", id.Profile, id.Sources.Profile},
		{"Username", id.Username, id.Sources.Username},
		{"Token", "••••••••", id.Sources.Token},
		{"Environment", env, id.Sources.Sandbox},
		{"Endpoint", client.BaseURL(), id.Sources.Endpoint()},
		{"Config", cfgPath},
	})
	return nil
}

// renderAuthStatus emits the credential summary in whichever format was asked
// for.
//
// This is the command CI runs to verify credentials before doing real work, so
// its output has to be parseable. It previously called out.Success followed by
// out.KVTable — and KVTable has no format guard, so `-o json` produced a JSON
// envelope followed by an ASCII table, which fails on the second document.
//
// Keys are lowercased for the structured views so they read as field names
// rather than display labels.
//
// Quiet prints the username the credentials verified as: the identity, which
// is what a script checking "am I logged in as the right account" compares.
//
// A row's optional third element is where the value came from (#246). The
// structured views carry it as a sibling key, "usernameSource" beside
// "username", so the value keys keep their string type. The Token row's value
// is a mask, so only its source is emitted there.
func renderAuthStatus(out *output.Config, rows [][]string) {
	if out.QuietMode {
		for _, r := range rows {
			if r[0] == "Username" {
				out.Quiet(r[1])
			}
		}
		return
	}
	switch out.Format {
	case output.FormatJSON, output.FormatYAML:
		fields := make(map[string]any, 2*len(rows)+1)
		for _, r := range rows {
			key := strings.ToLower(strings.ReplaceAll(r[0], " ", "_"))
			if len(r) > 2 && r[2] != "" {
				fields[key+"Source"] = r[2]
			}
			if key != "token" {
				fields[key] = r[1]
			}
		}
		// A boolean, not the string "true" it used to be (#187).
		fields["verified"] = true
		if out.Format == output.FormatJSON {
			_ = out.JSON(fields)
			return
		}
		_ = out.YAML(fields)
	default:
		out.Success("Credentials verified")
		table := make([][]string, 0, len(rows))
		for _, r := range rows {
			v := r[1]
			if len(r) > 2 {
				v = configcmd.WithSource(out, v, r[2])
			}
			table = append(table, []string{r[0], v})
		}
		out.KVTable(table)
	}
}

func runAuthLogout(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// The profile API commands would use, so logout removes the credential in
	// use and not, say, prod when NAMECOM_PROFILE names staging.
	profile := config.ActiveProfile(cfgFile, logoutProfile)
	if profile == "" {
		if len(cfgFile.Profiles) == 0 {
			return errors.New("no profiles configured")
		}
		return fmt.Errorf("%d profiles exist but none is the default — pass --profile to choose one", len(cfgFile.Profiles))
	}

	if _, ok := cfgFile.Profiles[profile]; !ok {
		return fmt.Errorf("profile %q not found in config", profile)
	}
	delete(cfgFile.Profiles, profile)
	if cfgFile.Default == profile {
		cfgFile.Default = ""
	}
	if cmdutil.IsDryRun(cmd) {
		// --dry-run describes the removal and keeps the file; it used to
		// delete the profile.
		configcmd.PreviewChange(out, configcmd.Change{
			Action:  "remove_profile",
			Profile: profile,
			Default: cfgFile.Default,
			Summary: fmt.Sprintf("remove profile %q", profile),
		})
		return nil
	}
	if err := config.Save(cfgFile); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	out.Success(fmt.Sprintf("Removed profile %q", profile))
	return nil
}
