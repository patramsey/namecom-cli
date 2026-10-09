package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
		// #236: a declined or cancelled prompt is a failure a script can see.
		{"declined prompt", cmdutil.ErrAborted, 1},
		{"declined with detail", fmt.Errorf("%w: profile kept", cmdutil.ErrAborted), 1},
		{"usage error", cmdutil.NewUsageError(errors.New("unknown flag: --bogus")), 2},
		{"wrapped usage error", fmt.Errorf("ctx: %w", cmdutil.NewUsageError(errors.New("bad arg"))), 2},
		{"auth error", cmdutil.NewAuthError(errors.New("no credentials configured")), 3},
		{"wrapped auth error", fmt.Errorf("ctx: %w", cmdutil.NewAuthError(errors.New("nope"))), 3},
		{"api 401", &api.APIError{StatusCode: 401}, 3},
		{"api 403", &api.APIError{StatusCode: 403}, 3},
		// #324: a 403 about the domain is not a credential problem.
		{"api 403 expired domain", &api.APIError{StatusCode: 403, Message: "Permission denied. The domain is expired."}, 1},
		{"api 404", &api.APIError{StatusCode: 404}, 4},
		{"api 429", &api.APIError{StatusCode: 429}, 5},
		// #243: a write that may have gone through.
		{"write outcome unknown", &api.OutcomeUnknownError{Method: "POST", Err: &api.APIError{StatusCode: 500}}, 6},
		// #156: a DNS failure is a runtime error, not the usage code its
		// panic used to exit with.
		{"dns failure", fmt.Errorf("getting domain: %w", &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}), 1},
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
// names) genuinely wants the API client. persistentPreRunE gives it a factory
// that builds the client only when a completion function asks, and
// CompleteDomains returns no suggestions when that fails.
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

// TestBaseURLOverride guards the escape hatch that lets the CLI be pointed at
// something other than production or sandbox.
//
// Without it, the only way to exercise a command end-to-end is to construct an
// api.Client in Go — running the built binary against a local stub silently
// went to the real api.name.com instead. That is how an unintended request to
// production happened during development.
//
// Because the flag redirects authenticated traffic, it must warn when it points
// somewhere other than name.com: the account credential goes wherever it says.
func TestBaseURLOverride(t *testing.T) {
	tests := []struct {
		name, baseURL string
		wantWarn      bool
	}{
		{"local stub warns", "http://127.0.0.1:8080", true},
		{"arbitrary host warns", "https://example.invalid", true},
		{"official production is silent", "https://api.name.com", false},
		{"official sandbox is silent", "https://api.dev.name.com", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := baseURLWarning(tc.baseURL); (got != "") != tc.wantWarn {
				t.Errorf("baseURLWarning(%q) = %q; warning expected = %v", tc.baseURL, got, tc.wantWarn)
			}
		})
	}
}

// TestBaseURLOverride_Rejected covers values that cannot work, caught before a
// request rather than as an opaque transport error.
func TestBaseURLOverride_Rejected(t *testing.T) {
	for _, bad := range []string{"not a url", "ftp://example.com", "api.name.com", "//example.com"} {
		if err := validateBaseURL(bad); err == nil {
			t.Errorf("validateBaseURL(%q) should reject: needs an absolute http(s) URL", bad)
		}
	}
	for _, good := range []string{"https://api.name.com", "http://127.0.0.1:8080", "https://stub.test:9000/prefix"} {
		if err := validateBaseURL(good); err != nil {
			t.Errorf("validateBaseURL(%q) should accept, got: %v", good, err)
		}
	}
}

// sdkNotFound returns the error the Core SDK itself produces for a 404, by
// making a real call against a stub — not an error constructed by hand.
//
// That distinction is the whole point. TestExitCode above only ever fed
// exitCode an *api.APIError, the pre-migration client's type, so it kept
// passing while every command that returned the SDK's own error unconverted
// exited 1 on a 404 instead of 4. A fixture built from the type the code
// already understands cannot catch a failure to understand a different one.
func sdkNotFound(t *testing.T) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	_, err = client.SDK().Domains.GetDomain(context.Background(),
		&coreapigo.GetDomainRequest{DomainName: "example.com"})
	if err == nil {
		t.Fatal("stub returned 404 but the SDK reported no error")
	}
	if _, ok := errors.AsType[*api.APIError](err); ok {
		t.Fatal("fixture is already an *api.APIError; it would not exercise the SDK's own error type")
	}
	return err
}

