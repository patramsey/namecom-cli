package domain

import (
	"fmt"
	"sort"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// -- requirements --

var requirementsCmd = &cobra.Command{
	Use:   "requirements <tld>",
	Short: "Show registry requirements and capabilities for a TLD",
	Long: `Show the extra fields a registry requires to register under a TLD, plus that
TLD's capabilities (supported years, DNSSEC, privacy, transfer lock).

Many ccTLDs demand registry-specific fields, and registering an
internationalized domain requires the character-set code for the script. The
API is the only source of truth for these — the spec declines to document them
because "they vary wildly between registries and TLDs" — so this command is how
you discover what 'domain register --tld-requirement' needs.

Requirements are deeply nested with conditional rules, so JSON or YAML output
is usually the more useful view.`,
	Example: `  namecom domain requirements fr
  namecom domain requirements ca -o json
  namecom domain requirements com -o json | jq '.requirements'`,
	Args: cmdutil.ExactArgs(1),
	RunE: runRequirements,
}

func runRequirements(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	// TLDs are given without a leading dot; punycode TLDs must be the ASCII
	// form ("for the `онлайн` TLD, you would submit `xn--80asehdb`").
	tld := cmdutil.CanonicalDomain(args[0])
	if tld == "" {
		return fmt.Errorf("tld is required (e.g. 'namecom domain requirements fr')")
	}

	stop := out.Spin("Fetching TLD requirements…")
	result, err := client.SDK().DomainInfo.GetRequirement(cmd.Context(),
		&coreapigo.GetRequirementRequest{Tld: tld})
	stop()
	if err != nil {
		// Converted before classifying: IsNotFound inspects *api.APIError, and
		// the "pass the TLD without a leading dot" hint is the whole value of
		// recognising a 404 here.
		err = api.FromSDKError(err)
		if cmdutil.IsNotFound(err) {
			return cmdutil.NotFound(err, fmt.Sprintf("no requirements found for TLD %q — pass the TLD without a leading dot (e.g. 'fr', not '.fr')", tld))
		}
		return err
	}

	if out.QuietMode {
		// Echoing the TLD back tells the caller nothing they did not already
		// type. The useful scriptable answer is which fields the registry
		// requires, one per line, so `--tld-requirement` can be built from it.
		// A notice is text to read, with nothing to submit, so it is not a
		// --tld-requirement key (#172); the table shows it instead.
		var names []string
		if result.Requirements != nil {
			for name, f := range result.Requirements.Fields {
				if f != nil && f.Type == coreapigo.RequirementFieldTypeNotice {
					continue
				}
				names = append(names, name)
			}
			sort.Strings(names)
		}
		out.PrintQuiet(names)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	default:
		info, badge := result.TldInfo, out.BoolBadge
		if info == nil {
			// An omitted tldInfo means the capabilities are unknown, not
			// unsupported, so render dashes rather than a column of "no".
			info = &coreapigo.ResellerTldInfo{}
			badge = func(bool) string { return "—" }
		}
		out.Title(tld)
		out.KVTable([][]string{
			{"Registration years", joinYears(info.AllowedRegistrationYears)},
			{"DNSSEC", badge(info.SupportsDnssec)},
			{"WHOIS privacy", badge(info.SupportsPrivacy)},
			{"Transfer lock", badge(info.SupportsTransferLock)},
			{"Premium domains", badge(info.SupportsPremium)},
		})
		// Notices are the one part of the requirements that is flat text, and
		// the SDK says they must be shown before registration — .ca's warns
		// that an order not meeting the rules will fail (#172).
		for _, notice := range requirementNotices(result.Requirements) {
			fmt.Fprintln(out.Writer, "Note: "+notice)
		}
		// The requirements themselves are nested and conditional; a table would
		// misrepresent them, so point at the structured view rather than
		// flattening it into something misleading.
		out.Hint(fmt.Sprintf("Run 'namecom domain requirements %s -o json' to see the full field requirements", tld))
		out.Hint("Pass them at registration with 'domain register --tld-requirement key=value' (repeatable)")
	}
	return nil
}

// requirementNotices returns the text of req's top-level notice fields, in
// field-name order so the output is stable.
func requirementNotices(req *coreapigo.Requirement) []string {
	if req == nil {
		return nil
	}
	var names []string
	for name, f := range req.Fields {
		if f != nil && f.Type == coreapigo.RequirementFieldTypeNotice && f.Description != nil && *f.Description != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	notices := make([]string, 0, len(names))
	for _, name := range names {
		notices = append(notices, *req.Fields[name].Description)
	}
	return notices
}

func joinYears(years []int) string {
	if len(years) == 0 {
		return "—"
	}
	s := ""
	for i, y := range years {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%d", y)
	}
	return s
}

// -- claims --

var claimsPurchaseType string

var claimsCmd = &cobra.Command{
	Use:   "claims <domain>",
	Short: "Check a domain for trademark (TMCH) claims",
	Long: `Check whether a domain matches a registered trademark in the Trademark
Clearinghouse.

During a gTLD's claims period, registering a name that matches a registered
trademark requires the registrant to be shown a claim notice and acknowledge
it. 'domain register' performs this check automatically and will not proceed
without acknowledgement — this command is for inspecting a domain beforehand.`,
	Example: `  namecom domain claims tiktok.page
  namecom domain claims example.com -o json`,
	Args: cmdutil.ExactArgs(1),
	RunE: runClaims,
	// Claims matter for a name you are about to register, so the domains
	// already in the account are the wrong candidates (#187).
	ValidArgsFunction: cobra.NoFileCompletions,
}

func init() {
	claimsCmd.Flags().StringVar(&claimsPurchaseType, "purchase-type", "",
		"claims context: "+strings.Join(cmdutil.ClaimsPurchaseTypes, ", "))
	cmdutil.CompleteFlagValues(claimsCmd, "purchase-type", cmdutil.ClaimsPurchaseTypes)
}

func runClaims(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	domainName, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	body := coreapigo.DomainClaimsCheckRequest{Domain: domainName}
	if claimsPurchaseType != "" {
		if err := cmdutil.ValidOneOf("purchase-type", claimsPurchaseType, cmdutil.ClaimsPurchaseTypes); err != nil {
			return err
		}
		pt := coreapigo.DomainClaimsCheckRequestPurchaseType(claimsPurchaseType)
		body.PurchaseType = &pt
	}

	stop := out.Spin("Checking trademark claims…")
	result, err := client.SDK().DomainInfo.CheckDomainClaims(cmd.Context(), &body)
	stop()
	if err != nil {
		return err
	}

	claimed := result.ClaimID != nil && *result.ClaimID != ""

	if out.QuietMode {
		// Quiet mode is for scripting: print the claim ID when there is one and
		// nothing at all when there isn't, so `-q` output is directly testable.
		if claimed {
			out.PrintQuiet([]string{*result.ClaimID})
		}
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(result)
	case output.FormatYAML:
		return out.YAML(result)
	case output.FormatTSV:
		// The JSON document's keys and values. It printed a write's
		// success/changed/message rows, with "changed true", for a read
		// (#289).
		return out.TSVObject(result)
	default:
		if !claimed {
			out.Success(fmt.Sprintf("No trademark claims found for %s", domainName))
			if !derefBool(result.ClaimsProcessActive) {
				out.Hint("This TLD is not currently running a claims process")
			}
			return nil
		}
		renderClaimsNotice(out, result)
		out.Hint(fmt.Sprintf("Registering it requires acknowledgement: "+
			"'namecom domain register %s --acknowledge-claim'", domainName))
	}
	return nil
}
