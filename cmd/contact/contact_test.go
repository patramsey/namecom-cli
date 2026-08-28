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

// TestResend_ThrottledIsNotAnExitZero guards the pipeline this command's own
// help advertises:
//
//	namecom contact unverified -q | xargs -I{} namecom contact resend {}
//
// The API reports throttling as HTTP 200 with sent=false, and runResend
// returned nil either way — so every throttled record looked like a success.
// The warning goes to stderr, and --quiet suppresses out.Success entirely, so a
// sent and a throttled resend were indistinguishable on stdout AND by exit
// code. On a path whose stated stake is registry lock, "I resent them all" has
// to be true.
func TestResend_ThrottledIsNotAnExitZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sent":false,"verificationId":9911,"nextEligibleAt":"2026-08-01T12:15:00Z"}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _, _ := contactCmd(t, srv, output.FormatTable)
	err := runResend(cmd, []string{"9911"})
	if err == nil {
		t.Fatal("a throttled resend must not exit 0 — a script cannot tell it apart from success")
	}
	if !strings.Contains(err.Error(), "2026-08-01") {
		t.Errorf("error should say when a retry is allowed, got: %v", err)
	}
}

// TestResend_SentIsSuccess is the counterweight: an actual send must still
// succeed, or the xargs pipeline fails on every record.
func TestResend_SentIsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sent":true,"verificationId":9911,"nextEligibleAt":"2026-08-01T12:15:00Z"}`))
	}))
	t.Cleanup(srv.Close)

	cmd, _, _ := contactCmd(t, srv, output.FormatTable)
	if err := runResend(cmd, []string{"9911"}); err != nil {
		t.Fatalf("a successful resend must exit 0: %v", err)
	}
}

// TestUnverified_QuietFetchesEveryPage guards the same pipeline from the other
// end. runUnverified only paged with --all, and the "more pages exist" hint
// lived in the table branch that --quiet returns before reaching — so an
// account with more than one page of pending verifications silently resent for
// the first page only, with nothing on stdout or stderr to say so.
func TestUnverified_QuietFetchesEveryPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"unverifiedContacts":[{"verificationId":2,"email":"b@example.com"}],"totalCount":2}`))
			return
		}
		_, _ = w.Write([]byte(`{"unverifiedContacts":[{"verificationId":1,"email":"a@example.com"}],"totalCount":2,"nextPage":2}`))
	}))
	t.Cleanup(srv.Close)

	cmd, stdout, _ := contactCmd(t, srv, output.FormatTable)
	cmdutil.Out(cmd).QuietMode = true

	if err := runUnverified(cmd, nil); err != nil {
		t.Fatalf("runUnverified: %v", err)
	}
	got := stdout.String()
	for _, id := range []string{"1", "2"} {
		if !strings.Contains(got, id) {
			t.Errorf("--quiet must emit every pending verification, missing %q; got: %q", id, got)
		}
	}
}

// TestUnverified_JSONEnvelope covers the JSON and YAML branches of the list
// output, which had no test at all.
//
// Function-level coverage reported this file at 100% because runUnverified is
// entered through the table path; the format switch inside it was never
// exercised. `-o json` is the automation contract — the mode a script depends
// on, and the one where a silent change is least likely to be noticed by a
// human — so it is the branch that most deserves a test, not the least.
func TestUnverified_JSONEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format output.Format
		want   string
	}{
		{"json", output.FormatJSON, `"data"`},
		{"yaml", output.FormatYAML, "data:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"unverifiedContacts":[{"verificationId":9911,` +
					`"domainName":"example.com","email":"a@b.test"}],"totalCount":1}`))
			}))
			t.Cleanup(srv.Close)

			cmd, stdout, _ := contactCmd(t, srv, tc.format)
			if err := runUnverified(cmd, nil); err != nil {
				t.Fatalf("runUnverified: %v", err)
			}
			got := stdout.String()
			if !strings.Contains(got, tc.want) {
				t.Errorf("expected a %s envelope containing %q, got: %q", tc.name, tc.want, got)
			}
			// Assert the payload, not just the envelope. `"data": null` still
			// contains `"data"`, so checking for the key alone passes while the
			// command emits an empty result — which is exactly what happened
			// when this test was first written against the wrong stub key.
			if !strings.Contains(got, "9911") {
				t.Errorf("%s envelope carried no contacts: %q", tc.name, got)
			}
		})
	}
}

// TestUnverified_JSONEnvelopeCarriesNextPage covers the branch that only runs
// when the walk stopped early with more results available.
//
// nextPage is the whole reason the JSON envelope differs from the table: a
// script has no "showing the first page" hint to read, so this field is how it
// learns to ask for more. Without it the caller sees a complete-looking result
// set that silently is not.
func TestUnverified_JSONEnvelopeCarriesNextPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"unverifiedContacts":[{"verificationId":9911,"domainName":"example.com"}],` +
			`"totalCount":2,"nextPage":2,"lastPage":2}`))
	}))
	t.Cleanup(srv.Close)

	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			cmd, stdout, _ := contactCmd(t, srv, format)
			if err := runUnverified(cmd, nil); err != nil {
				t.Fatalf("runUnverified: %v", err)
			}
			if !strings.Contains(strings.ToLower(stdout.String()), "nextpage") {
				t.Errorf("%s envelope omitted the next page despite more results being available: %q",
					format, stdout.String())
			}
		})
	}
}
