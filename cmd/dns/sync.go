package dns

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	syncFile     string
	syncPrune    bool
	syncPruneAll bool
)

var syncCmd = &cobra.Command{
	Use:   "sync <domain>",
	Short: "Make a domain's DNS records match a file, showing the plan first",
	Long: `Compare a file of DNS records with the domain's live records and make the
live zone match: create what is missing, update what differs only in TTL or
priority, and — with --prune — delete what the file does not list. The file is
the JSON 'dns export' writes, or a BIND zone file such as 'dns export --zone'
writes ($ORIGIN, $TTL, @, relative names, quoted TXT strings, comments and
parentheses are understood; SOA records are skipped).

Records are matched on host, type and answer. A changed answer is a new
record: without --prune the old one stays beside it. The exception is a CNAME
or ANAME, which a name can hold only one of: its target is updated in place.

The plan is printed, then confirmed (--yes skips the question). --dry-run
prints the plan without sending anything — as one document with -o json — and
a run with nothing to change exits 0 without asking. Changes are applied
creates first, then updates, then deletes, so a run that stops partway leaves
extra records rather than missing ones. On the first failure nothing more is
sent, and the error says what was applied and what was not; fix the problem
and run sync again — it picks up from the live state.

Safety:
  Without --prune nothing is deleted.
  --prune deletes records not in the file, except NS records at the apex
    (the zone's delegation) and CAA records (which the API cannot
    recreate). Those are never changed or deleted by --prune.
  --prune-all is --prune that also changes and deletes those.
  A file with no records is refused with --prune or --prune-all.`,
	Example: `  namecom dns export example.com > example.json        # snapshot, then edit
  namecom dns sync example.com --file example.json --dry-run
  namecom dns sync example.com --file example.json

  # In CI: delete what the zone file does not list, without a prompt
  namecom dns sync example.com --file example.com.zone --prune --yes`,
	Args:              cmdutil.ExactArgs(1),
	RunE:              runSync,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	syncCmd.Flags().StringVar(&syncFile, "file", "", "records to sync to: a 'dns export' JSON file or a zone file, - for stdin (required)")
	syncCmd.Flags().BoolVar(&syncPrune, "prune", false, "delete live records the file does not list (never apex NS or CAA)")
	syncCmd.Flags().BoolVar(&syncPruneAll, "prune-all", false, "like --prune, and also change or delete apex NS and CAA records")
	_ = syncCmd.MarkFlagRequired("file")
}

// syncChange is one write of a sync, as the result reports it.
type syncChange struct {
	Action string `json:"action"`
	planRecord
}

// syncFailure is the write a sync stopped at. OutcomeUnknown is set when it
// failed in a way that may have gone through — a 5xx, or a connection lost
// after sending — so it may be applied although it is not in "applied".
type syncFailure struct {
	syncChange
	Error          string `json:"error"`
	OutcomeUnknown bool   `json:"outcomeUnknown,omitempty"`
}

// syncResult is what a sync did: every write applied, in order, and — when it
// stopped early — the one that failed and those never attempted. Changed is
// whether anything was applied, as every write without a resource of its own
// reports it (#240).
type syncResult struct {
	Domain       string       `json:"domain"`
	Changed      bool         `json:"changed"`
	Applied      []syncChange `json:"applied"`
	Failed       *syncFailure `json:"failed,omitempty"`
	NotAttempted []syncChange `json:"notAttempted,omitempty"`
	Unchanged    int          `json:"unchanged"`
}

