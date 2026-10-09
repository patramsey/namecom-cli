package dns

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
)

// runSyncFile runs `dns sync example.com --file <file>` with the given
// flags against srv.
func runSyncFile(t *testing.T, srv *httptest.Server, o runOpts, file string, prune, pruneAll bool) (stdout, stderr string, err error) {
	t.Helper()
	cmd, out, errOut := commandFor(t, srv, o)
	syncFile, syncPrune, syncPruneAll = file, prune, pruneAll
	t.Cleanup(func() { syncFile, syncPrune, syncPruneAll = "", false, false })
	err = runSync(cmd, []string{"example.com"})
	return out.String(), errOut.String(), err
}

func prio(n int64) *int64 { return &n }

// syncFixture is a live zone and a zone file that differ in every way a plan
// can: one record to create, one to update, one to delete under --prune,
// one unchanged, and an apex NS --prune must leave alone.
func syncFixture(t *testing.T) (*fakeZone, *httptest.Server, string) {
	z, srv := newFakeZone(t,
		fakeRecord{ID: 1, Host: "", Type: "NS", Answer: "ns1.name.com", TTL: 300},
		fakeRecord{ID: 2, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300},
		fakeRecord{ID: 3, Host: "", Type: "MX", Answer: "mail.example.com", TTL: 300, Priority: prio(10)},
		fakeRecord{ID: 4, Host: "old", Type: "TXT", Answer: "remove me", TTL: 300},
	)
	file := writeFile(t, "example.com.zone", `$ORIGIN example.com.
$TTL 300
www   3600  IN A    192.0.2.1   ; TTL change: update
api         IN A    192.0.2.7   ; new: create
@           IN MX   10 mail     ; unchanged
`)
	return z, srv, file
}

// TestDNSSync_AppliesThenIsIdempotent runs a sync, then the same sync again:
// the first applies creates, updates and deletes in that order; the second
// finds nothing to do and sends nothing — which is what makes sync safe to
// re-run from a script or CI.
func TestDNSSync_AppliesThenIsIdempotent(t *testing.T) {
	z, srv, file := syncFixture(t)

	stdout, _, err := runSyncFile(t, srv, runOpts{yes: true}, file, true, false)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if got, want := z.writeLog(), []string{"POST", "PUT 2", "DELETE 4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("writes = %q, want %q (creates, then updates, then deletes)", got, want)
	}
	wantState := []string{
		"NS @ ns1.name.com 300",
		"A www 192.0.2.1 3600",
		"MX @ mail.example.com 300 prio=10",
		"A api 192.0.2.7 300",
	}
	if got := z.state(); !reflect.DeepEqual(got, wantState) {
		t.Errorf("zone after sync:\n  %q\nwant:\n  %q", got, wantState)
	}
	for _, s := range []string{
		"Plan for example.com: 1 create, 1 update, 1 delete, 1 unchanged",
		"create A api → 192.0.2.7",
		"update A www → 192.0.2.1 (TTL 300) [id 2]: ttl 300 → 3600",
		"delete TXT old → remove me",
		"keep   NS @ → ns1.name.com",
		"Synced example.com: created 1, updated 1, deleted 1",
	} {
		if !strings.Contains(stdout, s) {
			t.Errorf("output lacks %q:\n%s", s, stdout)
		}
	}

	// The same sync again, with no --yes and no terminal: nothing to change
	// means nothing to confirm, so it succeeds without asking.
	stdout, _, err = runSyncFile(t, srv, runOpts{}, file, true, false)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got := z.writeLog(); len(got) != 3 {
		t.Errorf("the second sync wrote again: %q", got[3:])
	}
	if !strings.Contains(stdout, "already matches the file") {
		t.Errorf("second sync should report no changes, got:\n%s", stdout)
	}
}

