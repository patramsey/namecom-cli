// Package vanity implements the `namecom vanity-ns` command group.
package vanity

import (
	"context"
	"fmt"
	"net"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom vanity-ns` parent command.
var Cmd = &cobra.Command{
	Use:   "vanity-ns",
	Short: "Configure custom branded nameservers (ns1.yourdomain.com)",
}

var (
	createHostname string
	createIPs      string
	updateIPs      string
	listAll        bool
)

var listCmd = &cobra.Command{
	Use:     "list <domain>",
	Aliases: []string{"ls"},
	Short:   "List vanity nameservers for a domain",
	Example: `  namecom vanity-ns list example.com
  namecom vanity-ns list example.com --all`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runList,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var getCmd = &cobra.Command{
	Use:   "get <domain> <hostname>",
	Short: "Get a vanity nameserver",
	Example: `  namecom vanity-ns get example.com ns1.example.com
  namecom vanity-ns get example.com ns1`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:     "create <domain>",
	Aliases: []string{"add"},
	Short:   "Create a vanity nameserver",
	Long: `Register a nameserver named under the domain, such as ns1.example.com, with
the IP addresses it answers on, so other domains can use it with
'domain set-ns'. The servers at those addresses must answer for those domains.`,
	Example: `  namecom vanity-ns create example.com --hostname ns1.example.com --ips 1.2.3.4
  namecom vanity-ns create example.com --hostname ns1.example.com --ips 1.2.3.4,5.6.7.8`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCreate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var updateCmd = &cobra.Command{
	Use:   "update <domain> <hostname>",
	Short: "Update vanity nameserver IPs",
	Long:  `Replace a vanity nameserver's IP addresses with those in --ips.`,
	Example: `  namecom vanity-ns update example.com ns1.example.com --ips 1.2.3.4,5.6.7.8
  namecom vanity-ns update example.com ns1 --ips 1.2.3.4`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runUpdate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var deleteCmd = &cobra.Command{
	Use:     "delete <domain> <hostname>",
	Aliases: []string{"rm"},
	Short:   "Delete a vanity nameserver",
	Long: `Delete a vanity nameserver. Move any domain that uses it to other
nameservers first ('domain set-ns'); the registry may refuse while one does.`,
	Example: `  namecom vanity-ns delete example.com ns1.example.com
  namecom vanity-ns delete example.com ns1`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runDelete,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	cmdutil.AddPageFlags(listCmd, &listAll, &listPage, &listLimit, "nameservers")

	createCmd.Flags().StringVar(&createHostname, "hostname", "", "nameserver hostname, either fully-qualified (ns1.example.com) or bare label (ns1) (required)")
	createCmd.Flags().StringVar(&createIPs, "ips", "", "comma-separated IP addresses (required)")
	_ = createCmd.MarkFlagRequired("hostname")
	_ = createCmd.MarkFlagRequired("ips")

	updateCmd.Flags().StringVar(&updateIPs, "ips", "", "comma-separated IP addresses (required)")
	_ = updateCmd.MarkFlagRequired("ips")

	cmdutil.GroupCmd(Cmd)
	cmdutil.MarkWrite(createCmd, updateCmd, deleteCmd)
	Cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd)
}