func runSync(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	domain, err := cmdutil.DomainArg(args, 0)
	if err != nil {
		return err
	}

	desired, _, err := readRecordsFile(syncFile, domain)
	if err != nil {
		return err
	}
	if err := prepareRecords(desired, true); err != nil {
		return err
	}
	// An empty file with --prune would delete the whole zone. That is far
	// likelier to be the wrong file, or a failed export piped in, than the
	// intent; 'dns delete' remains for clearing a zone deliberately.
	if len(desired) == 0 && (syncPrune || syncPruneAll) {
		return cmdutil.NewUsageError(fmt.Errorf("%s lists no records; refusing to prune every record from %s", syncFile, domain))
	}

	stop := out.Spin("Fetching DNS records…")
	live, _, _, err := fetchRecords(cmd, domain, 1, nil, true)
	stop()
	if err != nil {
		if cmdutil.IsNotFound(err) {
			return cmdutil.DomainNotFound(err, domain)
		}
		return err
	}

	plan, err := computePlan(domain, desired, live, planOptions{Prune: syncPrune, PruneAll: syncPruneAll})
	if err != nil {
		return err
	}

	// The dry run never prompts and sends nothing; it is the plan, plus the
	// exact requests a real run makes.
	if cmdutil.IsDryRun(cmd) {
		plan.DryRun = true
		plan.Requests = []output.DryRunRequest{}
		for _, o := range plan.ops {
			r := o.request(domain)
			r.DryRun = true
			plan.Requests = append(plan.Requests, r)
		}
		return printPlan(out, plan)
	}

	if plan.changes() == 0 {
		switch out.Format {
		case output.FormatJSON, output.FormatYAML:
			return printResult(out, &syncResult{Domain: domain, Applied: []syncChange{}, Unchanged: plan.Unchanged})
		}
		// In TSV, an empty plan's header row before the result's rows would
		// read as one table; the result alone says nothing changed.
		if out.Format == output.FormatTable {
			if err := printPlan(out, plan); err != nil {
				return err
			}
		}
		out.Unchanged(fmt.Sprintf("%s already matches the file: nothing to change", domain))
		return nil
	}

	if out.Format == output.FormatTable {
		if err := printPlan(out, plan); err != nil {
			return err
		}
	}
	if err := cmdutil.ConfirmWrite(cmd, fmt.Sprintf("Apply %s to %s (%s)?",
		output.Plural(plan.changes(), "change"), domain, planCounts(plan))); err != nil {
		return err
	}

	res := &syncResult{Domain: domain, Applied: []syncChange{}, Unchanged: plan.Unchanged}
	stop = out.Spin("Applying changes…")
	for i, o := range plan.ops {
		rec, err := applyOp(cmd, client, domain, o)
		if err != nil {
			stop()
			err = api.MarkWrite(err)
			res.Changed = len(res.Applied) > 0
			res.Failed = &syncFailure{syncChange: syncChange{Action: o.action, planRecord: o.record}, Error: err.Error()}
			// Decided here, while the failed write is the client's last
			// request, rather than by the root command (#243).
			if unknown, ok := errors.AsType[*api.OutcomeUnknownError](client.OutcomeUnknown(err)); ok {
				res.Failed.OutcomeUnknown = true
				err = &syncOutcomeUnknownError{unknown}
			}
			for _, rest := range plan.ops[i+1:] {
				res.NotAttempted = append(res.NotAttempted, syncChange{Action: rest.action, planRecord: rest.record})
			}
			return syncFailed(out, res, err)
		}
		res.Applied = append(res.Applied, syncChange{Action: o.action, planRecord: rec})
	}
	stop()
	res.Changed = len(res.Applied) > 0

	if out.Quiet() {
		return nil
	}
	switch out.Format {
	case output.FormatJSON, output.FormatYAML:
		return printResult(out, res)
	}
	out.Success(fmt.Sprintf("Synced %s: %s", domain, appliedCounts(res.Applied)))
	return nil
}

