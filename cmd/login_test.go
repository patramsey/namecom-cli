package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
)

// stubLoginForm answers the login form with a fixed username and token,
// answers the sandbox question with sandboxAnswer, and reports whether that
// question was asked. It also points the credential check at a stub that
// accepts anything, so no test that logs in can reach the real API; tests of
// the check itself replace it with stubHello.
//
// The real form cannot run under go test: the token field is a password input,
// which huh reads from a terminal even in accessible mode.
func stubLoginForm(t *testing.T, sandboxAnswer bool) (askedSandbox *bool) {
	t.Helper()
	return stubLoginAnswers(t, "alice", "tok", sandboxAnswer)
}

func stubLoginAnswers(t *testing.T, username, token string, sandboxAnswer bool) (askedSandbox *bool) {
	t.Helper()
	asked := new(bool)
	prev := askLogin
	askLogin = func(a *loginAnswers, askSandbox bool) error {
		*asked = askSandbox
		a.Username, a.Token = username, token
		if askSandbox {
			a.Sandbox = sandboxAnswer
		}
		return nil
	}
	t.Cleanup(func() { askLogin = prev })
	t.Cleanup(output.StubInteractive(true))
	stubOpenTokenPage(t, false)
	stubRetryLogin(t, false)
	stubReplaceProfile(t, true)
	prevGF := gf
	gf = globalFlags{baseURL: helloServer(t).URL}
	t.Cleanup(func() { gf = prevGF })
	return asked
}

// helloStub records what the credential check sent.
type helloStub struct {
	hits       atomic.Int32
	user, pass atomic.Value
}

// stubHello points the login credential check at a server answering status
// with body.
func stubHello(t *testing.T, status int, body string) *helloStub {
	t.Helper()
	s := &helloStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		u, p, _ := r.BasicAuth()
		s.user.Store(u)
		s.pass.Store(p)
		if r.URL.Path != "/core/v1/hello" {
			t.Errorf("credential check sent %s %s, want GET /core/v1/hello", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	gf.baseURL = srv.URL
	return s
}

// stubUnreachable points the credential check at a server that drops every
// connection without answering. A closed port would do the same job, but on
// Windows a refused connection to localhost takes about two seconds to fail.
func stubUnreachable(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	gf.baseURL = srv.URL
}

// stubSaveUnverified answers the "save unverified credentials?" question and
// reports whether it was asked.
func stubSaveUnverified(t *testing.T, answer bool) *bool {
	t.Helper()
	asked := new(bool)
	prev := confirmSaveUnverified
	confirmSaveUnverified = func(_ *output.Config, yes bool, _, _ string) (bool, error) {
		*asked = true
		return answer || yes, nil
	}
	t.Cleanup(func() { confirmSaveUnverified = prev })
	return asked
}

// stubRetryLogin answers "try again?" after a rejection and counts how often
// it was asked.
func stubRetryLogin(t *testing.T, answer bool) *int {
	t.Helper()
	asked := new(int)
	prev := confirmRetryLogin
	confirmRetryLogin = func(_ *output.Config, _ bool, _, _ string) (bool, error) {
		*asked++
		return answer, nil
	}
	t.Cleanup(func() { confirmRetryLogin = prev })
	return asked
}

// stubReplaceProfile answers "replace the existing profile?" and counts how
// often it was asked.
func stubReplaceProfile(t *testing.T, answer bool) *int {
	t.Helper()
	asked := new(int)
	prev := confirmReplaceProfile
	confirmReplaceProfile = func(_ *output.Config, _ bool, _, _ string) (bool, error) {
		*asked++
		return answer, nil
	}
	t.Cleanup(func() { confirmReplaceProfile = prev })
	return asked
}

func setLoginProfile(t *testing.T, name string) {
	t.Helper()
	prev := loginProfile
	loginProfile = name
	t.Cleanup(func() { loginProfile = prev })
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
			setLoginProfile(t, "sbx")

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

// TestAuthLogin_VerifiesBeforeSaving guards #229: login saved whatever was
// typed and said "Credentials saved", so a typo surfaced as a 401 on the first
// real command. It now checks the credentials with Hello first.
func TestAuthLogin_VerifiesBeforeSaving(t *testing.T) {
	t.Run("success saves and says who is logged in where", func(t *testing.T) {
		withConfig(t, "")
		stubLoginForm(t, false)
		setLoginProfile(t, "work")
		hello := stubHello(t, http.StatusOK, `{"username":"alice"}`)

		cmd, buf := jsonCmd(t)
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin: %v", err)
		}
		if hello.hits.Load() != 1 {
			t.Errorf("credential check sent %d requests, want 1", hello.hits.Load())
		}
		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("output is not one JSON document: %v\n%s", err, buf.String())
		}
		if want := "Logged in to production as alice (profile work)"; got["message"] != want {
			t.Errorf("message = %q, want %q", got["message"], want)
		}
		f, _ := config.Load()
		if p := f.Profiles["work"]; p.Username != "alice" || p.Token != "tok" {
			t.Errorf("saved %+v, want alice/tok", p)
		}
	})

	t.Run("sandbox is named in the message", func(t *testing.T) {
		withConfig(t, "")
		stubLoginAnswers(t, "alice-test", "tok", true)
		setLoginProfile(t, "sbx")
		stubHello(t, http.StatusOK, `{"username":"alice-test"}`)

		cmd, buf := jsonCmd(t)
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin: %v", err)
		}
		if !strings.Contains(buf.String(), "Logged in to sandbox as alice-test (profile sbx)") {
			t.Errorf("output does not say sandbox:\n%s", buf.String())
		}
	})

	t.Run("whitespace is trimmed before checking and saving", func(t *testing.T) {
		withConfig(t, "")
		stubLoginAnswers(t, "  alice\t", "faketoken123 \n", false)
		setLoginProfile(t, "work")
		hello := stubHello(t, http.StatusOK, `{"username":"alice"}`)

		cmd, _ := jsonCmd(t)
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin: %v", err)
		}
		if u, p := hello.user.Load(), hello.pass.Load(); u != "alice" || p != "faketoken123" {
			t.Errorf("credential check sent %q/%q, want trimmed alice/faketoken123", u, p)
		}
		f, _ := config.Load()
		if p := f.Profiles["work"]; p.Username != "alice" || p.Token != "faketoken123" {
			t.Errorf("saved %q/%q, want trimmed alice/faketoken123", p.Username, p.Token)
		}
	})
}

