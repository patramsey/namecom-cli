package domain

import (
	"fmt"
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
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List domains in your account",
	Example: `  namecom domain list                             # first page (250)
  namecom domain list --page 2                    # second page
  namecom domain list --all                       # all domains (good for scripting)
  namecom domain list --filter acme               # server-side wildcard search
  namecom domain list --tld io                    # filter by TLD
  namecom domain list --expiring-before 2026-09-01
  namecom domain list --sort expireDate
  namecom domain list --all -o json | jq -r '.data[].domainName'   # JSON is wrapped in a "data" envelope`,
	// Without it, cobra let a leaf command take any arguments and ignore
	// them: `domain list --all false` listed every domain.
	Args: cmdutil.NoArgs,
	RunE: runList,
}

var getCmd = &cobra.Command{
	Use:   "get <domain> [<domain>...]",
	Short: "Get details for one or more domains",
	Long: `Get details for one or more domains. With more than one, or with '-'
(read domains from stdin, one per line), JSON and YAML output is an array.`,
	Example: `  namecom domain get example.com
  namecom domain get example.com example.net
  namecom domain list -q | namecom domain get - -o json`,
	Args:              cmdutil.MinimumNArgs(1),
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
	listLimit          int
	listExpiringAfter  string
	listExpiringBefore string
)

func init() {
	listCmd.Flags().StringVar(&listFilter, "filter", "", "filter by domain name (supports * wildcard, e.g. '*acme*')")
	listCmd.Flags().StringVar(&listTLD, "tld", "", "filter by TLD (e.g. com, io)")
	listCmd.Flags().StringVar(&listSort, "sort", "", "sort by a domain property: "+strings.Join(sortFields, ", ")+" (passed to the API as is)")
	listCmd.Flags().StringVar(&listSortDir, "sort-dir", "", "sort direction: asc (default) or desc")
	listCmd.Flags().StringVar(&listExpiringAfter, "expiring-after", "", "show domains expiring on or after this date (YYYY-MM-DD)")
	listCmd.Flags().StringVar(&listExpiringBefore, "expiring-before", "", "show domains expiring on or before this date (YYYY-MM-DD)")
	cmdutil.AddPageFlags(listCmd, &listAll, &listPage, &listLimit, "domains")
	cmdutil.CompleteFlagValues(listCmd, "sort", sortFields)
	cmdutil.CompleteFlagValues(listCmd, "sort-dir", cmdutil.SortDirs)
}

// sortFields are the domain properties --sort lists and completes: the
// scalar fields of a domain in `domain list -o json`. The API documents sort
// only as "which domain property to order by", with no list, so the value is
// still sent as typed rather than checked against these.
var sortFields = []string{"domainName", "createDate", "expireDate", "renewalPrice",
	"autorenewEnabled", "locked", "privacyEnabled"}

// isFiltered reports whether any server-side filter flag is set.
func isFiltered(cmd *cobra.Command) bool {
	return slices.ContainsFunc([]string{"filter", "tld", "expiring-after", "expiring-before"}, cmd.Flags().Changed)
}

