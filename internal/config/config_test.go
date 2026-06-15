package config

import (
	"os"
	"path/filepath"
	"testing"
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
