package cmd

import (
	"errors"
	"fmt"
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
	Short: "Configure credentials interactively",
	Long: `Asks for your name.com API username and token, checks them with the API,
and saves them to a profile in the config file.

Create a token at ` + apiSettingsURL + `.
Sandbox credentials are separate from production ones, and the sandbox
username usually ends in -test; log in to the sandbox with --sandbox.`,
	Example: `  namecom auth login
  namecom auth login --profile staging
  namecom auth login --profile sandbox --sandbox`,
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

func init() {
	authLoginCmd.Flags().StringVar(&loginProfile, "profile", "default", "profile name to save credentials under")
	authLogoutCmd.Flags().StringVar(&logoutProfile, "profile", "", "profile to remove (defaults to the active profile)")
	// logout's local --profile shadows the global one, completion included.
	// login's names a profile that may not exist yet, so it offers none.
	_ = authLogoutCmd.RegisterFlagCompletionFunc("profile", cmdutil.CompleteProfiles)
	cmdutil.GroupCmd(authCmd)
	authCmd.AddCommand(authLoginCmd, authStatusCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuthLogin(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	if !output.IsInteractive() {
		return fmt.Errorf("auth login requires an interactive terminal; " +
			"set credentials via NAMECOM_USERNAME and NAMECOM_TOKEN environment variables instead")
	}
	// The credential check below honours --base-url, so reject a bad one
	// before the form rather than after it.
	if gf.baseURL != "" {
		if err := validateBaseURL(gf.baseURL); err != nil {
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
	// --dry-run writes nothing, so neither asks.
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
			out.Warn(fmt.Sprintf("profile %q left unchanged", loginProfile))
			return nil
		}
	}

	a := loginAnswers{Sandbox: sandbox}
	var verifiedAs string
	for {
		if err := askLogin(&a, !sandbox); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				out.Warn("aborted")
				return nil
			}
			return fmt.Errorf("form: %w", err)
		}
		// A token pasted with a trailing space or newline was saved with it,
		// and every request then failed with a 401 (#229).
		a.Username = strings.TrimSpace(a.Username)
		a.Token = strings.TrimSpace(a.Token)

		// Check the credentials before saving them (#229). This runs under
		// --dry-run too: Hello is a read that changes nothing, as the reads
		// behind other commands' previews are, and it lets the preview say
		// whether the save would go ahead. Only the write below is skipped.
		verifiedAs, err = verifyLogin(cmd, a)
		if err == nil || !isRejected(err) {
			break
		}
		rejected := rejectedLoginError(err, a)
		// A rejection used to end the command, and the user retyped
		// everything from the start (#239). Offer the form again, with the
		// username and sandbox answer kept. Not under --yes or --dry-run,
		// which promise not to ask.
		if cmdutil.IsYes(cmd) || cmdutil.IsDryRun(cmd) {
			return rejected
		}
		out.Warn(rejected.Error())
		again, cerr := confirmRetryLogin(out, false, "Try again?", "")
		if cerr != nil {
			return cerr
		}
		if !again {
			return rejected
		}
		a.Token = ""
	}
	switch {
	case err == nil:
	case cmdutil.IsDryRun(cmd):
		out.Warn(fmt.Sprintf("could not verify the credentials: %v", err))
	case cmdutil.IsYes(cmd):
		return fmt.Errorf("could not verify the credentials, so they were not saved (--yes never saves unverified credentials): %w", err)
	default:
		out.Warn(fmt.Sprintf("could not verify the credentials: %v", err))
		env := "production"
		if a.Sandbox {
			env = "sandbox"
		}
		ok, cerr := confirmSaveUnverified(out, false, "Save them anyway, unverified?",
			fmt.Sprintf("%s · profile %s (%s)", env, loginProfile, a.Username))
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
	cfgFile.Profiles[loginProfile] = config.Profile{
		Username: a.Username,
		Token:    a.Token,
		Sandbox:  a.Sandbox,
	}
	if cfgFile.Default == "" {
		cfgFile.Default = loginProfile
	}
	if cmdutil.IsDryRun(cmd) {
		// The form still runs, so the preview can say what would be saved —
		// everything but the token.
		configcmd.PreviewChange(out, configcmd.Change{
			Action:   "save_profile",
			Profile:  loginProfile,
			Username: a.Username,
			Sandbox:  &a.Sandbox,
			Default:  cfgFile.Default,
			Summary:  fmt.Sprintf("save profile %q (username %s, %s)", loginProfile, a.Username, api.DefaultBaseURL(a.Sandbox)),
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
	out.Hint("Enable tab completion: run 'namecom completion --help' for shell setup instructions")
	return nil
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
	client, err := api.New(api.Options{
		Creds:     config.Credentials{Username: a.Username, Token: a.Token, Sandbox: a.Sandbox},
		UserAgent: "namecom-cli/" + Version,
		Timeout:   gf.timeout,
		BaseURL:   gf.baseURL,
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
	endpoint := gf.baseURL
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
	if err != nil {
		// The error hints send users here to see which credentials are in
		// use, so a rejection says which ones were rejected rather than only
		// "Unauthorized" (#187). Wrapped, so the exit code is unchanged.
		cfgPath, _ := config.ActivePath()
		return fmt.Errorf("%w (profile %q, username %q, endpoint %s, config %s)",
			api.FromSDKError(err), id.Profile, id.Username, client.BaseURL(), cfgPath)
	}

	env := "production"
	if client.BaseURL() == "https://api.dev.name.com" {
		env = "sandbox"
	}

	cfgPath, _ := config.ActivePath()

	renderAuthStatus(out, [][]string{
		{"Profile", id.Profile},
		{"Username", id.Username},
		{"Environment", env},
		{"Endpoint", client.BaseURL()},
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
		fields := make(map[string]any, len(rows)+1)
		for _, r := range rows {
			fields[strings.ToLower(strings.ReplaceAll(r[0], " ", "_"))] = r[1]
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
		out.KVTable(rows)
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
