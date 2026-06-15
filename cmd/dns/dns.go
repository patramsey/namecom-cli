// Package dns implements the `namecom dns` command group.
package dns

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/namedotcom/namecom-cli/cmd/cmdutil"
	"github.com/namedotcom/namecom-cli/internal/api"
	"github.com/namedotcom/namecom-cli/internal/api/gen"
	"github.com/namedotcom/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom dns` parent command.
var Cmd = &cobra.Command{
	Use:   "dns",
	Short: "Create and manage DNS records (A, CNAME, MX, TXT, and more)",
}

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
	importFile   string
	importDryRun bool
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
  namecom dns update example.com 12345 --host www --answer example.com.`,
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
  namecom dns list example.com -q | xargs -I{} namecom dns delete example.com {}`,
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
	Example: `  namecom dns export example.com                     # JSON, pipe to a file
  namecom dns export example.com --zone               # RFC 1035 zone-file format
  namecom dns export example.com > records.json       # save for later import
  namecom dns export example.com --zone > example.com.zone`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runExport,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

var importCmd = &cobra.Command{
	Use:               "import <domain>",
	Short:             "Import DNS records from a JSON export file",
	Args:              cmdutil.ExactArgs(1),
	RunE:              runImport,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	listCmd.Flags().BoolVar(&listAll, "all", false, "fetch all pages automatically")
	listCmd.Flags().StringVar(&listType, "type", "", "filter by record type (A, AAAA, CNAME, MX, TXT, NS, SRV, ANAME, CAA)")

	createCmd.Flags().StringVar(&createType, "type", "", "record type: A, AAAA, ANAME, CNAME, MX, NS, SRV, TXT (required)")
	createCmd.Flags().StringVar(&createHost, "host", "@", "hostname relative to the zone (@ for apex)")
	createCmd.Flags().StringVar(&createAnswer, "answer", "", "record value (required)")
	createCmd.Flags().Int64Var(&createTTL, "ttl", 300, "TTL in seconds (minimum 300)")
	createCmd.Flags().Int64Var(&createPriority, "priority", 0, "priority for MX/SRV records")
	_ = createCmd.MarkFlagRequired("type")
	_ = createCmd.MarkFlagRequired("answer")

	updateCmd.Flags().StringVar(&updateType, "type", "", "new record type")
	updateCmd.Flags().StringVar(&updateHost, "host", "", "new host")
	updateCmd.Flags().StringVar(&updateAnswer, "answer", "", "new answer/value")
	updateCmd.Flags().Int64Var(&updateTTL, "ttl", 0, "new TTL in seconds")
	updateCmd.Flags().Int64Var(&updatePriority, "priority", 0, "new priority")

	exportCmd.Flags().BoolVar(&exportZone, "zone", false, "output RFC 1035 zone-file format instead of JSON")

	importCmd.Flags().StringVar(&importFile, "file", "", "JSON file to import (required)")
	importCmd.Flags().BoolVar(&importDryRun, "dry-run", false, "show what would be created without calling the API")
	_ = importCmd.MarkFlagRequired("file")

	Cmd.AddCommand(listCmd, createCmd, updateCmd, deleteCmd, exportCmd, importCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	domain := args[0]

	stop := out.Spin("Fetching DNS records…")
	records, hasMore, err := fetchAllRecords(cmd, domain, listAll)
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return fmt.Errorf("domain %q not found — run 'namecom domain list' to see your domains", domain)
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
			if r.Id != nil {
				ids = append(ids, strconv.Itoa(int(*r.Id)))
			}
		}
		out.PrintQuiet(ids)
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(records)
	case output.FormatYAML:
		return out.YAML(records)
	default:
		if len(records) == 0 {
			out.Empty("DNS record", fmt.Sprintf("Run 'namecom dns create %s --type A --answer 1.2.3.4' to add the first record", domain))
			return nil
		}
		if listType != "" {
			// Filtered: single flat table.
			out.Table(
				[]string{"ID", "TYPE", "HOST", "ANSWER", "TTL", "PRIORITY"},
				recordRows(out, records),
			)
		} else {
			// Unfiltered: group by type with section headers.
			renderGroupedRecords(out, records)
		}
		out.Count(len(records), "record")
		if hasMore {
			out.Hint("More records exist — pass --all to fetch all pages")
		} else {
			out.Hint(fmt.Sprintf("Run 'namecom domain get %s' to view domain details", domain))
		}
	}
	return nil
}