// TestExitCode_SDKNotFound pins the contract for the error commands actually
// return. Nine commands (domain get, transfer get, order get, …) exited 1 on a
// 404 because their SDK error reached exitCode unconverted.
func TestExitCode_SDKNotFound(t *testing.T) {
	sdkErr := sdkNotFound(t)
	tests := []struct {
		name string
		err  error
	}{
		{"raw SDK 404", sdkErr},
		{"wrapped SDK 404", fmt.Errorf("fetching domain: %w", sdkErr)},
		{"friendly not-found message over an SDK 404",
			cmdutil.DomainNotFound(sdkErr, "example.com")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != 4 {
				t.Errorf("exit code = %d, want 4 (not found)", got)
			}
		})
	}
}

// TestNormalizeError_Message pins what the user reads. Unconverted, the SDK's
// error prints as `404: {"message":"Not Found"}` — the raw response body.
func TestNormalizeError_Message(t *testing.T) {
	sdkErr := sdkNotFound(t)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"raw SDK 404", sdkErr, "Not Found"},
		{"context around it is kept", fmt.Errorf("fetching domain: %w", sdkErr), "fetching domain: Not Found"},
		{"friendly message is kept verbatim",
			cmdutil.DomainNotFound(sdkErr, "example.com"), `domain "example.com" not found`},
		{"non-API error is untouched", errors.New("boom"), "boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeError(tc.err).Error()
			if got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, `{"message"`) {
				t.Errorf("message leaks the raw response body: %q", got)
			}
		})
	}
}

// TestExitCode_InvalidPositionalArg guards issue #115: a positional argument
// the command cannot parse — a non-numeric ID, an on/off toggle that is
// neither — is an invocation mistake and must exit 2 like a bad flag does.
// Each case used to return a bare error and exit 1, which a script cannot
// tell apart from an API failure.
//
// The commands run through the real root, so the argument is parsed exactly
// where it is in production. The stub fails the test if any of them gets as
// far as a request: every case must be rejected before touching the API.
func TestExitCode_InvalidPositionalArg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s: a bad argument must fail before any API call", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	withConfig(t, loneProfile)

	tests := [][]string{
		{"dns", "delete", "example.com", "abc"},
		{"dns", "update", "example.com", "abc", "--answer", "1.2.3.4"},
		{"url", "get", "example.com", "abc"},
		{"url", "update", "example.com", "abc", "--to", "https://example.org"},
		{"url", "delete", "example.com", "abc"},
		{"order", "get", "abc"},
		{"contact", "resend", "abc"},
		{"contact", "verify", "abc"},
		{"domain", "lock", "maybe", "example.com"},
		{"domain", "autorenew", "maybe", "example.com"},
		{"domain", "privacy", "maybe", "example.com"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			prev := gf
			t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil) })
			rootCmd.SetArgs(append([]string{"--base-url", srv.URL, "--yes", "-o", "json"}, args...))
			err := cmdutil.ClassifyCobraUsage(rootCmd.ExecuteContext(context.Background()))
			if err == nil {
				t.Fatalf("namecom %s succeeded; want a usage error", strings.Join(args, " "))
			}
			// A usage error about something else — a mistyped flag in this
			// table — would exit 2 too, and pass for the wrong reason.
			if msg := err.Error(); !strings.Contains(msg, `"abc"`) && !strings.Contains(msg, `"maybe"`) {
				t.Fatalf("namecom %s failed for another reason: %v", strings.Join(args, " "), err)
			}
			if got := exitCode(err); got != 2 {
				t.Errorf("namecom %s exited %d (%v); want 2", strings.Join(args, " "), got, err)
			}
		})
	}
}

// TestExitCode_APIUnknownMethod guards issue #133. `namecom api FOO /x` was
// sent as-is; nginx answers an unknown method with 403, which exits 3 and
// tells the user to run `auth login` — for a typo in the method. The method
// is checked first, so the stub fails the test if a request goes out.
func TestExitCode_APIUnknownMethod(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s: an unknown method must fail before any API call", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	withConfig(t, loneProfile)

	// --dry-run too: a method that is not one cannot be previewed either.
	for _, extra := range [][]string{nil, {"--dry-run"}} {
		for _, method := range []string{"FOO", "gett", "CONNECT"} {
			t.Run(strings.Join(append([]string{method}, extra...), " "), func(t *testing.T) {
				prev := gf
				t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil) })
				args := append([]string{"--base-url", srv.URL, "-o", "json", "api", method, "/core/v1/hello"}, extra...)
				rootCmd.SetArgs(args)
				err := cmdutil.ClassifyCobraUsage(rootCmd.ExecuteContext(context.Background()))
				if err == nil {
					t.Fatalf("namecom api %s succeeded; want a usage error", method)
				}
				if msg := err.Error(); !strings.Contains(msg, method) || !strings.Contains(msg, "PATCH") {
					t.Errorf("error should name the method and the allowed ones, got: %v", err)
				}
				if got := exitCode(err); got != 2 {
					t.Errorf("namecom api %s exited %d (%v); want 2", method, got, err)
				}
			})
		}
	}
}

