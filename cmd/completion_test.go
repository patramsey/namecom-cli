package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
)

// runComplete drives cobra's hidden __complete command through the real root,
// which is what the shell runs on every TAB. It returns the candidates cobra
// printed, without the trailing ":<directive>" line.
func runComplete(t *testing.T, args ...string) []string {
	t.Helper()
	prev := gf
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil) })
	rootCmd.SetArgs(append([]string{cobra.ShellCompRequestCmd}, args...))
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("__complete %s: %v", strings.Join(args, " "), err)
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		got = append(got, line)
	}
	return got
}

// domainStub answers ListDomains with one domain and records the username of
// every request's Basic auth header.
type domainStub struct {
	*httptest.Server
	mu    sync.Mutex
	users []string
}

func newDomainStub(t *testing.T) *domainStub {
	t.Helper()
	s := &domainStub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := "?"
		if raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")); err == nil {
			user, _, _ = strings.Cut(string(raw), ":")
		}
		s.mu.Lock()
		s.users = append(s.users, user)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domains":[{"domainName":"example.com"}]}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *domainStub) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.users...)
}

// TestComplete_HonorsGlobalFlags guards issue #176. __complete disables flag
// parsing, so persistentPreRunE built the client from zero-valued globals
// before cobra parsed the flags of the command being completed: --profile,
// --token and --base-url typed on the line were ignored, and
// `namecom --profile prod dns list <TAB>` offered the default account's
// domains.
func TestComplete_HonorsGlobalFlags(t *testing.T) {
	// Two profiles and no default: nothing resolves unless --profile is read.
	const twoNoDefault = "profiles:\n" +
		"  alpha:\n    username: alphauser\n    token: atok\n" +
		"  beta:\n    username: betauser\n    token: btok\n"

	t.Run("--profile and --base-url", func(t *testing.T) {
		withConfig(t, twoNoDefault)
		srv := newDomainStub(t)
		got := runComplete(t, "--profile", "beta", "--base-url", srv.URL, "domain", "get", "")
		if len(got) != 1 || got[0] != "example.com" {
			t.Errorf("candidates = %v, want [example.com]", got)
		}
		if reqs := srv.requests(); len(reqs) != 1 || reqs[0] != "betauser" {
			t.Errorf("requests authenticated as %v, want one as betauser", reqs)
		}
	})

	t.Run("--token", func(t *testing.T) {
		withConfig(t, "profiles: {}\n")
		t.Setenv("NAMECOM_USERNAME", "envuser")
		srv := newDomainStub(t)
		got := runComplete(t, "dns", "list", "--token", "flagtok", "--base-url", srv.URL, "")
		if len(got) != 1 || got[0] != "example.com" {
			t.Errorf("candidates = %v, want [example.com]", got)
		}
		if reqs := srv.requests(); len(reqs) != 1 || reqs[0] != "envuser" {
			t.Errorf("requests authenticated as %v, want one as envuser", reqs)
		}
	})

	t.Run("an unknown --profile suggests nothing", func(t *testing.T) {
		withConfig(t, loneProfile)
		srv := newDomainStub(t)
		got := runComplete(t, "--profile", "nosuch", "--base-url", srv.URL, "domain", "get", "")
		if len(got) != 0 {
			t.Errorf("candidates = %v, want none for a profile that does not exist", got)
		}
		if reqs := srv.requests(); len(reqs) != 0 {
			t.Errorf("made %d requests with an unknown profile, want none", len(reqs))
		}
	})
}

// TestComplete_StaticCompletionSkipsCredentials guards issue #177. The client
// was built for every __complete, so a token_cmd — a password-manager helper,
// as the README recommends — ran on every TAB, even when completing
// subcommand names. Credentials are now resolved only when a completion
// function calls the API.
func TestComplete_StaticCompletionSkipsCredentials(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "token_cmd-ran")
	withConfig(t, "profiles:\n  work:\n    username: workuser\n    token_cmd: echo ran >> "+marker+"; echo wtok\n")

	ran := func() int {
		b, err := os.ReadFile(marker)
		if err != nil {
			return 0
		}
		return strings.Count(string(b), "ran")
	}

	for _, args := range [][]string{{""}, {"dns", ""}, {"domain", "list", "--output", ""}} {
		runComplete(t, args...)
	}
	if n := ran(); n != 0 {
		t.Fatalf("token_cmd ran %d times for static completions, want 0", n)
	}

	// Dynamic completion still resolves credentials, once.
	srv := newDomainStub(t)
	if got := runComplete(t, "--base-url", srv.URL, "domain", "get", ""); len(got) != 1 {
		t.Errorf("candidates = %v, want [example.com]", got)
	}
	if n := ran(); n != 1 {
		t.Errorf("token_cmd ran %d times for one dynamic completion, want 1", n)
	}
}