// TestDNSSync_DryRunJSON pins the --dry-run document: the plan, in camelCase,
// with the requests a real run sends under "data", as every multi-request dry
// run has them — and that those are the requests a real run does send, body
// for body.
func TestDNSSync_DryRunJSON(t *testing.T) {
	z, srv, file := syncFixture(t)
	stdout, _, err := runSyncFile(t, srv, runOpts{format: output.FormatJSON, dryRun: true}, file, true, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if w := z.writeLog(); len(w) != 0 {
		t.Fatalf("--dry-run sent writes: %q", w)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("dry run output is not one JSON document: %v\n%s", err, stdout)
	}
	for _, k := range []string{"domain", "dryRun", "prune", "pruneAll", "creates", "updates", "deletes", "unchanged", "kept", "data"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("plan document lacks %q: %s", k, stdout)
		}
	}
	if doc["dryRun"] != true || doc["unchanged"] != float64(1) {
		t.Errorf("dryRun/unchanged = %v/%v", doc["dryRun"], doc["unchanged"])
	}
	upd := doc["updates"].([]any)[0].(map[string]any)
	if upd["id"] != float64(2) || upd["ttl"] != float64(3600) || upd["before"].(map[string]any)["ttl"] != float64(300) {
		t.Errorf("update entry = %v", upd)
	}
	kept := doc["kept"].([]any)
	if len(kept) != 1 || !strings.Contains(kept[0].(map[string]any)["reason"].(string), "apex NS") {
		t.Errorf("kept = %v, want the apex NS with its reason", kept)
	}

	var previewed []string
	for _, r := range doc["data"].([]any) {
		m := r.(map[string]any)
		line := m["method"].(string) + " " + m["path"].(string)
		if b, ok := m["body"]; ok {
			enc, _ := json.Marshal(b)
			line += " " + canonical(string(enc))
		}
		previewed = append(previewed, line)
	}

	z2, srv2, _ := syncFixture(t)
	if _, _, err := runSyncFile(t, srv2, runOpts{yes: true}, file, true, false); err != nil {
		t.Fatalf("real run: %v", err)
	}
	if sent := z2.sentLog(); !reflect.DeepEqual(previewed, sent) {
		t.Errorf("--dry-run previews:\n  %q\nbut the real run sends:\n  %q", previewed, sent)
	}
}

// TestDNSSync_PruneSafety pins what each level of pruning may delete.
func TestDNSSync_PruneSafety(t *testing.T) {
	live := []fakeRecord{
		{ID: 1, Host: "", Type: "NS", Answer: "ns1.name.com", TTL: 300},
		{ID: 2, Host: "", Type: "CAA", Answer: `0 issue "letsencrypt.org"`, TTL: 300},
		{ID: 3, Host: "dev", Type: "NS", Answer: "ns1.elsewhere.net", TTL: 300},
		{ID: 4, Host: "stale", Type: "A", Answer: "192.0.2.9", TTL: 300},
		{ID: 5, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300},
	}
	const file = "www 300 IN A 192.0.2.1\n"
	cases := []struct {
		name            string
		prune, pruneAll bool
		deleted         []string
	}{
		{"without --prune nothing is deleted", false, false, nil},
		{"--prune spares apex NS and CAA", true, false, []string{"DELETE 3", "DELETE 4"}},
		{"--prune-all deletes them too", false, true, []string{"DELETE 1", "DELETE 2", "DELETE 3", "DELETE 4"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			z, srv := newFakeZone(t, live...)
			path := writeFile(t, "zone.txt", file)
			if _, _, err := runSyncFile(t, srv, runOpts{yes: true}, path, tc.prune, tc.pruneAll); err != nil {
				t.Fatalf("sync: %v", err)
			}
			if got := z.writeLog(); !reflect.DeepEqual(got, tc.deleted) {
				t.Errorf("writes = %q, want %q", got, tc.deleted)
			}
		})
	}
}

