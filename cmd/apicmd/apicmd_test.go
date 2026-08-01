package apicmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
