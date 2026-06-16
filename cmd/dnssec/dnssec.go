// Package dnssec implements the `namecom dnssec` command group.
package dnssec

import (
	"fmt"
	"strconv"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
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
	Use:   "list <domain>",
	Short: "List DNSSEC keys for a domain",
	Example: `  namecom dnssec list example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runList,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var getCmd = &cobra.Command{
	Use:   "get <domain> <digest>",
	Short: "Get a specific DNSSEC key",
	Example: `  namecom dnssec get example.com abc123def456`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:   "create <domain>",
	Short: "Add a DNSSEC key",
	Example: `  namecom dnssec create example.com --algorithm 8 --digest-type 2 --key-tag 12345 --digest abc123`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCreate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var deleteCmd = &cobra.Command{
	Use:   "delete <domain> <digest>",
	Short: "Remove a DNSSEC key",
	Example: `  namecom dnssec delete example.com abc123def456`,
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

	Cmd.AddCommand(listCmd, getCmd, createCmd, deleteCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain := args[0]

	stop := out.Spin("Fetching DNSSEC keys…")
	resp, err := client.Gen().ListDNSSECs(cmd.Context(), domain)
	stop()
	if err != nil {
		return err
	}
	var result gen.ListDNSSECsResponseSchema
	if err := api.Decode(resp, &result); err != nil {
		return err
	}

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
		return out.JSON(result.Dnssec)
	case output.FormatYAML:
		return out.YAML(result.Dnssec)
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

	stop := out.Spin("Fetching DNSSEC key…")
	resp, err := client.Gen().GetDNSSEC(cmd.Context(), args[0], args[1])
	stop()
	if err != nil {
		return err
	}
	var key gen.DNSSEC
	if err := api.Decode(resp, &key); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(key)
	case output.FormatYAML:
		return out.YAML(key)
	default:
		out.Table(
			[]string{"KEY TAG", "ALGORITHM", "DIGEST TYPE", "DIGEST"},
			dnssecRows([]gen.DNSSEC{key}),
		)
	}
	return nil
}

func runCreate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain := args[0]

	body := gen.CreateDNSSECJSONRequestBody{
		Algorithm:  &createAlgorithm,
		Digest:     &createDigest,
		DigestType: &createDigestType,
		KeyTag:     &createKeyTag,
	}

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s/dnssec", domain), nil)
		fmt.Fprintf(out.Writer, "  algorithm=%d digest=%s digestType=%d keyTag=%d\n",
			createAlgorithm, createDigest, createDigestType, createKeyTag)
		return nil
	}

	stop := out.Spin("Adding DNSSEC key…")
	resp, err := client.Gen().CreateDNSSEC(cmd.Context(), domain, body)
	stop()
	if err != nil {
		return err
	}
	var key gen.DNSSEC
	if err := api.Decode(resp, &key); err != nil {
		return err
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
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain, digest := args[0], args[1]

	ok, err := cmdutil.Confirm(yes, fmt.Sprintf("Remove DNSSEC key %s from %s?", digest, domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	if dryRun {
		out.DryRun("DELETE", fmt.Sprintf("/core/v1/domains/%s/dnssec/%s", domain, digest), nil)
		return nil
	}

	stop := out.Spin("Removing DNSSEC key…")
	resp, err := client.Gen().DeleteDNSSEC(cmd.Context(), domain, digest)
	stop()
	if err != nil {
		return err
	}
	if err := api.Decode(resp, nil); err != nil {
		return err
	}
	out.Success(fmt.Sprintf("Removed DNSSEC key from %s", domain))
	out.Hint(fmt.Sprintf("Run 'namecom dnssec list %s' to see remaining keys", domain))
	return nil
}

func dnssecRows(keys []gen.DNSSEC) [][]string {
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{
			strconv.Itoa(int(k.KeyTag)),
			strconv.Itoa(int(k.Algorithm)),
			strconv.Itoa(int(k.DigestType)),
			k.Digest,
		})
	}
	return rows
}