// applyOp sends one write of a plan, and returns the record as it now is: a
// create's carries the ID the API gave it.
func applyOp(cmd *cobra.Command, client *api.Client, domain string, o syncOp) (planRecord, error) {
	ctx := cmd.Context()
	rec := o.record
	switch o.action {
	case "create":
		created, err := client.SDK().DNS.CreateRecord(ctx, o.create)
		if err != nil {
			return rec, api.FromSDKError(err)
		}
		if created == nil || derefInt(created.ID) <= 0 {
			// As in `dns create`: a 2xx without an ID is not proof the
			// record exists (#185).
			return rec, &api.UnexpectedResponseError{Reason: "the response did not include the new record's ID"}
		}
		rec.ID = *created.ID
	case "update":
		_, err := client.SDK().DNS.UpdateRecord(ctx, o.update)
		if err != nil {
			return rec, api.FromSDKError(err)
		}
	default:
		err := client.SDK().DNS.DeleteRecord(ctx, &coreapigo.DeleteRecordRequest{DomainName: domain, ID: o.id})
		if err != nil {
			return rec, api.FromSDKError(err)
		}
	}
	return rec, nil
}

// syncFailed reports a sync that stopped at res.Failed: the result document
// in JSON and YAML, so a script sees exactly what landed, and warnings naming
// each applied write in table mode. The returned error keeps the API error's
// exit code.
func syncFailed(out *output.Config, res *syncResult, err error) error {
	switch out.Format {
	case output.FormatJSON, output.FormatYAML:
		if perr := printResult(out, res); perr != nil {
			return perr
		}
	default:
		if len(res.Applied) == 0 {
			out.Warn("nothing was changed before the failure")
		} else {
			out.Warn(fmt.Sprintf("applied before the failure (%s):", appliedCounts(res.Applied)))
			for _, c := range res.Applied {
				out.Warn("  " + changeLine(c.Action, c.planRecord))
			}
		}
		if n := len(res.NotAttempted); n > 0 {
			out.Warn(fmt.Sprintf("%s not attempted — run sync again after fixing the problem; it plans from the live zone", output.Plural(n, "change")))
		}
	}
	total := len(res.Applied) + 1 + len(res.NotAttempted)
	return fmt.Errorf("%s (change %d of %d; %d applied before it): %w",
		changeLine(res.Failed.Action, res.Failed.planRecord), len(res.Applied)+1, total, len(res.Applied), err)
}

// syncOutcomeUnknownError is a sync stopped by a write whose outcome is
// unknown. It keeps the *api.OutcomeUnknownError, so the exit code is 6 and
// the envelope names the idempotency key, but replaces its hint: a sync is not
// retried by pinning one request's key — the next run sends other requests —
// but by running it again, which plans from the live zone and so does not
// repeat a change that landed.
type syncOutcomeUnknownError struct{ error }

func (e *syncOutcomeUnknownError) Unwrap() error { return e.error }

func (e *syncOutcomeUnknownError) UserHint() string {
	return "outcome unknown: that change may or may not have been made — run sync again; it plans from the live zone, so a change that landed is not repeated"
}

func printResult(out *output.Config, res *syncResult) error {
	if out.Format == output.FormatYAML {
		return out.YAML(res)
	}
	return out.JSON(res)
}

// printPlan prints the plan: as one document in JSON and YAML, and in table
// mode as a line per change, then what is left alone and why.
func printPlan(out *output.Config, p *syncPlan) error {
	switch out.Format {
	case output.FormatJSON:
		return out.JSON(p)
	case output.FormatYAML:
		return out.YAML(p)
	}
	if out.QuietMode {
		return nil
	}
	if out.Format == output.FormatTSV {
		out.Table([]string{"ACTION", "TYPE", "HOST", "ANSWER", "TTL", "PRIORITY"}, planRows(p))
		return nil
	}
	w := out.Writer
	verb := "Plan"
	if p.DryRun {
		verb = "Plan (dry run, nothing sent)"
	}
	fmt.Fprintf(w, "%s for %s: %s, %d unchanged\n", verb, p.Domain, planCounts(p), p.Unchanged)
	for _, r := range p.Creates {
		fmt.Fprintln(w, "  "+changeLine("create", r))
	}
	for _, u := range p.Updates {
		fmt.Fprintf(w, "  update %s: %s\n", u.Before.summary(), strings.Join(u.Changes, ", "))
	}
	for _, r := range p.Deletes {
		fmt.Fprintln(w, "  "+changeLine("delete", r))
	}
	notInFile := 0
	for _, k := range p.Kept {
		if !p.Prune && strings.HasPrefix(k.Reason, "not in the file") {
			notInFile++
			continue
		}
		fmt.Fprintf(w, "  keep   %s: %s\n", k.summary(), k.Reason)
	}
	if notInFile > 0 {
		out.Note(fmt.Sprintf("%s not in the file %s kept — pass --prune to delete them",
			output.Plural(notInFile, "live record"), isAre(notInFile)))
	}
	for _, c := range p.Creates {
		if !p.Prune {
			if k := sameNameKept(p, c); k != "" {
				out.Warn(fmt.Sprintf("%s %s will keep %s beside the new record — pass --prune to replace it", c.Type, displayHost(&c.Host), k))
			}
		}
	}
	return nil
}

