// Package dnssec implements the `namecom dnssec` command group.
package dnssec

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom dnssec` parent command.
var Cmd = &cobra.Command{
	Use:   "dnssec",
	Short: "Enable DNSSEC signing to protect against DNS spoofing",
}

var (
	createAlgorithm  int32
	createDigest     string
	createDigestType int32
	createKeyTag     int32
)

var listCmd = &cobra.Command{
	Use:               "list <domain>",
	Short:             "List DNSSEC keys for a domain",
	Example:           `  namecom dnssec list example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runList,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var getCmd = &cobra.Command{
	Use:               "get <domain> <digest>",
	Short:             "Get a specific DNSSEC key",
	Example:           `  namecom dnssec get example.com abc123def456`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:               "create <domain>",
	Short:             "Add a DNSSEC key",
	Example:           `  namecom dnssec create example.com --algorithm 8 --digest-type 2 --key-tag 12345 --digest abc123`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCreate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var deleteCmd = &cobra.Command{
	Use:               "delete <domain> <digest>",
	Short:             "Remove a DNSSEC key",
	Example:           `  namecom dnssec delete example.com abc123def456`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runDelete,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	createCmd.Flags().Int32Var(&createAlgorithm, "algorithm", 0, "DNSSEC algorithm number (required)")
	createCmd.Flags().StringVar(&createDigest, "digest", "", "digest of the DNSKEY RR (required)")
	createCmd.Flags().Int32Var(&createDigestType, "digest-type", 0, "digest type number (required)")
	createCmd.Flags().Int32Var(&createKeyTag, "key-tag", 0, "key tag (required)")
	_ = createCmd.MarkFlagRequired("algorithm")
	_ = createCmd.MarkFlagRequired("digest")
	_ = createCmd.MarkFlagRequired("digest-type")
	_ = createCmd.MarkFlagRequired("key-tag")

	cmdutil.GroupCmd(Cmd)
	Cmd.AddCommand(listCmd, getCmd, createCmd, deleteCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	stop := out.Spin("Fetching DNSSEC keys…")
	result, err := client.SDK().DnsseCs.ListDnsseCs(cmd.Context(),
		&coreapigo.ListDnsseCsRequest{DomainName: domain})
	stop()
	if err != nil {
		return api.FromSDKError(err)
	}
	result.Dnssec = cmdutil.NonNil(result.Dnssec)

	if out.QuietMode {
		digests := make([]string, 0, len(result.Dnssec))
		for _, d := range result.Dnssec {
			digests = append(digests, d.Digest)
		}
		out.PrintQuiet(digests)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		// Use the same {"data":[…]} envelope every other list command emits, so
		// scripts can treat list output uniformly. ListDNSSECsResponseSchema has
		// no pagination fields, hence the nil/0 arguments.
		return out.JSONList(result.Dnssec, nil, 0)
	case output.FormatYAML:
		return out.YAMLList(result.Dnssec, nil, 0)
	default:
		if len(result.Dnssec) == 0 {
			out.Empty("DNSSEC key", fmt.Sprintf("Run 'namecom dnssec create %s --algorithm 8 --digest-type 2 --key-tag N --digest HEX' to add one", domain))
			return nil
		}
		out.Table(
			[]string{"KEY TAG", "ALGORITHM", "DIGEST TYPE", "DIGEST"},
			dnssecRows(result.Dnssec),
		)
		out.Count(len(result.Dnssec), "DNSSEC key")
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
	stop := out.Spin("Fetching DNSSEC key…")
	key, err := client.SDK().DnsseCs.GetDnssec(cmd.Context(),
		&coreapigo.GetDnssecRequest{DomainName: domain, Digest: args[1]})
	stop()
	if err != nil {
		return err
	}
	if err := cmdutil.RequireField("the key's digest", key.Digest); err != nil {
		return err
	}

	// --quiet prints the identifying value only, matching list commands.
	if out.QuietMode {
		out.PrintQuiet([]string{key.Digest})
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(key)
	case output.FormatYAML:
		return out.YAML(key)
	default:
		out.Table(
			[]string{"KEY TAG", "ALGORITHM", "DIGEST TYPE", "DIGEST"},
			dnssecRows([]*coreapigo.Dnssec{key}),
		)
	}
	return nil
}

func runCreate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	body := coreapigo.CreateDnssecBody{
		DomainName: domain,
		Algorithm:  int(createAlgorithm),
		Digest:     createDigest,
		DigestType: int(createDigestType),
		KeyTag:     int(createKeyTag),
	}

	var key *coreapigo.Dnssec
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.CreateDnssecBody]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/domains/%s/dnssec", domain),
		Body:   body,
		Spin:   "Adding DNSSEC key…",
	}, func(ctx context.Context, body coreapigo.CreateDnssecBody) error {
		var err error
		key, err = client.SDK().DnsseCs.CreateDnssec(ctx, &body)
		return api.FromSDKError(err)
	})
	if err != nil || !sent {
		return err
	}

	// The digest is what get and delete take, so it is the identifier.
	if out.QuietMode {
		digest := body.Digest
		if key != nil && key.Digest != "" {
			digest = key.Digest
		}
		out.Quiet(digest)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(key)
	case output.FormatYAML:
		return out.YAML(key)
	default:
		out.Success(fmt.Sprintf("Added DNSSEC key (digest: %s)", key.Digest))
		out.Hint(fmt.Sprintf("Run 'namecom dnssec list %s' to see all keys", domain))
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
	digest := args[1]

	// Escaped as the SDK escapes it, so --dry-run shows the path sent (#187).
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[cmdutil.NoBody]{
		Method: "DELETE",
		Path:   fmt.Sprintf("/core/v1/domains/%s/dnssec/%s", domain, url.PathEscape(digest)),
		Prompt: fmt.Sprintf("Remove DNSSEC key %s from %s?", digest, domain),
		Spin:   "Removing DNSSEC key…",
	}, func(ctx context.Context, _ cmdutil.NoBody) error {
		return api.FromSDKError(client.SDK().DnsseCs.DeleteDnssec(ctx,
			&coreapigo.DeleteDnssecRequest{DomainName: domain, Digest: digest}))
	})
	if err != nil || !sent {
		return err
	}
	out.Success(fmt.Sprintf("Removed DNSSEC key from %s", domain))
	out.Hint(fmt.Sprintf("Run 'namecom dnssec list %s' to see remaining keys", domain))
	return nil
}

func dnssecRows(keys []*coreapigo.Dnssec) [][]string {
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{
			strconv.Itoa(k.KeyTag),
			strconv.Itoa(k.Algorithm),
			strconv.Itoa(k.DigestType),
			k.Digest,
		})
	}
	return rows
}