func runList(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	ctx := cmd.Context()

	if err := cmdutil.ValidPage(listPage, listLimit); err != nil {
		return err
	}
	if err := cmdutil.ValidSortDir(listSortDir); err != nil {
		return err
	}
	if listExpiringAfter != "" {
		if err := cmdutil.ValidDate(listExpiringAfter, "expiring-after"); err != nil {
			return err
		}
	}
	// The API treats expireDateEnd as exclusive (#150), so "on or before D"
	// has to be sent as D+1. ValidDate guarantees a YYYY-MM-DD date.
	var expireEnd string
	if listExpiringBefore != "" {
		if err := cmdutil.ValidDate(listExpiringBefore, "expiring-before"); err != nil {
			return err
		}
		d, _ := time.Parse("2006-01-02", listExpiringBefore)
		expireEnd = d.AddDate(0, 0, 1).Format("2006-01-02")
	}

	// When a filter is active, auto-paginate — results are small and the user
	// expects to see everything matching, not just the first page.
	// So does --quiet without --page or --limit — see cmdutil.AutoPage.
	autoPage := cmdutil.AutoPage(cmd, listAll) || isFiltered(cmd)

	spin := out.StartSpinner("Fetching domains…")

	// Build query params from flags (shared across all page requests).
	buildParams := func(page int) *coreapigo.ListDomainsRequest {
		p := &coreapigo.ListDomainsRequest{Page: &page, PerPage: cmdutil.PerPage(listLimit)}
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
		if expireEnd != "" {
			p.ExpireDateEnd = &expireEnd
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
	domains = append(domains, cmdutil.NonNil(lastResult.Domains)...)

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
					pages[idx] = cmdutil.NonNil(r.Domains)
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
			//
			// cmdutil.NextPage guards the other half: a server that keeps
			// answering nextPage:2 would otherwise refetch page 2 forever.
			page, ok := cmdutil.NextPage(listPage, lastResult.NextPage, lastResult.LastPage)
			for ok {
				r, err := client.SDK().Domains.ListDomains(ctx, buildParams(page))
				if err != nil {
					spin.Stop()
					return api.FromSDKError(err)
				}
				domains = append(domains, cmdutil.NonNil(r.Domains)...)
				page, ok = cmdutil.NextPage(page, r.NextPage, r.LastPage)
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
		if hasMore {
			nextPage := listPage + 1
			if lastResult.NextPage != nil {
				nextPage = *lastResult.NextPage
			}
			cmdutil.QuietMorePages(out, nextPage)
		}
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		var np *int32
		if hasMore {
			np = cmdutil.Int32Page(lastResult.NextPage)
		}
		return out.JSONList(domains, np, cmdutil.Int32Count(lastResult.TotalCount))
	case output.FormatYAML:
		var np *int32
		if hasMore {
			np = cmdutil.Int32Page(lastResult.NextPage)
		}
		return out.YAMLList(domains, np, cmdutil.Int32Count(lastResult.TotalCount))
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
				out.BoolAlert(d.Locked, false),
				out.BoolBadge(d.PrivacyEnabled),
			})
		}
		out.Table(headers, rows)
		// One footer, short enough for 80 columns. --filter and --tld are in
		// the help; the footer only says how to see the rest.
		switch {
		case hasMore && lastResult.TotalCount > 0:
			nextPage := listPage + 1
			if lastResult.NextPage != nil {
				nextPage = *lastResult.NextPage
			}
			out.Footer(
				fmt.Sprintf("Showing %s–%s of %s", output.Thousands(lastResult.From),
					output.Thousands(lastResult.To), output.Plural(lastResult.TotalCount, "domain")),
				cmdutil.MorePages(nextPage),
			)
		case hasMore:
			out.Count(len(domains), "domain", cmdutil.MorePages(listPage+1))
		default:
			out.Count(len(domains), "domain")
		}
	}
	return nil
}

func runGet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	// One domain named on the command line keeps the single-object output
	// scripts already parse. Several, or "-" — however many lines it turns
	// out to hold — are a list (#244), so their output is the {"data": [...]}
	// every list prints (#240).
	list := len(args) != 1 || args[0] == cmdutil.StdinArg
	domains, err := cmdutil.DomainArgs(cmd, args)
	if err != nil {
		return err
	}
	fetched := make([]*coreapigo.DomainResponsePayload, 0, len(domains))
	for _, domain := range domains {
		stop := out.Spin("Fetching domain…")
		d, err := client.SDK().Domains.GetDomain(cmd.Context(),
			&coreapigo.GetDomainRequest{DomainName: domain})
		stop()
		if err != nil {
			if cmdutil.IsNotFound(err) {
				return cmdutil.NotFound(err, fmt.Sprintf("domain %q not found — run 'namecom domain list' to see your domains", domain))
			}
			return err
		}
		if err := cmdutil.RequireField("the domain name", d.DomainName); err != nil {
			return err
		}
		fetched = append(fetched, d)
	}

	// --quiet prints the identifying value only, matching list commands.
	if out.QuietMode {
		names := make([]string, len(fetched))
		for i, d := range fetched {
			names[i] = d.DomainName
		}
		out.PrintQuiet(names)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		if list {
			return out.JSONList(fetched, nil, 0)
		}
		return out.JSON(fetched[0])
	case output.FormatYAML:
		if list {
			return out.YAMLList(fetched, nil, 0)
		}
		return out.YAML(fetched[0])
	default:
		titles := make([]string, len(fetched))
		objs := make([][][]string, len(fetched))
		for i, d := range fetched {
			titles[i], objs[i] = d.DomainName, domainRows(out, d, time.Now())
		}
		if list {
			out.KVTables(titles, objs)
		} else {
			out.Title(titles[0])
			out.KVTable(objs[0])
		}
		// The next step is about one domain; for several it would repeat.
		if len(fetched) == 1 {
			out.Hint(domainHint(fetched[0], time.Now()))
		}
	}
	return nil
}

