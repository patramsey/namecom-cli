package domain

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// transferLockedRefusal is the API's answer to an UpdateDomain carrying
// `locked` during the 60-day transfer lock, as the sandbox returns it.
const transferLockedRefusal = `{"message":"Invalid Argument","details":"Domain can not be unlocked until 2026-11-28 06:37:39"}`

// toggleCmd builds a toggle command under a root carrying --yes and --dry-run,
// with exactly the one named set.
func toggleCmd(t *testing.T, srv *httptest.Server, flag string) *cobra.Command {
	t.Helper()
	cmd := withRootFlags(t, baseCmd(t, srv))
	if err := cmd.Root().PersistentFlags().Set(flag, "true"); err != nil {
		t.Fatalf("setting %s: %v", flag, err)
	}
	return cmd
}

// TestToggles_AlreadyInStateSendNothing covers #187: `lock on` for a domain
// that is already locked failed during the transfer lock, because the API
// refuses any body carrying `locked`. A toggle now reads the domain first and,
// when it is already in the requested state, says so and exits 0 without a
// PATCH — under --dry-run too, which previews nothing rather than a request
// that would not be made.
func TestToggles_AlreadyInStateSendNothing(t *testing.T) {
	defer output.StubInteractive(false)()

	for _, tc := range []struct {
		name string
		run  func(*cobra.Command, []string) error
		on   bool
		want string
	}{
		{"lock on", runLock, true, "Transfer lock is already on"},
		{"lock off", runLock, false, "Transfer lock is already off"},
		{"autorenew on", runAutorenew, true, "Auto-renewal is already on"},
		{"autorenew off", runAutorenew, false, "Auto-renewal is already off"},
		{"privacy on", runPrivacy, true, "WHOIS privacy is already on"},
		{"privacy off", runPrivacy, false, "WHOIS privacy is already off"},
	} {
		for _, flag := range []string{"yes", "dry-run"} {
			t.Run(tc.name+" --"+flag, func(t *testing.T) {
				var requests []string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests = append(requests, r.Method)
					w.Header().Set("Content-Type", "application/json")
					if r.Method != http.MethodGet {
						// What the API does to a restated lock in the window.
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(transferLockedRefusal))
						return
					}
					// Already in the requested state: the opposite of toggleStub.
					_, _ = w.Write([]byte(toggleStub(!tc.on)))
				}))
				t.Cleanup(srv.Close)

				cmd := toggleCmd(t, srv, flag)
				if err := tc.run(cmd, []string{onOff(tc.on), "example.com"}); err != nil {
					t.Fatalf("a toggle already in the requested state must succeed: %v", err)
				}
				if strings.Join(requests, " ") != http.MethodGet {
					t.Errorf("want a single GET and no write, got %v", requests)
				}
				if got := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String(); !strings.Contains(got, tc.want) {
					t.Errorf("want %q in the output, got %q", tc.want, got)
				}
			})
		}
	}
}

// TestLockRefusal_TransferLockIsExplained covers the other half of the lock
// item in #187: when the API refuses to unlock during the transfer lock, the
// error keeps its date but says why, and stays an API error (exit 1).
func TestLockRefusal_TransferLockIsExplained(t *testing.T) {
	defer output.StubInteractive(false)()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(transferLockedRefusal))
			return
		}
		_, _ = w.Write([]byte(updateTransferLockedDomain))
	}))
	t.Cleanup(srv.Close)

	for name, run := range map[string]func() error{
		"lock off": func() error {
			return runLock(toggleCmd(t, srv, "yes"), []string{"off", "example.com"})
		},
		"update --lock=false": func() error {
			cmd := withRootFlags(t, cmdForUpdate(t, srv))
			if err := cmd.Root().PersistentFlags().Set("yes", "true"); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Flags().Set("lock", "false"); err != nil {
				t.Fatalf("setting lock: %v", err)
			}
			return runUpdate(cmd, []string{"example.com"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil {
				t.Fatal("want the refusal as an error")
			}
			msg := err.Error()
			for _, want := range []string{"cannot be unlocked until 2026-11-28 (", "60-day transfer lock"} {
				if !strings.Contains(msg, want) {
					t.Errorf("want %q in the error, got %q", want, msg)
				}
			}
			if apiErr, ok := errors.AsType[*api.APIError](err); !ok || apiErr.StatusCode != http.StatusBadRequest {
				t.Errorf("the API error must stay reachable so the exit code is unchanged, got %T", err)
			}
		})
	}
}

