package config

import (
	"errors"
	"strings"
	"testing"
)

// TestParseBool pins the spellings a boolean NAMECOM_* variable accepts:
// Go's strconv set plus yes/no, on/off and y/n, in any case.
func TestParseBool(t *testing.T) {
	truths := []string{"1", "t", "T", "true", "TRUE", "True", "yes", "YES", "Yes", "y", "Y", "on", "ON", "On", " yes "}
	falses := []string{"0", "f", "F", "false", "FALSE", "False", "no", "NO", "n", "N", "off", "OFF", " off "}
	invalid := []string{"garbage", "2", "-1", "yess", "enable", "sandbox", "  ", "tru", "nope"}

	for _, s := range truths {
		if v, ok := ParseBool(s); !ok || !v {
			t.Errorf("ParseBool(%q) = %v, %v; want true, true", s, v, ok)
		}
	}
	for _, s := range falses {
		if v, ok := ParseBool(s); !ok || v {
			t.Errorf("ParseBool(%q) = %v, %v; want false, true", s, v, ok)
		}
	}
	for _, s := range invalid {
		if _, ok := ParseBool(s); ok {
			t.Errorf("ParseBool(%q) accepted a value that is not a boolean", s)
		}
	}
}

// TestSandboxEnv guards #225. NAMECOM_SANDBOX was read with strconv.ParseBool
// and every parse failure counted as false, so NAMECOM_SANDBOX=yes — set by a
// CI job precisely to stay out of production — sent registrations and renewals
// to api.name.com, overriding even a profile saved with sandbox: true.
func TestSandboxEnv(t *testing.T) {
	prodProfile := &File{Default: "p", Profiles: map[string]Profile{"p": {Username: "u", Token: "t"}}}
	sbxProfile := &File{Default: "s", Profiles: map[string]Profile{"s": {Username: "u", Token: "t", Sandbox: true}}}

	tests := []struct {
		name        string
		f           *File
		env         string
		wantSandbox bool
	}{
		{"yes selects sandbox", prodProfile, "yes", true},
		{"on selects sandbox", prodProfile, "on", true},
		{"Y selects sandbox", prodProfile, "Y", true},
		{"TRUE selects sandbox", prodProfile, "TRUE", true},
		{"1 selects sandbox", prodProfile, "1", true},
		{"no selects production over a sandbox profile", sbxProfile, "no", false},
		{"OFF selects production", sbxProfile, "OFF", false},
		{"unset keeps the profile's sandbox", sbxProfile, "", true},
		{"unset keeps the profile's production", prodProfile, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("NAMECOM_SANDBOX", tt.env)
			id, err := Identity(tt.f, Overrides{})
			if err != nil {
				t.Fatalf("Identity: %v", err)
			}
			creds, err := Resolve(tt.f, Overrides{})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if id.Sandbox != tt.wantSandbox || creds.Sandbox != tt.wantSandbox {
				t.Errorf("sandbox: Identity %v, Resolve %v; want %v", id.Sandbox, creds.Sandbox, tt.wantSandbox)
			}
		})
	}

	// An unrecognized value must never resolve to anything — least of all
	// production. Both a production and a sandbox profile are tried: the old
	// code fell back to false (production) even over sandbox: true.
	for _, bad := range []string{"garbage", "2", "sandbox", "  "} {
		for _, f := range []*File{prodProfile, sbxProfile} {
			t.Run("invalid "+bad, func(t *testing.T) {
				clearEnv(t)
				t.Setenv("NAMECOM_SANDBOX", bad)
				if id, err := Identity(f, Overrides{}); err == nil {
					t.Errorf("Identity accepted NAMECOM_SANDBOX=%q and resolved sandbox=%v", bad, id.Sandbox)
				} else {
					checkEnvErr(t, err, bad)
				}
				if creds, err := Resolve(f, Overrides{}); err == nil {
					t.Errorf("Resolve accepted NAMECOM_SANDBOX=%q and resolved sandbox=%v", bad, creds.Sandbox)
				} else {
					checkEnvErr(t, err, bad)
				}
			})
		}
	}

	t.Run("--sandbox wins over an invalid value", func(t *testing.T) {
		// The flag already decides, so the variable is not consulted; failing
		// here would make --sandbox unusable in a shell that exports junk.
		clearEnv(t)
		t.Setenv("NAMECOM_SANDBOX", "garbage")
		creds, err := Resolve(prodProfile, Overrides{Sandbox: true, SandboxSet: true})
		if err != nil || !creds.Sandbox {
			t.Errorf("Resolve with --sandbox = %+v, %v; want sandbox and no error", creds, err)
		}
	})
}

func checkEnvErr(t *testing.T, err error, value string) {
	t.Helper()
	var envErr *EnvError
	if !errors.As(err, &envErr) {
		t.Fatalf("error is %T, want *EnvError so the CLI exits 2: %v", err, err)
	}
	for _, want := range []string{"NAMECOM_SANDBOX", value, "not a boolean"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"NAMECOM_PROFILE", "NAMECOM_USERNAME", "NAMECOM_TOKEN", "NAMECOM_SANDBOX"} {
		t.Setenv(k, "")
	}
}