func runCreate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain := args[0]

	// Guided form when interactive and required flags not supplied.
	if output.IsInteractive() && !cmd.Flags().Changed("type") && !cmd.Flags().Changed("answer") {
		if err := dnsCreateForm(cmd); err != nil {
			return err
		}
	}

	body := gen.CreateRecordJSONRequestBody{
		Type:   gen.DNSCreateRecordBodyType(createType),
		Host:   createHost,
		Answer: createAnswer,
		Ttl:    &createTTL,
	}
	if createPriority != 0 {
		body.Priority = &createPriority
	}

	if dryRun {
		out.DryRun("POST", fmt.Sprintf("/core/v1/domains/%s/records", domain), body)
		return nil
	}

	resp, err := client.Gen().CreateRecord(cmd.Context(), domain, body)
	if err != nil {
		return err
	}
	var record gen.Record
	if err := api.Decode(resp, &record); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(record)
	case output.FormatYAML:
		return out.YAML(record)
	default:
		out.Success(fmt.Sprintf("Created %s record (id %d)", createType, derefInt32(record.Id)))
		out.Hint(fmt.Sprintf("Run 'namecom dns list %s' to see all records", domain))
	}
	return nil
}

func runUpdate(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain := args[0]

	id, err := parseID(args[1])
	if err != nil {
		return err
	}

	// Read-modify-write: fetch existing record so unset flags don't blank fields.
	getResp, err := client.Gen().GetRecord(cmd.Context(), domain, id)
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return fmt.Errorf("record %d not found on %s — run 'namecom dns list %s' to see record IDs", id, domain, domain)
		}
		return err
	}
	var current gen.Record
	if err := api.Decode(getResp, &current); err != nil {
		return err
	}

	body := gen.UpdateRecordJSONRequestBody{
		Type:   gen.DNSUpdateRecordBodyType(derefStr(current.Type)),
		Answer: derefStr(current.Answer),
		Ttl:    &current.Ttl,
		Host:   current.Host,
	}
	if current.Priority != nil {
		body.Priority = current.Priority
	}

	if cmd.Flags().Changed("type") {
		body.Type = gen.DNSUpdateRecordBodyType(updateType)
	}
	if cmd.Flags().Changed("answer") {
		body.Answer = updateAnswer
	}
	if cmd.Flags().Changed("host") {
		body.Host = &updateHost
	}
	if cmd.Flags().Changed("ttl") {
		body.Ttl = &updateTTL
	}
	if cmd.Flags().Changed("priority") {
		body.Priority = &updatePriority
	}

	if dryRun {
		out.DryRun("PUT", fmt.Sprintf("/core/v1/domains/%s/records/%d", domain, id), body)
		return nil
	}

	resp, err := client.Gen().UpdateRecord(cmd.Context(), domain, id, body)
	if err != nil {
		return err
	}
	var updated gen.Record
	if err := api.Decode(resp, &updated); err != nil {
		return err
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(updated)
	case output.FormatYAML:
		return out.YAML(updated)
	default:
		out.Success(fmt.Sprintf("Updated record %d", id))
		out.Hint(fmt.Sprintf("Run 'namecom dns list %s' to see all records", domain))
	}
	return nil
}