// TestExitCode_APIMethodFlag pins #270 through the root command: -X is
// parsed, and an unknown -X, -X disagreeing with the method argument, and
// --paginate with -X POST exit 2 before any request. A write named with -X is
// previewed under --dry-run, not sent.
func TestExitCode_APIMethodFlag(t *testing.T) {
	withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	for _, args := range [][]string{
		{"api", "-X", "FETCH", "/core/v1/hello"},
		{"api", "GET", "/core/v1/hello", "-X", "post"},
		{"api", "--method", "POST", "/core/v1/domains", "--paginate"},
	} {
		resetFlags(t, args)
		_, stderr, code := runContract(t, append([]string{"--base-url", srv.URL}, args...)...)
		if code != 2 {
			t.Errorf("namecom %s: exit %d, want 2; stderr:\n%s", strings.Join(args, " "), code, stderr)
		}
	}
	args := []string{"api", "-X", "delete", "/core/v1/domains/example.com/records/1", "--data", "", "--dry-run"}
	resetFlags(t, args)
	stdout, stderr, code := runContract(t, append([]string{"--base-url", srv.URL}, args...)...)
	if code != 0 || !strings.Contains(stdout, `"method": "DELETE"`) {
		t.Errorf("exit %d, stdout %q, stderr:\n%s", code, stdout, stderr)
	}
}

// TestExitCode_ExtraArgsAreUsageErrors pins #187: commands that take no
// arguments accepted and ignored any, so `namecom status example.com` looked
// like it reported on that domain.
func TestExitCode_ExtraArgsAreUsageErrors(t *testing.T) {
	// Should a command run anyway, keep it off any real config and API.
	withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected, got %s %s", r.Method, r.URL)
	}))
	t.Cleanup(srv.Close)
	for _, args := range [][]string{
		{"status", "example.com"},
		{"version", "extra"},
		{"auth", "status", "extra"},
		{"auth", "logout", "extra"},
		{"auth", "login", "extra"},
	} {
		args = append([]string{"--base-url", srv.URL}, args...)
		if got := exitCode(cmdutil.ClassifyCobraUsage(executeRoot(t, args...))); got != 2 {
			t.Errorf("namecom %s: exit %d, want 2", strings.Join(args, " "), got)
		}
	}
}

// TestExitCode_OpenTooManyArgs guards issue #165: "accepts at most 1 arg(s)"
// from cobra.MaximumNArgs was not classified, so `namecom open a.com b.com`
// exited 1 like a runtime failure. The launcher is stubbed in case the
// argument check ever stops firing: no test may open a real browser.
func TestExitCode_OpenTooManyArgs(t *testing.T) {
	withConfig(t, loneProfile)
	stubStart(t, func(name string) error {
		t.Errorf("launched %q: extra arguments must fail before opening anything", name)
		return nil
	})
	prev := gf
	t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil) })
	rootCmd.SetArgs([]string{"-o", "json", "open", "a.com", "b.com"})
	err := cmdutil.ClassifyCobraUsage(rootCmd.ExecuteContext(context.Background()))
	if err == nil {
		t.Fatal("namecom open a.com b.com succeeded; want a usage error")
	}
	if !strings.Contains(err.Error(), "at most 1 arg") {
		t.Fatalf("failed for another reason: %v", err)
	}
	if got := exitCode(err); got != 2 {
		t.Errorf("exited %d (%v); want 2", got, err)
	}
}

// resetFlags returns the flags of the command args name to their defaults,
// now and when the test ends. Subcommand flags are package variables that
// outlive one Execute, so a value another test passed would otherwise satisfy
// the flag this test leaves out, and a bad value this test passes would fail
// the next.
func resetFlags(t *testing.T, args []string) {
	t.Helper()
	c, _, err := rootCmd.Find(args)
	if err != nil {
		t.Fatalf("finding %v: %v", args, err)
	}
	reset := func() {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			// Set appends to a slice flag, so a slice is emptied instead.
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		})
	}
	reset()
	t.Cleanup(reset)
}