// planRows is a row per change of p, as -o tsv prints the plan (#268): the
// action, then the record as the change leaves it. Records the plan leaves
// alone are not changes, and have no row.
func planRows(p *syncPlan) [][]string {
	row := func(action string, r planRecord) []string {
		priority := ""
		if r.Priority != nil {
			priority = strconv.FormatInt(*r.Priority, 10)
		}
		return []string{action, r.Type, displayHost(&r.Host), r.Answer, strconv.FormatInt(r.TTL, 10), priority}
	}
	rows := make([][]string, 0, p.changes())
	for _, r := range p.Creates {
		rows = append(rows, row("create", r))
	}
	for _, u := range p.Updates {
		rows = append(rows, row("update", u.planRecord))
	}
	for _, r := range p.Deletes {
		rows = append(rows, row("delete", r))
	}
	return rows
}

// sameNameKept returns the answer of a live record left beside c — same host
// and type, not in the file — so that a changed answer without --prune is not
// mistaken for a replacement.
func sameNameKept(p *syncPlan, c planRecord) string {
	for _, k := range p.Kept {
		if k.Type == c.Type && normHost(k.Host) == normHost(c.Host) && strings.HasPrefix(k.Reason, "not in the file") {
			return k.Answer
		}
	}
	return ""
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func changeLine(action string, r planRecord) string {
	return fmt.Sprintf("%-6s %s", action, r.summary())
}

// planCounts is "2 creates, 1 update, 0 deletes".
func planCounts(p *syncPlan) string {
	return fmt.Sprintf("%s, %s, %s",
		output.Plural(len(p.Creates), "create"), output.Plural(len(p.Updates), "update"), output.Plural(len(p.Deletes), "delete"))
}

// appliedCounts is "created 2, updated 1, deleted 0".
func appliedCounts(applied []syncChange) string {
	n := map[string]int{}
	for _, c := range applied {
		n[c.Action]++
	}
	return fmt.Sprintf("created %d, updated %d, deleted %d", n["create"], n["update"], n["delete"])
}

// prepareRecords validates every record read from a file and converts its
// names to the ASCII form that is sent, before anything is written: a file
// whose fourth record was malformed used to write three and then fail,
// leaving the zone half-changed. allowCAA admits CAA records, which `dns
// sync` matches against the zone but never creates.
func prepareRecords(recs []inputRecord, allowCAA bool) error {
	for i := range recs {
		r := &recs[i]
		fail := func(err error) error {
			return fmt.Errorf("%s (%s %s): %w", r.Source, r.Type, r.Host, err)
		}
		if !allowCAA || r.Type != "CAA" {
			if err := cmdutil.ValidDNSCreateType(r.Type); err != nil {
				return fail(err)
			}
		}
		host, err := asciiHost(r.Host)
		if err != nil {
			return fail(err)
		}
		answer, err := asciiAnswer(r.Type, host, r.Answer)
		if err != nil {
			return fail(err)
		}
		if err := cmdutil.ValidTTL(r.TTL); err != nil {
			return fail(err)
		}
		if r.Priority != nil {
			if err := cmdutil.ValidPriority(*r.Priority); err != nil {
				return fail(err)
			}
		}
		r.Host, r.Answer = host, answer
	}
	return nil
}
