// Package dnssec implements the `namecom dnssec` command group.
package dnssec

import (
	"context"
	"encoding/hex"
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
	Short: "Manage DS records at the registry (DNSSEC)",
	// It said "Enable DNSSEC signing". These commands publish and remove the
	// registry's DS records; signing the zone is the DNS host's job (#237).
	Long: `Manage the DS records the registry publishes for a domain. They point
validating resolvers at the keys the domain's DNS host signs the zone with;
signing the zone is done by the DNS host, not by these commands.`,
}

var (
	createAlgorithm  int32
	createDigest     string
	createDigestType int32
	createKeyTag     int32
)

var listCmd = &cobra.Command{
	Use:               "list <domain>",
	Aliases:           []string{"ls"},
	Short:             "List a domain's DS records",
	Example:           `  namecom dnssec list example.com`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runList,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var getCmd = &cobra.Command{
	Use:               "get <domain> <digest>",
	Short:             "Get a DS record by its digest",
	Example:           `  namecom dnssec get example.com abc123def456`,
	Args:              cmdutil.ExactArgs(2),
	RunE:              runGet,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:     "create <domain>",
	Aliases: []string{"add"},
	Short:   "Add a DS record",
	Long: `Add a DS record at the registry. Take its values from the DNS host, which
publishes the matching DNSKEY: a DS record that matches none of the keys the
zone is signed with makes validating resolvers fail to resolve the domain.`,
	Example:           `  namecom dnssec create example.com --algorithm 8 --digest-type 2 --key-tag 12345 --digest abc123`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCreate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var deleteCmd = &cobra.Command{
	Use:     "delete <domain> <digest>",
	Aliases: []string{"rm"},
	Short:   "Remove a DS record",
	Long: `Remove a DS record from the registry. To turn DNSSEC off, remove every DS
record before the DNS host stops signing the zone. If others remain, at
least one must match a key the zone is signed with.`,
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
	cmdutil.MarkWrite(createCmd, deleteCmd)
	cmdutil.MarkList(listCmd)
	Cmd.AddCommand(listCmd, getCmd, createCmd, deleteCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	stop := out.Spin("Fetching DS records…")
	result, err := client.SDK().DnsseCs.ListDnsseCs(cmd.Context(),
		&coreapigo.ListDnsseCsRequest{DomainName: domain})
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return cmdutil.DomainNotFound(err, domain)
		}
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
		headers := []string{"KEY TAG", "ALGORITHM", "DIGEST TYPE", "DIGEST"}
		if len(result.Dnssec) == 0 {
			out.EmptyTable(headers, "DS record", fmt.Sprintf("Run 'namecom dnssec create %s --algorithm 8 --digest-type 2 --key-tag N --digest HEX' to add one", domain))
			return nil
		}
		out.Table(
			headers,
			dnssecRows(result.Dnssec),
		)
		out.Count(len(result.Dnssec), "DS record")
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
	stop := out.Spin("Fetching DS record…")
	key, err := client.SDK().DnsseCs.GetDnssec(cmd.Context(),
		&coreapigo.GetDnssecRequest{DomainName: domain, Digest: args[1]})
	stop()
	if err != nil {
		return keyError(err, domain, args[1])
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

	if err := validKey(createKeyTag, createDigestType, createDigest); err != nil {
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
		Spin:   "Adding DS record…",
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
		out.Success(fmt.Sprintf("Added DS record with key tag %d to %s (algorithm %d, digest type %d, digest %s)",
			body.KeyTag, domain, body.Algorithm, body.DigestType, body.Digest))
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

	// A dry run reads the key first, so one for a digest the domain does not
	// have fails not_found, as the delete would, rather than previewing it
	// with the DS warning (#292). A real run sends the delete alone and lets
	// the API refuse it: one request, not two.
	if cmdutil.IsDryRun(cmd) {
		if _, err := client.SDK().DnsseCs.GetDnssec(cmd.Context(),
			&coreapigo.GetDnssecRequest{DomainName: domain, Digest: digest}); err != nil {
			return keyError(err, domain, digest)
		}
	}

	// The delete gave no warning at all (#235). What these "keys" are is the
	// DS records the registry publishes for the zone, so what removing one
	// can break depends on what is left: resolvers that validate fail the
	// domain when the remaining DS records match none of the keys its DNS
	// host signs with, and simply stop validating when none remain — the
	// first step of turning DNSSEC off.
	if out.Format == output.FormatTable && !out.QuietMode {
		out.Warn(fmt.Sprintf("If other DS records remain for %s and none matches a key its DNS host signs the zone with, "+
			"validating resolvers will fail to resolve it. With no DS record left, validation simply stops.", domain))
	}

	// Escaped as the SDK escapes it, so --dry-run shows the path sent (#187).
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[cmdutil.NoBody]{
		Method: "DELETE",
		Path:   fmt.Sprintf("/core/v1/domains/%s/dnssec/%s", domain, url.PathEscape(digest)),
		Prompt: fmt.Sprintf("Remove DS record %s from %s?", digest, domain),
		Spin:   "Removing DS record…",
	}, func(ctx context.Context, _ cmdutil.NoBody) error {
		return api.FromSDKError(client.SDK().DnsseCs.DeleteDnssec(ctx,
			&coreapigo.DeleteDnssecRequest{DomainName: domain, Digest: digest}))
	})
	if err != nil || !sent {
		return err
	}
	out.Success(fmt.Sprintf("Removed DS record from %s", domain))
	return nil
}

// keyError names the DS record when the API found none: its own "Not Found" does
// not say what was missing.
func keyError(err error, domain, digest string) error {
	if cmdutil.IsNotFound(err) {
		return cmdutil.NotFound(err, fmt.Sprintf("DS record %s not found on %s", digest, domain),
			fmt.Sprintf("run 'namecom dnssec list %s' to see its digests", domain))
	}
	return api.FromSDKError(err)
}

// digestLengths is the hex length of each DS digest type's digest: SHA-1 (1),
// SHA-256 (2), GOST R 34.11-94 (3) and SHA-384 (4), per the IANA registry.
var digestLengths = map[int32]int{1: 40, 2: 64, 3: 64, 4: 96}

// validKey refuses a DS record the registry could not accept, before any
// request (#292). The key tag is a 16-bit field, and the digest is hex of the
// length its type produces; "xyz" and a key tag of 99999999 were previewed
// and sent. A digest type the table does not know is checked for hex alone.
func validKey(keyTag, digestType int32, digest string) error {
	if keyTag < 0 || keyTag > 65535 {
		return cmdutil.NewUsageError(fmt.Errorf("--key-tag must be between 0 and 65535, got %d", keyTag))
	}
	if _, err := hex.DecodeString(digest); err != nil || digest == "" {
		return cmdutil.NewUsageError(fmt.Errorf("--digest must be hexadecimal, got %q", digest))
	}
	if want, ok := digestLengths[digestType]; ok && len(digest) != want {
		return cmdutil.NewUsageError(fmt.Errorf("--digest must be %d hex characters for digest type %d, got %d", want, digestType, len(digest)))
	}
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
