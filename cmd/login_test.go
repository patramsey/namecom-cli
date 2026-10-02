package cmd

import (
	"testing"

	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
)

// stubLoginForm answers the login form with a fixed username and token,
// answers the sandbox question with sandboxAnswer, and reports whether that
// question was asked.
//
// The real form cannot run under go test: the token field is a password input,
// which huh reads from a terminal even in accessible mode.
func stubLoginForm(t *testing.T, sandboxAnswer bool) (askedSandbox *bool) {
	t.Helper()
	asked := new(bool)
	prev := askLogin
	askLogin = func(a *loginAnswers, askSandbox bool) error {
		*asked = askSandbox
		a.Username, a.Token = "alice", "tok"
		if askSandbox {
			a.Sandbox = sandboxAnswer
		}
		return nil
	}
	t.Cleanup(func() { askLogin = prev })
	t.Cleanup(output.StubInteractive(true))
	return asked
}

// TestAuthLogin_SandboxFlag guards #180: `auth login --sandbox` saved the
// profile with sandbox: false unless the user also answered Yes at the prompt,
// because the form's answer started false and the flag was never read.
func TestAuthLogin_SandboxFlag(t *testing.T) {
	tests := []struct {
		name        string
		flag        bool
		answer      bool
		wantAsked   bool
		wantSandbox bool
	}{
		{"--sandbox skips the question and saves sandbox", true, false, false, true},
		{"without the flag the question decides: yes", false, true, true, true},
		{"without the flag the question decides: no", false, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withConfig(t, "")
			asked := stubLoginForm(t, tt.answer)
			prev := loginProfile
			loginProfile = "sbx"
			t.Cleanup(func() { loginProfile = prev })

			cmd, _ := jsonCmd(t)
			cmd.PersistentFlags().Bool("sandbox", tt.flag, "")

			if err := runAuthLogin(cmd, nil); err != nil {
				t.Fatalf("runAuthLogin: %v", err)
			}
			if *asked != tt.wantAsked {
				t.Errorf("asked the sandbox question = %v, want %v", *asked, tt.wantAsked)
			}
			f, err := config.Load()
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			p, ok := f.Profiles["sbx"]
			if !ok {
				t.Fatalf("profile not saved: %v", f.Profiles)
			}
			if p.Sandbox != tt.wantSandbox || p.Username != "alice" || p.Token != "tok" {
				t.Errorf("saved %+v, want alice/tok with sandbox=%v", p, tt.wantSandbox)
			}
		})
	}
}
