// Package dns implements the `namecom dns` command group.
package dns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/huh"
	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// Cmd is the `namecom dns` parent command.
var Cmd = &cobra.Command{
	Use:   "dns",
	Short: "Create and manage DNS records (A, CNAME, MX, TXT, and more)",
}

// defaultTTL is the TTL `dns create` sends when --ttl is not given, and the
// one `dns import` sends for a record whose ttl is missing.
const defaultTTL = 300

var (
	listAll  bool
	listType string

	createType     string
	createHost     string
	createAnswer   string
	createTTL      int64
	createPriority int64

	updateType     string
	updateHost     string
	updateAnswer   string
	updateTTL      int64
	updatePriority int64

	exportZone bool
	importFile string
)

var listCmd = &cobra.Command{
	Use:   "list <domain>",
	Short: "List DNS records for a domain",
	Example: `  namecom dns list example.com
  namecom dns list example.com --type A
  namecom dns list example.com --type MX`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runList,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:   "create <domain>",
	Short: "Create a DNS record",
	Example: `  namecom dns create example.com --type A --answer 1.2.3.4
  namecom dns create example.com --type CNAME --host www --answer example.com.
  namecom dns create example.com --type MX --answer mail.example.com --priority 10
  namecom dns create example.com --type TXT --host @ --answer "v=spf1 include:_spf.example.com ~all"`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runCreate,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var updateCmd = &cobra.Command{
	Use:   "update <domain> <id>",
	Short: "Update a DNS record (read-modify-write: only supplied flags are changed)",
	Example: `  namecom dns update example.com 12345 --answer 1.2.3.4
  namecom dns update example.com 12345 --ttl 3600
  namecom dns update example.com 67890 --host www --answer example.com.   # a CNAME record`,
	Args: cmdutil.ExactArgs(2),
	RunE: runUpdate,
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return cmdutil.CompleteDomains(cmd, args, toComplete)
		}
		if len(args) == 1 {
			return cmdutil.CompleteRecordIDs(cmd, args[0])
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
}

var deleteCmd = &cobra.Command{
	Use:   "delete <domain> <id>",
	Short: "Delete a DNS record",
	Example: `  namecom dns delete example.com 12345
  namecom dns list example.com --type TXT -q | xargs -I{} namecom dns delete example.com {} --yes   # every TXT record`,
	Args: cmdutil.ExactArgs(2),
	RunE: runDelete,
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return cmdutil.CompleteDomains(cmd, args, toComplete)
		}
		if len(args) == 1 {
			return cmdutil.CompleteRecordIDs(cmd, args[0])
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
}

var exportCmd = &cobra.Command{
	Use:   "export <domain>",
	Short: "Export DNS records as JSON (default) or a zone-file snapshot",
	Example: `  namecom dns export example.com > records.json          # save for later import
  namecom dns export example.com --zone > example.com.zone
  namecom dns export old.com | namecom dns import new.com --file -   # clone records to another domain`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runExport,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var importCmd = &cobra.Command{
	Use:   "import <domain>",
	Short: "Import DNS records from a JSON export file",
	Example: `  namecom dns import example.com --file records.json
  namecom dns export old.com | namecom dns import new.com --file -   # pipe directly between domains
  namecom dns import example.com --file records.json --dry-run       # preview without applying`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runImport,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	listCmd.Flags().BoolVar(&listAll, "all", false, "fetch all pages automatically")
	listCmd.Flags().StringVar(&listType, "type", "", "filter by record type (A, AAAA, CNAME, MX, TXT, NS, SRV, ANAME, CAA)")

	createCmd.Flags().StringVar(&createType, "type", "", "record type: A, AAAA, ANAME, CNAME, MX, NS, SRV, TXT (required; prompted in a terminal)")
	createCmd.Flags().StringVar(&createHost, "host", "@", "hostname relative to the zone (@ for apex)")
	createCmd.Flags().StringVar(&createAnswer, "answer", "", "record value (required; prompted in a terminal)")
	createCmd.Flags().Int64Var(&createTTL, "ttl", defaultTTL, "TTL in seconds (minimum 300)")
	createCmd.Flags().Int64Var(&createPriority, "priority", 0, "priority for MX/SRV records")
	// --type and --answer are required, but not marked so: cobra would reject
	// the command before runCreate could offer the guided form (#230).
	// runCreate makes them a usage error itself when there is no terminal.

	updateCmd.Flags().StringVar(&updateType, "type", "", "new record type")
	updateCmd.Flags().StringVar(&updateHost, "host", "", "new host")
	updateCmd.Flags().StringVar(&updateAnswer, "answer", "", "new answer/value")
	updateCmd.Flags().Int64Var(&updateTTL, "ttl", 0, "new TTL in seconds")
	updateCmd.Flags().Int64Var(&updatePriority, "priority", 0, "new priority")

	exportCmd.Flags().BoolVar(&exportZone, "zone", false, "output RFC 1035 zone-file format instead of JSON")

	importCmd.Flags().StringVar(&importFile, "file", "", "JSON file to import (required)")
	_ = importCmd.MarkFlagRequired("file")

	cmdutil.GroupCmd(Cmd)
	Cmd.AddCommand(listCmd, createCmd, updateCmd, deleteCmd, exportCmd, importCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	// --type filters client-side, so it must see every page: filtering only
	// page 1 silently reports "no records" for a zone that has them. Matches
	// `domain list` and `order list`, which auto-page whenever a filter is set.
	autoPage := listAll || listType != ""

	stop := out.Spin("Fetching DNS records…")
	records, hasMore, nextPage, err := fetchAllRecords(cmd, domain, autoPage)
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return cmdutil.NotFound(err, fmt.Sprintf("domain %q not found — run 'namecom domain list' to see your domains", domain))
		}
		return err
	}

	// Apply --type filter.
	if listType != "" {
		upper := strings.ToUpper(listType)
		filtered := records[:0]
		for _, r := range records {
			if r.Type != nil && strings.ToUpper(*r.Type) == upper {
				filtered = append(filtered, r)
			}
		}
		records = filtered
	}

	if out.QuietMode {
		ids := make([]string, 0, len(records))
		for _, r := range records {
			if r.ID != nil {
				ids = append(ids, strconv.Itoa(*r.ID))
			}
		}
		out.PrintQuiet(ids)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSONList(records, cmdutil.Int32Page(nextPage), 0)
	case output.FormatYAML:
		return out.YAMLList(records, cmdutil.Int32Page(nextPage), 0)
	default:
		if len(records) == 0 {
			// Distinguish "this zone is empty" from "nothing matched the filter".
			// The generic message claimed the zone had no records at all and
			// suggested creating "the first record" with a type the user hadn't
			// asked about — three wrong statements for a filtered search.
			if listType != "" {
				out.Empty(strings.ToUpper(listType)+" record",
					fmt.Sprintf("Run 'namecom dns list %s' to see records of all types", domain))
				return nil
			}
			out.Empty("DNS record", fmt.Sprintf("Run 'namecom dns create %s --type A --answer 1.2.3.4' to add the first record", domain))
			return nil
		}
		if listType != "" {
			// Filtered: single flat table.
			headers := []string{"ID", "TYPE", "HOST", "ANSWER", "TTL"}
			if hasPriority(records) {
				headers = append(headers, "PRIORITY")
			}
			out.Table(headers, recordRows(out, records), output.Essential("ANSWER"))
		} else {
			// Unfiltered: group by type with section headers.
			renderGroupedRecords(out, records)
		}
		if hasMore {
			out.Count(len(records), "record", "more exist — pass --all for the rest")
		} else {
			out.Count(len(records), "record")
		}
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

	// Guided form for whatever --type and --answer left out. Without a
	// terminal there is no one to ask, so a missing one is a usage error.
	if missing := missingCreateFlags(); len(missing) > 0 {
		if !output.IsInteractive() {
			return cmdutil.RequiredFlags(true, missing...)
		}
		if err := dnsCreateForm(cmd); err != nil {
			if errors.Is(err, errFormAborted) {
				out.Warn("aborted")
				return nil
			}
			return err
		}
	}

	if createType == "" {
		return cmdutil.NewUsageError(fmt.Errorf("--type is required (A, AAAA, ANAME, CNAME, MX, NS, SRV, TXT)"))
	}
	if err := cmdutil.ValidDNSCreateType(createType); err != nil {
		return err
	}
	host, err := asciiHost(createHost)
	if err != nil {
		return err
	}
	answer, err := asciiAnswer(createType, host, createAnswer)
	if err != nil {
		return err
	}
	for _, w := range cmdutil.DNSAnswerWarnings(createType, answer, createPriority, cmd.Flags().Changed("priority")) {
		out.Warn(w)
	}
	if cmd.Flags().Changed("ttl") {
		if err := cmdutil.ValidTTL(createTTL); err != nil {
			return err
		}
	}
	if cmd.Flags().Changed("priority") {
		if err := cmdutil.ValidPriority(createPriority); err != nil {
			return err
		}
	}

	body := coreapigo.DNSCreateRecordBody{
		DomainName: domain,
		Type:       coreapigo.DNSCreateRecordBodyType(createType),
		Host:       host,
		Answer:     answer,
		TTL:        &createTTL,
	}
	// Gate on the flag, not the value: 0 is a valid MX/SRV priority, so deciding
	// by value makes `--priority 0` unsettable. The warning above already keys
	// off Changed(), and runUpdate/runImport do the same.
	if cmd.Flags().Changed("priority") {
		body.Priority = &createPriority
	}

	var record *coreapigo.Record
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.DNSCreateRecordBody]{
		Method: "POST",
		Path:   fmt.Sprintf("/core/v1/domains/%s/records", domain),
		Body:   body,
	}, func(ctx context.Context, body coreapigo.DNSCreateRecordBody) error {
		var err error
		record, err = client.SDK().DNS.CreateRecord(ctx, &body)
		if err == nil && (record == nil || derefInt(record.ID) <= 0) {
			// A 2xx without the new record's ID is not a record we can name:
			// a redirected POST answered as a GET printed "Created A record
			// (id 0)" and exited 0 having created nothing (#185, #187).
			return &api.UnexpectedResponseError{Reason: "the response did not include the new record's ID"}
		}
		return api.FromSDKError(err)
	})
	if err != nil || !sent {
		return err
	}

	if out.QuietMode {
		if record == nil || record.ID == nil {
			out.Warn("the API did not return the new record's ID")
			return nil
		}
		out.Quiet(strconv.Itoa(*record.ID))
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(record)
	case output.FormatYAML:
		return out.YAML(record)
	default:
		out.Success(fmt.Sprintf("Created %s %s → %s (id %d)",
			strings.ToUpper(createType), recordName(host, domain), answer, derefInt(record.ID)))
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

	id, err := parseID(args[1])
	if err != nil {
		return err
	}
	if cmd.Flags().Changed("priority") {
		if err := cmdutil.ValidPriority(updatePriority); err != nil {
			return err
		}
	}

	// Read-modify-write: fetch existing record so unset flags don't blank fields.
	current, err := client.SDK().DNS.GetRecord(cmd.Context(), &coreapigo.GetRecordRequest{
		DomainName: domain,
		ID:         id,
	})
	if err != nil {
		// Convert first: IsNotFound inspects *api.APIError, and an unconverted
		// SDK error would fall through to the generic message and exit 1
		// instead of 4.
		err = api.FromSDKError(err)
		if cmdutil.IsNotFound(err) {
			return cmdutil.NotFound(err, fmt.Sprintf("record %d not found on %s — run 'namecom dns list %s' to see record IDs", id, domain, domain))
		}
		return err
	}

	body := coreapigo.DNSUpdateRecordBody{
		DomainName: domain,
		ID:         id,
		Type:       coreapigo.DNSUpdateRecordBodyType(derefStr(current.Type)),
		Answer:     derefStr(current.Answer),
		TTL:        &current.TTL,
		Host:       current.Host,
	}
	if current.Priority != nil {
		body.Priority = current.Priority
	}
	// Merge --priority before anything reads body.Priority, so the warnings
	// below reason about the value we will actually send rather than an unset
	// flag variable.
	if cmd.Flags().Changed("priority") {
		body.Priority = &updatePriority
	}

	if cmd.Flags().Changed("type") {
		// The update endpoint rejects CAA just as create does.
		if err := cmdutil.ValidDNSCreateType(updateType); err != nil {
			return err
		}
		body.Type = coreapigo.DNSUpdateRecordBodyType(updateType)
	}
	if cmd.Flags().Changed("host") {
		host, err := asciiHost(updateHost)
		if err != nil {
			return err
		}
		body.Host = &host
	}
	if cmd.Flags().Changed("answer") {
		rtype := string(body.Type)
		host := ""
		if body.Host != nil {
			host = *body.Host
		}
		answer, err := asciiAnswer(rtype, host, updateAnswer)
		if err != nil {
			return err
		}
		for _, w := range cmdutil.DNSAnswerWarnings(rtype, answer, derefInt64(body.Priority), body.Priority != nil) {
			out.Warn(w)
		}
		body.Answer = answer
	} else if cmd.Flags().Changed("type") {
		// Type changed but answer kept from the existing record: re-validate the
		// existing answer against the new type so the mismatch is caught client-side
		// rather than returning a cryptic 422 from the API.
		rtype := string(body.Type)
		host := ""
		if body.Host != nil {
			host = *body.Host
		}
		if err := cmdutil.ValidDNSAnswer(rtype, host, body.Answer); err != nil {
			return fmt.Errorf("existing answer %q is not valid for new type %s: %w", body.Answer, rtype, err)
		}
	}
	if cmd.Flags().Changed("ttl") {
		if err := cmdutil.ValidTTL(updateTTL); err != nil {
			return err
		}
		body.TTL = &updateTTL
	}
	var updated *coreapigo.Record
	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[coreapigo.DNSUpdateRecordBody]{
		Method: "PUT",
		Path:   fmt.Sprintf("/core/v1/domains/%s/records/%d", domain, id),
		Body:   body,
	}, func(ctx context.Context, body coreapigo.DNSUpdateRecordBody) error {
		var err error
		updated, err = client.SDK().DNS.UpdateRecord(ctx, &body)
		return api.FromSDKError(err)
	})
	if err != nil || !sent || out.Quiet() {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(updated)
	case output.FormatYAML:
		return out.YAML(updated)
	default:
		name := fmt.Sprintf("%s %s (id %d)", string(body.Type), recordName(derefStr(body.Host), domain), id)
		if changes := recordChanges(current, body); len(changes) > 0 {
			out.Success("Updated " + name + ": " + strings.Join(changes, ", "))
		} else {
			out.Success("Updated " + name + ": no values changed")
		}
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

	id, err := parseID(args[1])
	if err != nil {
		return err
	}

	// Fetch the record so the prompt can show it. "Delete DNS record 12345
	// from D?" named only an ID, and was asked even for a record that did not
	// exist (#235). A missing one now fails here, before any prompt.
	stop := out.Spin("Fetching record…")
	current, err := client.SDK().DNS.GetRecord(cmd.Context(), &coreapigo.GetRecordRequest{DomainName: domain, ID: id})
	stop()
	if err != nil {
		err = api.FromSDKError(err)
		if cmdutil.IsNotFound(err) {
			return cmdutil.NotFound(err, fmt.Sprintf("record %d not found on %s — run 'namecom dns list %s' to see record IDs", id, domain, domain))
		}
		return err
	}

	sent, err := cmdutil.RunWrite(cmd, cmdutil.Write[cmdutil.NoBody]{
		Method: "DELETE",
		Path:   fmt.Sprintf("/core/v1/domains/%s/records/%d", domain, id),
		Prompt: fmt.Sprintf("Delete %s from %s?", recordSummary(current), domain),
		Spin:   "Deleting record…",
	}, func(ctx context.Context, _ cmdutil.NoBody) error {
		return api.FromSDKError(client.SDK().DNS.DeleteRecord(ctx, &coreapigo.DeleteRecordRequest{
			DomainName: domain,
			ID:         id,
		}))
	})
	if err != nil || !sent {
		return err
	}
	out.Success(fmt.Sprintf("Deleted record %d from %s", id, domain))
	return nil
}

func runExport(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	records, _, _, err := fetchAllRecords(cmd, domain, true)
	if err != nil {
		return err
	}

	if exportZone {
		for _, r := range records {
			rtype := derefStr(r.Type)
			rdata := derefStr(r.Answer)
			switch rtype {
			case "CNAME", "NS":
				rdata = qualify(rdata)
			case "MX", "SRV":
				rdata = qualifyTarget(rdata)
				// MX and SRV require priority prepended to rdata. Emit 0 when the
				// record has none — omitting it yields a line with the wrong field
				// count, which zone parsers reject.
				rdata = fmt.Sprintf("%d %s", derefInt64(r.Priority), rdata)
			case "TXT":
				// TXT rdata is a character-string: unquoted, a value containing
				// spaces (SPF, DKIM) parses as several separate strings and no
				// longer describes the same record.
				rdata = quoteTXT(rdata)
			case "ANAME":
				// ANAME is name.com's own type, not a standard RR, so a zone
				// parser rejects the line — and with it the whole file. Keep
				// the record visible as a comment instead.
				fmt.Fprintf(out.Writer, "; ANAME not representable in a zone file: %s\t%d\tIN\tANAME\t%s\n",
					derefStr(r.Fqdn), r.TTL, qualify(rdata))
				continue
			}
			fmt.Fprintf(out.Writer, "%s\t%d\tIN\t%s\t%s\n",
				derefStr(r.Fqdn), r.TTL, rtype, rdata)
		}
		return nil
	}

	// An empty zone leaves records nil, which marshals as `null`. Export `[]`,
	// as the list commands do through their envelope.
	if records == nil {
		records = []*coreapigo.Record{}
	}
	switch out.Format {
	case output.FormatYAML:
		return out.YAML(records)
	default:
		if err := out.JSON(records); err != nil {
			return err
		}
	}
	out.Hint(fmt.Sprintf("Use --zone for RFC 1035 zone-file format, or pipe to a file: namecom dns export %s > records.json", domain))
	return nil
}

func runImport(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	// The global --dry-run only. A local flag of the same name used to shadow
	// it, which hid the global one from this command's help (#187); a
	// persistent flag is accepted after the subcommand as well as before.
	dryRun := cmdutil.IsDryRun(cmd)

	data, err := readImportData(importFile)
	if err != nil {
		return fmt.Errorf("reading import file: %w", err)
	}

	// A malformed file is bad input, not a failed request: exit 2.
	data, err = decodeImportData(data)
	if err != nil {
		return cmdutil.NewUsageError(fmt.Errorf("decoding import file: %w", err))
	}
	var records []*coreapigo.Record
	if err := json.Unmarshal(data, &records); err != nil {
		return cmdutil.NewUsageError(fmt.Errorf("parsing import file: %w", err))
	}

	// Validate every record before writing any of them. `dns create` validates
	// type/host/answer client-side; the import loop did not, and import is not
	// transactional — so a file whose 4th record was malformed wrote 3 records
	// and then failed on a server-side 422, leaving the zone half-updated.
	for i, r := range records {
		// Normalize before validating, so what is checked is what is sent. The
		// API returns the apex host as "", which is what `dns export` writes;
		// send it as "@", the spelling `dns create --host` defaults to. A file
		// with no ttl decodes as 0, which the server rejects mid-import; give it
		// the same default `dns create --ttl` has.
		if derefStr(r.Host) == "" {
			apex := "@"
			r.Host = &apex
		}
		if r.TTL == 0 {
			r.TTL = defaultTTL
		}
		rtype, host, answer := derefStr(r.Type), derefStr(r.Host), derefStr(r.Answer)
		if err := cmdutil.ValidDNSCreateType(rtype); err != nil {
			return fmt.Errorf("record %d (%s %s): %w", i+1, rtype, host, err)
		}
		// Converted in place, so the request body carries the ASCII form.
		asciiH, err := asciiHost(host)
		if err != nil {
			return fmt.Errorf("record %d (%s %s): %w", i+1, rtype, host, err)
		}
		asciiA, err := asciiAnswer(rtype, asciiH, answer)
		if err != nil {
			return fmt.Errorf("record %d (%s %s): %w", i+1, rtype, host, err)
		}
		r.Host, r.Answer = &asciiH, &asciiA
		if err := cmdutil.ValidTTL(r.TTL); err != nil {
			return fmt.Errorf("record %d (%s %s): %w", i+1, rtype, host, err)
		}
		if r.Priority != nil {
			if err := cmdutil.ValidPriority(*r.Priority); err != nil {
				return fmt.Errorf("record %d (%s %s): %w", i+1, rtype, host, err)
			}
		}
	}

	created := 0
	var previews []output.DryRunRequest
	for _, r := range records {
		body := coreapigo.DNSCreateRecordBody{
			DomainName: domain,
			Type:       coreapigo.DNSCreateRecordBodyType(derefStr(r.Type)),
			Host:       derefStr(r.Host),
			Answer:     derefStr(r.Answer),
			TTL:        &r.TTL,
			Priority:   r.Priority,
		}

		if dryRun {
			previews = append(previews, output.DryRunRequest{
				Method: "POST", Path: fmt.Sprintf("/core/v1/domains/%s/records", domain), Body: body,
			})
			continue
		}

		_, err := client.SDK().DNS.CreateRecord(cmd.Context(), &body)
		if err != nil {
			err = api.MarkWrite(err) // not sent through RunWrite, so marked here
			// Report what already landed. Import is not transactional, so bailing
			// out with only the failure left the user unable to tell whether a
			// retry would duplicate the records written so far.
			if created > 0 {
				out.Warn(fmt.Sprintf("%d of %s were already created on %s before this failure — "+
					"remove them from the file or delete them before retrying, or the retry will duplicate them",
					created, output.Plural(len(records), "record"), domain))
			}
			return fmt.Errorf("creating %s %s (after %d of %d succeeded): %w",
				body.Type, body.Host, created, len(records), err)
		}
		created++
	}

	if dryRun {
		// One document for the whole plan in JSON and YAML modes, so a script
		// parses every request at once rather than a stream of them.
		return out.DryRunAll(previews)
	}
	out.Success(fmt.Sprintf("Imported %s to %s", output.Plural(created, "record"), domain))
	return nil
}

// fetchAllRecords pages through all DNS records for a domain.
func fetchAllRecords(cmd *cobra.Command, domain string, all bool) (records []*coreapigo.Record, hasMore bool, nextPage *int, err error) {
	client := cmdutil.APIClient(cmd)
	ctx := cmd.Context()

	page := 1
	var lastNextPage *int

	for {
		result, err2 := client.SDK().DNS.ListRecords(ctx, &coreapigo.ListRecordsRequest{
			DomainName: domain,
			Page:       &page,
		})
		if err2 != nil {
			return nil, false, nil, api.FromSDKError(err2)
		}
		records = append(records, cmdutil.NonNil(result.Records)...)
		lastNextPage = result.NextPage

		next, ok := cmdutil.NextPage(page, result.NextPage, result.LastPage)
		if !ok {
			break
		}
		if !all {
			hasMore = true
			break
		}
		page = next
	}
	if hasMore {
		nextPage = lastNextPage
	}
	return records, hasMore, nextPage, nil
}

// recordName is the name a record answers to: host joined to the domain, or
// the domain itself for the apex ("" or "@").
func recordName(host, domain string) string {
	if host == "" || host == "@" {
		return domain
	}
	return host + "." + domain
}

// recordSummary describes r in one line, "A www → 1.2.3.4 (TTL 300)", with
// the priority for a record that has one: "MX @ → mail.example.com
// (priority 10, TTL 300)".
func recordSummary(r *coreapigo.Record) string {
	if r == nil {
		return "the record"
	}
	detail := fmt.Sprintf("TTL %d", r.TTL)
	if r.Priority != nil {
		detail = fmt.Sprintf("priority %d, %s", *r.Priority, detail)
	}
	return fmt.Sprintf("%s %s → %s (%s)", derefStr(r.Type), displayHost(r.Host), derefStr(r.Answer), detail)
}

// recordChanges describes what an update changes, "answer 192.0.2.1 →
// 192.0.2.2", one entry per field whose value differs. The success line used
// to say only "Updated record 12345", which named neither the record nor the
// change (#238).
func recordChanges(current *coreapigo.Record, body coreapigo.DNSUpdateRecordBody) []string {
	var changes []string
	add := func(field, was, now string) {
		if was != now {
			changes = append(changes, fmt.Sprintf("%s %s → %s", field, orNone(was), orNone(now)))
		}
	}
	add("type", strings.ToUpper(derefStr(current.Type)), strings.ToUpper(string(body.Type)))
	add("host", displayHost(current.Host), displayHost(body.Host))
	add("answer", derefStr(current.Answer), body.Answer)
	add("ttl", strconv.FormatInt(current.TTL, 10), strconv.FormatInt(derefInt64(body.TTL), 10))
	prio := func(p *int64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatInt(*p, 10)
	}
	add("priority", prio(current.Priority), prio(body.Priority))
	return changes
}

func orNone(s string) string {
	if s == "" {
		return output.None
	}
	return s
}

// hasPriority reports whether any of records is a type that has a priority.
// The PRIORITY column is shown only then: on A, CNAME and TXT records it is
// always empty, and it took width a long answer needed (#233).
func hasPriority(records []*coreapigo.Record) bool {
	for _, r := range records {
		switch strings.ToUpper(derefStr(r.Type)) {
		case "MX", "SRV":
			return true
		}
	}
	return false
}

// recordRows renders records with a TYPE column, and a PRIORITY column when
// hasPriority says so.
func recordRows(out *output.Config, records []*coreapigo.Record) [][]string {
	withPriority := hasPriority(records)
	rows := make([][]string, 0, len(records))
	for _, r := range records {
		row := recordRow(out, r, withPriority)
		rows = append(rows, append([]string{row[0], out.TypeBadge(derefStr(r.Type))}, row[1:]...))
	}
	return rows
}

// recordRow is one record's ID, HOST, ANSWER and TTL cells, then PRIORITY if
// withPriority. A missing value is left empty; Table shows it as "—".
func recordRow(out *output.Config, r *coreapigo.Record, withPriority bool) []string {
	id := ""
	if r.ID != nil {
		id = strconv.Itoa(*r.ID)
	}
	row := []string{
		out.Dim(id),
		displayHost(r.Host),
		derefStr(r.Answer),
		out.Dim(strconv.FormatInt(r.TTL, 10)),
	}
	if withPriority {
		priority := ""
		if r.Priority != nil {
			priority = strconv.FormatInt(*r.Priority, 10)
		}
		row = append(row, priority)
	}
	return row
}

// dnsTypeOrder defines the preferred display order for grouped DNS output.
var dnsTypeOrder = []string{"A", "AAAA", "ANAME", "CNAME", "MX", "TXT", "NS", "SRV", "CAA"}

func renderGroupedRecords(out *output.Config, records []*coreapigo.Record) {
	// Bucket records by type, preserving insertion order per group.
	seen := map[string]bool{}
	var orderedTypes []string
	groups := map[string][]*coreapigo.Record{}
	for _, r := range records {
		t := strings.ToUpper(derefStr(r.Type))
		if !seen[t] {
			seen[t] = true
			orderedTypes = append(orderedTypes, t)
		}
		groups[t] = append(groups[t], r)
	}

	// Emit preferred types first, then any extras in encounter order.
	rendered := map[string]bool{}
	emit := func(t string) {
		if rendered[t] || len(groups[t]) == 0 {
			return
		}
		rendered[t] = true
		label := out.TypeBadge(t)
		fmt.Fprintf(out.Writer, "\n%s\n", label)
		headers := []string{"ID", "HOST", "ANSWER", "TTL"}
		if hasPriority(groups[t]) {
			headers = append(headers, "PRIORITY")
		}
		out.Table(headers, recordRowsNoType(out, groups[t]), output.Essential("ANSWER"))
	}
	for _, t := range dnsTypeOrder {
		emit(t)
	}
	for _, t := range orderedTypes {
		emit(t)
	}
}

// recordRowsNoType is like recordRows but omits the TYPE column (used in grouped view).
func recordRowsNoType(out *output.Config, records []*coreapigo.Record) [][]string {
	withPriority := hasPriority(records)
	rows := make([][]string, 0, len(records))
	for _, r := range records {
		rows = append(rows, recordRow(out, r, withPriority))
	}
	return rows
}

// runForm runs a huh form. It is replaceable in tests, which drive the form in
// huh's accessible (line-based) mode, since go test has no terminal.
var runForm = func(f *huh.Form) error { return f.Run() }

// StubFormRunner replaces how the guided `dns create` form is run, and returns
// a function that restores it. It is for tests outside this package, which
// drive `dns create` through the root command and have no terminal:
//
//	defer dns.StubFormRunner(func(f *huh.Form) error { ... })()
func StubFormRunner(run func(*huh.Form) error) func() {
	prev := runForm
	runForm = run
	return func() { runForm = prev }
}

// errFormAborted reports Ctrl-C in the guided form. runCreate prints "aborted"
// and exits 0, as a declined confirmation does.
var errFormAborted = errors.New("aborted")

// missingCreateFlags names the required `dns create` flags left empty.
func missingCreateFlags() []string {
	var missing []string
	if strings.TrimSpace(createType) == "" {
		missing = append(missing, "type")
	}
	if strings.TrimSpace(createAnswer) == "" {
		missing = append(missing, "answer")
	}
	return missing
}

// runFormStep runs one form, mapping Ctrl-C to errFormAborted. Every step
// goes through it: the priority step once ignored Ctrl-C and created the
// record without a priority (#230).
func runFormStep(f *huh.Form) error {
	if err := runForm(f); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return errFormAborted
		}
		return err
	}
	return nil
}

