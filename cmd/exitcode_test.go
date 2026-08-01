package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/spf13/cobra"
)

// TestExitCode guards the documented exit-code table, which scripts branch on:
//
//	0 success, 1 API/runtime, 2 usage, 3 auth, 4 not-found, 5 rate-limited
//
// exitCode derived the code solely from *api.APIError, so codes 2 and 3 were
// unreachable for anything that failed before an API call. A bad flag, a
// missing argument, and — worst — having no credentials at all, every one of
// them exited 1. The "no credentials" case also missed the exit-3 branch in
// Execute that prints "run 'namecom auth login'", so the most common auth
// failure got neither the code nor the hint.
func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil is success", nil, 0},
		{"generic runtime error", errors.New("boom"), 1},
		{"api 500", &api.APIError{StatusCode: 500}, 1},
		{"usage error", cmdutil.NewUsageError(errors.New("unknown flag: --bogus")), 2},
		{"wrapped usage error", fmt.Errorf("ctx: %w", cmdutil.NewUsageError(errors.New("bad arg"))), 2},
		{"auth error", cmdutil.NewAuthError(errors.New("no credentials configured")), 3},
		{"wrapped auth error", fmt.Errorf("ctx: %w", cmdutil.NewAuthError(errors.New("nope"))), 3},
		{"api 401", &api.APIError{StatusCode: 401}, 3},
		{"api 403", &api.APIError{StatusCode: 403}, 3},
		{"api 404", &api.APIError{StatusCode: 404}, 4},
		{"api 429", &api.APIError{StatusCode: 429}, 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// TestSkipClientInit_CoversCredentialFreeCommands guards a bootstrap problem:
// `completion` needs no API access, but it was not in the skip list, so
// `namecom completion zsh` failed with "no credentials configured" before the
// user had ever logged in. `auth login` finishes by suggesting shell
// completion, and `source <(namecom completion zsh)` in a shell rc broke every
// new shell until credentials existed.
//
// __complete is deliberately NOT in this list: dynamic completion (e.g. domain
// names) genuinely wants the API client. It degrades on its own — CompleteDomains
// returns no suggestions when the client is absent — so it is handled by
// tolerating a credential failure rather than skipping init outright.
func TestSkipClientInit_CoversCredentialFreeCommands(t *testing.T) {
	for _, name := range []string{"auth", "config", "open", "version", "completion", "help"} {
		t.Run(name, func(t *testing.T) {
			cmd := &cobra.Command{Use: name}
			root := &cobra.Command{Use: "namecom"}
			root.AddCommand(cmd)
			if !skipClientInit(cmd) {
				t.Errorf("%q requires no API credentials but would trigger client init", name)
			}
		})
	}
}

// TestSkipClientInit_StillRequiresCredentialsForAPICommands is the guard on the
// other side: broadening the skip list must not let a real API command run
// without credentials and fail later with a confusing error.
func TestSkipClientInit_StillRequiresCredentialsForAPICommands(t *testing.T) {
	for _, name := range []string{"domain", "dns", "email", "url", "vanity-ns", "transfer", "order", "dnssec", "api", "status"} {
		t.Run(name, func(t *testing.T) {
			cmd := &cobra.Command{Use: name}
			root := &cobra.Command{Use: "namecom"}
			root.AddCommand(cmd)
			if skipClientInit(cmd) {
				t.Errorf("%q talks to the API and must initialize credentials", name)
			}
		})
	}
}
