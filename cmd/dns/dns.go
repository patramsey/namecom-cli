// Package dns implements the `namecom dns` command group.
package dns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
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
	listAll bool
	// listPage and listLimit are --page and --limit.
	listPage, listLimit int
	listType            string
	listHost            string

	createType     string
	createHost     string
	createAnswer   string
	createTTL      int64
	createPriority int64
	// createIfNotExists is --if-not-exists.
	createIfNotExists bool

	updateType     string
	updateHost     string
	updateAnswer   string
	updateTTL      int64
	updatePriority int64

	exportZone bool
	importFile string
	// importSkipExisting is --skip-existing.
	importSkipExisting bool

	// deleteIfExists is --if-exists.
	deleteIfExists bool
)

var listCmd = &cobra.Command{
	Use:     "list <domain>",
	Aliases: []string{"ls"},
	Short:   "List DNS records for a domain",
	Long: `List DNS records for a domain, one page at a time: the API's page holds 500
records unless --limit says otherwise, so one page is usually the whole zone.

--type and --host filter the records on the page fetched; the API cannot
filter them. They do not change how many pages are fetched. If there are
more, the footer says so; --all filters the whole zone.`,
	Example: `  namecom dns list example.com
  namecom dns list example.com --type A
  namecom dns list example.com --type MX
  namecom dns list example.com --host www`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runList,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var createCmd = &cobra.Command{
	Use:     "create <domain>",
	Aliases: []string{"add"},
	Short:   "Create a DNS record",
	Long: `Create a DNS record on a domain whose DNS name.com hosts. In a terminal, a
form asks for --type and --answer when they are not passed.

CAA records show in 'dns list' but cannot be created through the API.`,
	Example: `  namecom dns create example.com --type A --answer 1.2.3.4
  namecom dns create example.com --type CNAME --host www --answer example.com.
  namecom dns create example.com --type MX --answer mail.example.com --priority 10
  namecom dns create example.com --type TXT --host @ --answer "v=spf1 include:_spf.example.com ~all"
  namecom dns create example.com --type A --host www --answer 1.2.3.4 --if-not-exists   # safe to re-run`,
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
		return cmdutil.CompleteRecordIDs(cmd, args[0])
	},
}

var deleteCmd = &cobra.Command{
	Use:     "delete <domain> <id> [<id>...]",
	Aliases: []string{"rm"},
	Short:   "Delete DNS records",
	Long: `Delete DNS records by ID, which 'dns list' shows. Several IDs are
confirmed once and deleted in order; the first failure stops the rest.`,
	Example: `  namecom dns delete example.com 12345
  namecom dns delete example.com 12345 12346

  # In a script, skip the confirmation; this deletes every TXT record:
  namecom dns delete example.com $(namecom dns list example.com --type TXT -q) --yes

  # Skip records already gone, so a retry does not fail:
  namecom dns delete example.com 12345 --yes --if-exists`,
	Args: cmdutil.MinimumNArgs(2),
	RunE: runDelete,
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return cmdutil.CompleteDomains(cmd, args, toComplete)
		}
		return cmdutil.CompleteRecordIDs(cmd, args[0])
	},
}

