package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/namedotcom/namecom-cli/cmd/cmdutil"
	"github.com/namedotcom/namecom-cli/internal/api"
	"github.com/namedotcom/namecom-cli/internal/api/gen"
	"github.com/namedotcom/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all domains in your account",
	Example: `  namecom domain list
  namecom domain list --all
  namecom domain list --filter acme
  namecom domain list --sort expireDate`,
	RunE:  runList,
}

var getCmd = &cobra.Command{
	Use:   "get <domain>",
	Short: "Get details for a single domain",
	Example: `  namecom domain get example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var (
	listFilter string
	listSort   string
	listAll    bool
)

func init() {
	listCmd.Flags().StringVar(&listFilter, "filter", "", "filter domains by name substring")
	listCmd.Flags().StringVar(&listSort, "sort", "", "sort field (e.g. domainName, expireDate)")
	listCmd.Flags().BoolVar(&listAll, "all", false, "fetch all pages automatically")
}

func runList(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	ctx := cmd.Context()

	stop := out.Spin("Fetching domains…")
	var domains []gen.DomainResponsePayload
	var page int32 = 1
	var hasMore bool

	for {
		params := &gen.ListDomainsParams{Page: &page}
		if listSort != "" {
			params.Sort = &listSort
		}
		resp, err := client.Gen().ListDomains(ctx, params)
		if err != nil {
			stop()
			return err
		}
		var result gen.ListDomainsResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			stop()
			return err
		}
		domains = append(domains, result.Domains...)

		if result.NextPage == nil || *result.NextPage == 0 {
			break
		}
		if !listAll {
			hasMore = true
			break
		}
		page = *result.NextPage
	}

	stop()

	// Apply client-side name filter.
	if listFilter != "" {
		filtered := domains[:0]
		for _, d := range domains {
			if strings.Contains(d.DomainName, listFilter) {
				filtered = append(filtered, d)
			}
		}
		domains = filtered
	}

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
			out.Empty("domain", "Run 'namecom domain register <domain>' to register your first domain")
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
		if hasMore {
			out.Hint("More domains exist — pass --all to fetch all pages")
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