// TestDNSSync_PartialFailure pins the report when a write fails partway: it
// stops there, says what was applied and what was not, keeps the API error's
// exit code — and a re-run after the fix completes the rest without
// duplicating anything.
func TestDNSSync_PartialFailure(t *testing.T) {
	z, srv := newFakeZone(t, fakeRecord{ID: 1, Host: "gone", Type: "A", Answer: "192.0.2.9", TTL: 300})
	file := writeFile(t, "records.json", `[
	  {"type":"A","host":"one","answer":"192.0.2.1","ttl":300},
	  {"type":"A","host":"boom","answer":"192.0.2.2","ttl":300},
	  {"type":"A","host":"three","answer":"192.0.2.3","ttl":300}
	]`)
	z.fail = func(method string, r fakeRecord) bool { return method == "POST" && r.Host == "boom" }

	t.Run("table", func(t *testing.T) {
		_, stderr, err := runSyncFile(t, srv, runOpts{yes: true}, file, true, false)
		if err == nil {
			t.Fatal("expected the sync to fail")
		}
		var apiErr *api.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 422 {
			t.Errorf("the error should keep the API's 422 for its exit code, got %v", err)
		}
		for _, s := range []string{"create A boom", "change 2 of 4", "1 applied before it"} {
			if !strings.Contains(err.Error(), s) {
				t.Errorf("error lacks %q: %v", s, err)
			}
		}
		for _, s := range []string{"applied before the failure (created 1, updated 0, deleted 0)", "create A one", "2 changes not attempted"} {
			if !strings.Contains(stderr, s) {
				t.Errorf("stderr lacks %q:\n%s", s, stderr)
			}
		}
		if got, want := z.writeLog(), []string{"POST", "POST"}; !reflect.DeepEqual(got, want) {
			t.Errorf("writes = %q, want it to stop at the failure: %q", got, want)
		}
	})

	t.Run("json", func(t *testing.T) {
		z.writes = nil
		stdout, _, err := runSyncFile(t, srv, runOpts{yes: true, format: output.FormatJSON}, file, true, false)
		if err == nil {
			t.Fatal("expected the sync to fail")
		}
		var res struct {
			Applied []struct {
				Action, Host string
				ID           int
			} `json:"applied"`
			Failed *struct {
				Action, Host, Error string
			} `json:"failed"`
			NotAttempted []struct{ Action, Host string } `json:"notAttempted"`
		}
		if err := json.Unmarshal([]byte(stdout), &res); err != nil {
			t.Fatalf("result is not one JSON document: %v\n%s", err, stdout)
		}
		// "one" was created by the table run, so this plan starts at boom.
		if len(res.Applied) != 0 || res.Failed == nil || res.Failed.Host != "boom" || res.Failed.Action != "create" ||
			!strings.Contains(res.Failed.Error, "stub refused it") || len(res.NotAttempted) != 2 {
			t.Errorf("result = %+v", res)
		}
	})

	t.Run("re-run after the fix", func(t *testing.T) {
		z.fail, z.writes = nil, nil
		if _, _, err := runSyncFile(t, srv, runOpts{yes: true}, file, true, false); err != nil {
			t.Fatalf("re-run: %v", err)
		}
		want := []string{"A one 192.0.2.1 300", "A boom 192.0.2.2 300", "A three 192.0.2.3 300"}
		if got := z.state(); !reflect.DeepEqual(got, want) {
			t.Errorf("zone = %q, want %q", got, want)
		}
		if got, want := z.writeLog(), []string{"POST", "POST", "DELETE 1"}; !reflect.DeepEqual(got, want) {
			t.Errorf("re-run writes = %q, want only what was left: %q", got, want)
		}
	})
}

// TestDNSSync_Confirmation pins that the plan is confirmed once, with its
// counts in the question, and that a decline or a missing --yes sends nothing.
func TestDNSSync_Confirmation(t *testing.T) {
	t.Run("declined", func(t *testing.T) {
		z, srv, file := syncFixture(t)
		var asked string
		defer cmdutil.StubConfirm(func(p string) bool { asked = p; return false })()
		_, _, err := runSyncFile(t, srv, runOpts{}, file, true, false)
		if !errors.Is(err, cmdutil.ErrAborted) {
			t.Errorf("a decline should be ErrAborted, got %v", err)
		}
		if want := "Apply 3 changes to example.com (1 create, 1 update, 1 delete)?"; asked != want {
			t.Errorf("prompt = %q, want %q", asked, want)
		}
		if w := z.writeLog(); len(w) != 0 {
			t.Errorf("a declined sync wrote %q", w)
		}
	})
	t.Run("no terminal and no --yes", func(t *testing.T) {
		z, srv, file := syncFixture(t)
		_, _, err := runSyncFile(t, srv, runOpts{}, file, true, false)
		var ue *cmdutil.UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.UserHint(), "--yes") {
			t.Errorf("want the usage error asking for --yes, got %v", err)
		}
		if w := z.writeLog(); len(w) != 0 {
			t.Errorf("an unconfirmed sync wrote %q", w)
		}
	})
}