var listPage, listLimit int

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	paging, err := cmdutil.ListPaging(cmd, listAll, listPage, listLimit)
	if err != nil {
		return err
	}

	spin := out.StartSpinner("Fetching vanity nameservers…")
	page := listPage
	var all []*coreapigo.VanityNameserverResponse
	var hasMore bool
	var nextPage int
	var lastResult *coreapigo.ListVanityNameserversResponse
	for {
		result, err := client.SDK().VanityNameservers.ListVanityNameservers(cmd.Context(),
			&coreapigo.ListVanityNameserversRequest{DomainName: domain, Page: &page, PerPage: paging.PerPage})
		if err != nil {
			spin.Stop()
			return api.FromSDKError(err)
		}
		all = append(all, cmdutil.NonNil(result.VanityNameservers)...)
		lastResult = result
		next, ok := cmdutil.NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			break
		}
		// cmdutil.ListPaging: --all, or --quiet without --page or --limit.
		if !paging.All {
			hasMore, nextPage = true, next
			break
		}
		page = next
		spin.Update(fmt.Sprintf("Fetching vanity nameservers… (page %d, %d so far)", page, len(all)))
	}
	spin.Stop()

	if out.QuietMode {
		hostnames := make([]string, 0, len(all))
		for _, v := range all {
			hostnames = append(hostnames, derefStr(v.Hostname))
		}
		out.PrintQuiet(hostnames)
		if hasMore {
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
		return out.JSONList(all, np, 0)
	case output.FormatYAML:
		var np *int32
		if hasMore {
			np = cmdutil.Int32Page(lastResult.NextPage)
		}
		return out.YAMLList(all, np, 0)
	default:
		if len(all) == 0 {
			cmdutil.EmptyPage(out, listPage, "vanity nameserver", fmt.Sprintf("Run 'namecom vanity-ns create %s --hostname ns1.%s --ips 1.2.3.4' to add one", domain, domain))
			return nil
		}
		out.Table(
			[]string{"HOSTNAME", "IPS"},
			vanityRows(all),
		)
		if hasMore {
			out.Count(len(all), "vanity nameserver", cmdutil.MorePages(nextPage))
		} else {
			out.Count(len(all), "vanity nameserver")
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
	hostname, err := vanityHostname(args[1], domain)
	if err != nil {
		return err
	}
	stop := out.Spin("Fetching vanity nameserver…")
	ns, err := client.SDK().VanityNameservers.GetVanityNameserver(cmd.Context(),
		&coreapigo.GetVanityNameserverRequest{DomainName: domain, Hostname: hostname})
	stop()
	if err != nil {
		return vanityError(err, domain, hostname)
	}
	if err := cmdutil.RequireField("the nameserver's hostname", ns.Hostname); err != nil {
		return err
	}

	// --quiet prints the identifying value only, matching list commands.
	if out.QuietMode {
		out.PrintQuiet([]string{derefStr(ns.Hostname)})
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(ns)
	case output.FormatYAML:
		return out.YAML(ns)
	default:
		out.Table(
			[]string{"HOSTNAME", "IPS"},
			vanityRows([]*coreapigo.VanityNameserverResponse{ns}),
		)
	}
	return nil
}

// vanityError names the nameserver when the API found none: its own "Not
// Found" does not say what was missing.
func vanityError(err error, domain, hostname string) error {
	if cmdutil.IsNotFound(err) {
		return cmdutil.NotFound(err, fmt.Sprintf("vanity nameserver %s not found on %s — run 'namecom vanity-ns list %s' to see them", hostname, domain, domain))
	}
	return api.FromSDKError(err)
}

// dryRunExists reads the nameserver under --dry-run, so a dry run of update
// or delete fails as the real request would — not_found, exit 4 — instead of
// previewing a change to a nameserver that is not there (#292). A real run
// sends the write alone and lets the API refuse it: one request, not two.
func dryRunExists(cmd *cobra.Command, domain, hostname string) error {
	if !cmdutil.IsDryRun(cmd) {
		return nil
	}
	_, err := cmdutil.APIClient(cmd).SDK().VanityNameservers.GetVanityNameserver(cmd.Context(),
		&coreapigo.GetVanityNameserverRequest{DomainName: domain, Hostname: hostname})
	if err != nil {
		return vanityError(err, domain, hostname)
	}
	return nil
}

// vanityLabel converts a nameserver hostname into the form the create endpoint
// expects. That endpoint takes only the subdomain portion — "ns1", deriving the
// rest from the URL path — while get/update/delete take the full hostname. Our
// help text documents the FQDN spelling everywhere, which is right for three of
// the four commands, so accept either form here rather than making create the
// odd one out.
//
// name is how the error messages refer to the input: "--hostname" for create,
// "hostname" for the positional argument of get/update/delete.
func vanityLabel(name, hostname, domain string) (string, error) {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if h == "" {
		return "", cmdutil.NewUsageError(fmt.Errorf("%s is required", name))
	}
	if !strings.Contains(h, ".") {
		return h, validVanityName(h, domain) // already a bare label
	}
	suffix := "." + domain
	if !strings.HasSuffix(h, suffix) {
		return "", fmt.Errorf("%s %q must be a subdomain of %s (e.g. ns1.%s)", name, hostname, domain, domain)
	}
	label := strings.TrimSuffix(h, suffix)
	if label == "" {
		return "", fmt.Errorf("%s %q must include a subdomain (e.g. ns1.%s)", name, hostname, domain)
	}
	return label, validVanityName(label, domain)
}

// validVanityName holds the nameserver a label names to the rules any other
// nameserver argument gets. `ns1..example.com` used to pass with the label
// "ns1." (#187), as did labels with spaces or characters no hostname has.
func validVanityName(label, domain string) error {
	return cmdutil.ValidNameserver(label+"."+domain, 0)
}

// vanityHostname is the FQDN that get/update/delete take as their path
// parameter. It goes through vanityLabel so that every spelling create accepts
// — a bare label, a trailing dot, any case — names the same nameserver here.
// Passing a bare `ns1` through unchanged made the API answer "Hostname not
// found." for a nameserver create had just made.
func vanityHostname(hostname, domain string) (string, error) {
	label, err := vanityLabel("hostname", hostname, domain)
	if err != nil {
		return "", err
	}
	return label + "." + domain, nil
}

func runCreate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	label, err := vanityLabel("--hostname", createHostname, domain)
	if err != nil {
		return err
	}

	ips, err := parseIPs(createIPs)
	if err != nil {
		return err
	}
	body := coreapigo.CreateVanityNameserverBody{
		DomainName: domain,
		Hostname:   label,
		Ips:        ips,
	}

	var ns *coreapigo.VanityNameserverResponse
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.CreateVanityNameserverBody]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/domains/%s/vanity_nameservers", domain),
		Body:   body,
		Spin:   "Creating vanity nameserver…",
	}, func(ctx context.Context, body coreapigo.CreateVanityNameserverBody) error {
		var err error
		ns, err = client.SDK().VanityNameservers.CreateVanityNameserver(ctx, &body)
		return api.FromSDKError(err)
	})
	if err != nil || !sent {
		return err
	}

	// The hostname is what get, update and delete take.
	if out.QuietMode {
		host := createHostname
		if ns != nil && derefStr(ns.Hostname) != "" {
			host = derefStr(ns.Hostname)
		}
		out.Quiet(host)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(ns)
	case output.FormatYAML:
		return out.YAML(ns)
	default:
		out.Success(fmt.Sprintf("Created vanity nameserver %s → %s", createHostname, ipList(splitIPs(createIPs))))
	}
	return nil
}

