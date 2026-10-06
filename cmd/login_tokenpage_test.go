package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// tokenPageStub records the "open the API token page?" question.
type tokenPageStub struct {
	asked  int
	detail string
}

// stubOpenTokenPage answers the "open the API token page?" question with
// answer, or with err when one is given. stubLoginAnswers installs it
// answering No, so no login test can reach the real prompt.
func stubOpenTokenPage(t *testing.T, answer bool, err ...error) *tokenPageStub {
	t.Helper()
	s := &tokenPageStub{}
	prev := askOpenTokenPage
	askOpenTokenPage = func(_, detail string) (bool, error) {
		s.asked++
		s.detail = detail
		if len(err) > 0 {
			return false, err[0]
		}
		return answer, nil
	}
	t.Cleanup(func() { askOpenTokenPage = prev })
	return s
}

// tokenPageCmd is a login command writing format to buffers, with the root
// flags offerTokenPage reads.
func tokenPageCmd(t *testing.T, format output.Format, quiet bool) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var buf, ebuf bytes.Buffer
	out := &output.Config{Format: format, QuietMode: quiet, Color: output.ColorNever, Writer: &buf, EWriter: &ebuf}
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(context.Background(), cmdutil.KeyOutput, out))
	cmd.SetIn(strings.NewReader("tok\n"))
	return cmd, &ebuf
}

// TestAuthLogin_OffersTokenPage covers #272: an interactive login offers to
// open the API token page before the form, with the opener `namecom open`
// uses. Every case stubs startCommand: no test may launch a real browser.
func TestAuthLogin_OffersTokenPage(t *testing.T) {
	setup := func(t *testing.T, answer bool) (*tokenPageStub, *[][]string) {
		t.Helper()
		withConfig(t, "")
		t.Setenv("BROWSER", "")
		stubLoginForm(t, false)
		setLoginProfile(t, "work")
		calls := stubStart(t, func(string) error { return nil })
		return stubOpenTokenPage(t, answer), calls
	}
	saved := func(t *testing.T) bool {
		t.Helper()
		f, err := config.Load()
		if err != nil {
			t.Fatalf("config.Load: %v", err)
		}
		_, ok := f.Profiles["work"]
		return ok
	}

	t.Run("yes opens the page, then the form runs", func(t *testing.T) {
		q, calls := setup(t, true)
		cmd, stderr := tokenPageCmd(t, output.FormatTable, false)
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin: %v", err)
		}
		if q.asked != 1 || q.detail != apiSettingsURL {
			t.Errorf("asked %d times with detail %q, want once with %q", q.asked, q.detail, apiSettingsURL)
		}
		if len(*calls) != 1 || (*calls)[0][len((*calls)[0])-1] != apiSettingsURL {
			t.Errorf("opener calls = %v, want one with %s", *calls, apiSettingsURL)
		}
		if !strings.Contains(stderr.String(), "Opening "+apiSettingsURL) {
			t.Errorf("stderr does not say it is opening the page:\n%s", stderr.String())
		}
		if !saved(t) {
			t.Error("the form did not follow: profile not saved")
		}
	})

	t.Run("no opens nothing, and the form runs", func(t *testing.T) {
		q, calls := setup(t, false)
		cmd, _ := tokenPageCmd(t, output.FormatTable, false)
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin: %v", err)
		}
		if q.asked != 1 || len(*calls) != 0 {
			t.Errorf("asked %d times, opener calls %v; want asked once, nothing opened", q.asked, *calls)
		}
		if !saved(t) {
			t.Error("profile not saved")
		}
	})

	t.Run("a browser that will not open is a warning with the URL", func(t *testing.T) {
		withConfig(t, "")
		t.Setenv("BROWSER", "")
		stubLoginForm(t, false)
		setLoginProfile(t, "work")
		stubStart(t, func(string) error { return errors.New("no opener") })
		stubOpenTokenPage(t, true)
		cmd, stderr := tokenPageCmd(t, output.FormatTable, false)
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin: %v", err)
		}
		if s := stderr.String(); !strings.Contains(s, "could not open a browser") || !strings.Contains(s, apiSettingsURL) {
			t.Errorf("stderr lacks the warning and URL:\n%s", s)
		}
		if !saved(t) {
			t.Error("the form did not follow the failed open: profile not saved")
		}
	})

	t.Run("Ctrl-C aborts before the form", func(t *testing.T) {
		withConfig(t, "")
		stubLoginForm(t, false)
		setLoginProfile(t, "work")
		calls := stubStart(t, func(string) error { return nil })
		stubOpenTokenPage(t, false, huh.ErrUserAborted)
		cmd, _ := tokenPageCmd(t, output.FormatTable, false)
		err := runAuthLogin(cmd, nil)
		if !errors.Is(err, cmdutil.ErrAborted) || exitCode(err) != 1 {
			t.Errorf("got %v (exit %d), want ErrAborted exiting 1", err, exitCode(err))
		}
		if len(*calls) != 0 || saved(t) {
			t.Errorf("opened %v or saved a profile after Ctrl-C", *calls)
		}
	})

	t.Run("sandbox notes its credentials are separate", func(t *testing.T) {
		q, calls := setup(t, true)
		cmd, _ := tokenPageCmd(t, output.FormatTable, false)
		cmd.PersistentFlags().Bool("sandbox", true, "")
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin: %v", err)
		}
		if !strings.HasPrefix(q.detail, apiSettingsURL) || !strings.Contains(q.detail, "sandbox credentials are separate") {
			t.Errorf("detail = %q, want the URL and the sandbox note", q.detail)
		}
		if len(*calls) != 1 || (*calls)[0][len((*calls)[0])-1] != apiSettingsURL {
			t.Errorf("opener calls = %v, want the same page under --sandbox", *calls)
		}
	})
}

