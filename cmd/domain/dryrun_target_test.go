package domain

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestRenew_DryRunOfDomainNotInAccount pins #292: `domain renew X
// --dry-run` for a domain not in the account quoted "Would charge $19.99"
// and exited 0. The dry run reads the domain first and fails not_found, as
// the renewal would, without fetching a price. A real run does not add the
// read: the renewal itself refuses.
func TestRenew_DryRunOfDomainNotInAccount(t *testing.T) {
	defer output.StubInteractive(false)()

	t.Run("dry run", func(t *testing.T) {
		var requests []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/core/v1/accountinfo/balance" {
				_, _ = w.Write([]byte(`{"balance":120}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}))
		t.Cleanup(srv.Close)

		cmd := withRootFlags(t, cmdForRenew(t, srv))
		if err := cmd.Root().PersistentFlags().Set("dry-run", "true"); err != nil {
			t.Fatal(err)
		}
		err := runRenew(cmd, []string{"nonexistent-zz9.com"})
		if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "nonexistent-zz9.com") {
			t.Fatalf("want not found naming the domain, got %v", err)
		}
		var domainReads int
		for _, r := range requests {
			if strings.Contains(r, "getPricing") {
				t.Errorf("no price should be fetched for a domain not in the account: %v", requests)
			}
			if r == "GET /core/v1/domains/nonexistent-zz9.com" {
				domainReads++
			}
		}
		if domainReads != 1 {
			t.Errorf("want one read of the domain, got %v", requests)
		}
		if stdout := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String(); strings.Contains(stdout, "Would charge") {
			t.Errorf("no charge should be quoted: %s", stdout)
		}
	})

	t.Run("real run does not read the domain", func(t *testing.T) {
		var requests []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"premium":false,"renewalPrice":19.99,"order":1,"totalPaid":19.99}`))
		}))
		t.Cleanup(srv.Close)

		cmd := withRootFlags(t, cmdForRenew(t, srv))
		if err := cmd.Root().PersistentFlags().Set("yes", "true"); err != nil {
			t.Fatal(err)
		}
		if err := runRenew(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runRenew: %v", err)
		}
		want := "GET /core/v1/domains/example.com:getPricing POST /core/v1/domains/example.com:renew"
		if got := strings.Join(requests, " "); got != want {
			t.Errorf("requests = %q, want %q", got, want)
		}
	})
}

// TestSetNS_NormalisesAndRefusesDuplicates pins #292: `set-ns --ns
// NS1.Example.org.` was refused for its trailing dot, which vanity-ns
// accepts, and a nameserver listed twice was previewed and sent.
func TestSetNS_NormalisesAndRefusesDuplicates(t *testing.T) {
	t.Run("trailing dot and case", func(t *testing.T) {
		var body string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		}))
		t.Cleanup(srv.Close)

		cmd := withRootFlags(t, cmdForSetNS(t, srv))
		if err := cmd.Root().PersistentFlags().Set("yes", "true"); err != nil {
			t.Fatal(err)
		}
		if err := cmd.ParseFlags([]string{"--ns", "NS1.Example.org.,ns2.example.org"}); err != nil {
			t.Fatal(err)
		}
		if err := runSetNS(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runSetNS: %v", err)
		}
		if !strings.Contains(body, `"nameservers":["ns1.example.org","ns2.example.org"]`) {
			t.Errorf("want the nameservers lower-cased without the dot, got %s", body)
		}
	})

	for _, list := range []string{"ns1.example.org,ns1.example.org", "ns1.example.org,NS1.example.org."} {
		t.Run("duplicate "+list, func(t *testing.T) {
			cmd := withRootFlags(t, cmdForSetNS(t, neverCalledServer(t)))
			if err := cmd.Root().PersistentFlags().Set("dry-run", "true"); err != nil {
				t.Fatal(err)
			}
			if err := cmd.ParseFlags([]string{"--ns", list}); err != nil {
				t.Fatal(err)
			}
			err := runSetNS(cmd, []string{"example.com"})
			if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok {
				t.Errorf("want a usage error, got %v", err)
			}
		})
	}
}