func runUpdate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	hostname, err := vanityHostname(args[1], domain)
	if err != nil {
		return err
	}

	ips, err := parseIPs(updateIPs)
	if err != nil {
		return err
	}
	if err := dryRunExists(cmd, domain, hostname); err != nil {
		return err
	}
	body := coreapigo.UpdateVanityNameserverBody{
		DomainName: domain,
		Hostname:   hostname,
		Ips:        ips,
	}

	var ns *coreapigo.VanityNameserverResponse
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.UpdateVanityNameserverBody]{
		Method: "PUT",
		Path:   fmt.Sprintf("/core/v1/domains/%s/vanity_nameservers/%s", domain, hostname),
		Body:   body,
		Spin:   "Updating vanity nameserver…",
	}, func(ctx context.Context, body coreapigo.UpdateVanityNameserverBody) error {
		var err error
		ns, err = client.SDK().VanityNameservers.UpdateVanityNameserver(ctx, &body)
		return api.FromSDKError(err)
	})
	if err != nil || !sent || out.Quiet() {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(ns)
	case output.FormatYAML:
		return out.YAML(ns)
	default:
		out.Success(fmt.Sprintf("Updated vanity nameserver %s: IPs now %s", hostname, ipList(ips)))
	}
	return nil
}

func runDelete(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	hostname, err := vanityHostname(args[1], domain)
	if err != nil {
		return err
	}

	if err := dryRunExists(cmd, domain, hostname); err != nil {
		return err
	}
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[cmdutil.NoBody]{
		Method: "DELETE",
		Path:   fmt.Sprintf("/core/v1/domains/%s/vanity_nameservers/%s", domain, hostname),
		Prompt: fmt.Sprintf("Delete vanity nameserver %s from %s?", hostname, domain),
		Spin:   "Deleting vanity nameserver…",
	}, func(ctx context.Context, _ cmdutil.NoBody) error {
		return api.FromSDKError(client.SDK().VanityNameservers.DeleteVanityNameserver(ctx,
			&coreapigo.DeleteVanityNameserverRequest{DomainName: domain, Hostname: hostname}))
	})
	if err != nil || !sent {
		return err
	}
	out.Success(fmt.Sprintf("Deleted vanity nameserver %s from %s", hostname, domain))
	return nil
}

// ipList joins glue IPs for a success line, or says there are none.
func ipList(ips []string) string {
	if len(ips) == 0 {
		return "none"
	}
	return strings.Join(ips, ", ")
}

func vanityRows(nss []*coreapigo.VanityNameserverResponse) [][]string {
	rows := make([][]string, 0, len(nss))
	for _, ns := range nss {
		rows = append(rows, []string{
			derefStr(ns.Hostname),
			strings.Join(ns.Ips, ", "),
		})
	}
	return rows
}

// splitIPs parses the --ips flag. It always returns a non-nil slice so an empty
// value marshals as [] rather than null: the API documents "Providing an empty
// array will remove all existing IPs", so `--ips ""` is the supported way to
// clear glue records. strings.Split("", ",") yields [""], which would send a
// blank address instead.
func splitIPs(s string) []string {
	ips := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			ips = append(ips, p)
		}
	}
	return ips
}

// parseIPs is splitIPs, refusing an entry that is not an IPv4 or IPv6
// address as `dns create` refuses one (#292). "999.1.1.1" was previewed and
// sent.
func parseIPs(s string) ([]string, error) {
	ips := splitIPs(s)
	for _, ip := range ips {
		if net.ParseIP(ip) == nil {
			return nil, cmdutil.NewUsageError(fmt.Errorf("--ips: %q is not an IPv4 or IPv6 address", ip))
		}
	}
	return ips, nil
}

// derefStr returns the value behind a *string, or "" when it is nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
