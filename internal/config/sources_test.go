package config

import (
	"errors"
	"testing"
)

// TestIdentity_Sources pins where each value is reported to come from (#246),
// which config show and auth status print and a 401's hint names.
func TestIdentity_Sources(t *testing.T) {
	two := &File{Default: "prod", Profiles: map[string]Profile{
		"prod":   {Username: "produser", Token: "p"},
		"helper": {Username: "h", TokenCmd: "echo tok", Sandbox: true},
	}}
	lone := &File{Profiles: map[string]Profile{"work": {Username: "w", Token: "t"}}}

	tests := []struct {
		name string
		f    *File
		ov   Overrides
		env  map[string]string
		want Sources
	}{
		{"all from the file default", two, Overrides{}, nil,
			Sources{Profile: "config default", Username: "profile prod", Token: "profile prod", Sandbox: "profile prod"}},
		{"implied default", lone, Overrides{}, nil,
			Sources{Profile: "implied default", Username: "profile work", Token: "profile work", Sandbox: "profile work"}},
		{"token_cmd", two, Overrides{Profile: "helper"}, nil,
			Sources{Profile: "flag --profile", Username: "profile helper", Token: "token_cmd", Sandbox: "profile helper"}},
		{"environment only, no file", nil, Overrides{}, map[string]string{
			"NAMECOM_USERNAME": "u", "NAMECOM_TOKEN": "t", "NAMECOM_SANDBOX": "1", "NAMECOM_BASE_URL": "http://stub"},
			Sources{Username: "env NAMECOM_USERNAME", Token: "env NAMECOM_TOKEN", Sandbox: "env NAMECOM_SANDBOX", BaseURL: "env NAMECOM_BASE_URL"}}, //nolint:gosec // G101: source names, not credentials
		{"flags beat the environment", two, Overrides{Username: "f", Token: "f", SandboxSet: true, BaseURL: "http://flag"},
			map[string]string{"NAMECOM_USERNAME": "u", "NAMECOM_TOKEN": "t", "NAMECOM_PROFILE": "helper", "NAMECOM_BASE_URL": "http://env"},
			Sources{Profile: "env NAMECOM_PROFILE", Username: "flag --username", Token: "flag --token", Sandbox: "flag --sandbox", BaseURL: "flag --base-url"}},
		{"nothing at all", nil, Overrides{}, nil, Sources{Sandbox: "default"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"NAMECOM_PROFILE", "NAMECOM_USERNAME", "NAMECOM_TOKEN", "NAMECOM_SANDBOX", "NAMECOM_BASE_URL"} {
				t.Setenv(k, tt.env[k])
			}
			got, err := Identity(tt.f, tt.ov)
			if err != nil {
				t.Fatalf("Identity: %v", err)
			}
			if got.Sources != tt.want {
				t.Errorf("Sources = %+v\nwant      %+v", got.Sources, tt.want)
			}
		})
	}
}

// TestBaseURLOverride: --base-url beats NAMECOM_BASE_URL, and the source
// names the one used, for the warning and the validation error.
func TestBaseURLOverride(t *testing.T) {
	t.Setenv("NAMECOM_BASE_URL", "http://env")
	if raw, src := BaseURLOverride(Overrides{BaseURL: "http://flag"}); raw != "http://flag" || SourceName(src) != "--base-url" {
		t.Errorf("with the flag: %q from %q", raw, src)
	}
	if raw, src := BaseURLOverride(Overrides{}); raw != "http://env" || SourceName(src) != "NAMECOM_BASE_URL" {
		t.Errorf("env only: %q from %q", raw, src)
	}
	creds, err := Resolve(nil, Overrides{Username: "u", Token: "t"})
	if err != nil || creds.BaseURL != "http://env" {
		t.Errorf("Resolve BaseURL = %q (%v), want NAMECOM_BASE_URL's", creds.BaseURL, err)
	}
	t.Setenv("NAMECOM_BASE_URL", "")
	if raw, src := BaseURLOverride(Overrides{}); raw != "" || src != "" {
		t.Errorf("neither: %q from %q", raw, src)
	}
}

// TestCheckComplete_MatchesResolve: config show fails through CheckComplete
// without resolving the token, so it must fail exactly when Resolve does.
func TestCheckComplete_MatchesResolve(t *testing.T) {
	two := &File{Profiles: map[string]Profile{"a": {Username: "a", Token: "t"}, "b": {Username: "b", Token: "t"}}}
	tests := []struct {
		name string
		f    *File
		env  map[string]string
	}{
		{"nothing", nil, nil},
		{"token only", nil, map[string]string{"NAMECOM_TOKEN": "t"}},
		{"username only", nil, map[string]string{"NAMECOM_USERNAME": "u"}},
		{"both", nil, map[string]string{"NAMECOM_USERNAME": "u", "NAMECOM_TOKEN": "t"}},
		{"two profiles, no default", two, nil},
		{"two profiles, no default, env credentials", two, map[string]string{"NAMECOM_USERNAME": "u", "NAMECOM_TOKEN": "t"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"NAMECOM_PROFILE", "NAMECOM_USERNAME", "NAMECOM_TOKEN", "NAMECOM_SANDBOX", "NAMECOM_BASE_URL"} {
				t.Setenv(k, tt.env[k])
			}
			_, resolveErr := Resolve(tt.f, Overrides{})
			id, err := Identity(tt.f, Overrides{})
			if err != nil {
				t.Fatalf("Identity: %v", err)
			}
			checkErr := CheckComplete(tt.f, id)
			if (resolveErr == nil) != (checkErr == nil) || errors.Is(resolveErr, ErrNoCredentials) != errors.Is(checkErr, ErrNoCredentials) {
				t.Errorf("Resolve: %v; CheckComplete: %v", resolveErr, checkErr)
			}
		})
	}
}