// TestAuthLogin_RejectedCredentialsAreNotSaved: a 401 says so, explains the
// likely sandbox/production mix-up, exits 3, and leaves the config alone.
func TestAuthLogin_RejectedCredentialsAreNotSaved(t *testing.T) {
	const existing = "default: prod\nprofiles:\n  prod:\n    username: old\n    token: oldtok\n"
	tests := []struct {
		name     string
		username string
		sandbox  bool
		want     []string
		dontWant []string
	}{
		{"production", "alice", false, []string{"rejected", "production"}, []string{"--sandbox"}},
		{"-test username against production", "alice-test", false, []string{"rejected", "-test", "--sandbox"}, nil},
		{"production username against sandbox", "alice", true, []string{"rejected", "sandbox", "-test"}, nil},
		{"-test username against sandbox", "alice-test", true, []string{"rejected", "sandbox"}, []string{"usually end"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := withConfig(t, existing)
			stubLoginAnswers(t, tt.username, "tok", tt.sandbox)
			setLoginProfile(t, "prod")
			stubHello(t, http.StatusUnauthorized, `{"message":"Unauthorized"}`)
			asked := stubSaveUnverified(t, true)

			cmd, _ := jsonCmd(t)
			err := runAuthLogin(cmd, nil)
			if err == nil {
				t.Fatal("runAuthLogin accepted credentials the API rejected")
			}
			if got := exitCode(err); got != 3 {
				t.Errorf("exit code = %d, want 3", got)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error does not mention %q: %v", w, err)
				}
			}
			for _, w := range tt.dontWant {
				if strings.Contains(err.Error(), w) {
					t.Errorf("error should not mention %q: %v", w, err)
				}
			}
			if *asked {
				t.Error("offered to save credentials the API rejected")
			}
			unchanged(t, path, existing)
		})
	}
}

