package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// sent is one request a bodyStub saw: "METHOD /path?query" and its body.
type sent struct {
	line, body string
}

// bodyStub is apiStub that keeps each request's body too.
func bodyStub(t *testing.T, overrides map[string]reply) (*httptest.Server, func() []sent) {
	t.Helper()
	var mu sync.Mutex
	var seen []sent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		line := r.Method + " " + r.URL.Path
		if q := r.URL.Query().Encode(); q != "" {
			line += "?" + q
		}
		mu.Lock()
		seen = append(seen, sent{line, string(body)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		rep, ok := overrides[r.Method+" "+r.URL.Path]
		if !ok {
			rep = defaultReply(r.Method, r.URL.Path, body)
		}
		if rep.status != 0 {
			w.WriteHeader(rep.status)
		}
		_, _ = w.Write([]byte(rep.body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []sent {
		mu.Lock()
		defer mu.Unlock()
		return append([]sent(nil), seen...)
	}
}

// run383 runs args against a bodyStub and returns what it printed and sent.
func run383(t *testing.T, routes map[string]reply, args ...string) (stdout, stderr string, code int, reqs []sent) {
	t.Helper()
	withConfig(t, loneProfile)
	resetFlags(t, args)
	srv, got := bodyStub(t, routes)
	stdout, stderr, code = runContract(t, append([]string{"--base-url", srv.URL}, args...)...)
	return stdout, stderr, code, got()
}

// sameJSON reports whether a and b are the same JSON document.
func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		t.Fatalf("not JSON: %q", a)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatalf("not JSON: %q", b)
	}
	bx, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(bx) == string(by)
}

// TestSearch_Flags pins #383 item 1: --tld, --purchase-type and
// --search-timeout go into Search's body, and a bad value is a usage error
// before any request.
func TestSearch_Flags(t *testing.T) {
	_, stderr, code, reqs := run383(t, nil, "domain", "search", "foo",
		"--tld", "com,IO", "--tld", ".dev", "--tld", "com", "--purchase-type", "Registration", "--search-timeout", "3s")
	if code != 0 || len(reqs) != 1 {
		t.Fatalf("exit %d, %d requests; stderr:\n%s", code, len(reqs), stderr)
	}
	want := `{"keyword":"foo","tldFilter":["com","io","dev"],"purchaseType":"registration","timeout":3000}`
	if !sameJSON(t, reqs[0].body, want) {
		t.Errorf("body %s, want %s", reqs[0].body, want)
	}

	// Without the flags the body is what it was.
	_, _, _, reqs = run383(t, nil, "domain", "search", "foo")
	if len(reqs) != 1 || !sameJSON(t, reqs[0].body, `{"keyword":"foo"}`) {
		t.Errorf("plain search sent %v", reqs)
	}

	many := make([]string, 51)
	for i := range many {
		many[i] = fmt.Sprintf("t%d", i)
	}
	for _, args := range [][]string{
		{"--purchase-type", "aftermarket"},
		{"--search-timeout", "100ms"},
		{"--search-timeout", "13s"},
		{"--search-timeout", "1500us"},
		{"--tld", "io,,com"},
		{"--tld", "c om"},
		{"--tld", strings.Join(many, ",")},
	} {
		_, stderr, code, reqs := run383(t, nil, append([]string{"domain", "search", "foo"}, args...)...)
		if code != 2 || len(reqs) != 0 {
			t.Errorf("%v: exit %d, %d requests, want 2 and none; stderr:\n%s", args, code, len(reqs), stderr)
		}
	}
}

// TestCheck_PurchaseType pins #383 item 6: --purchase-type goes into every
// CheckAvailability request check sends, and not into ZoneCheck's.
func TestCheck_PurchaseType(t *testing.T) {
	for _, args := range [][]string{
		{"domain", "check", "a.com", "--purchase-type", "registration"},
		{"domain", "check", "--authoritative", "a.com", "--purchase-type", "registration"},
	} {
		_, stderr, code, reqs := run383(t, nil, args...)
		if code != 0 || len(reqs) == 0 {
			t.Fatalf("%v: exit %d; stderr:\n%s", args, code, stderr)
		}
		for _, r := range reqs {
			has := strings.Contains(r.body, `"purchaseType":"registration"`)
			if strings.HasSuffix(r.line, "checkAvailability") != has {
				t.Errorf("%v: %s sent %s", args, r.line, r.body)
			}
		}
	}
	_, _, code, reqs := run383(t, nil, "domain", "check", "a.com", "--purchase-type", "bogus")
	if code != 2 || len(reqs) != 0 {
		t.Errorf("--purchase-type bogus: exit %d, %d requests", code, len(reqs))
	}
}

// TestDomainGet_Locks pins #383 item 2: the registry statuses are a row of
// the table, sanitised, only when there are some; TSV always has the row,
// last; JSON is unchanged.
func TestDomainGet_Locks(t *testing.T) {
	held := map[string]reply{"GET /core/v1/domains/example.com": {200,
		`{"domainName":"example.com","locked":true,"locks":["clientHold","clientTransferProhibited\u001b[31m"]}`}}
	stdout, stderr, code, _ := run383(t, held, "domain", "get", "example.com", "-o", "table")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Locks") || !strings.Contains(stdout, "clientHold, clientTransferProhibited") {
		t.Errorf("no Locks row:\n%s", stdout)
	}
	if strings.Contains(stdout, "\x1b[31m") {
		t.Errorf("an escape from the API reached the terminal:\n%q", stdout)
	}
	if strings.Index(stdout, "Locks") > strings.Index(stdout, "Privacy") {
		t.Errorf("Locks should come before Privacy, beside Locked:\n%s", stdout)
	}

	stdout, _, _, _ = run383(t, nil, "domain", "get", "example.com", "-o", "table")
	if strings.Contains(stdout, "Locks") {
		t.Errorf("a Locks row for a domain with none:\n%s", stdout)
	}

	stdout, _, _, _ = run383(t, held, "domain", "get", "example.com", "-o", "tsv")
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Locks\tclientHold, clientTransferProhibited") {
		t.Errorf("TSV's last row is %q, want Locks", last)
	}
	stdout, _, _, _ = run383(t, nil, "domain", "get", "example.com", "-o", "tsv")
	lines = strings.Split(strings.TrimSpace(stdout), "\n")
	if last := lines[len(lines)-1]; last != "Locks\t" && last != "Locks" {
		t.Errorf("TSV's last row is %q, want an empty Locks", last)
	}

	stdout, _, _, _ = run383(t, held, "domain", "get", "example.com", "-o", "json")
	if !strings.Contains(stdout, `"locks": [`) {
		t.Errorf("JSON lost locks:\n%s", stdout)
	}
}

