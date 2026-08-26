package contact

import (
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/patramsey/namecom-cli/internal/drifttest"
	"github.com/patramsey/namecom-cli/internal/output"
)

// build adapts contactCmd to the drifttest.Build signature.
func build(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd, _, _ := contactCmd(t, srv, output.FormatTable)
	return cmd
}

// TestRequestShape_Contact pins the wire request for the two mutating contact
// commands. Both act on a registrant rather than on our own data — resend puts
// an email in a third party's inbox, and verify marks a contact verified
// through a reseller-only endpoint — so a silently changed path or body is not
// something the caller can see or undo.
func TestRequestShape_Contact(t *testing.T) {
	const resendResponse = `{"sent":true,"verificationId":9911,"nextEligibleAt":"2026-08-01T12:15:00Z"}`

	// Both requests now carry an empty JSON object where the generated client
	// sent no body at all. The SDK models these bodyless endpoints with a
	// nillable Body field that it marshals regardless, so leaving it nil sends
	// the literal `null` — worse, since a strict parser will reject that for an
	// object-typed body while `{}` is unambiguous. There is no way to send no
	// body. See docs/upstream/core-api-go-forced-request-bodies.md.
	t.Run("resend", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST",
			Path:   "/core/v1/contacts/verify/9911:resend",
			Body:   `{}`,
		}, build, runResend, []string{"9911"}, resendResponse)
	})

	t.Run("verify", func(t *testing.T) {
		drifttest.AssertRequest(t, drifttest.Request{
			Method: "POST",
			Path:   "/core/v1/contacts/verify/9911",
			Body:   `{}`,
		}, build, runVerify, []string{"9911"}, `{}`)
	})
}

// TestDryRunMatchesRealRequest_Contact guards the fix that gave these commands
// a --dry-run branch at all.
//
// Before it, both ignored the flag. `contact resend --dry-run` sent the
// verification email: the one command in this group where "show me what you
// would do" and "do it" differed by an email arriving in someone else's inbox,
// and the flag is documented as "for write operations, print the request
// instead of sending it". drifttest fails the test if a dry run reaches the
// server at all, so a regression here cannot pass by printing the right line
// and sending the request anyway.
func TestDryRunMatchesRealRequest_Contact(t *testing.T) {
	t.Run("resend", func(t *testing.T) {
		drifttest.AssertDryRunMatches(t, build, runResend, []string{"9911"},
			`{"sent":true,"verificationId":9911}`)
	})

	t.Run("verify", func(t *testing.T) {
		drifttest.AssertDryRunMatches(t, build, runVerify, []string{"9911"}, `{}`)
	})
}
