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
	"fmt"
	"strconv"
	"time"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
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
	Args: cobra.NoArgs,
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
	unverifiedCmd.Flags().BoolVar(&listAll, "all", false, "fetch all pages")
	cmdutil.GroupCmd(Cmd)
	Cmd.AddCommand(unverifiedCmd, resendCmd, verifyCmd)
}

func runUnverified(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	spin := out.StartSpinner("Fetching unverified contacts…")
	var page int32 = 1
	var contacts []gen.UnverifiedContact
	var hasMore bool
	var lastResult gen.UnverifiedContactsResponseSchema
	for {
		params := &gen.UnverifiedContactsListParams{Page: &page}
		resp, err := client.Gen().UnverifiedContactsList(cmd.Context(), params)
		if err != nil {
			spin.Stop()
			return err
		}
		// Fresh variable per page — see cmd/dns/dns.go for why reusing one
		// decode target both corrupts earlier pages and never terminates.
		var result gen.UnverifiedContactsResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			spin.Stop()
			return err
		}
		contacts = append(contacts, result.UnverifiedContacts...)
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
			hasMore = true
			break
		}
		page = next
	}
	spin.Stop()

	if out.QuietMode {
		ids := make([]string, 0, len(contacts))
		for _, c := range contacts {
			ids = append(ids, strconv.FormatInt(c.VerificationId, 10))
		}
		out.PrintQuiet(ids)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		var np *int32
		if hasMore {
			np = lastResult.NextPage
		}
		return out.JSONList(contacts, np, lastResult.TotalCount)
	case output.FormatYAML:
		var np *int32
		if hasMore {
			np = lastResult.NextPage
		}
		return out.YAMLList(contacts, np, lastResult.TotalCount)
	default:
		if len(contacts) == 0 {
			out.Empty("unverified contact", "Newly triggered verifications can take ~10 minutes to appear")
			return nil
		}
		out.Table(
			[]string{"ID", "EMAIL", "DEADLINE", "DOMAINS"},
			unverifiedRows(out, contacts),
		)
		out.Count(len(contacts), "unverified contact")
		out.WarnBox(
			"Domains with unverified contacts may be LOCKED by the registry after the deadline.",
			"Run 'namecom contact resend <id>' to send the verification email again.",
		)
		if hasMore {
			out.Hint("Showing first page — pass --all to fetch all entries")
		}
	}
	return nil
}

func unverifiedRows(out *output.Config, contacts []gen.UnverifiedContact) [][]string {
	rows := make([][]string, 0, len(contacts))
	for _, c := range contacts {
		email := ""
		if c.Email != nil {
			email = string(*c.Email)
		}
		rows = append(rows, []string{
			strconv.FormatInt(c.VerificationId, 10),
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
	if cmdutil.IsDryRun(cmd) {
		out.DryRun("POST", fmt.Sprintf("/core/v1/contacts/verify/%d:resend", id), nil)
		return nil
	}

	stop := out.Spin("Resending verification email…")
	resp, err := client.Gen().ResendContactVerificationEmail(cmd.Context(), id,
		&gen.ResendContactVerificationEmailParams{})
	stop()
	if err != nil {
		return err
	}
	var result gen.ContactVerificationResendResponseSchema
	if err := api.Decode(resp, &result); err != nil {
		return err
	}

	// The API reports throttling as HTTP 200 with sent=false. Returning nil for
	// that made a throttled record indistinguishable from a resent one — same
	// exit code, and nothing on stdout under --quiet. The command's own example
	// pipes `contact unverified -q` into xargs, so "I resent them all" has to be
	// true or a domain silently misses its verification deadline.
	if !result.Sent {
		if out.Format == output.FormatJSON || out.Format == output.FormatYAML {
			// Still emit the payload so a script can read nextEligibleAt, then
			// fail so it cannot mistake this for a send.
			if out.Format == output.FormatJSON {
				_ = out.JSON(result)
			} else {
				_ = out.YAML(result)
			}
		}
		return fmt.Errorf("verification email not sent for record %d — throttled until %s "+
			"(the API allows one resend per record every 15 minutes)",
			result.VerificationId, result.NextEligibleAt.Format(time.RFC3339))
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	default:
		out.Success(fmt.Sprintf("Verification email resent for record %d", result.VerificationId))
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
	if cmdutil.IsDryRun(cmd) {
		out.DryRun("POST", fmt.Sprintf("/core/v1/contacts/verify/%d", id), nil)
		return nil
	}

	stop := out.Spin("Marking contact verified…")
	resp, err := client.Gen().VerifyContact(cmd.Context(), id, &gen.VerifyContactParams{})
	stop()
	if err != nil {
		return cmdutil.AsRestricted(err, "contact verify", "approved reseller")
	}
	if err := api.Decode(resp, nil); err != nil {
		return cmdutil.AsRestricted(err, "contact verify", "approved reseller")
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
func parseVerificationID(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid verification ID %q: must be a number "+
			"(run 'namecom contact unverified' to list them)", s)
	}
	return int32(n), nil
}