// TestDomainList_Filters pins #383 item 3.
func TestDomainList_Filters(t *testing.T) {
	unlocked := map[string]reply{"GET /core/v1/domains": {200, `{"domains":[{"domainName":"example.com","locked":false,"autorenewEnabled":false}],"totalCount":1,"lastPage":1}`}}
	_, stderr, code, reqs := run383(t, unlocked, "domain", "list", "--unlocked", "--no-autorenew", "--privacy", "--premium",
		"--created-after", "2025-01-01", "--created-before", "2025-12-31")
	if code != 0 || len(reqs) != 1 {
		t.Fatalf("exit %d, %v; stderr:\n%s", code, reqs, stderr)
	}
	want := "GET /core/v1/domains?autorenewEnabled=false&createDateEnd=2026-01-01&createDateStart=2025-01-01" +
		"&isPremium=true&locked=false&page=1&privacyEnabled=true"
	if reqs[0].line != want {
		t.Errorf("sent\n %s\nwant\n %s", reqs[0].line, want)
	}

	// The second half of a pair, =false, is the first.
	_, _, _, reqs = run383(t, nil, "domain", "list", "--unlocked=false", "--premium=false")
	if len(reqs) != 1 || reqs[0].line != "GET /core/v1/domains?isPremium=false&locked=true&page=1" {
		t.Errorf("sent %v", reqs)
	}

	for _, args := range [][]string{
		{"--locked", "--unlocked"},
		{"--autorenew", "--no-autorenew=false"},
		{"--privacy", "--no-privacy"},
		{"--created-after", "2026-02-01", "--created-before", "2026-01-01"},
		{"--created-after", "01/02/2026"},
	} {
		_, stderr, code, reqs := run383(t, nil, append([]string{"domain", "list"}, args...)...)
		if code != 2 || len(reqs) != 0 {
			t.Errorf("%v: exit %d, %d requests, want 2 and none; stderr:\n%s", args, code, len(reqs), stderr)
		}
	}

	// -q walks every page, and every page carries the filters.
	paged := map[string]reply{"GET /core/v1/domains": {200, `{"domains":[{"domainName":"example.com","locked":false}],"totalCount":2,"nextPage":2,"lastPage":2}`}}
	_, stderr, code, reqs = run383(t, paged, "domain", "list", "-q", "--unlocked", "--created-after", "2025-01-01")
	if code != 0 || len(reqs) != 2 {
		t.Fatalf("-q: exit %d, %v; stderr:\n%s", code, reqs, stderr)
	}
	for _, r := range reqs {
		if !strings.Contains(r.line, "locked=false") || !strings.Contains(r.line, "createDateStart=2025-01-01") || !strings.Contains(r.line, "perPage=1000") {
			t.Errorf("-q page without the filters: %s", r.line)
		}
	}

	// A filter the reply shows was ignored fails rather than listing the
	// domains it should have left out.
	_, stderr, code, _ = run383(t, nil, "domain", "list", "--unlocked", "-q")
	if code != 1 || !strings.Contains(stderr, "--unlocked") {
		t.Errorf("ignored --unlocked: exit %d, stderr:\n%s", code, stderr)
	}
}