var exportCmd = &cobra.Command{
	Use:   "export <domain>",
	Short: "Export DNS records as JSON (default) or a zone-file snapshot",
	Long: `Write a domain's records as a file 'dns import' and 'dns sync' read: JSON
(the default), or a zone file with --zone. -o yaml writes YAML, which they do
not read.

The output is a file format, so -o table, -o tsv and -q are usage errors;
'dns list' prints records as a table, as TSV, or one ID per line.`,
	Example: `  namecom dns export example.com > records.json          # save for later import
  namecom dns export example.com --zone > example.com.zone
  namecom dns export old.com | namecom dns import new.com --file -   # clone records to another domain`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runExport,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var importCmd = &cobra.Command{
	Use:   "import <domain>",
	Short: "Import DNS records from a JSON export or a zone file",
	Long: `Create every record in a file: the JSON 'dns export' writes, or a BIND zone
file such as 'dns export --zone' writes. Import only adds records; to make the
zone match a file, deleting and updating as well, use 'dns sync'.

With --skip-existing, records already in the zone (same host, type and
answer) are skipped rather than failing the import, so an import that stopped
partway can be run again.`,
	Example: `  namecom dns import example.com --file records.json
  namecom dns import example.com --file example.com.zone
  namecom dns export old.com | namecom dns import new.com --file -   # pipe directly between domains
  namecom dns import example.com --file records.json --dry-run       # preview without applying
  namecom dns import example.com --file records.json --skip-existing # re-run after a partial import`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runImport,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	cmdutil.AddPageFlags(listCmd, &listAll, &listPage, &listLimit, "records")
	listCmd.Flags().StringVar(&listType, "type", "", "show only records of this type (A, AAAA, CNAME, MX, TXT, NS, SRV, ANAME, CAA) on the page fetched")
	listCmd.Flags().StringVar(&listHost, "host", "", "show only records at this host (@ for the apex; www or www.example.com) on the page fetched")

	createCmd.Flags().StringVar(&createType, "type", "", "record type: A, AAAA, ANAME, CNAME, MX, NS, SRV, TXT; CAA is read-only through the API (required; prompted in a terminal)")
	createCmd.Flags().StringVar(&createHost, "host", "@", "host: www or www.example.com (@ or the domain for the apex)")
	createCmd.Flags().StringVar(&createAnswer, "answer", "", "record value (required; prompted in a terminal)")
	createCmd.Flags().Int64Var(&createTTL, "ttl", defaultTTL, "TTL in seconds (minimum 300)")
	createCmd.Flags().Int64Var(&createPriority, "priority", 0, "priority for MX/SRV records, lower preferred (required for them; not taken by other types)")
	createCmd.Flags().BoolVar(&createIfNotExists, "if-not-exists", false, "succeed without creating when a record with this host, type and answer exists, printing its ID")
	// --type and --answer are required, but not marked so: cobra would reject
	// the command before runCreate could offer the guided form (#230).
	// runCreate makes them a usage error itself when there is no terminal.

	updateCmd.Flags().StringVar(&updateType, "type", "", "new record type")
	updateCmd.Flags().StringVar(&updateHost, "host", "", "new host: www or www.example.com (@ or the domain for the apex)")
	updateCmd.Flags().StringVar(&updateAnswer, "answer", "", "new answer/value")
	updateCmd.Flags().Int64Var(&updateTTL, "ttl", 0, "new TTL in seconds")
	updateCmd.Flags().Int64Var(&updatePriority, "priority", 0, "new priority")

	exportCmd.Flags().BoolVar(&exportZone, "zone", false, "output RFC 1035 zone-file format instead of JSON")

	cmdutil.CompleteFlagValues(listCmd, "type", cmdutil.DNSRecordTypes)
	cmdutil.CompleteFlagValues(createCmd, "type", cmdutil.DNSCreateTypes)
	cmdutil.CompleteFlagValues(updateCmd, "type", cmdutil.DNSCreateTypes)

	importCmd.Flags().StringVar(&importFile, "file", "", "'dns export' JSON or zone file to import, - for stdin (required)")
	importCmd.Flags().BoolVar(&importSkipExisting, "skip-existing", false, "skip records already in the zone instead of failing on them")
	_ = importCmd.MarkFlagRequired("file")

	deleteCmd.Flags().BoolVar(&deleteIfExists, "if-exists", false, "skip IDs whose record does not exist instead of failing")

	cmdutil.GroupCmd(Cmd)
	cmdutil.MarkWrite(createCmd, updateCmd, deleteCmd, importCmd, syncCmd)
	Cmd.AddCommand(listCmd, createCmd, updateCmd, deleteCmd, exportCmd, importCmd, syncCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	if listType != "" {
		if err := cmdutil.ValidDNSType(listType); err != nil {
			return err
		}
	}
	var wantHost string
	if listHost != "" {
		if wantHost, err = filterHost(listHost, domain); err != nil {
			return err
		}
	}
	filtered := listType != "" || listHost != ""

	// --type and --host filter client-side, the records on the pages
	// fetched. They used to page fully, ignoring --page and --limit, so
	// `--type A --limit 1` fetched the zone one record per request (#281).
	// They now page as the unfiltered list does — see cmdutil.AutoPage — and
	// a page with no match still says when there are more.
	paging, err := cmdutil.ListPaging(cmd, listAll, listPage, listLimit)
	if err != nil {
		return err
	}

	stop := out.Spin("Fetching DNS records…")
	records, hasMore, nextPage, pos, err := fetchRecordsAt(cmd, domain, listPage, paging.PerPage, paging.All)
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return cmdutil.DomainNotFound(err, domain)
		}
		return err
	}

	// Apply --type filter.
	if listType != "" {
		upper := strings.ToUpper(listType)
		matched := records[:0]
		for _, r := range records {
			if r.Type != nil && strings.ToUpper(*r.Type) == upper {
				matched = append(matched, r)
			}
		}
		records = matched
	}
	if listHost != "" {
		matched := records[:0]
		for _, r := range records {
			if normHost(derefStr(r.Host)) == wantHost {
				matched = append(matched, r)
			}
		}
		records = matched
	}

	if out.QuietMode {
		ids := make([]string, 0, len(records))
		for _, r := range records {
			if r.ID != nil {
				ids = append(ids, strconv.Itoa(*r.ID))
			}
		}
		out.PrintQuiet(ids)
		if hasMore {
			next := listPage + 1
			if nextPage != nil {
				next = *nextPage
			}
			cmdutil.QuietMorePages(out, next)
		}
		return nil
	}

	// The zone's size, from the response: the JSON had nextPage but no
	// total, which the other lists the API counts have. --type and --host
	// filter the fetched records, so the zone's size is not the list's, and
	// is left out then.
	next := 0
	if hasMore {
		next = listPage + 1
		if nextPage != nil {
			next = *nextPage
		}
	}
	foot := cmdutil.Page("record", listPage, len(records), paging.All, pos.from, pos.to, pos.total, next)
	if filtered {
		foot = cmdutil.Page("record", listPage, len(records), true, 0, 0, 0, next)
	}
	switch out.Format {
	case output.FormatJSON:
		out.ListFooter(foot) // for a table --fields prints
		return out.JSONList(records, cmdutil.Int32Page(nextPage), cmdutil.Int32Count(foot.Total))
	case output.FormatYAML:
		return out.YAMLList(records, cmdutil.Int32Page(nextPage), cmdutil.Int32Count(foot.Total))
	default:
		headers := []string{"ID", "TYPE", "HOST", "ANSWER", "TTL"}
		if priorityColumn(out, records) {
			headers = append(headers, "PRIORITY")
		}
		if len(records) == 0 && hasMore {
			// The filter matched nothing on this page, not in the zone.
			if out.Format == output.FormatTSV {
				out.EmptyTable(headers, "", "")
			}
			next := listPage + 1
			if nextPage != nil {
				next = *nextPage
			}
			out.Footer(fmt.Sprintf("No matching records on page %d", listPage), cmdutil.MorePages(next))
			return nil
		}
		if len(records) == 0 {
			// Distinguish "this zone is empty" from "nothing matched the filter".
			// The generic message claimed the zone had no records at all and
			// suggested creating "the first record" with a type the user hadn't
			// asked about — three wrong statements for a filtered search.
			if filtered {
				noun := "DNS record"
				if listType != "" {
					noun = strings.ToUpper(listType) + " record"
				}
				hint := fmt.Sprintf("Run 'namecom dns list %s' to see every record", domain)
				if listHost == "" || out.Format == output.FormatTSV {
					cmdutil.EmptyPage(out, listPage, headers, noun, hint)
					return nil
				}
				// Not out.Empty, which pluralises the last word: "No DNS
				// record at wwws found." (#285).
				if out.Format == output.FormatTable && !out.QuietMode {
					fmt.Fprintln(out.EWriter, out.Dim("No "+output.PluralNoun(2, noun)+" at "+displayHost(&listHost)+" found."))
					out.Hint(hint)
				}
				return nil
			}
			cmdutil.EmptyPage(out, listPage, headers, "DNS record", fmt.Sprintf("Run 'namecom dns create %s --type A --answer 1.2.3.4' to add the first record", domain))
			return nil
		}
		// Filtered, or TSV, where a section heading would be read as a row:
		// a single flat table.
		if filtered || out.Format == output.FormatTSV {
			out.Table(headers, recordRows(out, records), output.Essential("ANSWER"))
		} else {
			// Unfiltered: group by type with section headers.
			renderGroupedRecords(out, records)
		}
		out.ListFooter(foot)
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
		if err := dnsCreateForm(cmd, domain); err != nil {
			return err
		}
	}

	if createType == "" {
		return cmdutil.NewUsageError(fmt.Errorf("--type is required (A, AAAA, ANAME, CNAME, MX, NS, SRV, TXT)"))
	}
	if err := cmdutil.ValidDNSCreateType(createType); err != nil {
		return err
	}
	// Checked without regard to case, so sent in the case the API takes:
	// "a" passed every check and the dry run, then the API refused it (#323).
	createType = strings.ToUpper(createType)
	host, err := zoneHost(createHost, domain)
	if err != nil {
		return err
	}
	answer, err := asciiAnswer(createType, host, createAnswer)
	if err != nil {
		return err
	}
	if err := checkPriority(createType, cmd.Flags().Changed("priority"), "--priority"); err != nil {
		return err
	}
	for _, w := range cmdutil.DNSAnswerWarnings(createType, answer) {
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

	if createIfNotExists {
		// A read, so it runs under --dry-run too: the preview then says
		// whether anything would be sent.
		existing, err := findRecord(cmd, domain, createType, host, answer)
		if err != nil {
			return err
		}
		if existing != nil {
			return reportExisting(cmd, existing, body)
		}
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
	if err != nil && !createIfNotExists {
		err = hintExisting(err, "pass --if-not-exists to treat an existing record as success")
	}
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
	case output.FormatJSON, output.FormatYAML:
		return printCreated(out, record, true)
	default:
		// The value the API stored, which can differ from the one sent:
		// `"a" "b"` is stored as `"a""b"`.
		if stored := derefStr(record.Answer); stored != "" {
			answer = stored
		}
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
	// Nothing to change is a usage error, as it is for `domain update`, and
	// is caught before the record is fetched.
	changed := false
	for _, f := range []string{"type", "host", "answer", "ttl", "priority"} {
		changed = changed || cmd.Flags().Changed(f)
	}
	if !changed {
		return cmdutil.NewUsageError(errors.New("nothing to update — pass at least one of --type, --host, --answer, --ttl, --priority"))
	}
	if cmd.Flags().Changed("priority") {
		if err := cmdutil.ValidPriority(updatePriority); err != nil {
			return err
		}
	}
	// The new host is checked before the record is fetched: a bad one needs
	// no request to refuse.
	var newHost string
	if cmd.Flags().Changed("host") {
		if newHost, err = zoneHost(updateHost, domain); err != nil {
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
			return cmdutil.NotFound(err, fmt.Sprintf("record %d not found on %s", id, domain),
				fmt.Sprintf("run 'namecom dns list %s' to see its record IDs", domain))
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
	// Merge --priority before anything reads body.Priority, so the check
	// below reasons about the value we will actually send rather than an
	// unset flag variable.
	if cmd.Flags().Changed("priority") {
		body.Priority = &updatePriority
	}

	if cmd.Flags().Changed("type") {
		// The update endpoint rejects CAA just as create does.
		if err := cmdutil.ValidDNSCreateType(updateType); err != nil {
			return err
		}
		body.Type = coreapigo.DNSUpdateRecordBodyType(strings.ToUpper(updateType))
	}
	// As create: --priority on a type without one, or an MX or SRV record
	// left with none (a --type change), is refused rather than sent.
	set := cmd.Flags().Changed("priority")
	if typeHasPriority(strings.ToUpper(string(body.Type))) {
		set = body.Priority != nil
	}
	if err := checkPriority(string(body.Type), set, "--priority"); err != nil {
		return err
	}
	// An MX or SRV record made a type without a priority: the fetched one
	// is not sent, as the API would drop it and the preview would show a
	// body that is not what gets stored (#323).
	if cmd.Flags().Changed("type") && !typeHasPriority(string(body.Type)) {
		body.Priority = nil
	}
	if cmd.Flags().Changed("host") {
		body.Host = &newHost
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
		for _, w := range cmdutil.DNSAnswerWarnings(rtype, answer) {
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
	name := fmt.Sprintf("%s %s (id %d)", string(body.Type), recordName(derefStr(body.Host), domain), id)

	// The flags ask for what the record already is: nothing is sent, under
	// --dry-run too. The PUT used to be sent anyway and reported as "no
	// values changed", with nothing in JSON to tell it from a change (#285).
	changes := recordChanges(current, body)
	if len(changes) == 0 {
		return printUpdated(out, current, false, name+" already has these values: nothing to change")
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
	if err != nil || !sent {
		return err
	}
	return printUpdated(out, updated, true, "Updated "+name+": "+strings.Join(changes, ", "))
}

// printUpdated prints the record `dns update` changed, or found already as
// asked: in JSON and YAML the record with "changed", so a script can tell a
// no-op from a change, and otherwise msg. --quiet prints nothing.
func printUpdated(out *output.Config, rec *coreapigo.Record, changed bool, msg string) error {
	if out.Quiet() {
		return nil
	}
	switch out.Format {
	case output.FormatJSON, output.FormatYAML:
		doc, err := output.WithChanged(rec, changed)
		if err != nil {
			return err
		}
		if out.Format == output.FormatYAML {
			return out.YAML(doc)
		}
		return out.JSON(doc)
	}
	out.Success(msg)
	return nil
}

func runDelete(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	// Several IDs are deleted in the order given, after one confirmation
	// (#244). A repeated ID is deleted once: the second DELETE would 404.
	var ids []int
	for _, a := range args[1:] {
		id, err := parseID(a)
		if err != nil {
			return err
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	// Fetch each record so the prompt can show it. "Delete DNS record 12345
	// from D?" named only an ID, and was asked even for a record that did not
	// exist (#235). A missing one now fails here, before any prompt — unless
	// --if-exists, which skips it and deletes the rest.
	var writes []cmdutil.Write[cmdutil.NoBody]
	var summaries []string
	var present, absent []int
	// Read together; the first failure stops the rest and is reported. A
	// missing record under --if-exists is not a failure: it comes back nil.
	stop := out.Spin("Fetching record…")
	currents, err := cmdutil.FetchEach(cmd.Context(), ids, func(ctx context.Context, id int) (*coreapigo.Record, error) {
		r, err := client.SDK().DNS.GetRecord(ctx, &coreapigo.GetRecordRequest{DomainName: domain, ID: id})
		if err = api.FromSDKError(err); cmdutil.IsNotFound(err) {
			if deleteIfExists {
				return nil, nil
			}
			return nil, cmdutil.NotFound(err, fmt.Sprintf("record %d not found on %s", id, domain),
				fmt.Sprintf("run 'namecom dns list %s' to see its record IDs", domain))
		}
		return r, err
	})
	stop()
	if err != nil {
		return err
	}
	for i, id := range ids {
		current := currents[i]
		if current == nil {
			absent = append(absent, id)
			continue
		}
		present = append(present, id)
		summaries = append(summaries, recordSummary(current))
		writes = append(writes, cmdutil.Write[cmdutil.NoBody]{
			Method: "DELETE",
			Path:   fmt.Sprintf("/core/v1/domains/%s/records/%d", domain, id),
			Spin:   "Deleting record…",
		})
	}
	if len(present) == 0 {
		return deleteAbsent(cmd, domain, absent)
	}
	// A record was found, so the domain exists and the rest really are gone.
	// A note, not a Success line: under --dry-run -o json stdout is the
	// preview alone.
	for _, id := range absent {
		out.Note(fmt.Sprintf("Record %d is not on %s: skipped", id, domain))
	}
	ids = present

	prompt := fmt.Sprintf("Delete %s from %s?", summaries[0], domain)
	if len(ids) > 1 {
		prompt = fmt.Sprintf("Delete these %d records from %s?\n  %s", len(ids), domain, strings.Join(summaries, "\n  "))
	}
	// A DELETE has no body to carry its ID, so send counts through ids:
	// RunWrites sends the writes one at a time, in order.
	next := 0
	done, err := cmdutil.RunWrites(cmd, prompt, writes, func(ctx context.Context, _ cmdutil.NoBody) error {
		id := ids[next]
		next++
		return api.FromSDKError(client.SDK().DNS.DeleteRecord(ctx, &coreapigo.DeleteRecordRequest{
			DomainName: domain,
			ID:         id,
		}))
	})
	if cmdutil.IsDryRun(cmd) {
		return err
	}
	// One document in JSON and YAML (#240), with each record's outcome; an
	// absent one skipped under --if-exists is "changed": false there.
	res := out.Results()
	for _, id := range ids[:done] {
		res.Add(output.ResultItem{Domain: domain, ID: id, Changed: true, Message: fmt.Sprintf("Deleted record %d from %s", id, domain)})
	}
	if out.Format == output.FormatJSON || out.Format == output.FormatYAML {
		for _, id := range absent {
			res.Add(output.ResultItem{Domain: domain, ID: id, Message: fmt.Sprintf("Record %d is not on %s: nothing to delete", id, domain)})
		}
	}
	summary := fmt.Sprintf("Deleted %s from %s", output.Plural(done, "record"), domain)
	if len(absent) > 0 {
		summary += fmt.Sprintf("; %d not there", len(absent))
	}
	res.Print(summary)
	if err != nil && done > 0 {
		return fmt.Errorf("deleting record %d: %w — stopped after deleting %d of %d records", ids[done], err, done, len(ids))
	}
	return err
}

func runExport(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}
	if err := exportFormat(cmd, domain); err != nil {
		return err
	}

	records, _, _, err := fetchRecords(cmd, domain, 1, nil, true)
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return cmdutil.DomainNotFound(err, domain)
		}
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

	// The {"data": [...]} envelope every list uses (#240); it was a bare
	// array. `dns import` and `dns sync` read both. The envelope also turns an
	// empty zone's nil into `[]` rather than `null`.
	switch out.Format {
	case output.FormatYAML:
		return out.YAMLList(records, nil, 0)
	default:
		if err := out.JSONList(records, nil, 0); err != nil {
			return err
		}
	}
	out.Hint(fmt.Sprintf("Use --zone for RFC 1035 zone-file format, or pipe to a file: namecom dns export %s > records.json", domain))
	return nil
}

// exportFormat refuses the output flags an export cannot honour: -o table,
// -o tsv and -q. They were ignored, so `dns export -o tsv` printed JSON and
// `-q` broke its "one ID per line" promise (#289). A terminal's default
// table, with no -o, still exports JSON: the export is a file format, and
// that is what it is for.
func exportFormat(cmd *cobra.Command, domain string) error {
	if cmdutil.Out(cmd).QuietMode {
		return cmdutil.NewUsageError(fmt.Errorf("dns export writes a file, not IDs, so it does not take --quiet; use 'namecom dns list %s -q' for record IDs", domain))
	}
	f := cmd.Flags().Lookup("output")
	if f == nil || !f.Changed {
		return nil
	}
	switch format, _ := output.ParseFormat(f.Value.String()); format {
	case output.FormatTable, output.FormatTSV:
		return cmdutil.NewUsageError(fmt.Errorf("dns export writes JSON, YAML (-o yaml) or a zone file (--zone), so it does not take -o %s; use 'namecom dns list %s -o %s'", format, domain, format))
	}
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

	records, _, err := readRecordsFile(importFile, domain)
	if err != nil {
		return err
	}
	// Validate every record before writing any of them. Import is not
	// transactional, so a file whose 4th record was malformed wrote 3
	// records and then failed on a server-side 422.
	if err := prepareRecords(records, false); err != nil {
		return err
	}

	// --skip-existing compares with the live zone, the dry run included,
	// so the preview lists only what would really be sent. A record listed
	// twice in the file is skipped the second time, as the API would
	// refuse it.
	skipped := 0
	if importSkipExisting {
		stop := out.Spin("Fetching DNS records…")
		live, _, _, err := fetchRecords(cmd, domain, 1, nil, true)
		stop()
		if err != nil {
			if cmdutil.IsNotFound(err) {
				return cmdutil.DomainNotFound(err, domain)
			}
			return err
		}
		present := map[recordKey]bool{}
		for _, r := range live {
			if r != nil {
				present[liveKey(r)] = true
			}
		}
		kept := records[:0]
		for _, r := range records {
			k := keyOf(r.Type, r.Host, r.Answer)
			if present[k] {
				skipped++
				continue
			}
			present[k] = true
			kept = append(kept, r)
		}
		records = kept
	}
	// Every record to be created, checked before the first is sent: an MX
	// or SRV record without a priority would be refused by the API.
	for _, r := range records {
		if err := checkPriority(r.Type, r.Priority != nil, "a priority in the file"); err != nil {
			return fmt.Errorf("%s (%s %s): %w", r.Source, r.Type, r.Host, err)
		}
	}

	created := 0
	var previews []output.DryRunRequest
	for _, r := range records {
		ttl := r.TTL
		body := coreapigo.DNSCreateRecordBody{
			DomainName: domain,
			Type:       coreapigo.DNSCreateRecordBodyType(r.Type),
			Host:       r.Host,
			Answer:     r.Answer,
			TTL:        &ttl,
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
			if !importSkipExisting {
				err = hintExisting(err, "pass --skip-existing to skip records already in the zone")
			}
			// Report what already landed. Import is not transactional, so bailing
			// out with only the failure left the user unable to tell whether a
			// retry would duplicate the records written so far.
			if created > 0 {
				// Under --skip-existing, "re-run with --skip-existing" was
				// advice to do what had just been done.
				next := "re-run with --skip-existing to continue without duplicating them"
				if importSkipExisting {
					next = "fix the failing record and re-run the same command; --skip-existing skips those already created"
				}
				out.Warn(fmt.Sprintf("%d of %s were already created on %s before this failure — %s",
					created, output.Plural(len(records), "record"), domain, next))
			}
			return fmt.Errorf("creating %s %s (after %d of %d succeeded): %w",
				body.Type, body.Host, created, len(records), err)
		}
		created++
	}

	if skipped > 0 {
		out.Note(fmt.Sprintf("Skipped %s already in the zone", output.Plural(skipped, "record")))
	}
	if dryRun {
		// One document for the whole plan in JSON and YAML modes, so a script
		// parses every request at once rather than a stream of them.
		return out.DryRunAll(previews)
	}
	msg := fmt.Sprintf("Imported %s to %s", output.Plural(created, "record"), domain)
	if skipped > 0 {
		msg += fmt.Sprintf(" (%d already present, skipped)", skipped)
	}
	// "changed": false when every record was already there (#240).
	if created == 0 {
		out.Unchanged(msg)
		return nil
	}
	out.Success(msg)
	return nil
}

// fetchRecords fetches a domain's DNS records from page start, perPage at a
// time (nil for the API's default), and every later page when all is set.
// Fetching every page with no perPage asks for cmdutil.MaxPerPage, the
// fewest requests: the whole-zone reads of export, sync, import and
// --if-not-exists paged at the API's default of 500 (#294).
//
// A start past the last page yields no records. The API answers it with page
// 1 instead (#290), so a script paging `dns list --page N` until a page came
// back empty never stopped; further past, with a 400. cmdutil.PastLastPage
// and PageOutOfRange say when.
func fetchRecords(cmd *cobra.Command, domain string, start int, perPage *int, all bool) (records []*coreapigo.Record, hasMore bool, nextPage *int, err error) {
	records, hasMore, nextPage, _, err = fetchRecordsAt(cmd, domain, start, perPage, all)
	return records, hasMore, nextPage, err
}

// recordsPos is where the records fetchRecordsAt returned sit in the zone,
// as the responses put it: the first and last record's positions and the
// zone's size. Zero when a response did not say.
type recordsPos struct{ from, to, total int }

// fetchRecordsAt is fetchRecords that also returns the position, for the
// list's footer and its JSON total.
func fetchRecordsAt(cmd *cobra.Command, domain string, start int, perPage *int, all bool) (records []*coreapigo.Record, hasMore bool, nextPage *int, pos recordsPos, err error) {
	client := cmdutil.APIClient(cmd)
	ctx := cmd.Context()
	if all && perPage == nil {
		n := cmdutil.MaxPerPage
		perPage = &n
	}

	page := start
	var lastNextPage *int

	for {
		result, err2 := client.SDK().DNS.ListRecords(ctx, &coreapigo.ListRecordsRequest{
			DomainName: domain,
			Page:       &page,
			PerPage:    perPage,
		})
		if page == start && cmdutil.PageOutOfRange(page, err2) {
			return nil, false, nil, pos, nil
		}
		if err2 != nil {
			return nil, false, nil, pos, api.FromSDKError(err2)
		}
		if page == start && cmdutil.PastLastPage(start, perPage, len(result.Records), result.TotalCount, result.LastPage) {
			return nil, false, nil, pos, nil
		}
		records = append(records, cmdutil.NonNil(result.Records)...)
		lastNextPage = result.NextPage
		pos.to, pos.total = result.To, result.TotalCount
		if page == start {
			pos.from = result.From
		}

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
	return records, hasMore, nextPage, pos, nil
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
// change (#238). A host or answer spelled differently but meaning the same
// record — "WWW", a trailing dot the API strips — is not a change, so an
// update that only respells the record sends nothing (#285).
func recordChanges(current *coreapigo.Record, body coreapigo.DNSUpdateRecordBody) []string {
	var changes []string
	add := func(field, was, now string) {
		if was != now {
			changes = append(changes, fmt.Sprintf("%s %s → %s", field, orNone(was), orNone(now)))
		}
	}
	rtype := strings.ToUpper(string(body.Type))
	add("type", strings.ToUpper(derefStr(current.Type)), rtype)
	if normHost(derefStr(current.Host)) != normHost(derefStr(body.Host)) {
		add("host", displayHost(current.Host), displayHost(body.Host))
	}
	if normAnswer(rtype, derefStr(current.Answer)) != normAnswer(rtype, body.Answer) {
		add("answer", derefStr(current.Answer), body.Answer)
	}
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

// priorityColumn reports whether a table of records has a PRIORITY column:
// when one of them is an MX or SRV record, and always in TSV, whose columns
// do not depend on which records there are (#289).
func priorityColumn(out *output.Config, records []*coreapigo.Record) bool {
	return out.Format == output.FormatTSV || hasPriority(records)
}

// recordRows renders records with a TYPE column, and a PRIORITY column when
// priorityColumn says so.
func recordRows(out *output.Config, records []*coreapigo.Record) [][]string {
	withPriority := priorityColumn(out, records)
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

// errFormAborted reports Ctrl-C in the guided form. It is cmdutil.ErrAborted,
// so the command exits 1, as a declined confirmation does (#236).
var errFormAborted = cmdutil.ErrAborted

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

func dnsCreateForm(cmd *cobra.Command, domain string) error {
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
					_, err := zoneHost(s, domain)
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
						_, err := asciiAnswer(createType, relHost(createHost, domain), s)
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
	return parseQuotedStrings(s, false)
}

// parseQuotedStrings is parseQuotedTXT that, with adjacent set, also reads
// strings with nothing between them, `"a""b"`: the form the API stores a
// multi-string TXT value in.
func parseQuotedStrings(s string, adjacent bool) (parts []string, ok bool) {
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
		if adjacent && s[i] == '"' {
			continue
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
