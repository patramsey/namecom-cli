package cmd

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/internal/config"
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
