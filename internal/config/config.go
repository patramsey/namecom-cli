// Package config loads name.com CLI credentials from a profile-based config
// file and resolves them against environment variables and command-line flags.
//
// Resolution precedence (highest first):
//
//  1. Explicit flags (--username, --token, --sandbox)
//  2. Environment variables (NAMECOM_USERNAME, NAMECOM_TOKEN, NAMECOM_SANDBOX)
//  3. The active profile selected by --profile or NAMECOM_PROFILE
//  4. The default profile recorded in the config file
//
// Resolution is implemented by hand rather than via Viper's AutomaticEnv to
// avoid surprising precedence with nested profile keys.
package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile is a single named set of credentials in the config file.
type Profile struct {
	Username string `yaml:"username"`
	Token    string `yaml:"token,omitempty"`
	// TokenCmd, when set and no token is otherwise available, is executed via
	// the shell and its trimmed stdout is used as the token. This keeps the
	// secret out of the config file (e.g. `op read op://vault/namecom/token`).
	TokenCmd string `yaml:"token_cmd,omitempty"`
	Sandbox  bool   `yaml:"sandbox,omitempty"`
}

// File is the on-disk config structure.
type File struct {
	Default  string             `yaml:"default"`
	Profiles map[string]Profile `yaml:"profiles"`
	// Icons selects the status-icon style for the interactive `browse` TUI:
	// "nerd" for Nerd Font glyphs, "ascii" (or empty) for the universal
	// fallback. Overridden by --icons / NAMECOM_ICONS.
	Icons string `yaml:"icons,omitempty"`
}

// Overrides carries values supplied by global CLI flags. Zero values mean
// "not set" except for Sandbox, whose presence is tracked by SandboxSet
// because false is a meaningful explicit value.
type Overrides struct {
	Profile    string
	Username   string
	Token      string
	Sandbox    bool
	SandboxSet bool
}

// Credentials is the fully resolved result handed to the API client.
type Credentials struct {
	Username string
	Token    string
	Sandbox  bool
	// Profile is the name of the profile that was selected, for diagnostics.
	Profile string
}

// ErrNoCredentials is returned when no username/token can be resolved from any
// source. Callers in TTY mode may offer to run `namecom auth login`.
var ErrNoCredentials = errors.New("no credentials configured")

// Path returns the config file path, honoring NAMECOM_CONFIG, then XDG
// (~/.config/namecom/config.yaml). The returned path is where new config is
// written; see resolveReadPath for read-time legacy fallback.
func Path() (string, error) {
	if p := os.Getenv("NAMECOM_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir() // respects XDG_CONFIG_HOME on Unix
	if err != nil {
		return "", fmt.Errorf("locating config dir: %w", err)
	}
	return filepath.Join(dir, "namecom", "config.yaml"), nil
}

// legacyPath is the pre-XDG location, read as a fallback for existing users.
func legacyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".namecom", "config.yaml"), nil
}

// resolveReadPath returns the path to read config from: the primary path if it
// exists, otherwise the legacy path if that exists, otherwise the primary path
// (so the "not found" path is the canonical one).
func resolveReadPath() (string, error) {
	primary, err := Path()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(primary); err == nil {
		return primary, nil
	}
	if legacy, lerr := legacyPath(); lerr == nil {
		if _, serr := os.Stat(legacy); serr == nil {
			return legacy, nil
		}
	}
	return primary, nil
}

// Load reads and parses the config file. A missing file is not an error: it
// returns an empty File so credentials can still come from flags or env.
func Load() (*File, error) {
	path, err := resolveReadPath()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{Profiles: map[string]Profile{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	// Warn (but do not fail) if the file is group/world accessible — it may
	// hold a plaintext token.
	if info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(os.Stderr, "warning: %s is accessible by other users (mode %#o); consider `chmod 600 %s`\n",
			path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	return &f, nil
}

// Save writes the config file to the primary (XDG) path with 0600 permissions,
// creating the parent directory as needed.
func Save(f *File) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	return nil
}

// Resolve merges the config file, environment, and flag overrides into a final
// set of credentials following the documented precedence.
func Resolve(f *File, ov Overrides) (Credentials, error) {
	if f == nil {
		f = &File{Profiles: map[string]Profile{}}
	}

	// Select the active profile name.
	profileName := firstNonEmpty(ov.Profile, os.Getenv("NAMECOM_PROFILE"), f.Default)
	prof := f.Profiles[profileName] // zero Profile if absent

	creds := Credentials{Profile: profileName}

	// Username: flag > env > profile.
	creds.Username = firstNonEmpty(ov.Username, os.Getenv("NAMECOM_USERNAME"), prof.Username)

	// Token: flag > env > profile.token > profile.token_cmd.
	creds.Token = firstNonEmpty(ov.Token, os.Getenv("NAMECOM_TOKEN"), prof.Token)
	if creds.Token == "" && prof.TokenCmd != "" {
		tok, err := runTokenCmd(prof.TokenCmd)
		if err != nil {
			return Credentials{}, fmt.Errorf("profile %q token_cmd: %w", profileName, err)
		}
		creds.Token = tok
	}

	// Sandbox: explicit flag > env > profile.
	switch {
	case ov.SandboxSet:
		creds.Sandbox = ov.Sandbox
	case os.Getenv("NAMECOM_SANDBOX") != "":
		creds.Sandbox = truthy(os.Getenv("NAMECOM_SANDBOX"))
	default:
		creds.Sandbox = prof.Sandbox
	}

	if creds.Username == "" || creds.Token == "" {
		return Credentials{}, ErrNoCredentials
	}
	return creds, nil
}

// runTokenCmd executes the token command through the shell and returns its
// trimmed stdout.
func runTokenCmd(cmdline string) (string, error) {
	cmd := exec.Command("sh", "-c", cmdline)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", errors.New("produced empty output")
	}
	return tok, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truthy(s string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(s))
	return err == nil && b
}
