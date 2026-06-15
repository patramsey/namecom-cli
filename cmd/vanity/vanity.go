// Package vanity implements the `namecom vanity-ns` command group.
package vanity

import (
	"fmt"
	"strings"

	"github.com/namedotcom/namecom-cli/cmd/cmdutil"
	"github.com/namedotcom/namecom-cli/internal/api"
	"github.com/namedotcom/namecom-cli/internal/api/gen"
	"github.com/namedotcom/namecom-cli/internal/output"
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
)

var listCmd = &cobra.Command{
	Use:   "list <domain>",
	Short: "List vanity nameservers for a domain",
	Example: `  namecom vanity-ns list example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runList,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var getCmd = &cobra.Command{
	Use:   "get <domain> <hostname>",
	Short: "Get a vanity nameserver",
	Example: `  namecom vanity-ns get example.com ns1.example.com`,
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
	Example: `  namecom vanity-ns update example.com ns1.example.com --ips 1.2.3.4,5.6.7.8`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runUpdate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var deleteCmd = &cobra.Command{
	Use:   "delete <domain> <hostname>",
	Short: "Delete a vanity nameserver",
	Example: `  namecom vanity-ns delete example.com ns1.example.com`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runDelete,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	createCmd.Flags().StringVar(&createHostname, "hostname", "", "fully-qualified nameserver hostname (required)")
	createCmd.Flags().StringVar(&createIPs, "ips", "", "comma-separated IP addresses (required)")
	_ = createCmd.MarkFlagRequired("hostname")
	_ = createCmd.MarkFlagRequired("ips")

	updateCmd.Flags().StringVar(&updateIPs, "ips", "", "comma-separated IP addresses (required)")
	_ = updateCmd.MarkFlagRequired("ips")

	Cmd.AddCommand(listCmd, getCmd, createCmd, updateCmd, deleteCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain := args[0]

	stop := out.Spin("Fetching vanity nameservers…")
	var page int32 = 1
	var all []gen.VanityNameserverResponseSchema
	for {
		params := &gen.ListVanityNameserversParams{Page: &page}
		resp, err := client.Gen().ListVanityNameservers(cmd.Context(), domain, params)
		if err != nil {
			stop()
			return err
		}
		var result gen.ListVanityNameserversResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			stop()
			return err
		}
		all = append(all, result.VanityNameservers...)
		if result.NextPage == nil || *result.NextPage == 0 {
			break
		}
		page = *result.NextPage
	}
	stop()

	if out.QuietMode {
		hostnames := make([]string, 0, len(all))
		for _, v := range all {
			hostnames = append(hostnames, v.Hostname)
		}
		out.PrintQuiet(hostnames)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(all)
	case output.FormatYAML:
		return out.YAML(all)
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
	}
	return nil
}

func runGet(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)

	stop := out.Spin("Fetching vanity nameserver…")
	resp, err := client.Gen().GetVanityNameserver(cmd.Context(), args[0], args[1])
	stop()
	if err != nil {
		return err
	}
	var ns gen.VanityNameserverResponseSchema
	if err := api.Decode(resp, &ns); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(ns)
	case output.FormatYAML:
		return out.YAML(ns)
	default:
		out.Table(
			[]string{"HOSTNAME", "IPS"},
			vanityRows([]gen.VanityNameserverResponseSchema{ns}),
		)
	}
	return nil
}

func runCreate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain := args[0]

	ips := splitIPs(createIPs)
	body := gen.CreateVanityNameserverJSONRequestBody{
		Hostname: createHostname,
		Ips:      ips,
	}

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s/vanity_nameservers", domain), nil)
		fmt.Fprintf(out.Writer, "  hostname=%s ips=%s\n", createHostname, createIPs)
		return nil
	}

	stop := out.Spin("Creating vanity nameserver…")
	resp, err := client.Gen().CreateVanityNameserver(cmd.Context(), domain, body)
	stop()
	if err != nil {
		return err
	}
	var ns gen.VanityNameserverResponseSchema
	if err := api.Decode(resp, &ns); err != nil {
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
	dryRun := cmdutil.IsDryRun(cmd)
	domain, hostname := args[0], args[1]

	ips := splitIPs(updateIPs)
	body := gen.UpdateVanityNameserverJSONRequestBody{
		Ips: &ips,
	}

	if dryRun {
		out.DryRun("PUT", fmt.Sprintf("/core/v1/domains/%s/vanity_nameservers/%s", domain, hostname), nil)
		fmt.Fprintf(out.Writer, "  ips=%s\n", updateIPs)
		return nil
	}

	stop := out.Spin("Updating vanity nameserver…")
	resp, err := client.Gen().UpdateVanityNameserver(cmd.Context(), domain, hostname, body)
	stop()
	if err != nil {
		return err
	}
	var ns gen.VanityNameserverResponseSchema
	if err := api.Decode(resp, &ns); err != nil {
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
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain, hostname := args[0], args[1]

	ok, err := cmdutil.Confirm(yes, fmt.Sprintf("Delete vanity nameserver %s from %s?", hostname, domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	if dryRun {
		out.DryRun("DELETE", fmt.Sprintf("/core/v1/domains/%s/vanity_nameservers/%s", domain, hostname), nil)
		return nil
	}

	stop := out.Spin("Deleting vanity nameserver…")
	resp, err := client.Gen().DeleteVanityNameserver(cmd.Context(), domain, hostname)
	stop()
	if err != nil {
		return err
	}
	if err := api.Decode(resp, nil); err != nil {
		return err
	}
	out.Success(fmt.Sprintf("Deleted vanity nameserver %s from %s", hostname, domain))
	out.Hint(fmt.Sprintf("Run 'namecom vanity-ns list %s' to see remaining nameservers", domain))
	return nil
}

func vanityRows(nss []gen.VanityNameserverResponseSchema) [][]string {
	rows := make([][]string, 0, len(nss))
	for _, ns := range nss {
		rows = append(rows, []string{
			ns.Hostname,
			strings.Join(ns.Ips, ", "),
		})
	}
	return rows
}

func splitIPs(s string) []string {
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
