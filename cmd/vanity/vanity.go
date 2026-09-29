// Package vanity implements the `namecom vanity-ns` command group.
package vanity

import (
	"context"
	"fmt"
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
	Use:   "list <domain>",
	Short: "List vanity nameservers for a domain",
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
	Use:   "create <domain>",
	Short: "Create a vanity nameserver",
	Example: `  namecom vanity-ns create example.com --hostname ns1.example.com --ips 1.2.3.4
  namecom vanity-ns create example.com --hostname ns1.example.com --ips 1.2.3.4,5.6.7.8`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCreate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var updateCmd = &cobra.Command{
	Use:   "update <domain> <hostname>",
	Short: "Update vanity nameserver IPs",
	Example: `  namecom vanity-ns update example.com ns1.example.com --ips 1.2.3.4,5.6.7.8
  namecom vanity-ns update example.com ns1 --ips 1.2.3.4`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runUpdate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var deleteCmd = &cobra.Command{
	Use:   "delete <domain> <hostname>",
	Short: "Delete a vanity nameserver",
	Example: `  namecom vanity-ns delete example.com ns1.example.com
  namecom vanity-ns delete example.com ns1`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runDelete,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	listCmd.Flags().BoolVar(&listAll, "all", false, "fetch all pages")

	createCmd.Flags().StringVar(&createHostname, "hostname", "", "nameserver hostname, either fully-qualified (ns1.example.com) or bare label (ns1) (required)")
	createCmd.Flags().StringVar(&createIPs, "ips", "", "comma-separated IP addresses (required)")
	_ = createCmd.MarkFlagRequired("hostname")
	_ = createCmd.MarkFlagRequired("ips")

	updateCmd.Flags().StringVar(&updateIPs, "ips", "", "comma-separated IP addresses (required)")
	_ = updateCmd.MarkFlagRequired("ips")

	cmdutil.GroupCmd(Cmd)
	Cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	spin := out.StartSpinner("Fetching vanity nameservers…")
	page := 1
	var all []*coreapigo.VanityNameserverResponse
	var hasMore bool
	var lastResult *coreapigo.ListVanityNameserversResponse
	for {
		result, err := client.SDK().VanityNameservers.ListVanityNameservers(cmd.Context(),
			&coreapigo.ListVanityNameserversRequest{DomainName: domain, Page: &page})
		if err != nil {
			spin.Stop()
			return api.FromSDKError(err)
		}
		all = append(all, result.VanityNameservers...)
		lastResult = result
		next, ok := cmdutil.NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			break
		}
		// --quiet returns before the "showing first page" hint, so stopping
		// early would truncate silently. Page fully whenever the caller cannot
		// be told there is more — see cmd/contact/contact.go.
		if !listAll && !out.QuietMode {
			hasMore = true
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
			out.Empty("vanity nameserver", fmt.Sprintf("Run 'namecom vanity-ns create %s --hostname ns1.%s --ips 1.2.3.4' to add one", domain, domain))
			return nil
		}
		out.Table(
			[]string{"HOSTNAME", "IPS"},
			vanityRows(all),
		)
		out.Count(len(all), "vanity nameserver")
		if hasMore {
			out.Hint("Showing first page — pass --all to fetch all entries")
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
		return "", fmt.Errorf("%s is required", name)
	}
	if !strings.Contains(h, ".") {
		return h, nil // already a bare label
	}
	suffix := "." + domain
	if !strings.HasSuffix(h, suffix) {
		return "", fmt.Errorf("%s %q must be a subdomain of %s (e.g. ns1.%s)", name, hostname, domain, domain)
	}
	label := strings.TrimSuffix(h, suffix)
	if label == "" {
		return "", fmt.Errorf("%s %q must include a subdomain (e.g. ns1.%s)", name, hostname, domain)
	}
	return label, nil
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

	ips := splitIPs(createIPs)
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

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(ns)
	case output.FormatYAML:
		return out.YAML(ns)
	default:
		out.Success(fmt.Sprintf("Created vanity nameserver %s", createHostname))
		out.Hint(fmt.Sprintf("Run 'namecom vanity-ns list %s' to see all nameservers", domain))
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

	ips := splitIPs(updateIPs)
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
	if err != nil || !sent {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(ns)
	case output.FormatYAML:
		return out.YAML(ns)
	default:
		out.Success(fmt.Sprintf("Updated vanity nameserver %s", hostname))
		out.Hint(fmt.Sprintf("Run 'namecom vanity-ns list %s' to see all nameservers", domain))
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
	out.Hint(fmt.Sprintf("Run 'namecom vanity-ns list %s' to see remaining nameservers", domain))
	return nil
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

// derefStr returns the value behind a *string, or "" when it is nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
