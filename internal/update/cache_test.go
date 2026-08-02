package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// isolateConfigDir points os.UserConfigDir() at a temp directory. The variable
// it reads differs by platform: HOME on darwin ($HOME/Library/Application
// Support), XDG_CONFIG_HOME on unix. Setting both keeps this test honest on
// whichever one is running it rather than passing vacuously on the other.
func isolateConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", dir)
	}
	got := cacheFile()
	if got == "" {
		t.Fatal("cacheFile() is empty after isolating the config dir")
	}
	return got
}

func TestCacheFile_UnderConfigDir(t *testing.T) {
	path := isolateConfigDir(t)
	if filepath.Base(path) != "version_check.json" {
		t.Errorf("cache file = %q, want it to be named version_check.json", path)
	}
	if filepath.Base(filepath.Dir(path)) != "namecom" {
		t.Errorf("cache file = %q, want it under a namecom/ directory", path)
	}
}

// The round trip is the contract: what writeCache stores, readCache returns.
func TestCache_RoundTrip(t *testing.T) {
	isolateConfigDir(t)

	if got, ok := readCache(); ok {
		t.Fatalf("readCache on an empty dir = (%q, true), want ok=false", got)
	}

	writeCache("1.2.3")

	got, ok := readCache()
	if !ok {
		t.Fatal("readCache after writeCache = ok:false, want the value back")
	}
	if got != "1.2.3" {
		t.Errorf("readCache = %q, want %q", got, "1.2.3")
	}
}

// The cache exists to keep the CLI off the network for a day. An entry older
// than the TTL must be ignored, or the check never refreshes; an entry inside
// it must be honored, or the cache does nothing and every invocation hits
// GitHub.
func TestCache_TTL(t *testing.T) {
	tests := []struct {
		name    string
		age     time.Duration
		wantOK  bool
		wantVal string
	}{
		{"fresh entry is used", time.Hour, true, "1.2.3"},
		{"just inside the TTL is used", cacheTTL - time.Minute, true, "1.2.3"},
		{"just past the TTL is ignored", cacheTTL + time.Minute, false, ""},
		{"ancient entry is ignored", 30 * 24 * time.Hour, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := isolateConfigDir(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			body, err := json.Marshal(versionCache{
				CheckedAt: time.Now().Add(-tt.age),
				Latest:    "1.2.3",
			})
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			got, ok := readCache()
			if ok != tt.wantOK {
				t.Errorf("readCache ok = %v, want %v (age %s, TTL %s)", ok, tt.wantOK, tt.age, cacheTTL)
			}
			if got != tt.wantVal {
				t.Errorf("readCache = %q, want %q", got, tt.wantVal)
			}
		})
	}
}

// Every failure path in readCache is deliberately silent — a broken cache must
// degrade to "no cached value", never to an error surfacing in the CLI or a
// bogus version being reported.
func TestReadCache_BadInputIsSilentlyIgnored(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"not json", "this is not json"},
		{"truncated json", `{"checked_at":`},
		{"wrong shape", `[1,2,3]`},
		{"empty file", ""},
		{"valid json, empty version", `{"checked_at":"` + time.Now().Format(time.RFC3339) + `","latest":""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := isolateConfigDir(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if got, ok := readCache(); ok || got != "" {
				t.Errorf("readCache = (%q, %v), want (\"\", false) for %s", got, ok, tt.name)
			}
		})
	}
}

// The cache can hold the user's update history; it does not belong to the
// group or to other users.
func TestWriteCache_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply on windows")
	}
	path := isolateConfigDir(t)
	writeCache("1.2.3")

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("cache file mode = %#o, want no group/other bits", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("cache dir mode = %#o, want no group/other bits", perm)
	}
}

// writeCache swallows its errors by design; the CLI must not fail because a
// version check could not be cached.
func TestWriteCache_UnwritablePathDoesNotPanic(t *testing.T) {
	path := isolateConfigDir(t)
	// Occupy the directory slot with a regular file so MkdirAll must fail.
	if err := os.MkdirAll(filepath.Dir(filepath.Dir(path)), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Dir(path), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	writeCache("1.2.3") // must not panic

	if got, ok := readCache(); ok || got != "" {
		t.Errorf("readCache = (%q, %v), want (\"\", false) when the cache could not be written", got, ok)
	}
}
