package dns

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// recordKey identifies a record for matching a file against the live zone:
// host, type and answer, each normalized so that spellings the API treats as
// the same record compare equal. TTL and priority are not part of it — a
// record that differs only in those is the same record, to be updated.
type recordKey struct {
	host, rtype, answer string
}

func keyOf(rtype, host, answer string) recordKey {
	rtype = strings.ToUpper(rtype)
	return recordKey{host: normHost(host), rtype: rtype, answer: normAnswer(rtype, answer)}
}

func liveKey(r *coreapigo.Record) recordKey {
	return keyOf(derefStr(r.Type), derefStr(r.Host), derefStr(r.Answer))
}

// normHost is a host for comparison: lowercase, with the apex as "" whether
// it was spelled "" (as the API returns it) or "@".
func normHost(h string) string {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "@" {
		return ""
	}
	return h
}

// normName is a target hostname for comparison: lowercase and without the
// trailing dot, which the API strips on storage. The root (a null MX) stays.
func normName(n string) string {
	if n == "." {
		return n
	}
	return strings.ToLower(strings.TrimSuffix(n, "."))
}

// normAnswer is an answer for comparison. Addresses compare by value, so
// 2001:DB8::1 matches 2001:db8:0::1; names by normName; a TXT value written
// as quoted character-strings matches the same value unquoted; CAA by its
// three fields.
func normAnswer(rtype, a string) string {
	switch rtype {
	case "A", "AAAA":
		if ip := net.ParseIP(a); ip != nil {
			return ip.String()
		}
	case "CNAME", "ANAME", "NS", "MX":
		return normName(a)
	case "SRV":
		f := strings.Fields(a)
		if len(f) == 3 {
			return f[0] + " " + f[1] + " " + normName(f[2])
		}
	case "TXT":
		if parts, ok := parseQuotedTXT(a); ok {
			return strings.Join(parts, "")
		}
	case "CAA":
		f := strings.SplitN(strings.TrimSpace(a), " ", 3)
		if len(f) == 3 {
			v := strings.TrimSpace(f[2])
			if parts, ok := parseQuotedTXT(v); ok {
				v = strings.Join(parts, "")
			}
			return f[0] + " " + strings.ToLower(f[1]) + " " + v
		}
	}
	return a
}

// typeHasPriority reports whether records of rtype carry a priority the API
// keeps. For every other type it is ignored, and so is not compared.
func typeHasPriority(rtype string) bool {
	return rtype == "MX" || rtype == "SRV"
}

// protectedReason says why sync leaves a live record alone unless
// --prune-all is given, or "" when nothing protects it.
//
//   - NS records at the apex are the zone's delegation. Deleting or changing
//     them can take the whole domain offline, and they are routinely absent
//     from a file that describes only the records someone maintains.
//   - CAA records cannot be created or updated through the API, so one
//     deleted by sync could not be put back by sync, or by `dns create`.
func protectedReason(r *coreapigo.Record) string {
	switch strings.ToUpper(derefStr(r.Type)) {
	case "NS":
		if normHost(derefStr(r.Host)) == "" {
			return "apex NS record (protected; pass --prune-all to change it)"
		}
	case "CAA":
		return "CAA record (protected: the API cannot recreate it; pass --prune-all to change it)"
	}
	return ""
}

// planRecord is a record as the plan and the result report it.
type planRecord struct {
	ID       int    `json:"id,omitempty"`
	Type     string `json:"type"`
	Host     string `json:"host"`
	Answer   string `json:"answer"`
	TTL      int64  `json:"ttl"`
	Priority *int64 `json:"priority,omitempty"`
}

func (r planRecord) summary() string {
	detail := fmt.Sprintf("TTL %d", r.TTL)
	if r.Priority != nil {
		detail = fmt.Sprintf("priority %d, %s", *r.Priority, detail)
	}
	s := fmt.Sprintf("%s %s → %s (%s)", r.Type, displayHost(&r.Host), r.Answer, detail)
	if r.ID > 0 {
		s += fmt.Sprintf(" [id %d]", r.ID)
	}
	return s
}

func livePlanRecord(r *coreapigo.Record) planRecord {
	host := derefStr(r.Host)
	if host == "" {
		host = "@"
	}
	pr := planRecord{
		ID: derefInt(r.ID), Type: strings.ToUpper(derefStr(r.Type)), Host: host,
		Answer: derefStr(r.Answer), TTL: r.TTL,
	}
	if typeHasPriority(pr.Type) {
		pr.Priority = r.Priority
	}
	return pr
}

func inputPlanRecord(r inputRecord) planRecord {
	pr := planRecord{Type: r.Type, Host: r.Host, Answer: r.Answer, TTL: r.TTL}
	if typeHasPriority(r.Type) {
		pr.Priority = r.Priority
	}
	return pr
}

// planUpdate is a live record the plan changes. The embedded record is what
// it will be; Before is what it is now; Changes says what differs.
type planUpdate struct {
	planRecord
	Before  planRecord `json:"before"`
	Changes []string   `json:"changes"`
}

// planKept is a live record the plan leaves alone although the file does not
// list it as it is, and why.
type planKept struct {
	planRecord
	Reason string `json:"reason"`
}

