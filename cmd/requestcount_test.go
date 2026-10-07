package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// apiStub answers every endpoint the CLI calls with a plausible reply, for
// one account holding example.com and example.net, and records each request
// as "METHOD /path?query" with the query's keys sorted. A reply in overrides,
// keyed "METHOD /path", wins over the default.
func apiStub(t *testing.T, overrides map[string]reply) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		line := r.Method + " " + r.URL.Path
		if q := r.URL.Query().Encode(); q != "" {
			line += "?" + q
		}
		mu.Lock()
		seen = append(seen, line)
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
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// Fixtures for apiStub. example.com is locked, with auto-renew and privacy
// on; record 42 and 43 are on it.
func stubDomain(name string) string {
	return fmt.Sprintf(`{"domainName":%q,"locked":true,"autorenewEnabled":true,"privacyEnabled":true,"expireDate":"2030-01-01T00:00:00Z"}`, name)
}

func stubRecord(id int) string {
	return fmt.Sprintf(`{"id":%d,"domainName":"example.com","host":"www","fqdn":"www.example.com.","type":"A","answer":"192.0.2.%d","ttl":300}`, id, id)
}

const (
	stubPricing  = `{"purchasePrice":12.99,"renewalPrice":14.99,"transferPrice":9.99,"premium":false}`
	stubURLEntry = `{"id":7,"domainName":"example.com","host":"www.example.com","forwardsTo":"https://example.org","type":"redirect"}`
	stubDNSSEC   = `{"domainName":"example.com","keyTag":12345,"algorithm":8,"digestType":2,"digest":"abc123"}`
	stubVanity   = `{"domainName":"example.com","hostname":"ns1.example.com","ips":["192.0.2.1"]}`
	stubTransfer = `{"domainName":"example.org","email":"a@example.org","status":"pending_transfer"}`
	stubOrder    = `{"id":1,"orderItems":[{"id":2,"name":"example.com","productType":"domain","price":10,"refundable":true}]}`
	stubMailbox  = `{"domainName":"example.com","emailBox":"info","emailTo":"you@example.org"}`
)

