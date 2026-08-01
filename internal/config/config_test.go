package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolvePrecedence(t *testing.T) {
	f := &File{
		Default: "prod",
		Profiles: map[string]Profile{
			"prod":    {Username: "profuser", Token: "proftoken"},
			"sandbox": {Username: "sbuser", Token: "sbtoken", Sandbox: true},
		},
	}

	tests := []struct {
		name     string
		env      map[string]string
		ov       Overrides
		wantUser string
		wantTok  string
		wantSB   bool
		wantErr  bool
	}{
		{
			name:     "default profile",
			wantUser: "profuser", wantTok: "proftoken", wantSB: false,
		},
		{
			name:     "flag profile selects sandbox",
			ov:       Overrides{Profile: "sandbox"},
			wantUser: "sbuser", wantTok: "sbtoken", wantSB: true,
		},
		{
			name:     "env profile selects sandbox",
			env:      map[string]string{"NAMECOM_PROFILE": "sandbox"},
			wantUser: "sbuser", wantTok: "sbtoken", wantSB: true,
		},
		{
			name:     "flags override env and profile",
			env:      map[string]string{"NAMECOM_USERNAME": "envuser", "NAMECOM_TOKEN": "envtok"},
			ov:       Overrides{Username: "flaguser", Token: "flagtok"},
			wantUser: "flaguser", wantTok: "flagtok",
		},
		{
			name:     "env overrides profile",
			env:      map[string]string{"NAMECOM_USERNAME": "envuser", "NAMECOM_TOKEN": "envtok"},
			wantUser: "envuser", wantTok: "envtok",
		},
		{
			name:     "sandbox flag false overrides sandbox profile",
			ov:       Overrides{Profile: "sandbox", SandboxSet: true, Sandbox: false},
			wantUser: "sbuser", wantTok: "sbtoken", wantSB: false,
		},
		{
			name:     "sandbox via env truthy",
			ov:       Overrides{Profile: "prod"},
			env:      map[string]string{"NAMECOM_SANDBOX": "true"},
			wantUser: "profuser", wantTok: "proftoken", wantSB: true,
		},
		{
			name:    "missing credentials errors",
			ov:      Overrides{Profile: "nonexistent"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Isolate env per subtest.
			for _, k := range []string{"NAMECOM_PROFILE", "NAMECOM_USERNAME", "NAMECOM_TOKEN", "NAMECOM_SANDBOX"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			creds, err := Resolve(f, tt.ov)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got creds %+v", creds)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if creds.Username != tt.wantUser {
				t.Errorf("username = %q, want %q", creds.Username, tt.wantUser)
			}
			if creds.Token != tt.wantTok {
				t.Errorf("token = %q, want %q", creds.Token, tt.wantTok)
			}
			if creds.Sandbox != tt.wantSB {
				t.Errorf("sandbox = %v, want %v", creds.Sandbox, tt.wantSB)
			}
		})
	}
}