// TestAuthLogin_TokenPageNotOffered: the question is for a person at a
// terminal filling in the form, so nothing that answers or skips the form,
// or whose output is for a script, asks it.
func TestAuthLogin_TokenPageNotOffered(t *testing.T) {
	tests := []struct {
		name   string
		format output.Format
		quiet  bool
		flag   string // root bool flag set true
		mode   string // "", "with-token" or "token-cmd"
	}{
		{name: "--yes", format: output.FormatTable, flag: "yes"},
		{name: "--dry-run", format: output.FormatTable, flag: "dry-run"},
		{name: "-o json", format: output.FormatJSON},
		{name: "-o yaml", format: output.FormatYAML},
		{name: "-q", format: output.FormatTable, quiet: true},
		{name: "--with-token", format: output.FormatTable, mode: "with-token"},
		{name: "--token-cmd", format: output.FormatTable, mode: "token-cmd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withConfig(t, "")
			stubLoginForm(t, false)
			setLoginProfile(t, "work")
			calls := stubStart(t, func(string) error { return nil })
			q := stubOpenTokenPage(t, true)
			if tt.mode != "" {
				ciLogin(t, "alice", tt.mode == "with-token", "")
				t.Cleanup(output.StubInteractive(true))
				if tt.mode == "token-cmd" {
					loginTokenCmd = "echo tok"
				}
				stubHello(t, http.StatusOK, `{"username":"alice"}`)
			}
			cmd, _ := tokenPageCmd(t, tt.format, tt.quiet)
			if tt.flag != "" {
				cmd.PersistentFlags().Bool(tt.flag, true, "")
			}
			if err := runAuthLogin(cmd, nil); err != nil {
				t.Fatalf("runAuthLogin: %v", err)
			}
			if q.asked != 0 || len(*calls) != 0 {
				t.Errorf("asked %d times, opener calls %v; want neither", q.asked, *calls)
			}
		})
	}

	t.Run("off a terminal", func(t *testing.T) {
		t.Cleanup(output.StubInteractive(false))
		calls := stubStart(t, func(string) error { return nil })
		q := stubOpenTokenPage(t, true)
		cmd, _ := tokenPageCmd(t, output.FormatTable, false)
		if err := offerTokenPage(cmd, false); err != nil {
			t.Fatalf("offerTokenPage: %v", err)
		}
		if q.asked != 0 || len(*calls) != 0 {
			t.Errorf("asked %d times, opener calls %v; want neither", q.asked, *calls)
		}
	})
}