// domainRows is the detail view of d. Renews at, Transfer lock and Registrant
// appear only when the response carries them: the JSON had all three while
// the table showed none, so the renewal price, the date an unlock becomes
// possible, and whose name the domain is in were a `-o json` away (#235).
func domainRows(out *output.Config, d *coreapigo.DomainResponsePayload, now time.Time) [][]string {
	rows := [][]string{
		{"Domain", d.DomainName},
		{"Created", out.Dim(formatTime(d.CreateDate))},
		{"Expires", out.ExpiryDate(d.ExpireDate)},
	}
	if d.RenewalPrice != nil {
		rows = append(rows, []string{"Renews at", output.Money(*d.RenewalPrice)})
	}
	rows = append(rows,
		[]string{"Auto-Renew", out.BoolBadge(d.AutorenewEnabled)},
		[]string{"Locked", out.BoolAlert(d.Locked, false)},
	)
	// An expiry in the past is a lock that has already lifted.
	if t := d.TransferLockExpiresAt; t != nil && t.After(now) {
		lock := "until " + t.Format("2006-01-02") + " (" + output.Relative(*t) + ")"
		if out.Format == output.FormatTSV {
			lock = t.Format("2006-01-02") // a date, as TSV prints Expires
		}
		rows = append(rows, []string{"Transfer lock", lock})
	}
	rows = append(rows, []string{"Privacy", out.BoolBadge(d.PrivacyEnabled)})
	if d.Contacts != nil {
		if r := registrantLabel(d.Contacts.Registrant); r != "" {
			rows = append(rows, []string{"Registrant", r})
		}
	}
	return append(rows, []string{"Nameservers", out.Dim(formatNS(d.Nameservers))})
}

// registrantLabel names a registrant — "Jane Doe (Acme Inc)" — and says
// whether its email is verified, which ICANN requires and an unverified
// contact can get the domain suspended for. "" when there is no name.
func registrantLabel(r *coreapigo.RegistrantContact) string {
	if r == nil {
		return ""
	}
	var name []string
	for _, p := range []*string{r.FirstName, r.LastName} {
		if p != nil && strings.TrimSpace(*p) != "" {
			name = append(name, strings.TrimSpace(*p))
		}
	}
	label := strings.Join(name, " ")
	if r.CompanyName != nil && strings.TrimSpace(*r.CompanyName) != "" {
		company := strings.TrimSpace(*r.CompanyName)
		if label == "" {
			label = company
		} else {
			label += " (" + company + ")"
		}
	}
	if label == "" {
		return ""
	}
	if r.IsVerified != nil {
		if *r.IsVerified {
			label += " · email verified"
		} else {
			label += " · email not verified"
		}
	}
	return label
}

// domainHint picks the next step for the domain's state. It always suggested
// `dns list`, even for an expired domain, where renewing is what is needed
// (#238).
func domainHint(d *coreapigo.DomainResponsePayload, now time.Time) string {
	if d.ExpireDate != nil {
		days := d.ExpireDate.Sub(now).Hours() / 24
		switch {
		case days < 0:
			return fmt.Sprintf("Run 'namecom domain renew %s' — it expired %s", d.DomainName, output.RelativeDays(days))
		case days < 30 && !d.AutorenewEnabled:
			return fmt.Sprintf("Run 'namecom domain renew %s' or 'namecom domain autorenew on %s' — it expires %s and will not renew itself",
				d.DomainName, d.DomainName, output.RelativeDays(days))
		}
	}
	return fmt.Sprintf("Run 'namecom dns list %s' to manage DNS records", d.DomainName)
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

// formatNS formats gen.Nameservers (type alias for []string) for display.
func formatNS(ns []string) string {
	if len(ns) == 0 {
		return ""
	}
	return strings.Join(ns, ", ")
}