// TestDNSSync_InvalidFileSendsNothing pins that the file is validated in full
// before the zone is touched.
func TestDNSSync_InvalidFileSendsNothing(t *testing.T) {
	for name, content := range map[string]string{
		"bad address":     "www A 192.0.2.1\nbad A not-an-ip\n",
		"listed twice":    "www A 192.0.2.1\nwww 600 A 192.0.2.1\n",
		"CNAME at apex":   "@ CNAME other.example.net.\n",
		"TTL below 300":   "www 60 A 192.0.2.1\n",
		"CAA not in zone": "@ CAA 0 issue \"letsencrypt.org\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			z, srv := newFakeZone(t)
			_, _, err := runSyncFile(t, srv, runOpts{yes: true}, writeFile(t, "zone.txt", content), true, false)
			var ue *cmdutil.UsageError
			if !errors.As(err, &ue) {
				t.Errorf("want a usage error, got %v", err)
			}
			if w := z.writeLog(); len(w) != 0 {
				t.Errorf("wrote %q for an invalid file", w)
			}
		})
	}
}

// TestDNSSync_EmptyFileDoesNotPrune pins the guard against the likeliest
// way to wipe a zone: --prune with an empty file — the wrong path, or a
// failed export piped in.
func TestDNSSync_EmptyFileDoesNotPrune(t *testing.T) {
	for name, content := range map[string]string{"json": "[]", "zone": "$TTL 300\n; nothing\n"} {
		t.Run(name, func(t *testing.T) {
			z, srv := newFakeZone(t, fakeRecord{ID: 1, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300})
			_, _, err := runSyncFile(t, srv, runOpts{yes: true}, writeFile(t, "empty."+name, content), true, false)
			var ue *cmdutil.UsageError
			if !errors.As(err, &ue) || !strings.Contains(err.Error(), "lists no records") {
				t.Errorf("want a usage error refusing to prune, got %v", err)
			}
			if w := z.writeLog(); len(w) != 0 {
				t.Errorf("wrote %q", w)
			}
		})
	}
}

// TestDNSSync_NothingToChangeTSV: with nothing to change, -o tsv prints the
// result's rows alone. An empty plan's header row before them would read as
// one table (#268). And they say nothing changed: it was Success, which
// printed changed<TAB>true. The rows are the result's JSON keys, which
// --fields picks from; they were success/changed/message (#325).
func TestDNSSync_NothingToChangeTSV(t *testing.T) {
	_, srv := newFakeZone(t, fakeRecord{ID: 1, Host: "www", Type: "A", Answer: "192.0.2.1", TTL: 300})
	file := writeFile(t, "example.com.zone", "$ORIGIN example.com.\n$TTL 300\nwww IN A 192.0.2.1\n")
	stdout, _, err := runSyncFile(t, srv, runOpts{format: output.FormatTSV}, file, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := "domain\texample.com\nchanged\tfalse\napplied\t[]\nfailed\t\nnotAttempted\t\nunchanged\t1\n"; stdout != want {
		t.Errorf("got:\n%q\nwant %q", stdout, want)
	}
}

func TestDNSSync_UnknownDomainIsNotFound(t *testing.T) {
	_, srv := newFakeZone(t)
	cmd, _, _ := commandFor(t, srv, runOpts{yes: true})
	syncFile = writeFile(t, "zone.txt", "www A 192.0.2.1\n")
	t.Cleanup(func() { syncFile = "" })
	err := runSync(cmd, []string{"other.com"})
	if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), `domain "other.com" not found`) {
		t.Errorf("want a not-found error naming the domain, got %v", err)
	}
}
