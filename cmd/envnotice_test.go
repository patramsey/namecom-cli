package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// TestEnvEndpointNotice: NAMECOM_SANDBOX overriding a profile's endpoint is
// said on stderr when a person is watching (#239, moved from #225).
func TestEnvEndpointNotice(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		profile  string
		env      string
		flag     bool // --sandbox passed
		tty      bool
		want     string // "" means no notice
	}{
		{"sandbox env over a production profile", twoProfiles, "prod", "1", false, true,
			`NAMECOM_SANDBOX=1 overrides profile "prod" (production): requests go to https://api.dev.name.com`},
		{"production env over a sandbox profile", twoProfiles, "staging", "false", false, true,
			`NAMECOM_SANDBOX=false overrides profile "staging" (sandbox): requests go to https://api.name.com`},
		{"env agrees with the profile", twoProfiles, "staging", "1", false, true, ""},
		{"--sandbox decides, not the env", twoProfiles, "prod", "1", true, true, ""},
		{"not a terminal", twoProfiles, "prod", "1", false, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withConfig(t, tt.contents)
			t.Setenv("NAMECOM_SANDBOX", tt.env)
			prevTTY := envNoticeTTY
			envNoticeTTY = func() bool { return tt.tty }
			t.Cleanup(func() { envNoticeTTY = prevTTY })
			prev := gf
			gf = globalFlags{profile: tt.profile, sandbox: tt.flag}
			t.Cleanup(func() { gf = prev })

			var stderr bytes.Buffer
			out := &output.Config{Format: output.FormatJSON, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &stderr}
			cmd := &cobra.Command{}
			cmd.Flags().Bool("sandbox", false, "")
			if tt.flag {
				_ = cmd.Flags().Set("sandbox", "true")
			}
			cmd.SetContext(context.WithValue(context.Background(), cmdutil.KeyOutput, out))
			if err := initContext(cmd); err != nil {
				t.Fatalf("initContext: %v", err)
			}
			got := strings.TrimSpace(strings.TrimPrefix(stderr.String(), "! "))
			if got != tt.want {
				t.Errorf("stderr = %q, want %q", got, tt.want)
			}
		})
	}
}
