package domain

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// recordingServer answers each "METHOD /path" in routes with its body (200),
// any other request with a 404, and returns the requests it saw.
func recordingServer(t *testing.T, routes map[string]string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		mu.Lock()
		seen = append(seen, key)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		body, ok := routes[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// TestSetNS_DryRunReadsTheDomain pins #362 item 1: `set-ns --dry-run` sent
// no request at all, so a domain not in the account was previewed with exit
// 0, the nameservers being replaced were never shown, and setting the ones
// already there previewed a change. A real run still sends the change alone
// (TestSetNS_NormalisesAndRefusesDuplicates).
func TestSetNS_DryRunReadsTheDomain(t *testing.T) {
	const current = `{"domainName":"example.com","nameservers":["NS1.name.com.","ns2.name.com"]}`
	run := func(t *testing.T, srv *httptest.Server, ns string) (string, string, error) {
		t.Helper()
		cmd := withRootFlags(t, cmdForSetNS(t, srv))
		if err := cmd.Root().PersistentFlags().Set("dry-run", "true"); err != nil {
			t.Fatal(err)
		}
		if err := cmd.ParseFlags([]string{"--ns", ns}); err != nil {
			t.Fatal(err)
		}
		err := runSetNS(cmd, []string{"example.com"})
		out := cmdutil.Out(cmd)
		return out.Writer.(*bytes.Buffer).String(), out.EWriter.(*bytes.Buffer).String(), err
	}

	t.Run("not in the account", func(t *testing.T) {
		srv, seen := recordingServer(t, nil)
		_, _, err := run(t, srv, "ns1.example.org,ns2.example.org")
		if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "example.com") {
			t.Fatalf("want not found naming the domain, got %v", err)
		}
		if got := strings.Join(seen(), " "); got != "GET /core/v1/domains/example.com" {
			t.Errorf("requests = %q, want the one read", got)
		}
	})

	t.Run("shows the nameservers replaced", func(t *testing.T) {
		srv, _ := recordingServer(t, map[string]string{"GET /core/v1/domains/example.com": current})
		stdout, stderr, err := run(t, srv, "ns1.example.org,ns2.example.org")
		if err != nil {
			t.Fatalf("runSetNS: %v", err)
		}
		if !strings.Contains(stderr, "example.com uses ns1.name.com, ns2.name.com now") {
			t.Errorf("want the current nameservers named, got stderr %q", stderr)
		}
		if !strings.Contains(stdout+stderr, ":setNameservers") {
			t.Errorf("want the preview, got %q %q", stdout, stderr)
		}
	})

	t.Run("nothing to change", func(t *testing.T) {
		srv, seen := recordingServer(t, map[string]string{"GET /core/v1/domains/example.com": current})
		// The same two, in another order and case: the registry does not
		// query them in order.
		stdout, stderr, err := run(t, srv, "ns2.name.com,ns1.name.com")
		if err != nil {
			t.Fatalf("runSetNS: %v", err)
		}
		if !strings.Contains(stdout, "already uses ns1.name.com, ns2.name.com; nothing to change") {
			t.Errorf("want nothing to change, got %q", stdout)
		}
		if strings.Contains(stdout+stderr, ":setNameservers") {
			t.Errorf("nothing should be previewed: %q %q", stdout, stderr)
		}
		if len(seen()) != 1 {
			t.Errorf("requests = %v, want the one read", seen())
		}
	})
}

// TestRegister_PriceOverrideDiffersFromQuote pins #362 item 2: `register
// --price 1 --dry-run` printed "Would charge: $1.00" for a $17.99 name with
// no word that the API refuses a purchase at another price.
func TestRegister_PriceOverrideDiffersFromQuote(t *testing.T) {
	defer output.StubInteractive(false)()
	routes := map[string]string{
		"POST /core/v1/domains:checkAvailability":    `{"results":[{"domainName":"zz-new.com","purchasable":true,"purchasePrice":17.99}]}`,
		"GET /core/v1/domains/zz-new.com:getPricing": `{"purchasePrice":17.99,"renewalPrice":17.99,"premium":false}`,
		"POST /core/v1/domaininfo/claims/zz-new.com": `{"domain":"zz-new.com"}`,
		"GET /core/v1/accountinfo/balance":           `{"balance":100}`,
	}
	for _, tc := range []struct {
		price string
		warn  bool
	}{{"1", true}, {"17.99", false}} {
		t.Run("--price "+tc.price, func(t *testing.T) {
			srv, _ := recordingServer(t, routes)
			cmd := withRootFlags(t, cmdForRegister(t, srv))
			t.Cleanup(func() { registerPrice = 0 })
			if err := cmd.Root().PersistentFlags().Set("dry-run", "true"); err != nil {
				t.Fatal(err)
			}
			if err := cmd.ParseFlags([]string{"--price", tc.price, "--years", "1"}); err != nil {
				t.Fatal(err)
			}
			if err := runRegister(cmd, []string{"zz-new.com"}); err != nil {
				t.Fatalf("runRegister: %v", err)
			}
			stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String()
			warned := strings.Contains(stderr, "--price $1.00 differs from the quoted price $17.99")
			quoted := strings.Contains(stderr, "quoted $17.99")
			if warned != tc.warn || quoted != tc.warn {
				t.Errorf("warned %v, quote shown %v, want %v:\n%s", warned, quoted, tc.warn, stderr)
			}
		})
	}
}

// TestRenew_DryRunShowsTheNewExpiry pins #362 item 2: the renew preview did
// not say where the renewal takes the expiry, nor warn of a term past ten
// years from today or a domain long expired.
func TestRenew_DryRunShowsTheNewExpiry(t *testing.T) {
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		expiry time.Time
		years  int
		want   []string
		absent []string
	}{
		{"within the limit", time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC), 2,
			[]string{"example.com expires 2028-01-01; renewing for 2 years runs it to 2030-01-01"},
			[]string{"refuse", "expired"}},
		{"past ten years", time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC), 9,
			[]string{"runs it to 2037-01-01", "more than 10 years from today"}, []string{"expired"}},
		{"expired", time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), 1,
			[]string{"example.com expired on 2024-03-01", "runs it to 2025-03-01"}, []string{"more than 10 years"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &stderr}
			previewRenewal(out, "example.com", &tc.expiry, tc.years, now)
			for _, w := range tc.want {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("want %q in:\n%s", w, stderr.String())
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(stderr.String(), a) {
					t.Errorf("did not want %q in:\n%s", a, stderr.String())
				}
			}
		})
	}

	t.Run("in the dry run", func(t *testing.T) {
		defer output.StubInteractive(false)()
		srv, _ := recordingServer(t, map[string]string{
			"GET /core/v1/domains/example.com":            `{"domainName":"example.com","expireDate":"2028-01-01T00:00:00Z"}`,
			"GET /core/v1/domains/example.com:getPricing": `{"renewalPrice":15,"premium":false}`,
			"GET /core/v1/accountinfo/balance":            `{"balance":100}`,
		})
		cmd := withRootFlags(t, cmdForRenew(t, srv))
		if err := cmd.Root().PersistentFlags().Set("dry-run", "true"); err != nil {
			t.Fatal(err)
		}
		if err := cmd.ParseFlags([]string{"--years", "10", "--price", "1"}); err != nil {
			t.Fatal(err)
		}
		if err := runRenew(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runRenew: %v", err)
		}
		stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String()
		for _, w := range []string{"runs it to 2038-01-01", "more than 10 years from today",
			"--price $1.00 differs from the quoted price $15.00", "quoted $15.00"} {
			if !strings.Contains(stderr, w) {
				t.Errorf("want %q in:\n%s", w, stderr)
			}
		}
	})
}

