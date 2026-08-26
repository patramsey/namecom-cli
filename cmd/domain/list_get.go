package domain

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
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
  namecom domain list --all -o json | jq -r '.data[].domainName'   # JSON is wrapped in a "data" envelope`,
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
	listSortDir        string
	listAll            bool
	listPage           int
	listExpiringAfter  string
	listExpiringBefore string
)

func init() {
	listCmd.Flags().StringVar(&listFilter, "filter", "", "filter by domain name (supports * wildcard, e.g. '*acme*')")
	listCmd.Flags().StringVar(&listTLD, "tld", "", "filter by TLD (e.g. com, io)")
	listCmd.Flags().StringVar(&listSort, "sort", "", "sort by a domain property (e.g. domainName, expireDate, createDate)")
	listCmd.Flags().StringVar(&listSortDir, "sort-dir", "", "sort direction: asc (default) or desc")
	listCmd.Flags().StringVar(&listExpiringAfter, "expiring-after", "", "show domains expiring on or after this date (YYYY-MM-DD)")
	listCmd.Flags().StringVar(&listExpiringBefore, "expiring-before", "", "show domains expiring on or before this date (YYYY-MM-DD)")
	listCmd.Flags().BoolVar(&listAll, "all", false, "fetch all pages (use with --output json for scripting)")
	listCmd.Flags().IntVar(&listPage, "page", 1, "page number to fetch (use with --all to start from a specific page)")
}

// isFiltered reports whether any server-side filter flag is set.
func isFiltered(cmd *cobra.Command) bool {
	return slices.ContainsFunc([]string{"filter", "tld", "expiring-after", "expiring-before"}, cmd.Flags().Changed)
}

