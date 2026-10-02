package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The config-writing commands ignored the global --dry-run (#166): `auth
// logout --dry-run` deleted the profile and `auth login --dry-run` saved one.
// Under --dry-run they describe the change and leave the file alone.

// unchanged fails the test if the file at path no longer holds before.
// before is "" for a file that must not exist.
func unchanged(t *testing.T, path, before string) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test temp file
	if before == "" {
		if err == nil {
			t.Errorf("--dry-run created %s:\n%s", path, data)
		}
		return
	}
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if string(data) != before {
		t.Errorf("--dry-run rewrote the config:\nbefore:\n%s\nafter:\n%s", before, data)
	}
}

func TestAuthLogout_DryRunLeavesConfig(t *testing.T) {
	path := withConfig(t, twoProfiles)
	prev := logoutProfile
	logoutProfile = "staging"
	t.Cleanup(func() { logoutProfile = prev })

	cmd, buf := jsonCmd(t)
	cmd.PersistentFlags().Bool("dry-run", true, "")
	if err := runAuthLogout(cmd, nil); err != nil {
		t.Fatalf("runAuthLogout --dry-run: %v", err)
	}
	unchanged(t, path, twoProfiles)

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not one JSON document: %v\n%s", err, buf.String())
	}
	if got["dry_run"] != true || got["action"] != "remove_profile" || got["profile"] != "staging" || got["config"] != path {
		t.Errorf("dry-run should describe removing staging from %s, got %v", path, got)
	}
	// prod stays the default, so the preview says so.
	if got["default"] != "prod" {
		t.Errorf("default after the change = %v, want prod", got["default"])
	}
}

func TestAuthLogin_DryRunLeavesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	withConfig(t, "")
	t.Setenv("NAMECOM_CONFIG", path) // a file that does not exist yet
	stubLoginForm(t, false)
	prev := loginProfile
	loginProfile = "work"
	t.Cleanup(func() { loginProfile = prev })

	cmd, buf := jsonCmd(t)
	cmd.PersistentFlags().Bool("dry-run", true, "")
	if err := runAuthLogin(cmd, nil); err != nil {
		t.Fatalf("runAuthLogin --dry-run: %v", err)
	}
	unchanged(t, path, "")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not one JSON document: %v\n%s", err, buf.String())
	}
	if got["dry_run"] != true || got["action"] != "save_profile" || got["profile"] != "work" ||
		got["username"] != "alice" || got["default"] != "work" {
		t.Errorf("dry-run should describe saving profile work for alice, got %v", got)
	}
	if strings.Contains(buf.String(), "tok") {
		t.Errorf("dry-run printed the token:\n%s", buf.String())
	}
}