func runDelete(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	yes := cmdutil.IsYes(cmd)
	dryRun := cmdutil.IsDryRun(cmd)
	domain := args[0]

	id, err := parseID(args[1])
	if err != nil {
		return err
	}

	ok, err := confirmDelete(yes, fmt.Sprintf("Delete DNS record %d from %s?", id, domain))
	if err != nil {
		return err
	}
	if !ok {
		out.Warn("aborted")
		return nil
	}

	if dryRun {
		out.DryRun("DELETE", fmt.Sprintf("/core/v1/domains/%s/records/%d", domain, id), nil)
		return nil
	}

	stop := out.Spin("Deleting record…")
	resp, err := client.Gen().DeleteRecord(cmd.Context(), domain, id)
	stop()
	if err != nil {
		return err
	}
	if err := api.Decode(resp, nil); err != nil {
		return err
	}
	out.Success(fmt.Sprintf("Deleted record %d from %s", id, domain))
	out.Hint(fmt.Sprintf("Run 'namecom dns list %s' to see remaining records", domain))
	return nil
}

func runExport(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	domain := args[0]

	records, _, err := fetchAllRecords(cmd, domain, true)
	if err != nil {
		return err
	}

	if exportZone {
		for _, r := range records {
			rtype := derefStr(r.Type)
			rdata := derefStr(r.Answer)
			// MX and SRV records require priority prepended to rdata.
			if (rtype == "MX" || rtype == "SRV") && r.Priority != nil {
				rdata = fmt.Sprintf("%d %s", *r.Priority, rdata)
			}
			fmt.Fprintf(out.Writer, "%s\t%d\tIN\t%s\t%s\n",
				derefStr(r.Fqdn), r.Ttl, rtype, rdata)
		}
		return nil
	}

	if err := out.JSON(records); err != nil {
		return err
	}
	out.Hint(fmt.Sprintf("Use --zone for RFC 1035 zone-file format, or pipe to a file: namecom dns export %s > records.json", domain))
	return nil
}

func runImport(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain := args[0]
	dryRun := importDryRun || cmdutil.IsDryRun(cmd)

	data, err := os.ReadFile(importFile)
	if err != nil {
		return fmt.Errorf("reading import file: %w", err)
	}

	var records []gen.Record
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("parsing import file: %w", err)
	}

	created := 0
	for _, r := range records {
		body := gen.CreateRecordJSONRequestBody{
			Type:     gen.DNSCreateRecordBodyType(derefStr(r.Type)),
			Host:     derefStr(r.Host),
			Answer:   derefStr(r.Answer),
			Ttl:      &r.Ttl,
			Priority: r.Priority,
		}

		if dryRun {
			b, _ := json.MarshalIndent(body, "", "  ")
			fmt.Fprintf(out.Writer, "POST /core/v1/domains/%s/records\n%s\n", domain, b)
			continue
		}

		resp, err := client.Gen().CreateRecord(cmd.Context(), domain, body)
		if err != nil {
			return fmt.Errorf("creating %s %s: %w", body.Type, body.Host, err)
		}
		if err := api.Decode(resp, nil); err != nil {
			return fmt.Errorf("creating %s %s: %w", body.Type, body.Host, err)
		}
		created++
	}

	if !dryRun {
		out.Success(fmt.Sprintf("Imported %d record(s) to %s", created, domain))
		out.Hint(fmt.Sprintf("Run 'namecom dns list %s' to verify the imported records", domain))
	}
	return nil
}

// fetchAllRecords pages through all DNS records for a domain.
func fetchAllRecords(cmd *cobra.Command, domain string, all bool) (records []gen.Record, hasMore bool, err error) {
	client := cmdutil.APIClient(cmd)
	ctx := cmd.Context()

	var page int32 = 1

	for {
		params := &gen.ListRecordsParams{Page: &page}
		resp, err2 := client.Gen().ListRecords(ctx, domain, params)
		if err2 != nil {
			return nil, false, err2
		}
		var result gen.ListRecordsResponseSchema
		if err2 := api.Decode(resp, &result); err2 != nil {
			return nil, false, err2
		}
		records = append(records, result.Records...)

		if result.NextPage == nil || *result.NextPage == 0 {
			break
		}
		if !all {
			hasMore = true
			break
		}
		page = *result.NextPage
	}
	return records, hasMore, nil
}