func dnsCreateForm(cmd *cobra.Command) error {
	typeOptions := []huh.Option[string]{
		huh.NewOption("A — IPv4 address", "A"),
		huh.NewOption("AAAA — IPv6 address", "AAAA"),
		huh.NewOption("ANAME — alias at apex", "ANAME"),
		huh.NewOption("CNAME — canonical name", "CNAME"),
		huh.NewOption("MX — mail exchange", "MX"),
		huh.NewOption("NS — name server", "NS"),
		huh.NewOption("SRV — service locator", "SRV"),
		huh.NewOption("TXT — text record", "TXT"),
	}

	ttlStr := strconv.FormatInt(createTTL, 10)
	priorityStr := ""
	if cmd.Flags().Changed("priority") {
		priorityStr = strconv.FormatInt(createPriority, 10)
	}

	// The validators are the ones runCreate applies after the form, IDN
	// conversion included, so a value runCreate would reject is asked for
	// again here rather than failing after everything was typed.
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Record type").
				Options(typeOptions...).
				Value(&createType),
			huh.NewInput().
				Title("Host (@ for apex)").
				Value(&createHost).
				Validate(func(s string) error {
					_, err := asciiHost(s)
					return err
				}),
			huh.NewInput().
				Title("Answer / value").
				Value(&createAnswer).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("answer is required")
					}
					if createType != "" {
						_, err := asciiAnswer(createType, createHost, s)
						return err
					}
					return nil
				}),
			huh.NewInput().
				Title("TTL (seconds, min 300)").
				Value(&ttlStr).
				Validate(func(s string) error {
					n, err := strconv.ParseInt(s, 10, 64)
					if err != nil {
						return fmt.Errorf("TTL must be a number")
					}
					return cmdutil.ValidTTL(n)
				}),
		),
	)

	if err := runFormStep(form); err != nil {
		return err
	}

	// Parse and validate TTL.
	if n, err := strconv.ParseInt(ttlStr, 10, 64); err == nil {
		if err := cmdutil.ValidTTL(n); err != nil {
			return err
		}
		createTTL = n
	}

	// Show priority input only for record types that use it.
	upper := strings.ToUpper(createType)
	if upper == "MX" || upper == "SRV" {
		priorityForm := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Priority (0-65535)").
					Value(&priorityStr).
					Validate(func(s string) error {
						n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
						if err != nil {
							return fmt.Errorf("priority must be a whole number")
						}
						return cmdutil.ValidPriority(n)
					}),
			),
		)
		if err := runFormStep(priorityForm); err != nil {
			return err
		}
	}

	markFormFlags(cmd, priorityStr)
	return nil
}

