package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List domains in your account",
	Example: `  namecom domain list                             # first page (250)
  namecom domain list --page 2                    # second page
  namecom domain list --all                       # all domains (good for scripting)
  namecom domain list --filter acme               # server-side wildcard search
  namecom domain list --tld io                    # filter by TLD
  namecom domain list --expiring-before 2026-09-01
  namecom domain list --sort expireDate
  namecom domain list --all -o json | jq -r '.[].domainName'`,
	RunE: runList,
}

var getCmd = &cobra.Command{
	Use:               "get <domain>",
	Short:             "Get details for a single domain",
	Example:           `  namecom domain get example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var (
	listFilter         string
	listTLD            string
	listSort           string
	listAll            bool
	listPage           int32
	listExpiringAfter  string
	listExpiringBefore string
)

func init() {
	listCmd.Flags().StringVar(&listFilter, "filter", "", "filter by domain name (supports * wildcard, e.g. '*acme*')")
	listCmd.Flags().StringVar(&listTLD, "tld", "", "filter by TLD (e.g. com, io)")
	listCmd.Flags().StringVar(&listSort, "sort", "", "sort field: domainName, expireDate, createDate")
	listCmd.Flags().StringVar(&listExpiringAfter, "expiring-after", "", "show domains expiring on or after this date (YYYY-MM-DD)")
	listCmd.Flags().StringVar(&listExpiringBefore, "expiring-before", "", "show domains expiring on or before this date (YYYY-MM-DD)")
	listCmd.Flags().BoolVar(&listAll, "all", false, "fetch all pages (use with --output json for scripting)")
	listCmd.Flags().Int32Var(&listPage, "page", 1, "page number to fetch (use with --all to start from a specific page)")
}

// isFiltered reports whether any server-side filter flag is set.
func isFiltered(cmd *cobra.Command) bool {
	for _, f := range []string{"filter", "tld", "expiring-after", "expiring-before"} {
		if cmd.Flags().Changed(f) {
			return true
		}
	}
	return false
}

func runList(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	ctx := cmd.Context()

	// When a filter is active, auto-paginate — results are small and the user
	// expects to see everything matching, not just the first page.
	autoPage := listAll || isFiltered(cmd)

	spin := out.StartSpinner("Fetching domains…")
	var domains []gen.DomainResponsePayload
	page := listPage
	if page < 1 {
		page = 1
	}
	var lastResult gen.ListDomainsResponseSchema
	var hasMore bool

	for {
		params := &gen.ListDomainsParams{Page: &page}
		if listSort != "" {
			params.Sort = &listSort
		}
		if listFilter != "" {
			f := filterToWildcard(listFilter)
			params.DomainName = &f
		}
		if listTLD != "" {
			tld := strings.TrimPrefix(listTLD, ".")
			params.Tld = &tld
		}
		if listExpiringAfter != "" {
			params.ExpireDateStart = &listExpiringAfter
		}
		if listExpiringBefore != "" {
			params.ExpireDateEnd = &listExpiringBefore
		}

		resp, err := client.Gen().ListDomains(ctx, params)
		if err != nil {
			spin.Stop()
			return err
		}
		var result gen.ListDomainsResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			spin.Stop()
			return err
		}
		domains = append(domains, result.Domains...)
		lastResult = result

		if result.NextPage == nil || *result.NextPage == 0 {
			break
		}
		if !autoPage {
			hasMore = true
			break
		}
		page = *result.NextPage
		spin.Update(fmt.Sprintf("Fetching domains… (page %d, %d so far)", page, len(domains)))
	}

	spin.Stop()

	if out.QuietMode {
		names := make([]string, 0, len(domains))
		for _, d := range domains {
			names = append(names, d.DomainName)
		}
		out.PrintQuiet(names)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(domains)
	case output.FormatYAML:
		return out.YAML(domains)
	default:
		if len(domains) == 0 {
			if isFiltered(cmd) {
				out.Warn("no domains matched — try a different filter")
			} else {
				out.Empty("domain", "Run 'namecom domain register <domain>' to register your first domain")
			}
			return nil
		}
		headers := []string{"DOMAIN", "EXPIRES", "AUTO-RENEW", "LOCKED", "PRIVACY"}
		rows := make([][]string, 0, len(domains))
		for _, d := range domains {
			rows = append(rows, []string{
				d.DomainName,
				out.ExpiryDate(d.ExpireDate),
				out.BoolBadge(bool(d.AutorenewEnabled)),
				out.BoolBadge(bool(d.Locked)),
				out.BoolBadge(bool(d.PrivacyEnabled)),
			})
		}
		out.Table(headers, rows)
		out.Count(len(domains), "domain")
		if hasMore && lastResult.TotalCount > 0 {
			nextPage := int32(0)
			if lastResult.NextPage != nil {
				nextPage = *lastResult.NextPage
			}
			hint := fmt.Sprintf(
				"Showing %d–%d of %d — use --page %d for next page, or --filter/--tld to narrow results, --all for everything",
				lastResult.From, lastResult.To, lastResult.TotalCount, nextPage,
			)
			out.Hint(hint)
		} else if hasMore {
			out.Hint("Showing first page — use --page 2 for next, --filter/--tld to narrow results, --all for everything")
		}
	}
	return nil
}

func runGet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	stop := out.Spin("Fetching domain…")
	resp, err := client.Gen().GetDomain(cmd.Context(), args[0])
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return fmt.Errorf("domain %q not found — run 'namecom domain list' to see your domains", args[0])
		}
		return err
	}
	var d gen.DomainResponsePayload
	if err := api.Decode(resp, &d); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(d)
	case output.FormatYAML:
		return out.YAML(d)
	default:
		out.Title(d.DomainName)
		rows := [][]string{
			{"Domain", d.DomainName},
			{"Created", out.Dim(formatTime(d.CreateDate))},
			{"Expires", out.ExpiryDate(d.ExpireDate)},
			{"Auto-Renew", out.BoolBadge(bool(d.AutorenewEnabled))},
			{"Locked", out.BoolBadge(bool(d.Locked))},
			{"Privacy", out.BoolBadge(bool(d.PrivacyEnabled))},
			{"Nameservers", out.Dim(formatNS(d.Nameservers))},
		}
		out.KVTable(rows)
		out.Hint(fmt.Sprintf("Run 'namecom dns list %s' to manage DNS records", d.DomainName))
	}
	return nil
}

// filterToWildcard wraps a bare search term in * wildcards so that --filter
// acme matches acme.io, acme.com, myacme.net, etc. Values that already
// contain a * are passed through unchanged, letting callers express exact
// prefix/suffix patterns like "*acme" or "acme*".
func filterToWildcard(f string) string {
	if strings.Contains(f, "*") {
		return f
	}
	return "*" + f + "*"
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// formatNS formats gen.Nameservers (type alias for []string) for display.
func formatNS(ns []string) string {
	if len(ns) == 0 {
		return ""
	}
	return strings.Join(ns, ", ")
}
