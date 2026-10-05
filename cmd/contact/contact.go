// Package contact implements the `namecom contact` command group, covering
// ICANN contact verification.
//
// This matters more than its size suggests. ICANN requires registrant contact
// verification, and the spec is explicit about the consequence: "If the contact
// record is not verified by this date, the domain may become locked by the
// registry. This is typically 15 days from the creation date." Both
// `domain register` and `domain contacts set` can start that clock — the latter
// because validation "is required by ICANN for all TLDs except country-code
// TLDs (ccTLDs)" whenever registrant details change.
package contact

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom contact` parent command.
var Cmd = &cobra.Command{
	Use:   "contact",
	Short: "Manage ICANN contact verification",
}

var listAll bool

var unverifiedCmd = &cobra.Command{
	Use:   "unverified",
	Short: "List contacts awaiting ICANN verification",
	Long: `List contacts on your account that still require ICANN verification.

If a contact is not verified by its deadline the registry may LOCK the affected
domains. The deadline is typically 15 days from when verification was triggered,
but varies by TLD and registry — always read the DEADLINE column rather than
assuming.

Newly created verification records take up to ~10 minutes to appear here: the
API populates them from a scheduled process, so an empty result immediately
after registering a domain does not mean there is nothing pending.`,
	Example: `  namecom contact unverified
  namecom contact unverified --all
  namecom contact unverified -q | xargs -I{} namecom contact resend {}`,
	Args: cmdutil.NoArgs,
	RunE: runUnverified,
}

var resendCmd = &cobra.Command{
	Use:   "resend <verification-id>",
	Short: "Resend a contact verification email",
	Long: `Resend the ICANN verification email for a pending contact.

Throttled by the API: at most one resend per verification record every 15
minutes. The response always reports when the next attempt is allowed, including
when a request is rejected for being too soon.`,
	Example: `  namecom contact resend 9911`,
	Args:    cmdutil.ExactArgs(1),
	RunE:    runResend,
}

var verifyCmd = &cobra.Command{
	Use:   "verify <verification-id>",
	Short: "Mark a contact as verified (approved resellers only)",
	Long: `Mark a contact verification record as verified without the end user clicking
the emailed link.