func recordRows(out *output.Config, records []gen.Record) [][]string {
	rows := make([][]string, 0, len(records))
	for _, r := range records {
		id := ""
		if r.Id != nil {
			id = strconv.Itoa(int(*r.Id))
		}
		priority := ""
		if r.Priority != nil {
			priority = strconv.FormatInt(*r.Priority, 10)
		}
		ttl := strconv.FormatInt(r.Ttl, 10)
		rows = append(rows, []string{
			out.Dim(id),
			out.TypeBadge(derefStr(r.Type)),
			derefStr(r.Host),
			derefStr(r.Answer),
			out.Dim(ttl),
			priority,
		})
	}
	return rows
}

// dnsTypeOrder defines the preferred display order for grouped DNS output.
var dnsTypeOrder = []string{"A", "AAAA", "ANAME", "CNAME", "MX", "TXT", "NS", "SRV", "CAA"}

func renderGroupedRecords(out *output.Config, records []gen.Record) {
	// Bucket records by type, preserving insertion order per group.
	seen := map[string]bool{}
	var orderedTypes []string
	groups := map[string][]gen.Record{}
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
		out.Table(
			[]string{"ID", "HOST", "ANSWER", "TTL", "PRIORITY"},
			recordRowsNoType(out, groups[t]),
		)
	}
	for _, t := range dnsTypeOrder {
		emit(t)
	}
	for _, t := range orderedTypes {
		emit(t)
	}
}

// recordRowsNoType is like recordRows but omits the TYPE column (used in grouped view).
func recordRowsNoType(out *output.Config, records []gen.Record) [][]string {
	rows := make([][]string, 0, len(records))
	for _, r := range records {
		id := ""
		if r.Id != nil {
			id = strconv.Itoa(int(*r.Id))
		}
		priority := ""
		if r.Priority != nil {
			priority = strconv.FormatInt(*r.Priority, 10)
		}
		rows = append(rows, []string{
			out.Dim(id),
			derefStr(r.Host),
			derefStr(r.Answer),
			out.Dim(strconv.FormatInt(r.Ttl, 10)),
			priority,
		})
	}
	return rows
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
		huh.NewOption("CAA — cert authority", "CAA"),
	}

	ttlStr := "300"
	priorityStr := ""

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Record type").
				Options(typeOptions...).
				Value(&createType),
			huh.NewInput().
				Title("Host (@ for apex)").
				Value(&createHost),
			huh.NewInput().
				Title("Answer / value").
				Value(&createAnswer).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("answer is required")
					}
					return nil
				}),
			huh.NewInput().
				Title("TTL (seconds, min 300)").
				Value(&ttlStr),
		),
	)

	if err := form.Run(); err != nil {
		if err == huh.ErrUserAborted {
			return fmt.Errorf("aborted")
		}
		return err
	}

	// Parse TTL back into the package-level var.
	if n, err := strconv.ParseInt(ttlStr, 10, 64); err == nil {
		createTTL = n
	}

	// Show priority input only for record types that use it.
	upper := strings.ToUpper(createType)
	if upper == "MX" || upper == "SRV" {
		priorityForm := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Priority").
					Value(&priorityStr),
			),
		)
		if err := priorityForm.Run(); err != nil && err != huh.ErrUserAborted {
			return err
		}
		if n, err := strconv.ParseInt(priorityStr, 10, 64); err == nil {
			createPriority = n
		}
	}

	// Mark flags as changed so the caller uses the form values.
	_ = cmd.Flags().Set("type", createType)
	_ = cmd.Flags().Set("answer", createAnswer)
	return nil
}

func confirmDelete(yes bool, msg string) (bool, error) {
	return cmdutil.Confirm(yes, msg)
}

func parseID(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid record ID %q: must be a number", s)
	}
	return int32(n), nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt32(n *int32) int32 {
	if n == nil {
		return 0
	}
	return *n
}