// TestStatus_PointsAtUnlocked pins #383 item 3's status hint.
func TestStatus_PointsAtUnlocked(t *testing.T) {
	stdout, stderr, _, _ := run383(t, nil, "status", "-o", "table")
	if !strings.Contains(stdout+stderr, "domain list --unlocked") {
		t.Errorf("status with an unlocked count does not point at domain list --unlocked:\n%s%s", stdout, stderr)
	}
	none := map[string]reply{"GET /core/v1/domains": {200, `{"domains":[],"totalCount":0}`}}
	stdout, stderr, _, _ = run383(t, none, "status", "-o", "table")
	if strings.Contains(stdout+stderr, "--unlocked") {
		t.Errorf("status with no unlocked domains points at --unlocked:\n%s%s", stdout, stderr)
	}
}

const stubTLDPrices = `{"pricing":[` +
	`{"tld":"com","duration":1,"registrationPrice":10.99,"renewalPrice":12.99,"transferInPrice":10.99,"domainRestorationPrice":80},` +
	`{"tld":"io","duration":1,"registrationPrice":39.99,"renewalPrice":44.99}` +
	`],"totalCount":2,"from":1,"to":2,"lastPage":1}`

// TestTLDPricing pins #383 item 4.
func TestTLDPricing(t *testing.T) {
	routes := map[string]reply{"GET /core/v1/tldpricing": {200, stubTLDPrices}}
	stdout, stderr, code, reqs := run383(t, routes, "domain", "tld-pricing", "--tld", ".COM,io", "--tld", "zzzz", "--years", "2", "-o", "table")
	if code != 0 || len(reqs) != 1 {
		t.Fatalf("exit %d, %v; stderr:\n%s", code, reqs, stderr)
	}
	// One tlds value, comma-joined: the API reads only the last of a
	// repeated key, so tlds=com&tlds=io priced io alone.
	if want := "GET /core/v1/tldpricing?duration=2&page=1&tlds=com%2Cio%2Czzzz"; reqs[0].line != want || strings.Count(reqs[0].line, "tlds=") != 1 {
		t.Errorf("sent %s, want %s", reqs[0].line, want)
	}
	for _, s := range []string{"TLD", "REGISTER", "$10.99", "$44.99", "$80.00", "—"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("table lacks %q:\n%s", s, stdout)
		}
	}
	if !strings.Contains(stderr, "no price for zzzz") || !strings.Contains(stderr, "2 years") {
		t.Errorf("stderr lacks the missing-TLD warning or the term:\n%s", stderr)
	}

	stdout, _, _, reqs = run383(t, routes, "domain", "tld-pricing", "-q")
	if stdout != "com\nio\n" || len(reqs) != 1 || reqs[0].line != "GET /core/v1/tldpricing?page=1&perPage=1000" {
		t.Errorf("-q printed %q, sent %v", stdout, reqs)
	}

	stdout, _, _, _ = run383(t, routes, "domain", "tld-pricing", "-o", "json")
	var doc struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || len(doc.Data) != 2 || doc.Data[0]["registrationPrice"] != 10.99 {
		t.Errorf("JSON: %v\n%s", err, stdout)
	}

	more := map[string]reply{"GET /core/v1/tldpricing": {200, `{"pricing":[{"tld":"com","duration":1}],"totalCount":800,"from":1,"to":25,"nextPage":2,"lastPage":32}`}}
	_, stderr, _, reqs = run383(t, more, "domain", "tld-pricing", "-o", "table")
	if len(reqs) != 1 || !strings.Contains(stderr, "--page 2") {
		t.Errorf("a page with more: %v\n%s", reqs, stderr)
	}

	many := make([]string, 26)
	for i := range many {
		many[i] = fmt.Sprintf("t%d", i)
	}
	for _, args := range [][]string{{"--years", "11"}, {"--years", "0"}, {"--tld", strings.Join(many, ",")}} {
		_, stderr, code, reqs := run383(t, routes, append([]string{"domain", "tld-pricing"}, args...)...)
		if code != 2 || len(reqs) != 0 {
			t.Errorf("%v: exit %d, %d requests; stderr:\n%s", args, code, len(reqs), stderr)
		}
	}
}

