package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestShow_ReportsSources: config show says where each value came from
// (#246), in a sibling key so "username" and the rest stay strings for
// existing scripts, and as a dimmed note in the table.
func TestShow_ReportsSources(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		cmd, buf := showCmdFor(t, output.FormatJSON, "")
		t.Setenv("NAMECOM_USERNAME", "envuser")
		t.Setenv("NAMECOM_TOKEN", "")
		t.Setenv("NAMECOM_SANDBOX", "")
		t.Setenv("NAMECOM_BASE_URL", "http://127.0.0.1:9")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		var got map[string]string
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("parsing: %v\n%s", err, buf.String())
		}
		want := map[string]string{
			"profile": "prod", "profileSource": "config default",
			"username": "envuser", "usernameSource": "env NAMECOM_USERNAME",
			"tokenSource": "profile prod",
			"endpoint":    "http://127.0.0.1:9", "endpointSource": "env NAMECOM_BASE_URL",
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %q, want %q", k, got[k], v)
			}
		}
	})

	t.Run("table", func(t *testing.T) {
		cmd, buf := showCmdFor(t, output.FormatTable, "sandy")
		t.Setenv("NAMECOM_TOKEN", "")
		t.Setenv("NAMECOM_USERNAME", "")
		t.Setenv("NAMECOM_SANDBOX", "")
		t.Setenv("NAMECOM_BASE_URL", "")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		for _, want := range []string{"(flag --profile)", "(profile sandy)"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("table does not show %q:\n%s", want, buf.String())
			}
		}
	})

	t.Run("token_cmd", func(t *testing.T) {
		cmd, buf := configCmd(t, output.FormatJSON)
		t.Setenv("NAMECOM_TOKEN", "")
		t.Setenv("NAMECOM_USERNAME", "")
		t.Setenv("NAMECOM_SANDBOX", "")
		t.Setenv("NAMECOM_BASE_URL", "")
		t.Setenv("NAMECOM_PROFILE", "helper")
		if err := runShow(cmd, nil); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		var got map[string]string
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("parsing: %v\n%s", err, buf.String())
		}
		if got["tokenSource"] != config.SourceTokenCmd {
			t.Errorf("tokenSource = %q, want token_cmd", got["tokenSource"])
		}
	})
}