Requires an approved reseller account: the spec states "This API is only
available to approved reseller accounts. Contact name.com support to request
access." It exists for resellers who have already completed verification through
their own process. Everyone else should use 'contact resend' and have the
contact click the link.`,
	Example: `  namecom contact verify 9911`,
	Args:    cmdutil.ExactArgs(1),
	RunE:    runVerify,
}

func init() {
	cmdutil.AddPageFlags(unverifiedCmd, &listAll, &listPage, &listLimit, "unverified contact")
	cmdutil.GroupCmd(Cmd)
	Cmd.AddCommand(unverifiedCmd, resendCmd, verifyCmd)
}

var listPage, listLimit int

func runUnverified(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	if err := cmdutil.ValidPage(listPage, listLimit); err != nil {
		return err
	}

	spin := out.StartSpinner("Fetching unverified contacts…")
	page := listPage
	var contacts []*coreapigo.UnverifiedContact
	var hasMore bool
	var nextPage int
	var lastResult *coreapigo.UnverifiedContactsResponse
	for {
		result, err := client.SDK().ContactVerification.UnverifiedContactsList(cmd.Context(),
			&coreapigo.UnverifiedContactsListRequest{Page: &page, PerPage: cmdutil.PerPage(listLimit)})
		if err != nil {
			spin.Stop()
			return err
		}
		// Fresh variable per page — see cmd/dns/dns.go for why reusing one
		// decode target both corrupts earlier pages and never terminates.
		contacts = append(contacts, cmdutil.NonNil(result.UnverifiedContacts)...)
		lastResult = result
		next, ok := cmdutil.NextPage(page, result.NextPage, &result.LastPage)
		if !ok {
			break
		}
		// --quiet is for scripting, and the "showing first page" hint lives in
		// the table branch that quiet mode returns before reaching — so stopping
		// early here truncates silently. Page fully whenever the caller cannot
		// be told there is more.
		if !listAll && !out.QuietMode {
			hasMore, nextPage = true, next
			break
		}
		page = next
	}
	spin.Stop()

	if out.QuietMode {
		ids := make([]string, 0, len(contacts))
		for _, c := range contacts {
			ids = append(ids, strconv.FormatInt(c.VerificationID, 10))
		}
		out.PrintQuiet(ids)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		var np *int32
		if hasMore {
			np = cmdutil.Int32Page(lastResult.NextPage)
		}
		return out.JSONList(contacts, np, cmdutil.Int32Count(lastResult.TotalCount))
	case output.FormatYAML:
		var np *int32
		if hasMore {
			np = cmdutil.Int32Page(lastResult.NextPage)
		}
		return out.YAMLList(contacts, np, cmdutil.Int32Count(lastResult.TotalCount))
	default:
		if len(contacts) == 0 {
			out.Empty("unverified contact", "Newly triggered verifications can take ~10 minutes to appear")
			return nil
		}
		out.Table(
			[]string{"ID", "EMAIL", "DEADLINE", "DOMAINS"},
			unverifiedRows(out, contacts),
			output.Essential("DOMAINS"),
		)
		if hasMore {
			out.Count(len(contacts), "unverified contact", cmdutil.MorePages(nextPage))
		} else {
			out.Count(len(contacts), "unverified contact")
		}
		out.WarnBox(
			deadlineWarning(contacts, time.Now()),
			"Run 'namecom contact resend <id>' to send the verification email again.",
		)
	}
	return nil
}

// deadlineWarning words the callout by tense. It said domains "may be LOCKED
// … after the deadline" when every deadline had passed weeks before (#238).
func deadlineWarning(contacts []*coreapigo.UnverifiedContact, now time.Time) string {
	passed, pending := 0, 0
	for _, c := range contacts {
		if c.VerifyBy == nil {
			continue
		}
		if c.VerifyBy.Before(now) {
			passed++
		} else {
			pending++
		}
	}
	switch {
	case passed > 0 && pending == 0:
		return "Verification deadline passed — the registry may suspend or lock these domains at any time."
	case passed > 0:
		return "Some deadlines have passed — the registry may suspend or lock those domains at any time, " +
			"and the rest after their deadline."
	default:
		return "The registry may suspend or lock these domains if the contact is not verified by the deadline."
	}
}

func unverifiedRows(out *output.Config, contacts []*coreapigo.UnverifiedContact) [][]string {
	rows := make([][]string, 0, len(contacts))
	for _, c := range contacts {
		email := ""
		if c.Email != nil {
			email = *c.Email
		}
		rows = append(rows, []string{
			strconv.FormatInt(c.VerificationID, 10),
			email,
			out.ExpiryDate(c.VerifyBy), // colors by urgency, same as domain expiry
			joinDomains(c.Domains),
		})
	}
	return rows
}

func joinDomains(domains []string) string {
	switch len(domains) {
	case 0:
		return ""
	case 1:
		return domains[0]
	}
	return fmt.Sprintf("%s (+%d more)", domains[0], len(domains)-1)
}

func runResend(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	id, err := parseVerificationID(args[0])
	if err != nil {
		return err
	}

	// --dry-run is documented as "for write operations, print the request
	// instead of sending it". This command sends a verification email to a
	// registrant — an irreversible, externally visible side effect and exactly
	// what someone reaches for --dry-run to avoid. It previously ignored the
	// flag and sent the email anyway.
	//
	// NoBody: the {} sent is the SDK's EmptyObject placeholder
	// (namedotcom/core-api-go#8), not a body the user supplies.
	var result *coreapigo.ContactVerificationResendResponse
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[cmdutil.NoBody]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/contacts/verify/%d:resend", id),
		Spin:   "Resending verification email…",
	}, func(ctx context.Context, _ cmdutil.NoBody) error {
		var err error
		result, err = client.SDK().ContactVerification.ResendContactVerificationEmail(ctx,
			&coreapigo.ResendContactVerificationEmailRequest{VerificationID: id, Body: &coreapigo.EmptyObject{}})
		return err
	})
	if err != nil || !sent {
		return err
	}

	// Sent is a plain bool, so a reply without it decoded as false and was
	// reported as throttled "until 0001-01-01" (#212). The SDK marks the field
	// required and gives it no default, so its absence is not an answer
	// either way.
	if !hasSent(result) {
		return &api.UnexpectedResponseError{Reason: "the response did not say whether the email was sent", Write: true}
	}

	// The API reports throttling as HTTP 200 with sent=false. Returning nil for
	// that made a throttled record indistinguishable from a resent one — same
	// exit code, and nothing on stdout under --quiet. The command's own example
	// pipes `contact unverified -q` into xargs, so "I resent them all" has to be
	// true or a domain silently misses its verification deadline.
	if !result.Sent {
		if (out.Format == output.FormatJSON || out.Format == output.FormatYAML) && !out.QuietMode &&
			!result.NextEligibleAt.IsZero() {
			// Still emit the payload so a script can read nextEligibleAt, then
			// fail so it cannot mistake this for a send. Without one it would
			// read 0001-01-01, so it is left out.
			if out.Format == output.FormatJSON {
				_ = out.JSON(result)
			} else {
				_ = out.YAML(result)
			}
		}
		until := ""
		if !result.NextEligibleAt.IsZero() {
			until = " until " + result.NextEligibleAt.Format(time.RFC3339)
		}
		return fmt.Errorf("verification email not sent for record %d — throttled%s "+
			"(the API allows one resend per record every 15 minutes)",
			id, until)
	}

	if out.Quiet() {
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	default:
		out.Success(fmt.Sprintf("Verification email resent for record %d", result.VerificationID))
		out.Hint("The contact must click the link in the email; it cannot be confirmed from here")
	}
	return nil
}

func runVerify(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	id, err := parseVerificationID(args[0])
	if err != nil {
		return err
	}

	// Same omission as resend: this marks a contact verified through a
	// reseller-only endpoint and had no --dry-run branch.
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[cmdutil.NoBody]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/contacts/verify/%d", id),
		Spin:   "Marking contact verified…",
	}, func(ctx context.Context, _ cmdutil.NoBody) error {
		return client.SDK().ContactVerification.VerifyContact(ctx,
			&coreapigo.VerifyContactRequest{VerificationID: id, Body: &coreapigo.EmptyObject{}})
	})
	if !sent {
		return err
	}
	if err != nil {
		// Convert before classifying: AsRestricted inspects *api.APIError to
		// recognise the 403 this reseller-only endpoint returns.
		return cmdutil.AsRestricted(api.FromSDKError(err), "contact verify", "approved reseller")
	}

	out.Success(fmt.Sprintf("Contact verification record %d marked verified", id))
	return nil
}

// parseVerificationID converts the CLI argument to the int32 the verify and
// resend endpoints take.
//
// Note the width mismatch in the API itself: UnverifiedContact.VerificationId
// is an int64, but both operations that consume it accept int32. The values in
// practice are far below the int32 ceiling, but parse at 32 bits so an
// out-of-range id fails here with a clear message instead of silently
// truncating into a request for a different record.
func parseVerificationID(s string) (int, error) {
	n, ok := cmdutil.PositiveID(s)
	if !ok {
		return 0, cmdutil.NewUsageError(fmt.Errorf("invalid verification ID %q: must be a positive whole number "+
			"(run 'namecom contact unverified' to list them)", s))
	}
	return int(n), nil
}

// hasSent reports whether the resend reply carried a non-null "sent". The SDK
// keeps the raw body, which String returns, but no record of which fields were
// present.
func hasSent(r *coreapigo.ContactVerificationResendResponse) bool {
	var probe struct {
		Sent *bool `json:"sent"`
	}
	return json.Unmarshal([]byte(r.String()), &probe) == nil && probe.Sent != nil
}