// syncPlan is what `dns sync` will do. It is printed by --dry-run, as one
// document in JSON and YAML, and shown before the confirmation otherwise.
type syncPlan struct {
	Domain    string       `json:"domain"`
	DryRun    bool         `json:"dryRun"`
	Prune     bool         `json:"prune"`
	PruneAll  bool         `json:"pruneAll"`
	Creates   []planRecord `json:"creates"`
	Updates   []planUpdate `json:"updates"`
	Deletes   []planRecord `json:"deletes"`
	Unchanged int          `json:"unchanged"`
	Kept      []planKept   `json:"kept"`
	// Requests are the API calls the plan makes, in the order they are
	// sent: the body each carries is the one a real run sends. Only a dry
	// run reports them.
	Requests []output.DryRunRequest `json:"requests,omitempty"`

	ops []syncOp
}

// changes is the number of writes the plan makes.
func (p *syncPlan) changes() int { return len(p.ops) }

// syncOp is one write of a plan, with the body built once: the preview and
// the request sent both come from it.
type syncOp struct {
	action string // "create", "update" or "delete"
	record planRecord
	before planRecord // an update's record as it is now
	create *coreapigo.DNSCreateRecordBody
	update *coreapigo.DNSUpdateRecordBody
	id     int
}

func (o syncOp) request(domain string) output.DryRunRequest {
	switch o.action {
	case "create":
		return output.DryRunRequest{Method: "POST", Path: fmt.Sprintf("/core/v1/domains/%s/records", domain), Body: o.create}
	case "update":
		return output.DryRunRequest{Method: "PUT", Path: fmt.Sprintf("/core/v1/domains/%s/records/%d", domain, o.id), Body: o.update}
	}
	return output.DryRunRequest{Method: "DELETE", Path: fmt.Sprintf("/core/v1/domains/%s/records/%d", domain, o.id)}
}

// planOptions are the sync flags that shape a plan.
type planOptions struct {
	// Prune deletes live records the file does not list, except protected
	// ones (see protectedReason).
	Prune bool
	// PruneAll is Prune that also deletes and updates protected records.
	PruneAll bool
}

// computePlan compares desired, the validated records of a file, with live,
// the domain's current records, and returns the writes that make the zone
// match the file.
//
//   - A file record with a live match on host, type and answer is unchanged,
//     or an update when its TTL or (MX, SRV) priority differs.
//   - A file record with no match is a create — except a CNAME or ANAME
//     whose host has exactly one live record of that type left unmatched.
//     A name holds one CNAME, so creating a second would only be refused:
//     that live record is updated to the new target instead.
//   - A live record the file does not list is deleted under --prune, and
//     kept otherwise. Protected records are kept, and not updated, unless
//     --prune-all is given.
//
// A file that lists one record twice, or a CAA record the zone does not
// already have exactly (the API cannot create or update CAA), is a usage
// error, found before anything is sent.
func computePlan(domain string, desired []inputRecord, live []*coreapigo.Record, opts planOptions) (*syncPlan, error) {
	if opts.PruneAll {
		opts.Prune = true
	}
	p := &syncPlan{
		Domain: domain, Prune: opts.Prune, PruneAll: opts.PruneAll,
		Creates: []planRecord{}, Updates: []planUpdate{}, Deletes: []planRecord{}, Kept: []planKept{},
	}

	var liveRecs []*coreapigo.Record
	for _, r := range live {
		if r != nil {
			liveRecs = append(liveRecs, r)
		}
	}
	matched := make([]bool, len(liveRecs))
	byKey := map[recordKey][]int{}
	for i, r := range liveRecs {
		k := liveKey(r)
		byKey[k] = append(byKey[k], i)
	}
	takeMatch := func(k recordKey) int {
		for _, i := range byKey[k] {
			if !matched[i] {
				matched[i] = true
				return i
			}
		}
		return -1
	}

	seen := map[recordKey]string{}
	var unmatched []inputRecord
	var creates, updates, deletes []syncOp
	for _, d := range desired {
		k := keyOf(d.Type, d.Host, d.Answer)
		if first, dup := seen[k]; dup {
			return nil, cmdutil.NewUsageError(fmt.Errorf("%s lists %s %s → %s again (first at %s)",
				d.Source, d.Type, d.Host, d.Answer, first))
		}
		seen[k] = d.Source

		i := takeMatch(k)
		if i < 0 {
			unmatched = append(unmatched, d)
			continue
		}
		cur := liveRecs[i]
		changes := recordDiff(cur, d)
		if len(changes) == 0 {
			p.Unchanged++
			continue
		}
		if reason := protectedReason(cur); reason != "" {
			switch {
			case d.Type == "CAA": // refused by the API even under --prune-all
				reason = "CAA record: the API cannot update it (" + strings.Join(changes, ", ") + " not applied)"
			case !opts.PruneAll:
				reason += " — the file asks for " + strings.Join(changes, ", ")
			default:
				reason = ""
			}
			if reason != "" {
				p.Kept = append(p.Kept, planKept{planRecord: livePlanRecord(cur), Reason: reason})
				continue
			}
		}
		updates = append(updates, updateOp(domain, cur, d))
	}

	for _, d := range unmatched {
		if d.Type == "CNAME" || d.Type == "ANAME" {
			if i := soleUnmatched(liveRecs, matched, d); i >= 0 {
				matched[i] = true
				updates = append(updates, updateOp(domain, liveRecs[i], d))
				continue
			}
		}
		if d.Type == "CAA" {
			return nil, cmdutil.NewUsageError(fmt.Errorf("%s: CAA %s → %s is not in the zone, and name.com's API cannot create CAA records — remove it from the file",
				d.Source, d.Host, d.Answer))
		}
		body := &coreapigo.DNSCreateRecordBody{
			DomainName: domain,
			Type:       coreapigo.DNSCreateRecordBodyType(d.Type),
			Host:       d.Host,
			Answer:     d.Answer,
			TTL:        &d.TTL,
			Priority:   d.Priority,
		}
		creates = append(creates, syncOp{action: "create", record: inputPlanRecord(d), create: body})
	}

	for i, r := range liveRecs {
		if matched[i] {
			continue
		}
		pr := livePlanRecord(r)
		switch reason := protectedReason(r); {
		case !opts.Prune:
			p.Kept = append(p.Kept, planKept{planRecord: pr, Reason: "not in the file (pass --prune to delete it)"})
		case reason != "" && !opts.PruneAll:
			p.Kept = append(p.Kept, planKept{planRecord: pr, Reason: reason})
		case pr.ID <= 0:
			p.Kept = append(p.Kept, planKept{planRecord: pr, Reason: "the API returned it without an ID, so it cannot be deleted"})
		default:
			deletes = append(deletes, syncOp{action: "delete", record: pr, id: pr.ID})
		}
	}

	// Creates before updates before deletes: a run stopped partway leaves
	// the zone with extra records rather than missing ones.
	p.ops = append(append(creates, updates...), deletes...)
	for _, o := range p.ops {
		switch o.action {
		case "create":
			p.Creates = append(p.Creates, o.record)
		case "delete":
			p.Deletes = append(p.Deletes, o.record)
		}
	}
	for _, o := range updates {
		p.Updates = append(p.Updates, planUpdate{planRecord: o.record, Before: o.before, Changes: recordDiffStrings(o.before, o.record)})
	}
	return p, nil
}

