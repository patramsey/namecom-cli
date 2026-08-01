package apicmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// apiCmd wires a command against srv with real credentials configured.
func apiCmd(t *testing.T, srv *httptest.Server) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	client, err := api.New(api.Options{
		BaseURL: srv.URL,
		Creds:   config.Credentials{Username: "alice", Token: "s3cret"},
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatJSON, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	t.Cleanup(func() { apiBody = ""; apiHeaders = nil })
	return cmd, &buf
}

// TestAPI_SendsAuthorization guards a total-breakage bug: runAPI built a request
// by hand and sent it via client.HTTPClient(). Auth is injected by a request
// editor registered on the *generated* client, not by the http.Client's
// transport, so every `namecom api` call went out unauthenticated and 401'd.
// The command's own Long text promises "Auth, rate limiting, and retries are
// applied automatically".
//
// The 401 was doubly misleading: it maps to exit code 3, which tells the user to
// run `namecom auth login` — sending them to re-enter credentials that were
// already correct.
func TestAPI_SendsAuthorization(t *testing.T) {
	var gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := apiCmd(t, srv)
	if err := runAPI(cmd, []string{"GET", "/core/v1/domains"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}

	if gotAuth == "" {
		t.Error("namecom api sent no Authorization header — every call would 401")
	}
	// "alice:s3cret" base64-encoded.
	if want := "Basic YWxpY2U6czNjcmV0"; gotAuth != want {
		t.Errorf("expected %q, got %q", want, gotAuth)
	}
	if gotUA == "" {
		t.Error("namecom api sent no User-Agent header")
	}
}

// TestAPI_UserHeaderOverridesDefault pins that --header still wins, so the
// escape hatch stays usable for testing alternate auth.
func TestAPI_UserHeaderOverridesDefault(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := apiCmd(t, srv)
	apiHeaders = []string{"Authorization: Bearer OVERRIDE"}
	if err := runAPI(cmd, []string{"GET", "/core/v1/domains"}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if gotAuth != "Bearer OVERRIDE" {
		t.Errorf("--header should override the default auth, got %q", gotAuth)
	}
}

// TestAPI_ReturnsAPIErrorForExitCode pins that a non-2xx from `namecom api`
// produces an *api.APIError. runAPI returned a plain fmt.Errorf, so root.go's
// exitCode mapping (which type-asserts *api.APIError) fell through to the
// generic 1 — losing the documented 3/5 codes that scripts branch on, and
// suppressing APIError.UserHint().
func TestAPI_ReturnsAPIErrorForExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Permission Denied","details":"bad token"}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := apiCmd(t, srv)
	err := runAPI(cmd, []string{"GET", "/core/v1/domains"})
	if err == nil {
		t.Fatal("expected an error for HTTP 401, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.APIError so exit codes map correctly, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected StatusCode 401, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "Permission Denied" {
		t.Errorf("API's own message should be preserved, got %q", apiErr.Message)
	}
}

// TestBuildAPIURL_CannotRetargetAnotherHost is a credential-exfiltration guard.
//
// `namecom api` takes an arbitrary path from argv and the resulting request
// carries the account's Authorization header. If a crafted path could move the
// request to another host, the credential goes with it.
//
// This asserts on the URL that gets BUILT, deliberately. An earlier version
// stood up an httptest server and checked what its handler observed — which is
// no check at all: if a hostile path really does retarget the request, the local
// handler never runs and every assertion passes vacuously. It was verified to
// pass against a knowingly vulnerable implementation (url.ResolveReference)
// while real requests carrying the credential went to an external host. Even
// asserting "the call returned an error" is too weak, because a DNS failure
// produces an error only AFTER the connection was attempted.
func TestBuildAPIURL_CannotRetargetAnotherHost(t *testing.T) {
	const base = "https://api.name.com"

	hostile := []string{
		"//evil.example/steal",
		"https://evil.example/steal",
		"http://evil.example/steal",
		"../../../../evil.example/steal",
		"/core/v1/../../../evil.example",
		`\\evil.example\steal`,
		"https://user:pass@evil.example/steal",
		"//evil.example",
	}
	for _, p := range hostile {
		t.Run(p, func(t *testing.T) {
			got, err := buildAPIURL(base, p)
			if err != nil {
				return // refusing outright is a fine outcome
			}
			u, perr := url.Parse(got)
			if perr != nil {
				t.Fatalf("built an unparseable URL %q: %v", got, perr)
			}
			if u.Host != "api.name.com" {
				t.Errorf("path %q built %q — host %q, credential would be sent off-site",
					p, got, u.Host)
			}
			if u.Scheme != "https" {
				t.Errorf("path %q downgraded the scheme to %q", p, u.Scheme)
			}
		})
	}
}

// TestBuildAPIURL_KeepsLegitimatePaths is the counterweight: the guard must not
// break ordinary use.
func TestBuildAPIURL_KeepsLegitimatePaths(t *testing.T) {
	const base = "https://api.name.com"
	cases := map[string]string{
		"/core/v1/domains":             "https://api.name.com/core/v1/domains",
		"core/v1/domains":              "https://api.name.com/core/v1/domains",
		"/core/v1/domains/example.com": "https://api.name.com/core/v1/domains/example.com",
	}
	for in, want := range cases {
		got, err := buildAPIURL(base, in)
		if err != nil {
			t.Errorf("buildAPIURL(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("buildAPIURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAPI_HeaderInjectionRejected pins that --header cannot smuggle extra
// headers or a request line via embedded CRLF.
func TestAPI_HeaderInjectionRejected(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _ := apiCmd(t, srv)
	apiHeaders = []string{"X-Probe: ok\r\nX-Injected: yes"}
	// Either a rejection or a sanitized send is fine; a smuggled header is not.
	_ = runAPI(cmd, []string{"GET", "/core/v1/domains"})

	if got.Get("X-Injected") != "" {
		t.Errorf("CRLF in --header smuggled an additional header: %v", got)
	}
}

// TestAPI_CredentialNotForwardedOnCrossHostRedirect guards the other direction:
// the path is safe, but a response can still try to move the request. Go's
// http.Client strips sensitive headers when a redirect crosses hosts — this
// pins that behavior so a future custom CheckRedirect cannot silently undo it.
func TestAPI_CredentialNotForwardedOnCrossHostRedirect(t *testing.T) {
	var attackerSawAuth string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerSawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(attacker.Close)

	// httptest binds 127.0.0.1; Go compares redirect hosts by NAME (ports are
	// ignored), so two httptest servers look like the same domain and the header
	// is legitimately copied. Point the redirect at "localhost" instead — same
	// machine, genuinely different hostname — to exercise the cross-domain path.
	attackerHost := strings.Replace(attacker.URL, "127.0.0.1", "localhost", 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attackerHost+"/steal", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	cmd, _ := apiCmd(t, origin)
	_ = runAPI(cmd, []string{"GET", "/core/v1/domains"})

	if attackerSawAuth != "" {
		t.Errorf("Authorization header followed a cross-host redirect: %q", attackerSawAuth)
	}
}