// TestRegister_NewBodyFields pins #383 item 5: --nameservers, --promo-code
// and --lock go into the body sent, and --lock only when given.
func TestRegister_NewBodyFields(t *testing.T) {
	last := func(reqs []sent) map[string]any {
		t.Helper()
		var body map[string]any
		r := reqs[len(reqs)-1]
		if r.line != "POST /core/v1/domains" {
			t.Fatalf("last request %s, want the create", r.line)
		}
		_ = json.Unmarshal([]byte(r.body), &body)
		return body
	}
	_, stderr, code, reqs := run383(t, nil, "domain", "register", "new.com", "--years", "1", "--yes",
		"--nameservers", "NS1.example.net.,ns2.example.net", "--nameservers", "ns3.example.net",
		"--promo-code", "SAVE10", "--lock=false")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	body := last(reqs)
	d, _ := body["domain"].(map[string]any)
	if fmt.Sprint(d["nameservers"]) != "[ns1.example.net ns2.example.net ns3.example.net]" || d["locked"] != false || body["promoCode"] != "SAVE10" {
		t.Errorf("body %v", body)
	}

	// The dry run previews them, and sends no create.
	stdout, _, code, reqs := run383(t, nil, "domain", "register", "new.com", "--years", "1", "--dry-run",
		"--nameservers", "ns1.example.net,ns2.example.net", "--promo-code", "SAVE10", "--lock")
	if code != 0 || !strings.Contains(stdout, `"promoCode": "SAVE10"`) || !strings.Contains(stdout, `"ns2.example.net"`) ||
		!strings.Contains(stdout, `"locked": true`) {
		t.Errorf("dry run: exit %d\n%s", code, stdout)
	}
	for _, r := range reqs {
		if r.line == "POST /core/v1/domains" {
			t.Errorf("the dry run sent the create")
		}
	}

	_, _, _, reqs = run383(t, nil, "domain", "register", "new.com", "--years", "1", "--yes")
	body = last(reqs)
	d, _ = body["domain"].(map[string]any)
	if _, ok := d["locked"]; ok {
		t.Errorf("locked sent without --lock: %v", body)
	}
	if _, ok := d["nameservers"]; ok {
		t.Errorf("nameservers sent without --nameservers: %v", body)
	}
	if _, ok := body["promoCode"]; ok {
		t.Errorf("promoCode sent without --promo-code: %v", body)
	}

	for _, args := range [][]string{
		{"--nameservers", "ns1.example.net,ns1.example.net"},
		{"--nameservers", "not a host"},
		{"--promo-code", " "},
	} {
		_, stderr, code, reqs := run383(t, nil, append([]string{"domain", "register", "new.com", "--yes"}, args...)...)
		if code != 2 || len(reqs) != 0 {
			t.Errorf("%v: exit %d, %d requests; stderr:\n%s", args, code, len(reqs), stderr)
		}
	}
}

// TestPricingYears_OrderType pins #383 item 6's other two flags.
func TestPricingYears_OrderType(t *testing.T) {
	stdout, stderr, code, reqs := run383(t, nil, "domain", "pricing", "example.com", "--years", "3", "-o", "table")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	found := false
	for _, r := range reqs {
		found = found || r.line == "GET /core/v1/domains/example.com:getPricing?years=3"
	}
	if !found || !strings.Contains(stdout+stderr, "for 3 years") {
		t.Errorf("sent %v; printed:\n%s%s", reqs, stdout, stderr)
	}
	// The API prices a transfer for one year whatever --years says, so the
	// row must not read as part of the 3-year total.
	if !strings.Contains(stdout, "Transfer (1 year)") {
		t.Errorf("transfer row not labelled one year:\n%s", stdout)
	}
	stdout, _, _, _ = run383(t, nil, "domain", "pricing", "example.com", "-o", "table")
	if strings.Contains(stdout, "(1 year)") {
		t.Errorf("transfer labelled without --years:\n%s", stdout)
	}
	stdout, _, _, _ = run383(t, nil, "domain", "pricing", "example.com", "--years", "3", "-o", "json")
	if !strings.Contains(stdout, `"transferPrice"`) {
		t.Errorf("JSON keys changed:\n%s", stdout)
	}
	if _, _, code, reqs := run383(t, nil, "domain", "pricing", "example.com", "--years", "11"); code != 2 || len(reqs) != 0 {
		t.Errorf("--years 11: exit %d, %d requests", code, len(reqs))
	}

	_, _, code, reqs = run383(t, nil, "order", "list", "--type", "Renewal")
	if code != 0 || len(reqs) != 1 || reqs[0].line != "GET /core/v1/orders?dir=desc&page=1&type=renewal" {
		t.Errorf("exit %d, sent %v", code, reqs)
	}
}
