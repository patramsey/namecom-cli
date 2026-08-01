package contact

import (
	"bytes"
	"context"
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

func contactCmd(t *testing.T, srv *httptest.Server, format output.Format) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var stdout, stderr bytes.Buffer
	out := &output.Config{Format: format, Color: output.ColorNever, Writer: &stdout, EWriter: &stderr}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	t.Cleanup(func() { listAll = false })
	return cmd, &stdout, &stderr
}

// TestVerify_RestrictedErrorExplainsTheGate covers the reason these gated
// operations are exposed at all. Resellers use this CLI, so hiding the command
// would leave them without tooling — but everyone else must not be told to
// "check your credentials" when the credentials are fine and the account simply
// is not enrolled.
//
// A 403 also maps to exit code 3, whose top-level hint points at
// 'namecom auth login'. Without this wrapping, an ordinary user is sent to
// re-enter a working token.
func TestVerify_RestrictedErrorExplainsTheGate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Permission Denied"}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _, _ := contactCmd(t, srv, output.FormatTable)
	err := runVerify(cmd, []string{"9911"})
	if err == nil {
		t.Fatal("expected an error for a 403")
	}

	var restricted *cmdutil.RestrictedError
	if !errors.As(err, &restricted) {
		t.Fatalf("a 403 here should be classified as a restricted-access error, got %T", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "reseller") {
		t.Errorf("error should name the approval required, got: %s", msg)
	}
	if !strings.Contains(msg, "support") {
		t.Errorf("error should say how to get access, got: %s", msg)
	}
	// The underlying APIError must survive so exit-code mapping still works.
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("original *api.APIError must remain unwrappable for exit codes, got: %v", err)
	}
}

// TestVerify_NonForbiddenErrorsPassThrough guards the other direction: only a
// 403 means "not enrolled". A 404 or 500 must keep its own meaning rather than
// being relabelled as an access problem.
func TestVerify_NonForbiddenErrorsPassThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _, _ := contactCmd(t, srv, output.FormatTable)
	err := runVerify(cmd, []string{"9911"})
	if err == nil {
		t.Fatal("expected an error for a 404")
	}
	var restricted *cmdutil.RestrictedError
	if errors.As(err, &restricted) {
		t.Errorf("a 404 must not be reported as a restricted-access problem: %v", err)
	}
}

// TestUnverified_WarnsAboutRegistryLock pins that the listing states the
// consequence. The spec: "If the contact record is not verified by this date,
// the domain may become locked by the registry."
func TestUnverified_WarnsAboutRegistryLock(t *testing.T) {
	const resp = `{
	  "unverifiedContacts": [
	    {"verificationId": 9911, "email": "alice@example.com",
	     "verifyBy": "2026-08-20T00:00:00Z", "domains": ["example.com","example.net"]}
	  ],
	  "totalCount": 1
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)

	cmd, stdout, stderr := contactCmd(t, srv, output.FormatTable)
	if err := runUnverified(cmd, nil); err != nil {
		t.Fatalf("runUnverified: %v", err)
	}
	combined := stdout.String() + stderr.String()

	if !strings.Contains(combined, "9911") {
		t.Errorf("verification ID must be shown so it can be passed to resend: %s", combined)
	}
	if !strings.Contains(strings.ToLower(combined), "lock") {
		t.Errorf("output must state the registry-lock consequence: %s", combined)
	}
	if !strings.Contains(combined, "example.com") {
		t.Errorf("affected domains must be shown: %s", combined)
	}
}

// TestResend_ThrottledIsNotReportedAsSuccess guards a misleading-output case:
// the API answers a throttled resend with HTTP 200 and sent=false, so treating
// a 2xx as "email sent" would tell the user something untrue.
func TestResend_ThrottledIsNotReportedAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sent":false,"verificationId":9911,"nextEligibleAt":"2026-08-01T12:15:00Z"}`))
	}))
	t.Cleanup(srv.Close)

	cmd, stdout, stderr := contactCmd(t, srv, output.FormatTable)
	if err := runResend(cmd, []string{"9911"}); err != nil {
		t.Fatalf("runResend: %v", err)
	}
	combined := stdout.String() + stderr.String()

	if strings.Contains(combined, "✓") {
		t.Errorf("a throttled resend must not render as success: %s", combined)
	}
	if !strings.Contains(combined, "2026-08-01") {
		t.Errorf("output should say when a retry is allowed: %s", combined)
	}
}

// TestParseVerificationID_RejectsOutOfRange covers a width mismatch in the API:
// UnverifiedContact.VerificationId is int64, but the resend and verify
// operations take int32. Truncating silently would send a request for a
// different record.
func TestParseVerificationID_RejectsOutOfRange(t *testing.T) {
	if _, err := parseVerificationID("9911"); err != nil {
		t.Fatalf("a normal id must parse: %v", err)
	}
	if _, err := parseVerificationID("4294967296"); err == nil {
		t.Error("an id beyond int32 must be rejected, not truncated into another record")
	}
	if _, err := parseVerificationID("abc"); err == nil {
		t.Error("a non-numeric id must be rejected")
	}
}