// TestUnknownTLDIsUsage pins #362 item 3: `domain list --tld zzzz` exits 2,
// but `domain requirements zzzz` (400 "Invalid Tld") and `domain register
// x.zzzz` exited 1, as an API failure.
func TestUnknownTLDIsUsage(t *testing.T) {
	isUsage := func(err error) bool {
		_, ok := errors.AsType[*cmdutil.UsageError](err)
		return ok
	}
	t.Run("requirements", func(t *testing.T) {
		srv, _ := jsonServer(t, http.StatusBadRequest, `{"message":"Invalid Tld"}`)
		err := runRequirements(cmdWithOutput(t, srv, tableOut(&bytes.Buffer{})), []string{"zzzz"})
		if !isUsage(err) || !strings.Contains(err.Error(), "zzzz") {
			t.Errorf("want a usage error naming the TLD, got %v", err)
		}
	})
	t.Run("requirements, another 400", func(t *testing.T) {
		srv, _ := jsonServer(t, http.StatusBadRequest, `{"message":"Bad Request"}`)
		if err := runRequirements(cmdWithOutput(t, srv, tableOut(&bytes.Buffer{})), []string{"fr"}); err == nil || isUsage(err) {
			t.Errorf("want the API error as it was, got %v", err)
		}
	})
	for name, check := range map[string]struct {
		status int
		body   string
	}{
		"register, a 422":             {http.StatusUnprocessableEntity, `{"message":"None of the submitted domains are valid"}`},
		"register, the name left out": {http.StatusOK, `{"results":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := jsonServer(t, check.status, check.body)
			cmd := withRootFlags(t, cmdForRegister(t, srv))
			if err := cmd.Root().PersistentFlags().Set("dry-run", "true"); err != nil {
				t.Fatal(err)
			}
			if err := cmd.ParseFlags([]string{"--years", "1"}); err != nil {
				t.Fatal(err)
			}
			err := runRegister(cmd, []string{"x.zzzz"})
			if !isUsage(err) || !strings.Contains(err.Error(), "x.zzzz") {
				t.Errorf("want a usage error naming the domain, got %v", err)
			}
		})
	}
}

// TestRequirements_HeaderNamesTheResolvedTLD pins #362 item 3: `domain
// requirements example.com` answers for com, and the header said
// "example.com".
func TestRequirements_HeaderNamesTheResolvedTLD(t *testing.T) {
	srv, _ := jsonServer(t, http.StatusOK, `{"tldInfo":{"tld":"com","allowedRegistrationYears":[1]},"requirements":{},"contacts":{}}`)
	var stdout bytes.Buffer
	out := tableOut(&stdout)
	if err := runRequirements(cmdWithOutput(t, srv, out), []string{"example.com"}); err != nil {
		t.Fatalf("runRequirements: %v", err)
	}
	first, _, _ := strings.Cut(stdout.String(), "\n")
	if strings.TrimSpace(first) != "com" {
		t.Errorf("header = %q, want com", first)
	}
	stderr := out.EWriter.(*bytes.Buffer).String()
	if !strings.Contains(stderr, "example.com is not a TLD") || !strings.Contains(stderr, "domain requirements com -o json") {
		t.Errorf("want the note and a hint naming com, got %q", stderr)
	}
}
