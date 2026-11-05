package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadWarnings writes body as the config file, loads it, and returns the
// warnings the load raised.
func loadWarnings(t *testing.T, body string) (string, []string) {
	t.Helper()
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NAMECOM_CONFIG", path)
	TakeWarnings()
	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return path, TakeWarnings()
}

// TestLoad_WarnsOfUnknownKeys pins #358: a profile's base_url was ignored
// and the token went to production, and a "sandox: true" would have done
// the same while the user believed they were on the sandbox. Every unknown
// key now warns, naming the key, where it is and the file, with the key
// meant when one is close and the way to set an endpoint for one that
// looks like it.
func TestLoad_WarnsOfUnknownKeys(t *testing.T) {
	path, warnings := loadWarnings(t, `default: work
defaults: other
profiles:
  work:
    username: u
    token: t
    sandox: true
    tokencmd: echo x
    base_url: http://127.0.0.1:8080
    region: eu
`)
	want := []string{
		`unknown config key "defaults" at the top level of ` + path + ` is ignored; did you mean "default"?`,
		`unknown config key "sandox" in profile "work" of ` + path + ` is ignored; did you mean "sandbox"?`,
		`unknown config key "tokencmd" in profile "work" of ` + path + ` is ignored; did you mean "token_cmd"?`,
		`unknown config key "base_url" in profile "work" of ` + path + ` is ignored; the API endpoint is set with --base-url or NAMECOM_BASE_URL, not in the config file`,
		`unknown config key "region" in profile "work" of ` + path + ` is ignored; known keys are username, token, token_cmd, sandbox`,
	}
	if got := strings.Join(warnings, "\n"); got != strings.Join(want, "\n") {
		t.Errorf("warnings:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

// TestLoad_NoWarningForKeysNamecomWrites: a file Save wrote, every key
// set, raises nothing.
func TestLoad_NoWarningForKeysNamecomWrites(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("NAMECOM_CONFIG", path)
	f := &File{Default: "a", Icons: "nerd", Profiles: map[string]Profile{
		"a": {Username: "u", Token: "t", TokenCmd: "echo t", Sandbox: true},
	}}
	if err := Save(f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	TakeWarnings()
	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if w := TakeWarnings(); len(w) != 0 {
		t.Errorf("warnings for a file namecom wrote: %q", w)
	}
}