// privacyNotPurchased is the API's 409 for enabling WHOIS privacy on a domain
// that has none purchased.
const privacyNotPurchased = `{"message":"You may need to purchase WHOIS Privacy"}`

// TestToggles_PromptByRisk covers #227: removing the lock, turning privacy off
// and changing auto-renewal either way ask first, while turning privacy on
// (which never charges, #187) and locking do not. A decline sends nothing and
// exits 0, --yes sends without asking, and `domain update` asks exactly what
// the toggle command for the same change asks.
func TestToggles_PromptByRisk(t *testing.T) {
	for _, tc := range []struct {
		field, cmd string
		run        func(*cobra.Command, []string) error
		on         bool
		want       string // the prompt's opening, or "" for no prompt
	}{
		{"lock", "lock", runLock, false, "Remove the transfer lock on example.com? Anyone with its auth code"},
		{"lock", "lock", runLock, true, ""},
		{"privacy", "privacy", runPrivacy, false, "Turn off WHOIS privacy for example.com?"},
		{"privacy", "privacy", runPrivacy, true, ""},
		{"autorenew", "autorenew", runAutorenew, false, "Turn off auto-renewal for example.com?"},
		{"autorenew", "autorenew", runAutorenew, true, "Turn on auto-renewal for example.com? name.com will renew it"},
	} {
		serve := func(t *testing.T) (*httptest.Server, *int) {
			var writes int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					writes++
				}
				_, _ = w.Write([]byte(toggleStub(tc.on)))
			}))
			t.Cleanup(srv.Close)
			return srv, &writes
		}
		routes := map[string]func(*httptest.Server) (*cobra.Command, func() error){
			tc.cmd + " " + onOff(tc.on): func(srv *httptest.Server) (*cobra.Command, func() error) {
				cmd := withRootFlags(t, baseCmd(t, srv))
				return cmd, func() error { return tc.run(cmd, []string{onOff(tc.on), "example.com"}) }
			},
			"update --" + tc.field + "=" + strconv.FormatBool(tc.on): func(srv *httptest.Server) (*cobra.Command, func() error) {
				cmd := withRootFlags(t, cmdForUpdate(t, srv))
				if err := cmd.Flags().Set(tc.field, strconv.FormatBool(tc.on)); err != nil {
					t.Fatalf("setting %s: %v", tc.field, err)
				}
				return cmd, func() error { return runUpdate(cmd, []string{"example.com"}) }
			},
		}
		for name, build := range routes {
			t.Run(name+"/decline", func(t *testing.T) {
				asked := ""
				defer cmdutil.StubConfirm(func(p string) bool { asked = p; return false })()
				srv, writes := serve(t)
				_, run := build(srv)
				err := run()
				if tc.want == "" {
					if err != nil {
						t.Fatalf("no prompt, so want success, got %v", err)
					}
					if asked != "" || *writes != 1 {
						t.Errorf("want no prompt and one write, got prompt %q and %d write(s)", asked, *writes)
					}
					return
				}
				if !errors.Is(err, cmdutil.ErrAborted) {
					t.Fatalf("a decline must fail with cmdutil.ErrAborted (exit 1), got %v", err)
				}
				if !strings.HasPrefix(asked, tc.want) {
					t.Errorf("prompt %q should start %q", asked, tc.want)
				}
				if *writes != 0 {
					t.Errorf("declined, but %d write(s) were sent", *writes)
				}
			})
			t.Run(name+"/yes", func(t *testing.T) {
				defer output.StubInteractive(false)()
				srv, writes := serve(t)
				cmd, run := build(srv)
				if err := cmd.Root().PersistentFlags().Set("yes", "true"); err != nil {
					t.Fatal(err)
				}
				if err := run(); err != nil || *writes != 1 {
					t.Errorf("with --yes want one write and no error, got %d write(s), err %v", *writes, err)
				}
			})
		}
	}
}