// TestAuthLogin_Unreachable: when the API cannot be reached the credentials
// are unverified, not wrong — so the user decides, and --yes never decides
// for them.
func TestAuthLogin_Unreachable(t *testing.T) {
	tests := []struct {
		name      string
		answer    bool
		yes       bool
		wantSaved bool
		wantErr   bool
		wantAsked bool
	}{
		{"saved when the user says yes", true, false, true, false, true},
		{"not saved when the user says no", false, false, false, false, true},
		{"--yes refuses rather than saving unverified", false, true, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := withConfig(t, loneProfile)
			stubLoginForm(t, false)
			setLoginProfile(t, "fresh")
			stubUnreachable(t)
			asked := stubSaveUnverified(t, tt.answer)

			cmd, _ := jsonCmd(t)
			cmd.PersistentFlags().Bool("yes", tt.yes, "")
			err := runAuthLogin(cmd, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("runAuthLogin error = %v, want error %v", err, tt.wantErr)
			}
			if *asked != tt.wantAsked {
				t.Errorf("asked to save unverified = %v, want %v", *asked, tt.wantAsked)
			}
			f, _ := config.Load()
			if _, saved := f.Profiles["fresh"]; saved != tt.wantSaved {
				t.Errorf("profile saved = %v, want %v", saved, tt.wantSaved)
			}
			if !tt.wantSaved {
				unchanged(t, path, loneProfile)
			}
		})
	}
}

// TestAuthLogin_DryRunVerifiesButNeverWrites: the check is a read, so
// --dry-run runs it — the preview then answers "would this work?" — but the
// config file is never touched, whatever the answer.
func TestAuthLogin_DryRunVerifiesButNeverWrites(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		path := withConfig(t, loneProfile)
		stubLoginForm(t, false)
		setLoginProfile(t, "fresh")
		hello := stubHello(t, http.StatusOK, `{"username":"alice"}`)

		cmd, _ := jsonCmd(t)
		cmd.PersistentFlags().Bool("dry-run", true, "")
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin --dry-run: %v", err)
		}
		if hello.hits.Load() != 1 {
			t.Errorf("--dry-run sent %d credential checks, want 1", hello.hits.Load())
		}
		unchanged(t, path, loneProfile)
	})

	t.Run("rejected", func(t *testing.T) {
		path := withConfig(t, loneProfile)
		stubLoginForm(t, false)
		setLoginProfile(t, "fresh")
		stubHello(t, http.StatusUnauthorized, `{"message":"Unauthorized"}`)

		cmd, _ := jsonCmd(t)
		cmd.PersistentFlags().Bool("dry-run", true, "")
		if err := runAuthLogin(cmd, nil); err == nil || exitCode(err) != 3 {
			t.Errorf("--dry-run with rejected credentials = %v, want an exit-3 error", err)
		}
		unchanged(t, path, loneProfile)
	})

	t.Run("unreachable previews without asking", func(t *testing.T) {
		path := withConfig(t, loneProfile)
		stubLoginForm(t, false)
		setLoginProfile(t, "fresh")
		stubUnreachable(t)
		asked := stubSaveUnverified(t, true)

		cmd, buf := jsonCmd(t)
		cmd.PersistentFlags().Bool("dry-run", true, "")
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin --dry-run: %v", err)
		}
		if *asked {
			t.Error("--dry-run asked whether to save")
		}
		if !strings.Contains(buf.String(), "save_profile") {
			t.Errorf("--dry-run did not preview the save:\n%s", buf.String())
		}
		unchanged(t, path, loneProfile)
	})
}

// TestAuthLogin_SaysWhereTokensComeFrom: neither the help nor the form said
// where to get a token, and nothing mentioned that sandbox credentials are
// separate (#239). The rejection hint points at the same page.
func TestAuthLogin_SaysWhereTokensComeFrom(t *testing.T) {
	for _, want := range []string{apiSettingsURL, "-test"} {
		if !strings.Contains(authLoginCmd.Long, want) {
			t.Errorf("auth login help does not mention %q:\n%s", want, authLoginCmd.Long)
		}
	}
	if !strings.Contains(tokenFieldDescription, apiSettingsURL) {
		t.Errorf("token field description does not link %s: %q", apiSettingsURL, tokenFieldDescription)
	}
	var rejected *loginRejectedError
	if !strings.Contains(rejected.UserHint(), apiSettingsURL) {
		t.Errorf("rejection hint does not link %s: %q", apiSettingsURL, rejected.UserHint())
	}
}