// TestExitCode_MissingRequiredFlagIsUsage pins #236: a flag a command needs,
// left out off a terminal, exited 1 from url and email create — even where the
// help said "(required)" — while transfer create exited 2. Declining to send a
// write without --yes is a missing flag too, and exited 1 as well.
func TestExitCode_MissingRequiredFlagIsUsage(t *testing.T) {
	withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// dns delete reads the record first so its prompt can show it (#235);
		// that read is allowed. Nothing else — and no write — may be sent.
		if r.Method == http.MethodGet && r.URL.Path == "/core/v1/domains/example.com/records/123" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":123,"domainName":"example.com","host":"www","fqdn":"www.example.com.","type":"A","answer":"192.0.2.1","ttl":300}`))
			return
		}
		t.Errorf("no request expected, got %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"url", "create", "example.com"}, `"to"`},
		{[]string{"email", "create", "example.com", "info"}, `"to"`},
		{[]string{"email", "update", "example.com", "info"}, `"to"`},
		{[]string{"transfer", "create", "example.com"}, `"auth-code"`},
		{[]string{"dns", "create", "example.com"}, `"type", "answer"`},
		{[]string{"order", "refund"}, `"item-ids"`},
		{[]string{"domain", "contacts", "set", "example.com"}, `"contacts-file"`},
		{[]string{"dns", "delete", "example.com", "123"}, "confirmation required for"},
		{[]string{"email", "delete", "example.com", "info"}, "confirmation required for"},
	} {
		t.Run(strings.Join(tc.args[:2], " "), func(t *testing.T) {
			resetFlags(t, tc.args)
			err := cmdutil.ClassifyCobraUsage(executeRoot(t, append([]string{"--base-url", srv.URL, "-o", "json"}, tc.args...)...))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("namecom %s: error %v, want one naming %s", strings.Join(tc.args, " "), err, tc.want)
			}
			if got := exitCode(err); got != 2 {
				t.Errorf("namecom %s exited %d (%v); want 2", strings.Join(tc.args, " "), got, err)
			}
		})
	}
}

// pagedLists is every list command the API pages.
var pagedLists = [][]string{
	{"domain", "list"},
	{"dns", "list", "example.com"},
	{"email", "list", "example.com"},
	{"url", "list", "example.com"},
	{"vanity-ns", "list", "example.com"},
	{"transfer", "list"},
	{"order", "list"},
	{"contact", "unverified"},
}

// TestPagedLists_PageAndLimit pins #236: --page existed only on domain list,
// so `order list --page 2` was an unknown flag, and no list could set the
// page size. Each now sends both.
func TestPagedLists_PageAndLimit(t *testing.T) {
	withConfig(t, loneProfile)
	for _, args := range pagedLists {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			var query url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)
			resetFlags(t, args)
			full := append([]string{"--base-url", srv.URL, "-o", "json"}, args...)
			if err := executeRoot(t, append(full, "--page", "3", "--limit", "10")...); err != nil {
				t.Fatalf("namecom %s --page 3 --limit 10: %v", strings.Join(args, " "), err)
			}
			if query.Get("page") != "3" || query.Get("perPage") != "10" {
				t.Errorf("sent page=%q perPage=%q, want 3 and 10", query.Get("page"), query.Get("perPage"))
			}
		})
	}
}

// TestPagedLists_BadPageIsUsage pins #236: `--page 0` exited 1.
func TestPagedLists_BadPageIsUsage(t *testing.T) {
	withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected, got %s %s", r.Method, r.URL)
	}))
	t.Cleanup(srv.Close)
	for _, args := range pagedLists {
		// #290: --limit 0 silently meant the API's default page, and --limit
		// 1001 was a 400 from the API, exit 1.
		for _, bad := range [][]string{{"--page", "0"}, {"--limit", "-1"}, {"--limit", "0"}, {"--limit", "1001"}, {"--all", "--limit", "0"}} {
			t.Run(strings.Join(append(args[:2:2], bad...), " "), func(t *testing.T) {
				resetFlags(t, args)
				full := append(append([]string{"--base-url", srv.URL, "-o", "json"}, args...), bad...)
				err := cmdutil.ClassifyCobraUsage(executeRoot(t, full...))
				if got := exitCode(err); got != 2 {
					t.Errorf("namecom %s exited %d (%v), want 2", strings.Join(full[4:], " "), got, err)
				}
			})
		}
	}
}
