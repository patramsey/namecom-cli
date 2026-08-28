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
// When current is "dev" (a local build), the check is skipped.
func Check(current string) string {
	if current == "" || current == "dev" {
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
		return fmt.Sprintf(
			"A newer version is available: v%s  (current: v%s) — see github.com/patramsey/namecom-cli/releases",
			latest, current,
		)
	}
	return ""
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
