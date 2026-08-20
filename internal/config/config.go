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
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
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

// Path returns the config file path, honoring NAMECOM_CONFIG, then the
// platform user config directory. That directory is NOT ~/.config everywhere:
// os.UserConfigDir gives ~/Library/Application Support on darwin,
// $XDG_CONFIG_HOME (or ~/.config) on unix, and %AppData% on windows. Saying
// "XDG" here previously read as a promise of ~/.config on every platform,
// which sent people looking for their credentials in a directory macOS never
// uses. The returned path is where new config is written; see resolveReadPath
// for the read-time legacy fallback.
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
	// An explicit NAMECOM_CONFIG is absolute: never fall back to the legacy
	// location. That variable exists to isolate a run (CI, tests, a scratch
	// profile), and falling back on a missing file silently pointed those runs
	// at the user's real — possibly production — credentials.
	if os.Getenv("NAMECOM_CONFIG") != "" {
		return primary, nil
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

// ActivePath returns the config file this process will actually read from and
// write to.
//
// Path() reports the canonical (XDG) location, which is where NEW config is
// created — but Load and Save both go through resolveReadPath, which prefers an
// existing legacy ~/.namecom/config.yaml. Reporting Path() to the user in that
// situation names a file the CLI is not using, so anyone following the message
// looks in the wrong place.
func ActivePath() (string, error) {
	return resolveReadPath()
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
	// Warn (but do not fail) if the file is group/world accessible — it may hold
	// a plaintext token. Only when stderr is a terminal: this is raw text
	// written ahead of the structured error envelope, so emitting it into a
	// pipe corrupts stderr for anything parsing it. A human sees it; a script
	// gets clean output. (Save() now repairs the mode on the next write.)
	if info.Mode().Perm()&0o077 != 0 && term.IsTerminal(int(os.Stderr.Fd())) {
		fmt.Fprintf(os.Stderr, "warning: %s is accessible by other users (mode %#o); consider `chmod 600 %s`\n",
			path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is this tool's own config location (XDG, legacy, or NAMECOM_CONFIG), never an API response
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

// Save writes the config file with 0600 permissions, creating the parent
// directory as needed.
//
// It writes back to the same file Load reads. Writing unconditionally to the
// XDG path while Load honored the legacy fallback forked the config in two:
// `auth logout` removed a profile, wrote an empty file to the XDG path, and
// reported success while the credential stayed readable in the legacy file —
// now invisible, because the new empty file shadowed it.
func Save(f *File) error {
	path, err := resolveReadPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := encodeConfig(path, f)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	// WriteFile only applies its mode when creating the file, so an existing
	// world-readable config stayed that way — and we'd just written a token into
	// it. Load warns about loose permissions; this is what actually fixes them.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("securing config %s: %w", path, err)
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
	profileName := firstNonEmpty(ov.Profile, os.Getenv("NAMECOM_PROFILE"), f.Default, impliedDefault(f))
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
		// Distinguish "nothing is configured" from "several profiles exist and
		// none is marked default" — the second needs a different fix, and the
		// generic message sent people to `auth login`, which overwrites.
		if profileName == "" && len(f.Profiles) > 1 {
			names := make([]string, 0, len(f.Profiles))
			for n := range f.Profiles {
				names = append(names, n)
			}
			sort.Strings(names)
			return Credentials{}, fmt.Errorf(
				"%w: %d profiles exist (%s) but none is the default — pass --profile, set NAMECOM_PROFILE, or run 'namecom config use <profile>'",
				ErrNoCredentials, len(names), strings.Join(names, ", "))
		}
		return Credentials{}, ErrNoCredentials
	}
	return creds, nil
}

// impliedDefault names the profile to use when nothing selected one: no
// --profile, no NAMECOM_PROFILE, and no top-level `default:` key in the file.
//
// Without it the name resolved to "", f.Profiles[""] returned the zero Profile,
// and the CLI reported "no credentials configured — run 'namecom auth login'"
// while sitting on a perfectly good profile that `config list-profiles` was
// happily printing. `auth login` writes the `default:` key, so this only bit
// hand-edited files — which is exactly how token_cmd has to be configured.
//
// A profile actually named "default" wins; failing that, a lone profile is
// unambiguous enough to use. Two or more unnamed candidates stay an error,
// because guessing between them is worse than saying so.
func impliedDefault(f *File) string {
	if _, ok := f.Profiles["default"]; ok {
		return "default"
	}
	if len(f.Profiles) == 1 {
		for name := range f.Profiles {
			return name
		}
	}
	return ""
}

// tokenCmdTimeout bounds how long a credential helper may run. Generous enough
// for an interactive unlock (biometric prompt, hardware key touch) but finite:
// unbounded, a helper blocked on a locked vault or a prompt with no TTY hung
// the CLI forever, and --timeout covers only HTTP. Overridable in tests.
var tokenCmdTimeout = 15 * time.Second

// runTokenCmd executes the token command through the shell and returns its
// trimmed stdout.
func runTokenCmd(cmdline string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tokenCmdTimeout)
	defer cancel()

	// G204: running a shell string is the feature, not a lapse. token_cmd exists
	// so a token can come from `op read ...` or `pass show ...` instead of living
	// in the config file, and those invocations need pipes and quoting. cmdline
	// comes only from the user's own config file — never from a flag, an
	// environment variable, or an API response — so anyone who can set it can
	// already run commands as this user. The mitigations that do apply are the
	// timeout above and the process group below.
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline) //nolint:gosec
	cmd.Stderr = os.Stderr
	setProcessGroup(cmd)
	// WaitDelay bounds how long Wait blocks AFTER the deadline kills the shell.
	//
	// CommandContext only kills the direct child. A helper written as a pipeline
	// — `op read … | tr -d '\n'`, `vault read … | jq -r .token`, which is how
	// most of them are written — forks grandchildren that inherit the stdout
	// pipe, and cmd.Output() keeps reading that pipe long after sh is gone.
	// Without this the timeout bounded nothing for exactly the common case: it
	// only appeared to work when the shell exec'd itself into a single command.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if ctx.Err() != nil {
		// Report the deadline explicitly. The bare exec error here is a killed
		// signal, which surfaced as a misleading "produced empty output".
		return "", fmt.Errorf("timed out after %s (is it waiting on a prompt?)", tokenCmdTimeout)
	}
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

// encodeConfig serializes f, preserving anything already in the file that the
// File struct does not model.
//
// Marshalling the struct directly discards every key it does not know about —
// comments, a `theme` used by the sibling TUI, per-profile fields written by
// other tooling — the first time the CLI writes the file. The user is never
// told; the data is just gone on the next `auth login` or `config use`.
//
// So the existing document is decoded into a node tree, the keys this package
// owns are updated in place, and the rest is written back untouched. If the
// file is absent or unparseable there is nothing to preserve, and a plain
// marshal is correct.
func encodeConfig(path string, f *File) ([]byte, error) {
	existing, err := os.ReadFile(path) //nolint:gosec // G304: same config path the caller is about to write back to
	if err != nil {
		return marshalConfig(f)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil || len(doc.Content) == 0 {
		return marshalConfig(f)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return marshalConfig(f)
	}

	// Re-encode the managed fields, then graft each one onto the existing tree.
	managed, err := marshalConfig(f)
	if err != nil {
		return nil, err
	}
	var newDoc yaml.Node
	if err := yaml.Unmarshal(managed, &newDoc); err != nil || len(newDoc.Content) == 0 {
		return marshalConfig(f)
	}
	newRoot := newDoc.Content[0]

	for i := 0; i+1 < len(newRoot.Content); i += 2 {
		key, val := newRoot.Content[i], newRoot.Content[i+1]
		if key.Value == "profiles" {
			// Merge profile-by-profile so per-profile keys we do not model
			// (region, notes, whatever) survive on profiles that still exist.
			mergeProfiles(root, val)
			continue
		}
		setMapValue(root, key.Value, val)
	}
	// Profiles removed from f (e.g. by `auth logout`) must disappear from the
	// file too, or a "deleted" credential stays on disk.
	pruneProfiles(root, f.Profiles)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	return buf.Bytes(), nil
}

func marshalConfig(f *File) ([]byte, error) {
	data, err := yaml.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	return data, nil
}

// setMapValue replaces the value for key, or appends the pair if absent.
// Replacing the value node in place keeps the key's own comments attached.
func setMapValue(mapping *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			// Carry over any comments already on the value being replaced.
			val.HeadComment = mapping.Content[i+1].HeadComment
			val.LineComment = mapping.Content[i+1].LineComment
			val.FootComment = mapping.Content[i+1].FootComment
			mapping.Content[i+1] = val
			return
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	mapping.Content = append(mapping.Content, k, val)
}

// mergeProfiles updates each profile's managed fields while leaving unknown
// per-profile keys in place.
func mergeProfiles(root, newProfiles *yaml.Node) {
	existing := mapValue(root, "profiles")
	if existing == nil || existing.Kind != yaml.MappingNode {
		setMapValue(root, "profiles", newProfiles)
		return
	}
	for i := 0; i+1 < len(newProfiles.Content); i += 2 {
		name, fields := newProfiles.Content[i], newProfiles.Content[i+1]
		target := mapValue(existing, name.Value)
		if target == nil || target.Kind != yaml.MappingNode {
			setMapValue(existing, name.Value, fields)
			continue
		}
		for j := 0; j+1 < len(fields.Content); j += 2 {
			setMapValue(target, fields.Content[j].Value, fields.Content[j+1])
		}
	}
}

// pruneProfiles removes profiles that are no longer in f, so `auth logout`
// actually deletes the credential rather than leaving it behind.
func pruneProfiles(root *yaml.Node, keep map[string]Profile) {
	profiles := mapValue(root, "profiles")
	if profiles == nil || profiles.Kind != yaml.MappingNode {
		return
	}
	var kept []*yaml.Node
	for i := 0; i+1 < len(profiles.Content); i += 2 {
		if _, ok := keep[profiles.Content[i].Value]; ok {
			kept = append(kept, profiles.Content[i], profiles.Content[i+1])
		}
	}
	profiles.Content = kept
}

func mapValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}
