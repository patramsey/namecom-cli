package dns

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// liveRec is a record as the API returns it: the apex host as "", targets
// without their trailing dot.
func liveRec(id int, rtype, host, answer string, ttl int64, prio ...int64) *coreapigo.Record {
	r := &coreapigo.Record{ID: &id, Type: &rtype, Host: &host, Answer: &answer, TTL: ttl}
	if len(prio) > 0 {
		r.Priority = &prio[0]
	}
	return r
}

// want is a desired record as a file gives it, already validated.
func want(rtype, host, answer string, ttl int64, prio ...int64) inputRecord {
	r := inputRecord{Source: "line 1", Type: rtype, Host: host, Answer: answer, TTL: ttl}
	if len(prio) > 0 {
		r.Priority = &prio[0]
	}
	return r
}

// opLines describes a plan's writes in the order they would be sent.
func opLines(p *syncPlan) []string {
	var lines []string
	for _, o := range p.ops {
		switch o.action {
		case "create":
			lines = append(lines, fmt.Sprintf("create %s %s %s", o.record.Type, o.record.Host, o.record.Answer))
		case "update":
			lines = append(lines, fmt.Sprintf("update #%d: %s", o.id, strings.Join(recordDiffStrings(o.before, o.record), ", ")))
		default:
			lines = append(lines, fmt.Sprintf("delete #%d", o.id))
		}
	}
	return lines
}

func keptIDs(p *syncPlan) []int {
	var ids []int
	for _, k := range p.Kept {
		ids = append(ids, k.ID)
	}
	return ids
}

// TestComputePlan is the plan's decision table: what each combination of
// file record, live record and flag turns into.
func TestComputePlan(t *testing.T) {
	cases := []struct {
		name      string
		desired   []inputRecord
		live      []*coreapigo.Record
		opts      planOptions
		ops       []string
		unchanged int
		kept      []int
	}{
		{
			name:      "identical records change nothing",
			desired:   []inputRecord{want("A", "@", "192.0.2.1", 300), want("MX", "@", "mail.example.com", 300, 10)},
			live:      []*coreapigo.Record{liveRec(1, "A", "", "192.0.2.1", 300), liveRec(2, "MX", "", "mail.example.com", 300, 10)},
			unchanged: 2,
		},
		{
			name: "spellings the API treats as one record match",
			desired: []inputRecord{
				want("CNAME", "WWW", "Example.com.", 300),
				want("AAAA", "@", "2001:DB8:0::1", 300),
				want("TXT", "@", `"v=spf1 " "-all"`, 300),
				want("SRV", "_sip._tcp", "10 5060 SIP.example.com.", 300, 5),
				want("CAA", "@", `0 ISSUE "letsencrypt.org"`, 300),
			},
			live: []*coreapigo.Record{
				liveRec(1, "CNAME", "www", "example.com", 300),
				liveRec(2, "AAAA", "", "2001:db8::1", 300),
				liveRec(3, "TXT", "", "v=spf1 -all", 300),
				liveRec(4, "SRV", "_sip._tcp", "10 5060 sip.example.com", 300, 5),
				liveRec(5, "CAA", "", `0 issue "letsencrypt.org"`, 300),
			},
			unchanged: 5,
		},
		{
			name:    "a missing record is created",
			desired: []inputRecord{want("A", "www", "192.0.2.1", 300)},
			ops:     []string{"create A www 192.0.2.1"},
		},
		{
			name:    "a TTL change is an update",
			desired: []inputRecord{want("A", "www", "192.0.2.1", 3600)},
			live:    []*coreapigo.Record{liveRec(7, "A", "www", "192.0.2.1", 300)},
			ops:     []string{"update #7: ttl 300 → 3600"},
		},
		{
			name:    "an MX priority change is an update",
			desired: []inputRecord{want("MX", "@", "mail.example.com", 300, 20)},
			live:    []*coreapigo.Record{liveRec(7, "MX", "", "mail.example.com", 300, 10)},
			ops:     []string{"update #7: priority 10 → 20"},
		},
		{
			name:      "a file MX with no priority keeps the live one",
			desired:   []inputRecord{want("MX", "@", "mail.example.com", 300)},
			live:      []*coreapigo.Record{liveRec(7, "MX", "", "mail.example.com", 300, 10)},
			unchanged: 1,
		},
		{
			name:    "a changed answer without --prune adds the record and keeps the old one",
			desired: []inputRecord{want("A", "www", "192.0.2.2", 300)},
			live:    []*coreapigo.Record{liveRec(7, "A", "www", "192.0.2.1", 300)},
			ops:     []string{"create A www 192.0.2.2"},
			kept:    []int{7},
		},
		{
			name:    "a changed answer with --prune creates first, then deletes",
			desired: []inputRecord{want("A", "www", "192.0.2.2", 300)},
			live:    []*coreapigo.Record{liveRec(7, "A", "www", "192.0.2.1", 300)},
			opts:    planOptions{Prune: true},
			ops:     []string{"create A www 192.0.2.2", "delete #7"},
		},
		{
			name:    "a CNAME's new target is an update: a name holds one CNAME",
			desired: []inputRecord{want("CNAME", "www", "new.example.net", 600)},
			live:    []*coreapigo.Record{liveRec(7, "CNAME", "www", "old.example.net", 300)},
			ops:     []string{"update #7: answer old.example.net → new.example.net, ttl 300 → 600"},
		},
		{
			name:    "order is creates, then updates, then deletes",
			desired: []inputRecord{want("A", "b", "192.0.2.2", 600), want("A", "c", "192.0.2.3", 300)},
			live:    []*coreapigo.Record{liveRec(1, "A", "a", "192.0.2.1", 300), liveRec(2, "A", "b", "192.0.2.2", 300)},
			opts:    planOptions{Prune: true},
			ops:     []string{"create A c 192.0.2.3", "update #2: ttl 300 → 600", "delete #1"},
		},
		{
			name: "--prune never deletes apex NS or CAA",
			live: []*coreapigo.Record{
				liveRec(1, "NS", "", "ns1.name.com", 300),
				liveRec(2, "CAA", "", `0 issue "letsencrypt.org"`, 300),
				liveRec(3, "NS", "dev", "ns1.other.net", 300),
				liveRec(4, "A", "www", "192.0.2.1", 300),
			},
			opts: planOptions{Prune: true},
			ops:  []string{"delete #3", "delete #4"},
			kept: []int{1, 2},
		},
		{
			name: "--prune-all deletes them too",
			live: []*coreapigo.Record{
				liveRec(1, "NS", "", "ns1.name.com", 300),
				liveRec(2, "CAA", "", `0 issue "letsencrypt.org"`, 300),
			},
			opts: planOptions{PruneAll: true},
			ops:  []string{"delete #1", "delete #2"},
		},
		{
			name:    "an apex NS TTL change is held back without --prune-all",
			desired: []inputRecord{want("NS", "@", "ns1.name.com", 3600)},
			live:    []*coreapigo.Record{liveRec(1, "NS", "", "ns1.name.com", 300)},
			opts:    planOptions{Prune: true},
			kept:    []int{1},
		},
		{
			name:    "and applied with it",
			desired: []inputRecord{want("NS", "@", "ns1.name.com", 3600)},
			live:    []*coreapigo.Record{liveRec(1, "NS", "", "ns1.name.com", 300)},
			opts:    planOptions{PruneAll: true},
			ops:     []string{"update #1: ttl 300 → 3600"},
		},
		{
			name:    "a CAA TTL change is never sent: the API refuses CAA updates",
			desired: []inputRecord{want("CAA", "@", `0 issue "letsencrypt.org"`, 3600)},
			live:    []*coreapigo.Record{liveRec(1, "CAA", "", `0 issue "letsencrypt.org"`, 300)},
			opts:    planOptions{PruneAll: true},
			kept:    []int{1},
		},
		{
			name:    "a CNAME is not paired with a second live CNAME it could be confused with",
			desired: []inputRecord{want("CNAME", "www", "new.example.net", 300)},
			live:    []*coreapigo.Record{liveRec(1, "CNAME", "www", "a.example.net", 300), liveRec(2, "CNAME", "www", "b.example.net", 300)},
			ops:     []string{"create CNAME www new.example.net"},
			kept:    []int{1, 2},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := computePlan("example.com", tc.desired, tc.live, tc.opts)
			if err != nil {
				t.Fatalf("computePlan: %v", err)
			}
			if got := opLines(p); !reflect.DeepEqual(got, tc.ops) {
				t.Errorf("writes:\n  %q\nwant:\n  %q", got, tc.ops)
			}
			if p.Unchanged != tc.unchanged {
				t.Errorf("unchanged = %d, want %d", p.Unchanged, tc.unchanged)
			}
			if got := keptIDs(p); !reflect.DeepEqual(got, tc.kept) {
				t.Errorf("kept IDs = %v, want %v (%+v)", got, tc.kept, p.Kept)
			}
			if len(p.Creates)+len(p.Updates)+len(p.Deletes) != len(p.ops) {
				t.Errorf("the plan lists %d changes but makes %d writes", len(p.Creates)+len(p.Updates)+len(p.Deletes), len(p.ops))
			}
		})
	}
}