// TestComplete_ShortDeadlineNoRetries guards issue #178. Completion used the
// full --timeout (30s by default) with retries and backoff, so an API that
// accepted connections but never answered froze the shell for 30s per TAB.
func TestComplete_ShortDeadlineNoRetries(t *testing.T) {
	withConfig(t, loneProfile)

	t.Run("a hung API gives up quickly", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		t.Cleanup(srv.Close)
		t.Cleanup(func() { close(release) })

		start := time.Now()
		got := runComplete(t, "--base-url", srv.URL, "domain", "get", "")
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("completion took %s against a hung API, want about %s",
				elapsed.Round(time.Millisecond), cmdutil.CompletionTimeout)
		}
		if len(got) != 0 {
			t.Errorf("candidates = %v, want none on timeout", got)
		}
	})

	t.Run("a 503 is not retried", func(t *testing.T) {
		var requests atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(srv.Close)

		runComplete(t, "--base-url", srv.URL, "dns", "delete", "example.com", "")
		if n := requests.Load(); n != 1 {
			t.Errorf("made %d requests, want 1: completion must not retry", n)
		}
	})
}

// TestComplete_FlagValuesAndProfiles pins #187: -o, --color and --profile
// values, and the profile argument of `config use`, completed filenames.
func TestComplete_FlagValuesAndProfiles(t *testing.T) {
	withConfig(t, twoProfiles)
	tests := []struct {
		args []string
		want []string
	}{
		{[]string{"domain", "list", "-o", ""}, []string{"table", "json", "yaml"}},
		{[]string{"domain", "list", "--output", ""}, []string{"table", "json", "yaml"}},
		{[]string{"status", "--color", ""}, []string{"auto", "always", "never"}},
		{[]string{"status", "--profile", ""}, []string{"prod", "staging"}},
		{[]string{"auth", "logout", "--profile", ""}, []string{"prod", "staging"}},
		{[]string{"config", "use", ""}, []string{"prod", "staging"}},
	}
	for _, tc := range tests {
		got := runComplete(t, tc.args...)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("complete %q = %v, want %v", tc.args, got, tc.want)
		}
	}
	// Commands whose arguments are new names or a raw path offer no files:
	// no candidates, and a directive that turns the shell's file fallback off.
	for _, args := range [][]string{
		{"domain", "register", ""}, {"domain", "check", ""}, {"domain", "search", ""},
		{"transfer", "create", ""}, {"api", "GET", ""}, {"config", "use", ""},
	} {
		if d := completeDirective(t, args...); d&cobra.ShellCompDirectiveNoFileComp == 0 {
			t.Errorf("complete %q directive = %d, want NoFileComp set", args, d)
		}
	}
	if got := runComplete(t, "api", ""); !slices.Contains(got, "GET") || !slices.Contains(got, "DELETE") {
		t.Errorf("complete api = %v, want the HTTP methods", got)
	}
	// Once the profile is given, `config use` has nothing more to offer.
	if got := runComplete(t, "config", "use", "prod", ""); len(got) != 0 {
		t.Errorf("complete config use prod = %v, want none", got)
	}
}

// completeDirective is runComplete for the directive: the ":<n>" line cobra
// prints last, which tells the shell whether to fall back to filenames.
func completeDirective(t *testing.T, args ...string) cobra.ShellCompDirective {
	t.Helper()
	prev := gf
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil) })
	rootCmd.SetArgs(append([]string{cobra.ShellCompRequestCmd}, args...))
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("__complete %s: %v", strings.Join(args, " "), err)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if n, ok := strings.CutPrefix(line, ":"); ok {
			d, err := strconv.Atoi(n)
			if err != nil {
				t.Fatalf("bad directive line %q", line)
			}
			return cobra.ShellCompDirective(d)
		}
	}
	t.Fatalf("no directive in %q", stdout.String())
	return 0
}