func runList(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	ctx := cmd.Context()

	if listPage < 1 {
		return fmt.Errorf("--page must be 1 or greater (got %d)", listPage)
	}
	if err := cmdutil.ValidSortDir(listSortDir); err != nil {
		return err
	}
	if listExpiringAfter != "" {
		if err := cmdutil.ValidDate(listExpiringAfter, "expiring-after"); err != nil {
			return err
		}
	}
	if listExpiringBefore != "" {
		if err := cmdutil.ValidDate(listExpiringBefore, "expiring-before"); err != nil {
			return err
		}
	}

	// When a filter is active, auto-paginate — results are small and the user
	// expects to see everything matching, not just the first page.
	autoPage := listAll || isFiltered(cmd)

	spin := out.StartSpinner("Fetching domains…")

	// Build query params from flags (shared across all page requests).
	buildParams := func(page int) *coreapigo.ListDomainsRequest {
		p := &coreapigo.ListDomainsRequest{Page: &page}
		if listSort != "" {
			p.Sort = &listSort
		}
		if listSortDir != "" {
			p.Dir = &listSortDir
		}
		if listFilter != "" {
			f := filterToWildcard(listFilter)
			p.DomainName = &f
		}
		if listTLD != "" {
			tld := strings.TrimPrefix(listTLD, ".")
			p.Tld = &tld
		}
		if listExpiringAfter != "" {
			p.ExpireDateStart = &listExpiringAfter
		}
		if listExpiringBefore != "" {
			p.ExpireDateEnd = &listExpiringBefore
		}
		return p
	}

	var domains []*coreapigo.DomainResponsePayload
	var lastResult *coreapigo.ListDomainsResponse
	var hasMore bool

	// Fetch page 1 first to discover LastPage.
	lastResult, err := client.SDK().Domains.ListDomains(ctx, buildParams(listPage))
	if err != nil {
		spin.Stop()
		return api.FromSDKError(err)
	}
	domains = append(domains, lastResult.Domains...)

	if lastResult.NextPage != nil && *lastResult.NextPage != 0 {
		if !autoPage {
			hasMore = true
		} else if lastResult.LastPage != nil && *lastResult.LastPage > listPage {
			// Fetch the remaining pages in parallel, continuing from the page we
			// already fetched. Starting at 2 unconditionally meant `--all --page 5`
			// refetched page 5 (duplicating it) and pulled in pages 2-4 the user
			// asked to skip — while the printed count still matched totalCount, so
			// it looked correct.
			start := listPage
			last := *lastResult.LastPage
			pages := make([][]*coreapigo.DomainResponsePayload, last-start)
			var mu sync.Mutex
			g, gctx := errgroup.WithContext(ctx)
			// Bound concurrency: an account with many pages would otherwise queue
			// every request at once behind the shared rate limiter.
			g.SetLimit(5)
			for p := start + 1; p <= last; p++ {
				// No narrowing conversion any more: the SDK takes page numbers
				// as int, which is what the loop already uses.
				idx := p - start - 1
				g.Go(func() error {
					r, err := client.SDK().Domains.ListDomains(gctx, buildParams(p))
					if err != nil {
						return api.FromSDKError(err)
					}
					pages[idx] = r.Domains
					mu.Lock()
					lastResult = r
					mu.Unlock()
					return nil
				})
			}
			spin.Update(fmt.Sprintf("Fetching domains… (%d pages in parallel)", last))
			if err := g.Wait(); err != nil {
				spin.Stop()
				return err
			}
			for _, pg := range pages {
				domains = append(domains, pg...)
			}
		} else {
			// LastPage unknown; walk sequentially via NextPage. Decode into a
			// fresh variable each iteration — reusing one target lets the JSON
			// decoder overwrite pointers in already-appended pages, and leaves a
			// stale non-nil NextPage when the last page omits the key, which
			// never terminates. Same hazard as cmd/dns/dns.go documents.
			next := lastResult.NextPage
			for next != nil && *next != 0 {
				r, err := client.SDK().Domains.ListDomains(ctx, buildParams(*next))
				if err != nil {
					spin.Stop()
					return api.FromSDKError(err)
				}
				domains = append(domains, r.Domains...)
				next = r.NextPage
			}
		}
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
		var np *int32
		if hasMore {
			np = int32Page(lastResult.NextPage)
		}
		return out.JSONList(domains, np, int32Count(lastResult.TotalCount))
	case output.FormatYAML:
		var np *int32
		if hasMore {
			np = int32Page(lastResult.NextPage)
		}
		return out.YAMLList(domains, np, int32Count(lastResult.TotalCount))
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
				out.BoolBadge(d.AutorenewEnabled),
				out.BoolBadge(d.Locked),
				out.BoolBadge(d.PrivacyEnabled),
			})
		}
		out.Table(headers, rows)
		out.Count(len(domains), "domain")
		if hasMore && lastResult.TotalCount > 0 {
			nextPage := 0
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

	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	stop := out.Spin("Fetching domain…")
	d, err := client.SDK().Domains.GetDomain(cmd.Context(),
		&coreapigo.GetDomainRequest{DomainName: domain})
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return fmt.Errorf("domain %q not found — run 'namecom domain list' to see your domains", args[0])
		}
		return err
	}

	// --quiet prints the identifying value only, matching list commands.
	if out.QuietMode {
		out.PrintQuiet([]string{d.DomainName})
		return nil
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
			{"Auto-Renew", out.BoolBadge(d.AutorenewEnabled)},
			{"Locked", out.BoolBadge(d.Locked)},
			{"Privacy", out.BoolBadge(d.PrivacyEnabled)},
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

// int32Page and int32Count narrow the SDK's int page number and total to the
// int32 the output envelope uses. Same rationale as cmd/dns: the envelope's
// types are shared with groups still on the generated client, so they are not
// widened to suit one of them. The count is clamped rather than converted so a
// value that cannot fit degrades to "very large" instead of wrapping negative.
func int32Page(p *int) *int32 {
	if p == nil || *p > math.MaxInt32 || *p < math.MinInt32 {
		return nil
	}
	v := int32(*p)
	return &v
}

func int32Count(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < 0 {
		return 0
	}
	return int32(n)
}
