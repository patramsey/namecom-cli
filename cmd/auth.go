package cmd

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage name.com API credentials",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Configure credentials interactively",
	Example: `  namecom auth login
  namecom auth login --profile staging`,
	RunE:  runAuthLogin,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Verify credentials by calling the API hello endpoint",
	Example: `  namecom auth status
  namecom auth status --profile staging`,
	RunE:  runAuthStatus,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove credentials for the active profile",
	Example: `  namecom auth logout
  namecom auth logout --profile staging`,
	RunE:  runAuthLogout,
}

var loginProfile string
var logoutProfile string

func init() {
	authLoginCmd.Flags().StringVar(&loginProfile, "profile", "default", "profile name to save credentials under")
	authLogoutCmd.Flags().StringVar(&logoutProfile, "profile", "", "profile to remove (defaults to the active profile)")
	authCmd.AddCommand(authLoginCmd, authStatusCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuthLogin(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	if !output.IsInteractive() {
		return fmt.Errorf("auth login requires an interactive terminal; " +
			"set credentials via NAMECOM_USERNAME and NAMECOM_TOKEN environment variables instead")
	}

	var (
		username string
		token    string
		sandbox  bool
	)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Username").
				Description("Your name.com API username (shown in the API settings page)").
				Placeholder("yourname").
				Value(&username).
				Validate(func(s string) error {
					if s == "" {
						return errors.New("username is required")
					}
					return nil
				}),

			huh.NewInput().
				Title("API Token").
				Description("Your name.com API token — kept secret in the config file (chmod 600)").
				Placeholder("••••••••••••••••").
				EchoMode(huh.EchoModePassword).
				Value(&token).
				Validate(func(s string) error {
					if s == "" {
						return errors.New("token is required")
					}
					return nil
				}),

			huh.NewConfirm().
				Title("Use sandbox API?").
				Description("Sends requests to api.dev.name.com instead of api.name.com").
				Value(&sandbox),
		),
	)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			out.Warn("aborted")
			return nil
		}
		return fmt.Errorf("form: %w", err)
	}

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfgFile.Profiles == nil {
		cfgFile.Profiles = make(map[string]config.Profile)
	}
	cfgFile.Profiles[loginProfile] = config.Profile{
		Username: username,
		Token:    token,
		Sandbox:  sandbox,
	}
	if cfgFile.Default == "" {
		cfgFile.Default = loginProfile
	}
	if err := config.Save(cfgFile); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	path, _ := config.Path()
	out.Success(fmt.Sprintf("Credentials saved to %s (profile: %s)", path, loginProfile))
	out.Hint("Run 'namecom status' to see your account overview")
	out.Hint("Enable tab completion: run 'namecom completion --help' for shell setup instructions")
	return nil
}

func runAuthStatus(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	// auth status needs the client; init it explicitly since PersistentPreRunE
	// is skipped for the auth group.
	if err := initContext(cmd); err != nil {
		return err
	}
	client := cmdutil.APIClient(cmd)
	resp, err := client.Gen().Hello(cmd.Context())
	if err != nil {
		return err
	}
	if err := api.Decode(resp, nil); err != nil {
		return err
	}

	ov := cmdutil.Overrides(cmd)
	cfgFile := cmdutil.CfgFile(cmd)
	profileName := cfgFile.Default
	if profileName == "" {
		profileName = "default"
	}
	if ov.Profile != "" {
		profileName = ov.Profile
	}
	baseURL := client.BaseURL()
	username := ""
	if p, ok := cfgFile.Profiles[profileName]; ok {
		username = p.Username
	}
	if ov.Username != "" {
		username = ov.Username
	}
	msg := fmt.Sprintf("Authenticated as %s (profile: %s, endpoint: %s)", username, profileName, baseURL)
	if username == "" {
		msg = fmt.Sprintf("Authenticated (profile: %s, endpoint: %s)", profileName, baseURL)
	}
	out.Success(msg)
	return nil
}

func runAuthLogout(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Determine active profile: --profile flag > config default > "default".
	profile := logoutProfile
	if profile == "" {
		profile = cfgFile.Default
	}
	if profile == "" {
		profile = "default"
	}

	if _, ok := cfgFile.Profiles[profile]; !ok {
		return fmt.Errorf("profile %q not found in config", profile)
	}
	delete(cfgFile.Profiles, profile)
	if cfgFile.Default == profile {
		cfgFile.Default = ""
	}
	if err := config.Save(cfgFile); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	out.Success(fmt.Sprintf("Removed profile %q", profile))
	return nil
}