func TestResolveTokenCmd(t *testing.T) {
	for _, k := range []string{"NAMECOM_PROFILE", "NAMECOM_USERNAME", "NAMECOM_TOKEN", "NAMECOM_SANDBOX"} {
		t.Setenv(k, "")
	}
	f := &File{
		Default: "prod",
		Profiles: map[string]Profile{
			"prod": {Username: "u", TokenCmd: "printf secret-token"},
		},
	}
	creds, err := Resolve(f, Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Token != "secret-token" {
		t.Errorf("token = %q, want %q", creds.Token, "secret-token")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NAMECOM_CONFIG", filepath.Join(dir, "config.yaml"))

	want := &File{
		Default:  "prod",
		Profiles: map[string]Profile{"prod": {Username: "u", Token: "t"}},
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File must be 0600.
	info, err := os.Stat(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %#o, want 0600", perm)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Default != "prod" || got.Profiles["prod"].Token != "t" {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestResolveNilFile(t *testing.T) {
	for _, k := range []string{"NAMECOM_PROFILE", "NAMECOM_USERNAME", "NAMECOM_TOKEN", "NAMECOM_SANDBOX"} {
		t.Setenv(k, "")
	}
	t.Setenv("NAMECOM_USERNAME", "envuser")
	t.Setenv("NAMECOM_TOKEN", "envtoken")

	creds, err := Resolve(nil, Overrides{})
	if err != nil {
		t.Fatalf("Resolve(nil) unexpected error: %v", err)
	}
	if creds.Username != "envuser" || creds.Token != "envtoken" {
		t.Errorf("creds = %+v, want envuser/envtoken", creds)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	t.Setenv("NAMECOM_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	f, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Profiles) != 0 {
		t.Errorf("expected empty profiles, got %+v", f.Profiles)
	}
}

// TestExplicitConfigPathIsNotOverridden guards a production-safety bug:
// resolveReadPath stat'd the primary path and, on a miss, fell back to the
// legacy ~/.namecom/config.yaml. Because NAMECOM_CONFIG *is* the primary path
// when set, pointing it at a nonexistent file silently loaded the user's real
// credentials instead. Isolation is the entire reason that variable exists — a
// CI job or test harness that set it to a scratch path was quietly running
// against production.
func TestExplicitConfigPathIsNotOverridden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacyDir := filepath.Join(home, ".namecom")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("creating legacy dir: %v", err)
	}
	legacy := filepath.Join(legacyDir, "config.yaml")
	if err := os.WriteFile(legacy, []byte("default: real\nprofiles:\n  real:\n    username: prod\n    token: PRODUCTION-TOKEN\n"), 0o600); err != nil {
		t.Fatalf("writing legacy config: %v", err)
	}

	t.Setenv("NAMECOM_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.yaml"))

	f, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := f.Profiles["real"]; ok {
		t.Error("NAMECOM_CONFIG pointing at a missing file fell back to the real config — isolation broken")
	}
	if len(f.Profiles) != 0 {
		t.Errorf("expected an empty config, got %d profiles", len(f.Profiles))
	}
}

// TestSaveWritesBackToLoadedFile guards a credential-persistence bug: Load read
// via the legacy fallback while Save always wrote the XDG path. So `auth logout`
// deleted the profile from the in-memory copy, wrote an empty file to a new
// location, reported success — and left the token sitting in the legacy file,
// now invisible because the new empty file shadowed it.
func TestSaveWritesBackToLoadedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("NAMECOM_CONFIG", "")

	legacyDir := filepath.Join(home, ".namecom")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("creating legacy dir: %v", err)
	}
	legacy := filepath.Join(legacyDir, "config.yaml")
	if err := os.WriteFile(legacy, []byte("default: old\nprofiles:\n  old:\n    username: alice\n    token: LEGACY-TOKEN\n"), 0o600); err != nil {
		t.Fatalf("writing legacy config: %v", err)
	}

	f, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := f.Profiles["old"]; !ok {
		t.Fatalf("legacy config was not loaded: %+v", f.Profiles)
	}

	// Simulate `auth logout`.
	delete(f.Profiles, "old")
	f.Default = ""
	if err := Save(f); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("reading legacy config after save: %v", err)
	}
	if strings.Contains(string(data), "LEGACY-TOKEN") {
		t.Errorf("token still on disk after logout — Save wrote elsewhere:\n%s", string(data))
	}
}

// TestSaveRepairsUnsafePermissions guards a credential-exposure gap: os.WriteFile
// only applies its mode when CREATING a file, so a config that was already
// 0644 stayed world-readable forever. Load warns about it but nothing ever fixed
// it, so `auth login` wrote a freshly entered token into a readable file.
func TestSaveRepairsUnsafePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("default: a\nprofiles: {}\n"), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("NAMECOM_CONFIG", path)

	f, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f.Profiles = map[string]Profile{"a": {Username: "alice", Token: "NEW-TOKEN"}}
	if err := Save(f); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("config holding a token should be 0600 after save, got %04o", mode)
	}
}

// TestRunTokenCmd_TimesOut guards a CI hang: runTokenCmd used exec.Command with
// no context and no deadline, so a credential helper blocked on a locked vault,
// an expired SSO session, or a prompt with no TTY kept the CLI alive forever.
// --timeout is HTTP-only and does not cover this.
func TestRunTokenCmd_TimesOut(t *testing.T) {
	// Shorten the bound so the test is fast; the production default is longer to
	// allow an interactive unlock.
	prev := tokenCmdTimeout
	tokenCmdTimeout = 300 * time.Millisecond
	t.Cleanup(func() { tokenCmdTimeout = prev })

	start := time.Now()
	_, err := runTokenCmd("sleep 30")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error from a hanging token_cmd")
	}
	if elapsed > 5*time.Second {
		t.Errorf("token_cmd was not bounded: took %v", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should say it timed out so the user knows why, got: %v", err)
	}
}

// TestRunTokenCmd_Succeeds pins the normal path still works.
func TestRunTokenCmd_Succeeds(t *testing.T) {
	tok, err := runTokenCmd("echo '  s3cret  '")
	if err != nil {
		t.Fatalf("runTokenCmd: %v", err)
	}
	if tok != "s3cret" {
		t.Errorf("expected trimmed token %q, got %q", "s3cret", tok)
	}
}
