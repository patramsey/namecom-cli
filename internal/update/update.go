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
	"strconv"
	"strings"
	"time"
)

const (
	releaseURL = "https://api.github.com/repos/patramsey/namecom-cli/releases/latest"
	cacheTTL   = 24 * time.Hour
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
	data, err := os.ReadFile(path)
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
func isNewer(candidate, current string) bool {
	c := parseSemver(candidate)
	cur := parseSemver(current)
	for i := 0; i < len(c); i++ {
		if i >= len(cur) {
			return c[i] > 0
		}
		if c[i] > cur[i] {
			return true
		}
		if c[i] < cur[i] {
			return false
		}
	}
	return false
}

func parseSemver(v string) []int {
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}