// markFormFlags marks the values dnsCreateForm collected as changed flags, so
// runCreate uses them. runCreate attaches a priority only when
// Changed("priority"), so a priority stored without marking it was dropped.
func markFormFlags(cmd *cobra.Command, priorityStr string) {
	_ = cmd.Flags().Set("type", createType)
	_ = cmd.Flags().Set("answer", createAnswer)
	priorityStr = strings.TrimSpace(priorityStr)
	if _, err := strconv.ParseInt(priorityStr, 10, 64); err == nil {
		_ = cmd.Flags().Set("priority", priorityStr)
	}
}

func parseID(s string) (int, error) {
	n, ok := cmdutil.PositiveID(s)
	if !ok {
		return 0, cmdutil.NewUsageError(fmt.Errorf("invalid record ID %q: must be a positive whole number", s))
	}
	// The SDK reports and accepts record IDs as int; PositiveID still bounds
	// at 32 bits so an ID that could not have come from this API is rejected
	// here rather than at the server.
	return int(n), nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

func derefInt64(n *int64) int64 {
	if n == nil {
		return 0
	}
	return *n
}

// asciiHost validates a host and returns the form to send, with any Unicode
// labels in punycode (#187): the server got them as typed, and --dry-run
// previewed them that way. The typed value is checked first, so its errors read
// as they always have; the converted one again, since DNS length limits apply
// to what is sent.
func asciiHost(host string) (string, error) {
	if err := cmdutil.ValidDNSHost(host); err != nil {
		return "", err
	}
	a, err := cmdutil.ASCIIHostname(host, "--host")
	if err != nil {
		return "", err
	}
	if a != host {
		if err := cmdutil.ValidDNSHost(a); err != nil {
			return "", err
		}
	}
	return a, nil
}

// asciiAnswer is asciiHost for a record answer. Only the hostname a CNAME,
// ANAME, MX, NS or SRV record points at is converted; other answers (TXT, A,
// AAAA) are not names and are returned as typed.
func asciiAnswer(rtype, host, answer string) (string, error) {
	if err := cmdutil.ValidDNSAnswer(rtype, host, answer); err != nil {
		return "", err
	}
	var a string
	var err error
	switch rtype = strings.ToUpper(rtype); rtype {
	case "CNAME", "ANAME", "MX", "NS":
		a, err = cmdutil.ASCIIHostname(answer, rtype+" record target")
	case "SRV":
		// "weight port target": only the last field is a name.
		i := strings.LastIndexByte(answer, ' ')
		var target string
		target, err = cmdutil.ASCIIHostname(answer[i+1:], "SRV record target")
		a = answer[:i+1] + target
	default:
		return answer, nil
	}
	if err != nil {
		return "", err
	}
	if a != answer {
		if err := cmdutil.ValidDNSAnswer(rtype, host, a); err != nil {
			return "", err
		}
	}
	return a, nil
}

// qualify appends the trailing dot that makes a hostname absolute in a zone
// file. The API strips it on storage, so a CNAME to example.net comes back as
// "example.net" — which a zone parser reads as example.net.<origin>. A name
// that already ends in "." (including the root, ".") is left alone.
func qualify(name string) string {
	if name == "" || strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

// qualifyTarget qualifies the hostname in MX or SRV rdata, which is its last
// field: the answer is "target" for MX and "weight port target" for SRV (the
// priority is carried separately). The numeric fields are left untouched.
func qualifyTarget(rdata string) string {
	i := strings.LastIndexByte(rdata, ' ')
	return rdata[:i+1] + qualify(rdata[i+1:])
}

// maxCharString is the RFC 1035 limit on one character-string, in bytes.
const maxCharString = 255

// quoteTXT wraps TXT rdata in quoted character-strings, escaping embedded
// backslashes and quotes. A value over 255 bytes (a 2048-bit DKIM key, say) is
// split into several strings, which is how a zone file spells one long TXT
// value. The split is on bytes of the unescaped value, so it can never fall
// inside an escape sequence, and it backs off to a UTF-8 boundary so a
// character is not cut in two.
//
// A value that is already a well-formed run of quoted character-strings, each
// within the limit, is taken as zone syntax and rewritten in the same form.
// Anything else that merely starts and ends with a quote — `"a"b"`, or one
// quoted string over 255 bytes — used to be passed through as-is, and the zone
// did not load (#187); it is now content like any other value.
//
// Control characters are written as RFC 1035 \DDD decimal escapes: a raw
// newline inside a quoted string leaves the quotes unbalanced, and the whole
// zone fails to load.
func quoteTXT(s string) string {
	if parts, ok := parseQuotedTXT(s); ok {
		for i, p := range parts {
			parts[i] = `"` + escapeTXT(p) + `"`
		}
		return strings.Join(parts, " ")
	}
	var parts []string
	for {
		n := len(s)
		if n > maxCharString {
			n = maxCharString
			for n > 0 && !utf8.RuneStart(s[n]) {
				n--
			}
			if n == 0 { // not UTF-8 at all; split on the byte limit
				n = maxCharString
			}
		}
		parts = append(parts, `"`+escapeTXT(s[:n])+`"`)
		s = s[n:]
		if s == "" {
			return strings.Join(parts, " ")
		}
	}
}

// escapeTXT escapes one character-string's bytes for use between quotes.
func escapeTXT(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' || c == '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case isControl(c):
			fmt.Fprintf(&b, `\%03d`, c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// parseQuotedTXT reads s as zone-file TXT rdata: one or more quoted
// character-strings separated by spaces or tabs. It returns the unescaped
// bytes of each, and ok == false unless every string is closed, every escape
// is complete (\X, or \DDD with DDD at most 255), and every string fits in
// 255 bytes. A raw control character inside the quotes is taken as itself.
func parseQuotedTXT(s string) (parts []string, ok bool) {
	i := 0
	for {
		if i >= len(s) || s[i] != '"' {
			return nil, false
		}
		i++
		var b strings.Builder
		for {
			if i >= len(s) {
				return nil, false // unterminated
			}
			c := s[i]
			if c == '"' {
				i++
				break
			}
			if c != '\\' {
				b.WriteByte(c)
				i++
				continue
			}
			if i+1 >= len(s) {
				return nil, false
			}
			if !isDigit(s[i+1]) {
				b.WriteByte(s[i+1])
				i += 2
				continue
			}
			if i+4 > len(s) || !isDigit(s[i+2]) || !isDigit(s[i+3]) {
				return nil, false
			}
			n, err := strconv.ParseUint(s[i+1:i+4], 10, 8)
			if err != nil {
				return nil, false
			}
			b.WriteByte(byte(n))
			i += 4
		}
		if b.Len() > maxCharString {
			return nil, false
		}
		parts = append(parts, b.String())
		if i == len(s) {
			return parts, true
		}
		if s[i] != ' ' && s[i] != '\t' {
			return nil, false
		}
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isControl reports whether c is an ASCII control character.
func isControl(c byte) bool { return c < 0x20 || c == 0x7f }

// readImportData reads the import payload from a path, or from stdin when the
// path is "-". The help examples advertise `dns export old.com | dns import
// new.com --file -`, so the pipe form has to work. Mirrors cmd/apicmd.
func readImportData(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	// G304: reading a caller-named file is this function's entire purpose —
	// --file is the documented way to pass an import payload.
	return os.ReadFile(path) //nolint:gosec
}

// decodeImportData returns the import payload as UTF-8 with no byte-order
// mark. Windows PowerShell 5.1's `>` writes UTF-16LE with a BOM, so the
// documented `dns export X > records.json` produced a file json.Unmarshal
// could not read; some editors add a UTF-8 BOM. A UTF-8 or UTF-16 BOM picks
// the encoding and is dropped. Without one the bytes pass through unchanged.
func decodeImportData(data []byte) ([]byte, error) {
	decoded, _, err := transform.Bytes(unicode.BOMOverride(encoding.Nop.NewDecoder()), data)
	return decoded, err
}

// displayHost renders a record's host for a table. The API returns the apex as
// an empty string, which printed as an empty cell — indistinguishable from a
// column that failed to render. "@" is the spelling --host takes for the apex,
// so input and output now agree. JSON and YAML keep the API's value.
func displayHost(h *string) string {
	if h == nil || *h == "" {
		return "@"
	}
	return *h
}