// TestUpdate_SeveralRiskyFlagsAskOnce: each change that prompts is named in
// the one question, and a flag restating the current state adds nothing.
func TestUpdate_SeveralRiskyFlagsAskOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","locked":true,"autorenewEnabled":true,"privacyEnabled":false}`))
	}))
	t.Cleanup(srv.Close)
	cmd := withRootFlags(t, cmdForUpdate(t, srv))
	for flag, v := range map[string]string{"lock": "false", "autorenew": "false", "privacy": "false"} {
		if err := cmd.Flags().Set(flag, v); err != nil {
			t.Fatal(err)
		}
	}
	var prompts []string
	defer cmdutil.StubConfirm(func(p string) bool { prompts = append(prompts, p); return false })()
	if err := runUpdate(cmd, []string{"example.com"}); !errors.Is(err, cmdutil.ErrAborted) {
		t.Fatalf("declined: got %v, want cmdutil.ErrAborted", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("want one prompt, got %q", prompts)
	}
	for _, want := range []string{"transfer lock", "auto-renewal"} {
		if !strings.Contains(prompts[0], want) {
			t.Errorf("prompt %q should mention %q", prompts[0], want)
		}
	}
	if strings.Contains(prompts[0], "WHOIS privacy") {
		t.Errorf("privacy is already off, so the prompt %q should not mention it", prompts[0])
	}
}

// TestPrivacyOn_NotPurchasedIsExplained covers the 409 half of the privacy
// item in #187: the CLI cannot buy privacy, so the API's "You may need to
// purchase WHOIS Privacy" becomes an error saying where to buy it, still an
// API error (exit 1). The same 409 to turning privacy off is not about a
// purchase and is left alone.
func TestPrivacyOn_NotPurchasedIsExplained(t *testing.T) {
	defer output.StubInteractive(false)()

	serve := func(t *testing.T, current string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPatch {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(privacyNotPurchased))
				return
			}
			_, _ = w.Write([]byte(current))
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	for name, run := range map[string]func(*httptest.Server) error{
		"privacy on": func(srv *httptest.Server) error {
			return runPrivacy(toggleCmd(t, srv, "yes"), []string{"on", "example.com"})
		},
		"update --privacy=true": func(srv *httptest.Server) error {
			cmd := withRootFlags(t, cmdForUpdate(t, srv))
			if err := cmd.Root().PersistentFlags().Set("yes", "true"); err != nil {
				t.Fatalf("setting yes: %v", err)
			}
			if err := cmd.Flags().Set("privacy", "true"); err != nil {
				t.Fatalf("setting privacy: %v", err)
			}
			return runUpdate(cmd, []string{"example.com"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := run(serve(t, toggleStub(true)))
			if err == nil {
				t.Fatal("want an error")
			}
			for _, want := range []string{"not purchased", "https://www.name.com/account"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("want %q in the error, got %q", want, err)
				}
			}
			if apiErr, ok := errors.AsType[*api.APIError](err); !ok || apiErr.StatusCode != http.StatusConflict {
				t.Errorf("the API error must stay reachable so the exit code is unchanged, got %T", err)
			}
		})
	}

	t.Run("privacy off keeps the API's error", func(t *testing.T) {
		err := runPrivacy(toggleCmd(t, serve(t, toggleStub(false)), "yes"), []string{"off", "example.com"})
		if err == nil || strings.Contains(err.Error(), "not purchased") {
			t.Errorf("want the API's own error, got %v", err)
		}
	})
}

// TestToggles_DomainFirst pins #236: `domain lock example.com on` failed,
// though every other command takes the domain first. Both orders send the
// same PATCH.
func TestToggles_DomainFirst(t *testing.T) {
	defer output.StubInteractive(false)()
	for _, args := range [][]string{{"on", "example.com"}, {"example.com", "on"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var patched string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPatch {
					patched = r.URL.Path
				}
				_, _ = w.Write([]byte(toggleStub(true)))
			}))
			t.Cleanup(srv.Close)
			if err := runLock(toggleCmd(t, srv, "yes"), args); err != nil {
				t.Fatalf("lock %s: %v", strings.Join(args, " "), err)
			}
			if patched != "/core/v1/domains/example.com" {
				t.Errorf("lock %s patched %q, want /core/v1/domains/example.com", strings.Join(args, " "), patched)
			}
		})
	}
}
