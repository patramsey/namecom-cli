package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// TestAuthLogin_OffersRetryAfterRejection guards #239: a 401 at login ended
// the command, and the user started again from nothing. It now offers the
// form again, keeping the username and clearing the token; declining, --yes
// and --dry-run still end with the exit-3 rejection.
func TestAuthLogin_OffersRetryAfterRejection(t *testing.T) {
	t.Run("retry with a corrected token saves it", func(t *testing.T) {
		withConfig(t, "")
		stubLoginForm(t, false)
		setLoginProfile(t, "work")
		asked := stubRetryLogin(t, true)

		// The first form answers a bad token; the second sees what the first
		// left behind, then answers the good one.
		var calls int
		var secondUser, secondToken string
		prev := askLogin
		askLogin = func(a *loginAnswers, _ bool) error {
			calls++
			if calls == 1 {
				a.Username, a.Token = "alice", "bad"
				return nil
			}
			secondUser, secondToken = a.Username, a.Token
			a.Token = "good"
			return nil
		}
		t.Cleanup(func() { askLogin = prev })

		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if _, p, _ := r.BasicAuth(); p != "good" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"username":"alice"}`))
		}))
		t.Cleanup(srv.Close)
		gf.baseURL = srv.URL

		cmd, _ := jsonCmd(t)
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin: %v", err)
		}
		if *asked != 1 || calls != 2 || hits.Load() != 2 {
			t.Errorf("asked to retry %d times, form ran %d times, %d checks; want 1, 2, 2", *asked, calls, hits.Load())
		}
		if secondUser != "alice" || secondToken != "" {
			t.Errorf("second form started with %q/%q, want the username kept and the token cleared", secondUser, secondToken)
		}
		f, _ := config.Load()
		if p := f.Profiles["work"]; p.Token != "good" {
			t.Errorf("saved token %q, want the corrected one", p.Token)
		}
	})

	for _, tt := range []struct {
		name      string
		flag      string
		wantAsked int
	}{
		{"declining ends with the rejection", "", 1},
		{"--yes never asks", "yes", 0},
		{"--dry-run never asks", "dry-run", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := withConfig(t, loneProfile)
			stubLoginForm(t, false)
			setLoginProfile(t, "fresh")
			stubHello(t, http.StatusUnauthorized, `{"message":"Unauthorized"}`)
			asked := stubRetryLogin(t, false)

			cmd, _ := jsonCmd(t)
			if tt.flag != "" {
				cmd.PersistentFlags().Bool(tt.flag, true, "")
			}
			err := runAuthLogin(cmd, nil)
			if err == nil || exitCode(err) != 3 {
				t.Errorf("runAuthLogin = %v, want the exit-3 rejection", err)
			}
			if *asked != tt.wantAsked {
				t.Errorf("asked to retry %d times, want %d", *asked, tt.wantAsked)
			}
			unchanged(t, path, loneProfile)
		})
	}
}

// TestAuthLogin_AsksBeforeReplacingAProfile guards #239: logging in again
// under an existing profile replaced its credentials without a word.
func TestAuthLogin_AsksBeforeReplacingAProfile(t *testing.T) {
	for _, tt := range []struct {
		name      string
		flag      string
		answer    bool
		wantAsked int
		wantSaved bool
	}{
		{"yes replaces", "", true, 1, true},
		{"no keeps the profile", "", false, 1, false},
		{"--yes replaces without asking", "yes", false, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := withConfig(t, loneProfile)
			stubLoginForm(t, false)
			setLoginProfile(t, "work")
			replace := stubReplaceProfile(t, tt.answer)

			cmd, _ := jsonCmd(t)
			if tt.flag != "" {
				cmd.PersistentFlags().Bool(tt.flag, true, "")
			}
			err := runAuthLogin(cmd, nil)
			// Declining is an abort, which exits 1 like every other (#236).
			if tt.wantSaved && err != nil {
				t.Fatalf("runAuthLogin: %v", err)
			}
			if !tt.wantSaved && !errors.Is(err, cmdutil.ErrAborted) {
				t.Fatalf("runAuthLogin = %v, want cmdutil.ErrAborted", err)
			}
			if *replace != tt.wantAsked {
				t.Errorf("asked to replace %d times, want %d", *replace, tt.wantAsked)
			}
			f, _ := config.Load()
			if saved := f.Profiles["work"].Username == "alice"; saved != tt.wantSaved {
				t.Errorf("profile replaced = %v, want %v", saved, tt.wantSaved)
			}
			if !tt.wantSaved {
				unchanged(t, path, loneProfile)
			}
		})
	}

	t.Run("a new profile is not asked about", func(t *testing.T) {
		withConfig(t, loneProfile)
		stubLoginForm(t, false)
		setLoginProfile(t, "other")
		replace := stubReplaceProfile(t, false)
		cmd, _ := jsonCmd(t)
		if err := runAuthLogin(cmd, nil); err != nil {
			t.Fatalf("runAuthLogin: %v", err)
		}
		if *replace != 0 {
			t.Error("asked to replace a profile that did not exist")
		}
	})
}

// TestAuthLogin_HintNamesTheProfile guards #239: after logging in to a
// profile that is not the default, "Run 'namecom status'" showed the default
// profile's account.
func TestAuthLogin_HintNamesTheProfile(t *testing.T) {
	for _, tt := range []struct {
		name, contents, profile string
		want, dontWant          []string
	}{
		{"first login becomes the default", "", "work",
			[]string{"Run 'namecom status' to"}, []string{"--profile"}},
		{"another profile is the default", twoProfiles, "work",
			[]string{"namecom status --profile work", "namecom config use work"}, nil},
		{"logging in to the default again", twoProfiles, "prod",
			[]string{"Run 'namecom status' to"}, []string{"--profile"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			withConfig(t, tt.contents)
			stubLoginForm(t, false)
			setLoginProfile(t, tt.profile)

			var buf bytes.Buffer
			// One buffer for both streams: the success line is on stdout, the
			// hints on stderr.
			out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &buf, EWriter: &buf}
			cmd := &cobra.Command{}
			cmd.SetContext(context.WithValue(context.Background(), cmdutil.KeyOutput, out))
			if err := runAuthLogin(cmd, nil); err != nil {
				t.Fatalf("runAuthLogin: %v", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(buf.String(), w) {
					t.Errorf("output does not contain %q:\n%s", w, buf.String())
				}
			}
			for _, w := range tt.dontWant {
				if strings.Contains(buf.String(), w) {
					t.Errorf("output contains %q:\n%s", w, buf.String())
				}
			}
		})
	}
}