// soleUnmatched returns the index of the only unmatched live record with
// d's host and type, or -1 when there is none or more than one.
func soleUnmatched(live []*coreapigo.Record, matched []bool, d inputRecord) int {
	found := -1
	for i, r := range live {
		if matched[i] || !strings.EqualFold(derefStr(r.Type), d.Type) || normHost(derefStr(r.Host)) != normHost(d.Host) {
			continue
		}
		if found >= 0 || protectedReason(r) != "" || derefInt(r.ID) <= 0 {
			return -1
		}
		found = i
	}
	return found
}

// recordDiff lists what d changes about cur, comparing the TTL, the answer
// (when it differs other than in spelling) and, for MX and SRV, a priority the
// file states.
func recordDiff(cur *coreapigo.Record, d inputRecord) []string {
	return recordDiffStrings(livePlanRecord(cur), inputPlanRecord(d))
}

func recordDiffStrings(before, after planRecord) []string {
	var changes []string
	if normAnswer(after.Type, before.Answer) != normAnswer(after.Type, after.Answer) {
		changes = append(changes, fmt.Sprintf("answer %s → %s", before.Answer, after.Answer))
	}
	if before.TTL != after.TTL {
		changes = append(changes, fmt.Sprintf("ttl %d → %d", before.TTL, after.TTL))
	}
	if typeHasPriority(after.Type) && after.Priority != nil && derefInt64(before.Priority) != *after.Priority {
		was := ""
		if before.Priority != nil {
			was = strconv.FormatInt(*before.Priority, 10)
		}
		changes = append(changes, fmt.Sprintf("priority %s → %d", orNone(was), *after.Priority))
	}
	return changes
}

// updateOp is the full PUT that makes cur into d. The update endpoint
// replaces the record, so every field is sent: the type and host as the API
// has them, and the answer, TTL and priority from the file — or the live
// priority, when the file states none.
func updateOp(domain string, cur *coreapigo.Record, d inputRecord) syncOp {
	id := derefInt(cur.ID)
	ttl := d.TTL
	body := &coreapigo.DNSUpdateRecordBody{
		DomainName: domain,
		ID:         id,
		Type:       coreapigo.DNSUpdateRecordBodyType(strings.ToUpper(derefStr(cur.Type))),
		Host:       cur.Host,
		Answer:     d.Answer,
		TTL:        &ttl,
	}
	if typeHasPriority(d.Type) {
		body.Priority = cur.Priority
		if d.Priority != nil {
			body.Priority = d.Priority
		}
	}
	before := livePlanRecord(cur)
	rec := before
	rec.Answer, rec.TTL = d.Answer, ttl
	if typeHasPriority(rec.Type) {
		rec.Priority = body.Priority
	}
	return syncOp{action: "update", record: rec, before: before, update: body, id: id}
}
