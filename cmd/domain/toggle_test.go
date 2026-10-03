package domain

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
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
			for _, want := range []string{"cannot be unlocked until 2026-11-28 06:37:39", "60-day transfer lock"} {
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