// defaultReply is apiStub's answer to an endpoint no case overrides.
func defaultReply(method, path string, body []byte) reply {
	// The availability endpoints answer for the names they were asked about.
	var names struct {
		DomainNames []string `json:"domainNames"`
	}
	_ = json.Unmarshal(body, &names)
	echo := func(row string) reply {
		rows := make([]string, len(names.DomainNames))
		for i, n := range names.DomainNames {
			rows[i] = fmt.Sprintf(row, n)
		}
		return reply{200, `{"results":[` + strings.Join(rows, ",") + `]}`}
	}

	p := strings.TrimPrefix(path, "/core/v1")
	seg := strings.Split(strings.TrimPrefix(p, "/"), "/")
	switch {
	case p == "/hello":
		return reply{200, `{"username":"workuser"}`}
	case p == "/accountinfo/balance":
		return reply{200, `{"balance":100}`}
	case p == "/domains" && method == http.MethodGet:
		return reply{200, `{"domains":[` + stubDomain("example.com") + `],"totalCount":1,"from":1,"to":1,"lastPage":1}`}
	case p == "/domains" && method == http.MethodPost:
		return reply{200, `{"domain":` + stubDomain("new.com") + `,"order":1,"totalPaid":12.99}`}
	case p == "/domains:checkAvailability":
		return echo(`{"domainName":%q,"purchasable":true,"purchasePrice":12.99,"renewalPrice":14.99}`)
	case p == "/zonecheck":
		return echo(`{"domainName":%q,"available":true}`)
	case p == "/domains:search":
		return reply{200, `{"results":[{"domainName":"foo.com","purchasable":true,"purchasePrice":12.99}]}`}
	case strings.HasPrefix(p, "/domaininfo/claims/"):
		return reply{200, `{"domain":"` + seg[2] + `"}`}
	case strings.HasPrefix(p, "/domaininfo/requirements/"):
		return reply{200, `{"tld":"` + seg[2] + `"}`}
	case strings.HasSuffix(p, ":getPricing"):
		return reply{200, stubPricing}
	case strings.HasSuffix(p, ":getAuthCode"):
		return reply{200, `{"authCode":"abc-123"}`}
	case strings.HasSuffix(p, ":renew"):
		return reply{200, `{"domain":` + stubDomain("example.com") + `,"order":1,"totalPaid":14.99}`}
	case strings.HasSuffix(p, ":setNameservers"), strings.HasSuffix(p, ":setContacts"):
		return reply{200, stubDomain("example.com")}
	case seg[0] == "domains" && len(seg) == 2:
		return reply{200, stubDomain(seg[1])}
	case seg[0] == "domains" && len(seg) >= 3 && seg[2] == "records":
		switch {
		case len(seg) == 3 && method == http.MethodGet:
			return reply{200, `{"records":[` + stubRecord(42) + `,` + stubRecord(43) + `],"totalCount":2,"from":1,"to":2,"lastPage":1}`}
		case len(seg) == 3:
			return reply{200, stubRecord(44)}
		case method == http.MethodDelete:
			return reply{200, `{}`}
		case seg[3] == "42" || seg[3] == "43":
			return reply{200, stubRecord(map[string]int{"42": 42, "43": 43}[seg[3]])}
		}
		return reply{404, `{"message":"Not Found"}`}
	case seg[0] == "domains" && len(seg) >= 3 && seg[2] == "dnssec":
		if len(seg) == 3 && method == http.MethodGet {
			return reply{200, `{"dnssec":[` + stubDNSSEC + `],"totalCount":1}`}
		}
		if method == http.MethodDelete {
			return reply{200, `{}`}
		}
		return reply{200, stubDNSSEC}
	case seg[0] == "domains" && len(seg) >= 3 && seg[2] == "email":
		switch {
		case len(seg) == 4 && method == http.MethodGet:
			return reply{200, `{"emailForwarding":[` + stubMailbox + `],"totalCount":1,"lastPage":1}`}
		case method == http.MethodDelete:
			return reply{200, `{}`}
		case method == http.MethodGet:
			return reply{200, stubMailbox}
		}
		// A create or update stores what it was sent.
		return reply{200, string(body)}
	case seg[0] == "domains" && len(seg) >= 3 && seg[2] == "url":
		if method == http.MethodGet {
			return reply{200, `{"urlForwarding":[` + stubURLEntry + `],"totalCount":1,"lastPage":1}`}
		}
		return reply{200, stubURLEntry}
	case seg[0] == "urlforwarding":
		if method == http.MethodDelete {
			return reply{200, `{}`}
		}
		return reply{200, stubURLEntry}
	case seg[0] == "domains" && len(seg) >= 3 && seg[2] == "vanity_nameservers":
		switch {
		case len(seg) == 3 && method == http.MethodGet:
			return reply{200, `{"vanityNameservers":[` + stubVanity + `],"totalCount":1,"lastPage":1}`}
		case method == http.MethodDelete:
			return reply{200, `{}`}
		}
		return reply{200, stubVanity}
	case p == "/transfers" && method == http.MethodGet:
		return reply{200, `{"transfers":[` + stubTransfer + `],"totalCount":1,"lastPage":1}`}
	case p == "/transfers":
		return reply{200, `{"transfer":` + stubTransfer + `,"order":1,"totalPaid":9.99}`}
	case p == "/transfers/internal/in":
		return reply{200, stubDomain("example.org")}
	case strings.HasPrefix(p, "/transfers/eligibility/"):
		return reply{200, `{"domainName":"` + seg[2] + `","atName":true,"supportsInternalTransfer":true}`}
	case strings.HasPrefix(p, "/transfers/external/out/"):
		return reply{200, `{"domainName":"example.com","status":"canceled"}`}
	case strings.HasPrefix(p, "/transfers/") && strings.HasSuffix(p, ":cancel"):
		return reply{200, stubTransfer}
	case strings.HasPrefix(p, "/transfers/"):
		return reply{200, stubTransfer}
	case p == "/orders":
		return reply{200, `{"orders":[` + stubOrder + `],"totalCount":1,"lastPage":1}`}
	case strings.HasPrefix(p, "/orders/"):
		return reply{200, stubOrder}
	case p == "/refund":
		return reply{200, `{"results":[{"orderItemId":2,"orderItemStatus":"refunded"}],"totalRefundAmount":10}`}
	case p == "/contacts/unverified":
		return reply{200, `{"unverifiedContacts":[{"verificationId":9911,"email":"a@example.com","domains":["example.com"]}],"totalCount":1,"lastPage":1}`}
	case strings.HasSuffix(p, ":resend"):
		return reply{200, `{"verificationId":9911,"sent":true}`}
	}
	return reply{200, `{}`}
}

// runWithTerminal is runContract with IsInteractive answering terminal, and
// every confirmation answered yes.
func runWithTerminal(t *testing.T, terminal bool, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	if !terminal {
		return runContract(t, args...)
	}
	t.Cleanup(cmdutil.StubConfirm(func(string) bool { return true }))
	return runContractTTY(t, true, args...)
}

