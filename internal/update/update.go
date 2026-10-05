// Package update checks for newer releases on GitHub and returns a
// human-readable notification string when one is available.
// Checks are cached for 24 hours so the network is only hit once per day.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	releaseURL  = "https://api.github.com/repos/patramsey/namecom-cli/releases/latest"
	cacheTTL    = 24 * time.Hour
	httpTimeout = 2 * time.Second
)

type versionCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// Check returns a non-empty notification string when a newer version than
// current is available. Returns "" on any error or when up to date.
// current should be the bare version without a leading "v" (e.g. "1.2.3").
// When current is "dev" (a local build), or NAMECOM_NO_UPDATE_NOTIFIER turns
// the notice off, the check is skipped.
func Check(current string) string {
	if current == "" || current == "dev" || Disabled() {
		return ""
	}
	// A `git describe` build — "v0.4.0-2-g48cf186", or anything "-dirty" — is
	// not the release it is derived from, and semver orders it *below* that
	// release even though in git terms it is ahead. Comparing would tell
	// someone who just built HEAD to "upgrade" to the version they are already
	// past. There is nothing useful to say about an untagged build, so say
	// nothing.
	if semver.Prerelease("v"+strings.TrimPrefix(current, "v")) != "" {
		return ""
	}
	latest, err := latestVersion()
	if err != nil || latest == "" {
		return ""
	}
	if isNewer(latest, current) {
		return notice(latest, current, upgradeHint(executable()))
	}
	return ""
}

// DisableEnv names the variable that turns the update notice off.
const DisableEnv = "NAMECOM_NO_UPDATE_NOTIFIER"

// Disabled reports whether DisableEnv is set to anything but an explicit
// "off" value (0, false, no, off). There was no way to turn the notice off.
func Disabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(DisableEnv))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// notice is the text Check returns. It used to say only "see
// github.com/…/releases", leaving the user to work out how their copy was
// installed and how to upgrade it; gh prints the exact command.
func notice(latest, current, upgrade string) string {
	return fmt.Sprintf("A new release of namecom is available: v%s → v%s\n%s\nSet %s=1 to turn this notice off.",
		strings.TrimPrefix(current, "v"), strings.TrimPrefix(latest, "v"), upgrade, DisableEnv)
}

// executable is the running binary's path with symlinks resolved, so a
// Homebrew install is seen in its Cellar rather than at the bin/ link.
func executable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// upgradeHint says how to upgrade the binary at exe, judged from where it is
// installed: Homebrew keeps formulas under a Cellar directory, and `go
// install` writes to GOBIN or GOPATH/bin, naming the binary after the module
// (namecom-cli). Anything else is taken to be a downloaded release archive.
func upgradeHint(exe string) string {
	if exe == "" {
		return releasesHint
	}
	slashed := filepath.ToSlash(exe)
	if strings.Contains(slashed, "/Cellar/") {
		return "To upgrade, run: brew upgrade namecom"
	}
	if strings.HasPrefix(strings.ToLower(filepath.Base(exe)), "namecom-cli") || inGoBin(filepath.Dir(exe)) {
		return "To upgrade, run: go install github.com/patramsey/namecom-cli@latest"
	}
	return releasesHint
}

const releasesHint = "To upgrade, download it from https://github.com/patramsey/namecom-cli/releases/latest"

// inGoBin reports whether dir is where `go install` puts binaries: GOBIN, or
// bin under each GOPATH entry (by default ~/go). It reads the environment
// rather than running `go env`, which may not be installed.
func inGoBin(dir string) bool {
	var dirs []string
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		dirs = append(dirs, gobin)
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			gopath = filepath.Join(home, "go")
		}
	}
	for _, p := range filepath.SplitList(gopath) {
		if p != "" {
			dirs = append(dirs, filepath.Join(p, "bin"))
		}
	}
	for _, d := range dirs {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

func latestVersion() (string, error) {
	if v, ok := readCache(); ok {
		return v, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	version := strings.TrimPrefix(release.TagName, "v")
	writeCache(version)
	return version, nil
}

func cacheFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "namecom", "version_check.json")
}

func readCache() (string, bool) {
	path := cacheFile()
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is cacheFile(), derived from os.UserCacheDir() — never external input
	if err != nil {
		return "", false
	}
	var c versionCache
	if err := json.Unmarshal(data, &c); err != nil {
		return "", false
	}
	if time.Since(c.CheckedAt) > cacheTTL {
		return "", false
	}
	return c.Latest, c.Latest != ""
}

func writeCache(version string) {
	path := cacheFile()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	data, _ := json.Marshal(versionCache{CheckedAt: time.Now(), Latest: version})
	_ = os.WriteFile(path, data, 0o600)
}

// isNewer returns true if candidate is a strictly higher semver than current.
// Both values are expected without a leading "v"; this function adds it for
// golang.org/x/mod/semver which requires canonical "vX.Y.Z" form.
// isNewer reports whether candidate is a later release than current.
//
// Both sides are normalised to a single leading "v" rather than having one
// prepended blindly. The two build paths disagree about the prefix: goreleaser
// sets main.version from {{.Version}} ("0.4.0"), while the Makefile sets it
// from `git describe` ("v0.4.0-2-g48cf186"). Prepending to the latter produced
// "vv0.4.0-…", which is not valid semver, so this returned false and the
// upgrade notice silently never appeared for locally built binaries.
//
// Anything that is not valid semver on either side reports false. A version
// this cannot parse is not grounds for telling someone to upgrade.
func isNewer(candidate, current string) bool {
	c := "v" + strings.TrimPrefix(candidate, "v")
	cur := "v" + strings.TrimPrefix(current, "v")
	if !semver.IsValid(c) || !semver.IsValid(cur) {
		return false
	}
	return semver.Compare(c, cur) > 0
}