// TestComputePlan_Refusals pins the plans that are usage errors, refused
// before anything is sent.
func TestComputePlan_Refusals(t *testing.T) {
	cases := map[string]struct {
		desired []inputRecord
		live    []*coreapigo.Record
		want    string
	}{
		"a record listed twice": {
			desired: []inputRecord{want("A", "www", "192.0.2.1", 300), {Source: "line 9", Type: "A", Host: "WWW", Answer: "192.0.2.1", TTL: 600}},
			want:    "line 9 lists A WWW → 192.0.2.1 again (first at line 1)",
		},
		"a CAA record the zone lacks": {
			desired: []inputRecord{want("CAA", "@", `0 issue "letsencrypt.org"`, 300)},
			want:    "cannot create CAA records",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := computePlan("example.com", tc.desired, tc.live, planOptions{Prune: true})
			var ue *cmdutil.UsageError
			if !errors.As(err, &ue) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("computePlan error = %v, want a usage error containing %q", err, tc.want)
			}
		})
	}
}

// TestComputePlan_UpdateBodies pins the PUT an update sends: the whole
// record, since the endpoint replaces it, with the host and type as the API
// has them.
func TestComputePlan_UpdateBodies(t *testing.T) {
	p, err := computePlan("example.com",
		[]inputRecord{want("MX", "@", "mail.example.com", 3600)},
		[]*coreapigo.Record{liveRec(7, "MX", "", "mail.example.com", 300, 10)}, planOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ops) != 1 || p.ops[0].update == nil {
		t.Fatalf("want one update, got %q", opLines(p))
	}
	b := p.ops[0].update
	if b.ID != 7 || derefStr(b.Host) != "" || string(b.Type) != "MX" || b.Answer != "mail.example.com" ||
		derefInt64(b.TTL) != 3600 || derefInt64(b.Priority) != 10 {
		t.Errorf("update body = %+v (host %q, ttl %d, priority %v)", b, derefStr(b.Host), derefInt64(b.TTL), b.Priority)
	}
}