// requestCase is one row of TestRequestCounts.
type requestCase struct {
	args []string
	// terminal runs the command as if at a terminal, with every
	// confirmation answered yes.
	terminal bool
	// routes override apiStub's defaults.
	routes map[string]reply
	// code is the exit code expected, 0 by default.
	code int
	// want is every request sent, in order. Requests sent in parallel are
	// one step, written together(...), and may arrive in any order.
	want []string
	// why, when set, says why a request that could look avoidable is kept.
	why string
}

// TestRequestCounts pins the requests each command sends: method, path and
// query, in order (#294). It is the inventory of what every command costs
// against the API, and the guard that keeps a command from quietly gaining a
// request it does not need — a GET repeated, a prompt's pre-fetch made under
// --yes, a page walked at less than perPage 1000.
//
// A new command, or a new mode of an old one that changes what it sends,
// gets a row here. Where a request is a deliberate trade-off rather than a
// necessity, the row's why says so.
func TestRequestCounts(t *testing.T) {
	withConfig(t, loneProfile)

	dir := t.TempDir()
	recordsFile := filepath.Join(dir, "records.json")
	contactsFile := filepath.Join(dir, "contacts.json")
	for name, body := range map[string]string{
		recordsFile:  `[{"type":"A","host":"www","answer":"192.0.2.42","ttl":300},{"type":"A","host":"api","answer":"192.0.2.9","ttl":300}]`,
		contactsFile: `{"registrant":{"firstName":"A","lastName":"B","email":"a@example.com"}}`,
	} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	notFound := reply{404, `{"message":"Not Found"}`}
	// The end of status's expiry window, 30 days out.
	soon := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	unlocked := reply{200, `{"domainName":"example.com","locked":false,"autorenewEnabled":false,"privacyEnabled":false}`}

	cases := map[string]requestCase{
		// Account
		"status": {
			args: []string{"status"},
			why:  "five independent reads, sent together; the two counts ask for one domain and read totalCount",
			want: []string{together(
				"GET /core/v1/accountinfo/balance",
				"GET /core/v1/domains?expireDateEnd="+soon+"&page=1&perPage=1000",
				"GET /core/v1/domains?page=1&perPage=1",
				"GET /core/v1/domains?locked=false&page=1&perPage=1",
				"GET /core/v1/transfers?page=1&perPage=1000",
			)},
		},
		"status -q": {
			args: []string{"status", "-q"},
			want: []string{
				"GET /core/v1/domains?expireDateEnd=" + soon + "&page=1&perPage=1000",
			},
		},
		"auth status": {
			args: []string{"auth", "status"},
			want: []string{
				"GET /core/v1/hello",
			},
		},
		"config list-profiles": {args: []string{"config", "list-profiles"}},
		"api GET": {
			args: []string{"api", "/core/v1/hello"},
			want: []string{
				"GET /core/v1/hello",
			},
		},
		"api --paginate": {
			args: []string{"api", "/core/v1/orders", "--paginate"},
			want: []string{
				"GET /core/v1/orders?perPage=1000",
			},
		},

		// domain
		"domain list": {
			args: []string{"domain", "list"},
			want: []string{
				"GET /core/v1/domains?page=1",
			},
		},
		"domain list --all": {
			args: []string{"domain", "list", "--all"},
			want: []string{
				"GET /core/v1/domains?page=1&perPage=1000",
			},
		},
		"domain list --all, three pages": {
			args:   []string{"domain", "list", "--all", "--limit", "2"},
			routes: map[string]reply{"GET /core/v1/domains": {200, `{"domains":[` + stubDomain("example.com") + `],"totalCount":3,"nextPage":2,"lastPage":3}`}},
			why:    "page 1 says how many there are; the rest are fetched together, 1000 a page whatever --limit says",
			want: []string{
				"GET /core/v1/domains?page=1&perPage=1000",
				together("GET /core/v1/domains?page=2&perPage=1000", "GET /core/v1/domains?page=3&perPage=1000"),
			},
		},
		"domain list -q": {
			args: []string{"domain", "list", "-q"},
			want: []string{
				"GET /core/v1/domains?page=1&perPage=1000",
			},
		},
		"domain list --tld": {
			args: []string{"domain", "list", "--tld", "com"},
			want: []string{
				"GET /core/v1/domains?page=1&tld=com",
			},
		},
		"domain get": {
			args: []string{"domain", "get", "example.com"},
			want: []string{
				"GET /core/v1/domains/example.com",
			},
		},
		"domain get, two": {
			args: []string{"domain", "get", "example.com", "example.net"},
			want: []string{
				together("GET /core/v1/domains/example.com", "GET /core/v1/domains/example.net"),
			},
		},
		"domain search": {
			args: []string{"domain", "search", "foo"},
			want: []string{
				"POST /core/v1/domains:search",
			},
		},
		"domain check": {
			args: []string{"domain", "check", "a.com"},
			want: []string{
				"POST /core/v1/zonecheck",
				"POST /core/v1/domains:checkAvailability",
			},
		},
		"domain check, three": {
			args: []string{"domain", "check", "a.com", "b.com", "c.com"},
			why:  "ZoneCheck rules out the taken names; the rest go to the registry together, 50 a request, which prices them",
			want: []string{
				"POST /core/v1/zonecheck",
				"POST /core/v1/domains:checkAvailability",
			},
		},
		"domain check --authoritative": {
			args: []string{"domain", "check", "--authoritative", "a.com", "b.com"},
			want: []string{
				"POST /core/v1/domains:checkAvailability",
			},
		},
		"domain register --yes": {
			args: []string{"domain", "register", "new.com", "--years", "1", "--yes"},
			why:  "availability first, so a taken name costs nothing more; then, together, pricing for the premium and --max-price gates and the claims check TMCH requires",
			want: []string{
				"POST /core/v1/domains:checkAvailability",
				together("GET /core/v1/domains/new.com:getPricing?years=1", "POST /core/v1/domaininfo/claims/new.com"),
				"POST /core/v1/domains",
			},
		},
		"domain register --dry-run": {
			args: []string{"domain", "register", "new.com", "--years", "1", "--dry-run"},
			want: []string{
				"POST /core/v1/domains:checkAvailability",
				together(
					"GET /core/v1/domains/new.com:getPricing?years=1",
					"GET /core/v1/accountinfo/balance",
					"POST /core/v1/domaininfo/claims/new.com",
				),
			},
		},
		"domain register, prompted": {
			args:     []string{"domain", "register", "new.com", "--years", "1"},
			terminal: true,
			want: []string{
				"POST /core/v1/domains:checkAvailability",
				together(
					"GET /core/v1/domains/new.com:getPricing?years=1",
					"GET /core/v1/accountinfo/balance",
					"POST /core/v1/domaininfo/claims/new.com",
				),
				"POST /core/v1/domains",
			},
		},
		"domain renew --yes": {
			args: []string{"domain", "renew", "example.com", "--yes"},
			why:  "pricing for the premium and --max-price gates, which --yes does not skip",
			want: []string{
				"GET /core/v1/domains/example.com:getPricing?years=1",
				"POST /core/v1/domains/example.com:renew",
			},
		},
		"domain renew --dry-run": {
			args: []string{"domain", "renew", "example.com", "--dry-run"},
			why:  "the GET fails a dry run for a domain not in the account, as the renewal would (#292)",
			want: []string{
				"GET /core/v1/domains/example.com",
				together("GET /core/v1/domains/example.com:getPricing?years=1", "GET /core/v1/accountinfo/balance"),
			},
		},
		"domain renew, prompted": {
			args:     []string{"domain", "renew", "example.com"},
			terminal: true,
			want: []string{
				together("GET /core/v1/domains/example.com:getPricing?years=1", "GET /core/v1/accountinfo/balance"),
				"POST /core/v1/domains/example.com:renew",
			},
		},
		"domain pricing": {
			args: []string{"domain", "pricing", "example.com"},
			why:  "availability gives the aftermarket price pricing leaves out (#187)",
			want: []string{
				together("GET /core/v1/domains/example.com:getPricing", "POST /core/v1/domains:checkAvailability"),
			},
		},
		"domain auth-code": {
			args: []string{"domain", "auth-code", "example.com"},
			want: []string{
				"GET /core/v1/domains/example.com:getAuthCode",
			},
		},
		"domain claims": {
			args: []string{"domain", "claims", "example.com"},
			want: []string{
				"POST /core/v1/domaininfo/claims/example.com",
			},
		},
		"domain requirements": {
			args: []string{"domain", "requirements", "fr"},
			want: []string{
				"GET /core/v1/domaininfo/requirements/fr",
			},
		},
		"domain lock on, already": {
			args: []string{"domain", "lock", "on", "example.com", "--yes"},
			why:  "the GET finds the lock already on, so no PATCH; the API refuses restating it during the 60-day transfer lock (#187)",
			want: []string{
				"GET /core/v1/domains/example.com",
			},
		},
		"domain lock off": {
			args: []string{"domain", "lock", "off", "example.com", "--yes"},
			why:  "the GET skips a no-op PATCH and warns about the 60-day transfer lock (#287)",
			want: []string{
				"GET /core/v1/domains/example.com",
				"PATCH /core/v1/domains/example.com",
			},
		},
		"domain lock off, two": {
			args: []string{"domain", "lock", "off", "example.com", "example.net", "--yes"},
			want: []string{
				together("GET /core/v1/domains/example.com", "GET /core/v1/domains/example.net"),
				"PATCH /core/v1/domains/example.com",
				"PATCH /core/v1/domains/example.net",
			},
		},
		"domain autorenew off --dry-run": {
			args: []string{"domain", "autorenew", "off", "example.com", "--dry-run"},
			want: []string{
				"GET /core/v1/domains/example.com",
			},
		},
		"domain privacy on": {
			args:   []string{"domain", "privacy", "on", "example.com", "--yes"},
			routes: map[string]reply{"GET /core/v1/domains/example.com": unlocked},
			want: []string{
				"GET /core/v1/domains/example.com",
				"PATCH /core/v1/domains/example.com",
			},
		},
		"domain update --privacy=true": {
			args: []string{"domain", "update", "example.com", "--privacy=true", "--yes"},
			want: []string{
				"PATCH /core/v1/domains/example.com",
			},
		},
		"domain update --lock=false": {
			args: []string{"domain", "update", "example.com", "--lock=false", "--yes"},
			why:  "as the toggles: the GET leaves out a field already in that state (#287)",
			want: []string{
				"GET /core/v1/domains/example.com",
				"PATCH /core/v1/domains/example.com",
			},
		},
		"domain set-ns": {
			args: []string{"domain", "set-ns", "example.com", "--ns", "ns1.name.com,ns2.name.com", "--yes"},
			want: []string{
				"POST /core/v1/domains/example.com:setNameservers",
			},
		},
		"domain contacts get": {
			args: []string{"domain", "contacts", "get", "example.com"},
			want: []string{
				"GET /core/v1/domains/example.com",
			},
		},
		"domain contacts set": {
			args: []string{"domain", "contacts", "set", "example.com", "--contacts-file", contactsFile, "--yes"},
			want: []string{
				"POST /core/v1/domains/example.com:setContacts",
			},
		},

		// dns
		"dns list": {
			args: []string{"dns", "list", "example.com"},
			want: []string{
				"GET /core/v1/domains/example.com/records?page=1",
			},
		},
		"dns list --all": {
			args: []string{"dns", "list", "example.com", "--all"},
			want: []string{
				"GET /core/v1/domains/example.com/records?page=1&perPage=1000",
			},
		},
		"dns list -q": {
			args: []string{"dns", "list", "example.com", "-q"},
			want: []string{
				"GET /core/v1/domains/example.com/records?page=1&perPage=1000",
			},
		},
		"dns create": {
			args: []string{"dns", "create", "example.com", "--type", "A", "--host", "api", "--answer", "192.0.2.9", "--yes"},
			want: []string{
				"POST /core/v1/domains/example.com/records",
			},
		},
		"dns create --if-not-exists": {
			args: []string{"dns", "create", "example.com", "--type", "A", "--host", "api", "--answer", "192.0.2.9", "--if-not-exists", "--yes"},
			why:  "the whole zone, once, at perPage 1000",
			want: []string{
				"GET /core/v1/domains/example.com/records?page=1&perPage=1000",
				"POST /core/v1/domains/example.com/records",
			},
		},
		"dns update": {
			args: []string{"dns", "update", "example.com", "42", "--ttl", "600", "--yes"},
			why:  "read-modify-write: the PUT replaces the whole record",
			want: []string{
				"GET /core/v1/domains/example.com/records/42",
				"PUT /core/v1/domains/example.com/records/42",
			},
		},
		"dns update, no change": {
			args: []string{"dns", "update", "example.com", "42", "--ttl", "300", "--yes"},
			want: []string{
				"GET /core/v1/domains/example.com/records/42",
			},
		},
		"dns delete": {
			args: []string{"dns", "delete", "example.com", "42", "--yes"},
			why:  "the GET shows the record in the prompt and checks every ID before deleting any (#235)",
			want: []string{
				"GET /core/v1/domains/example.com/records/42",
				"DELETE /core/v1/domains/example.com/records/42",
			},
		},
		"dns delete, two": {
			args: []string{"dns", "delete", "example.com", "42", "43", "--yes"},
			want: []string{
				together("GET /core/v1/domains/example.com/records/42", "GET /core/v1/domains/example.com/records/43"),
				"DELETE /core/v1/domains/example.com/records/42",
				"DELETE /core/v1/domains/example.com/records/43",
			},
		},
		"dns delete --if-exists, gone": {
			args: []string{"dns", "delete", "example.com", "99", "--if-exists", "--yes"},
			why:  "a one-record list tells a missing record from a missing domain",
			want: []string{
				"GET /core/v1/domains/example.com/records/99",
				"GET /core/v1/domains/example.com/records?page=1&perPage=1",
			},
		},
		"dns export": {
			args: []string{"dns", "export", "example.com"},
			want: []string{
				"GET /core/v1/domains/example.com/records?page=1&perPage=1000",
			},
		},
		"dns import": {
			args: []string{"dns", "import", "example.com", "--file", recordsFile, "--yes"},
			want: []string{
				"POST /core/v1/domains/example.com/records",
				"POST /core/v1/domains/example.com/records",
			},
		},
		"dns import --skip-existing": {
			args: []string{"dns", "import", "example.com", "--file", recordsFile, "--skip-existing", "--yes"},
			why:  "the whole zone, once, at perPage 1000",
			want: []string{
				"GET /core/v1/domains/example.com/records?page=1&perPage=1000",
				"POST /core/v1/domains/example.com/records",
			},
		},
		"dns sync": {
			args: []string{"dns", "sync", "example.com", "--file", recordsFile, "--yes"},
			why:  "the whole zone, once, at perPage 1000",
			want: []string{
				"GET /core/v1/domains/example.com/records?page=1&perPage=1000",
				"POST /core/v1/domains/example.com/records",
			},
		},
		"dns sync --dry-run": {
			args: []string{"dns", "sync", "example.com", "--file", recordsFile, "--dry-run"},
			want: []string{
				"GET /core/v1/domains/example.com/records?page=1&perPage=1000",
			},
		},

		// dnssec
		"dnssec list": {
			args: []string{"dnssec", "list", "example.com"},
			want: []string{
				"GET /core/v1/domains/example.com/dnssec",
			},
		},
		"dnssec get": {
			args: []string{"dnssec", "get", "example.com", "abc123"},
			want: []string{
				"GET /core/v1/domains/example.com/dnssec/abc123",
			},
		},
		"dnssec create": {
			args: []string{"dnssec", "create", "example.com", "--algorithm", "8", "--digest-type", "2", "--key-tag", "12345", "--digest", strings.Repeat("ab", 32), "--yes"},
			want: []string{
				"POST /core/v1/domains/example.com/dnssec",
			},
		},
		"dnssec delete": {
			args: []string{"dnssec", "delete", "example.com", "abc123", "--yes"},
			want: []string{
				"DELETE /core/v1/domains/example.com/dnssec/abc123",
			},
		},
		"dnssec delete --dry-run": {
			args: []string{"dnssec", "delete", "example.com", "abc123", "--dry-run"},
			why:  "the GET fails a dry run for a missing key, as the delete would (#292)",
			want: []string{
				"GET /core/v1/domains/example.com/dnssec/abc123",
			},
		},

		// email
		"email list": {
			args: []string{"email", "list", "example.com"},
			want: []string{
				"GET /core/v1/domains/example.com/email/forwarding?page=1",
			},
		},
		"email get": {
			args: []string{"email", "get", "example.com", "info"},
			want: []string{
				"GET /core/v1/domains/example.com/email/forwarding/info",
			},
		},
		"email create": {
			args: []string{"email", "create", "example.com", "info", "--to", "you@example.org", "--yes"},
			want: []string{
				"POST /core/v1/domains/example.com/email/forwarding",
			},
		},
		"email update": {
			args: []string{"email", "update", "example.com", "info", "--to", "new@example.org", "--yes"},
			want: []string{
				"PUT /core/v1/domains/example.com/email/forwarding/info",
			},
		},
		"email delete": {
			args: []string{"email", "delete", "example.com", "info", "--yes"},
			want: []string{
				"DELETE /core/v1/domains/example.com/email/forwarding/info",
			},
		},
		"email delete, prompted": {
			args:     []string{"email", "delete", "example.com", "info"},
			why:      "the GET only words the prompt; it is skipped without one",
			terminal: true,
			want: []string{
				"GET /core/v1/domains/example.com/email/forwarding/info",
				"DELETE /core/v1/domains/example.com/email/forwarding/info",
			},
		},

		// url
		"url list": {
			args: []string{"url", "list", "example.com"},
			want: []string{
				"GET /core/v1/urlforwarding/example.com?page=1",
			},
		},
		"url get": {
			args: []string{"url", "get", "example.com", "7"},
			want: []string{
				"GET /core/v1/urlforwarding/example.com/7",
			},
		},
		"url create": {
			args: []string{"url", "create", "example.com", "--to", "https://example.org", "--yes"},
			want: []string{
				"POST /core/v1/domains/example.com/url/forwarding",
			},
		},
		"url update": {
			args: []string{"url", "update", "example.com", "7", "--to", "https://example.net", "--yes"},
			why:  "read-modify-write: unset flags keep the current values",
			want: []string{
				"GET /core/v1/urlforwarding/example.com/7",
				"PATCH /core/v1/urlforwarding/example.com/7",
			},
		},
		"url delete": {
			args: []string{"url", "delete", "example.com", "7", "--yes"},
			why:  "the GET shows the forwarding in the prompt and decides the apex A-record note after the delete (#286)",
			want: []string{
				"GET /core/v1/urlforwarding/example.com/7",
				"DELETE /core/v1/urlforwarding/example.com/7",
			},
		},

		// vanity-ns
		"vanity-ns list": {
			args: []string{"vanity-ns", "list", "example.com"},
			want: []string{
				"GET /core/v1/domains/example.com/vanity_nameservers?page=1",
			},
		},
		"vanity-ns get": {
			args: []string{"vanity-ns", "get", "example.com", "ns1.example.com"},
			want: []string{
				"GET /core/v1/domains/example.com/vanity_nameservers/ns1.example.com",
			},
		},
		"vanity-ns create": {
			args: []string{"vanity-ns", "create", "example.com", "--hostname", "ns1.example.com", "--ips", "192.0.2.1", "--yes"},
			want: []string{
				"POST /core/v1/domains/example.com/vanity_nameservers",
			},
		},
		"vanity-ns update": {
			args: []string{"vanity-ns", "update", "example.com", "ns1.example.com", "--ips", "192.0.2.2", "--yes"},
			want: []string{
				"PUT /core/v1/domains/example.com/vanity_nameservers/ns1.example.com",
			},
		},
		"vanity-ns update --dry-run": {
			args: []string{"vanity-ns", "update", "example.com", "ns1.example.com", "--ips", "192.0.2.2", "--dry-run"},
			why:  "the GET fails a dry run for a missing nameserver, as the update would (#292)",
			want: []string{
				"GET /core/v1/domains/example.com/vanity_nameservers/ns1.example.com",
			},
		},
		"vanity-ns delete": {
			args: []string{"vanity-ns", "delete", "example.com", "ns1.example.com", "--yes"},
			want: []string{
				"DELETE /core/v1/domains/example.com/vanity_nameservers/ns1.example.com",
			},
		},

		// transfer
		"transfer list": {
			args: []string{"transfer", "list"},
			want: []string{
				"GET /core/v1/transfers?page=1",
			},
		},
		"transfer get": {
			args: []string{"transfer", "get", "example.org"},
			want: []string{
				"GET /core/v1/transfers/example.org",
			},
		},
		"transfer create": {
			args: []string{"transfer", "create", "example.org", "--auth-code", "XYZ123", "--yes"},
			why:  "pricing for the premium and --max-price gates, which --yes does not skip",
			want: []string{
				"GET /core/v1/domains/example.org:getPricing",
				"POST /core/v1/transfers",
			},
		},
		"transfer create --dry-run": {
			args:   []string{"transfer", "create", "example.org", "--auth-code", "XYZ123", "--dry-run"},
			why:    "the GET fails a dry run for a domain already in the account (#292)",
			routes: map[string]reply{"GET /core/v1/domains/example.org": notFound},
			want: []string{
				"GET /core/v1/domains/example.org",
				together("GET /core/v1/domains/example.org:getPricing", "GET /core/v1/accountinfo/balance"),
			},
		},
		"transfer internal-in": {
			args: []string{"transfer", "internal-in", "example.org", "--auth-code", "XYZ123", "--yes"},
			want: []string{
				"POST /core/v1/transfers/internal/in",
			},
		},
		"transfer cancel": {
			args: []string{"transfer", "cancel", "example.org", "--yes"},
			want: []string{
				"POST /core/v1/transfers/example.org:cancel",
			},
		},
		"transfer cancel --dry-run": {
			args: []string{"transfer", "cancel", "example.org", "--dry-run"},
			why:  "the GET fails a dry run for a missing transfer, as the cancel would",
			want: []string{
				"GET /core/v1/transfers/example.org",
			},
		},
		"transfer cancel, no terminal": {
			args: []string{"transfer", "cancel", "example.org"},
			code: 2,
		},
		"transfer cancel, prompted": {
			args:     []string{"transfer", "cancel", "example.org"},
			terminal: true,
			want: []string{
				"GET /core/v1/transfers/example.org",
				"POST /core/v1/transfers/example.org:cancel",
			},
		},
		"transfer cancel-outbound": {
			args: []string{"transfer", "cancel-outbound", "example.com", "--yes"},
			want: []string{
				"POST /core/v1/transfers/external/out/example.com:cancel",
			},
		},
		"transfer eligibility": {
			args: []string{"transfer", "eligibility", "example.com"},
			want: []string{
				"GET /core/v1/transfers/eligibility/example.com",
			},
		},
		"transfer eligibility -o table": {
			args: []string{"transfer", "eligibility", "example.com", "-o", "table"},
			why:  "only a table asks whether the domain is in this account, for its hint (#293)",
			want: []string{
				"GET /core/v1/transfers/eligibility/example.com",
				"GET /core/v1/domains/example.com",
			},
		},

		// order
		"order list": {
			args: []string{"order", "list"},
			want: []string{
				"GET /core/v1/orders?dir=desc&page=1",
			},
		},
		"order list -q": {
			args: []string{"order", "list", "-q"},
			want: []string{
				"GET /core/v1/orders?dir=desc&page=1&perPage=1000",
			},
		},
		"order get": {
			args: []string{"order", "get", "1"},
			want: []string{
				"GET /core/v1/orders/1",
			},
		},
		"order refund": {
			args: []string{"order", "refund", "--order-id", "1", "--item-ids", "2", "--yes"},
			want: []string{
				"POST /core/v1/refund",
			},
		},
		"order refund, no terminal": {
			args: []string{"order", "refund", "--order-id", "1", "--item-ids", "2"},
			code: 2,
		},
		"order refund, prompted": {
			args:     []string{"order", "refund", "--order-id", "1", "--item-ids", "2"},
			why:      "the GET only words the prompt; it is skipped without one",
			terminal: true,
			want: []string{
				"GET /core/v1/orders/1",
				"POST /core/v1/refund",
			},
		},

		// contact
		"contact unverified": {
			args: []string{"contact", "unverified"},
			want: []string{
				"GET /core/v1/contacts/unverified?page=1",
			},
		},
		"contact resend": {
			args: []string{"contact", "resend", "9911", "--yes"},
			want: []string{
				"POST /core/v1/contacts/verify/9911:resend",
			},
		},
		"contact verify": {
			args: []string{"contact", "verify", "9911", "--yes"},
			want: []string{
				"POST /core/v1/contacts/verify/9911",
			},
		},

		// Shell completion: one request per TAB.
		"complete a domain": {
			args: []string{"__complete", "domain", "get", "exa"},
			why:  "one filtered page while the shell waits; 250 rather than 1000 keeps it small",
			want: []string{
				"GET /core/v1/domains?domainName=%2Aexa%2A&page=1&perPage=250",
			},
		},
		"complete a record ID": {
			args: []string{"__complete", "dns", "delete", "example.com", ""},
			want: []string{
				"GET /core/v1/domains/example.com/records?page=1&perPage=1000",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// cobra adds __complete only once it runs; the command it
			// completes for is the one with flags to reset.
			resetFlags(t, slices.DeleteFunc(slices.Clone(tc.args), func(a string) bool { return a == "__complete" }))
			srv, requests := apiStub(t, tc.routes)
			_, stderr, code := runWithTerminal(t, tc.terminal, append([]string{"--base-url", srv.URL}, tc.args...)...)
			if code != tc.code {
				t.Errorf("exit %d, want %d; stderr:\n%s", code, tc.code, stderr)
			}
			if got := requests(); !matchSteps(got, tc.want) {
				t.Errorf("namecom %s sent %d requests, want %d:\n got: %#v\nwant: %#v",
					strings.Join(tc.args, " "), len(got), stepCount(tc.want), got, tc.want)
				if tc.why != "" {
					t.Logf("the requests wanted are a deliberate trade-off: %s", tc.why)
				}
			}
		})
	}
}

// together is a step of requests a command sends in parallel, so in no fixed
// order.
func together(requests ...string) string { return strings.Join(requests, " | ") }

// stepCount is how many requests steps holds.
func stepCount(steps []string) int {
	n := 0
	for _, s := range steps {
		n += len(strings.Split(s, " | "))
	}
	return n
}

// matchSteps reports whether got is steps in order, each step's requests in
// any order.
func matchSteps(got, steps []string) bool {
	if len(got) != stepCount(steps) {
		return false
	}
	for _, s := range steps {
		step := strings.Split(s, " | ")
		n := len(step)
		if !slices.Equal(slices.Sorted(slices.Values(got[:n])), slices.Sorted(slices.Values(step))) {
			return false
		}
		got = got[n:]
	}
	return true
}
