package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/drifttest"
	"github.com/spf13/cobra"
)

// notInAccount answers the two reads `transfer create --dry-run` makes before
// pricing: GET /core/v1/domains/<name>, to see whether the domain is already
// in the account, with a 404; and the availability check, to see whether it
// is registered at all, with a name that is taken. It passes every other
// request to srv. The stubs the transfer tests share answer everything with
// one document, which would read as the domain being in the account.
func notInAccount(t *testing.T, srv *httptest.Server) *httptest.Server {
	t.Helper()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/core/v1/domains/") &&
			!strings.ContainsAny(strings.TrimPrefix(r.URL.Path, "/core/v1/domains/"), "/:") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, ":checkAvailability") {
			var req struct {
				DomainNames []string `json:"domainNames"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			name := ""
			if len(req.DomainNames) > 0 {
				name = req.DomainNames[0]
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"results":[{"domainName":%q,"purchasable":false}]}`, name)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	return front
}

// TestCreate_DryRunOfDomainAlreadyInAccount pins #292: `transfer create
// --dry-run` for a domain already in this account quoted the transfer fee
// and exited 0. The dry run reads the domain and refuses with a usage error
// before any pricing; a real run does not add the read.
func TestCreate_DryRunOfDomainAlreadyInAccount(t *testing.T) {
	t.Run("dry run", func(t *testing.T) {
		var requests []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"domainName":"example.com","locked":true}`))
		}))
		t.Cleanup(srv.Close)

		// Straight at srv, not through notInAccount: the domain is found.
		cmd := direct(t, cmdForTransferCreate(t, srv), srv)
		if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123"}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { createAuthCode = "" })
		cmd = drifttest.WithDryRun(t, cmd, true)
		err := runCreate(cmd, []string{"example.com"})
		if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok || !strings.Contains(err.Error(), "already in this account") {
			t.Fatalf("want a usage error saying it is already in the account, got %v", err)
		}
		if got := strings.Join(requests, " "); got != "GET /core/v1/domains/example.com" {
			t.Errorf("requests = %q, want the one read", got)
		}
		if strings.Contains(cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String(), "Would charge") {
			t.Error("no charge should be quoted")
		}
	})

	t.Run("real run does not read the domain", func(t *testing.T) {
		var requests []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"transferPrice":12.99,"order":1,"totalPaid":12.99}`))
		}))
		t.Cleanup(srv.Close)

		cmd := direct(t, cmdForTransferCreate(t, srv), srv)
		if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123", "--yes"}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { createAuthCode = "" })
		if err := runCreate(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runCreate: %v", err)
		}
		want := "GET /core/v1/domains/example.com:getPricing POST /core/v1/transfers"
		if got := strings.Join(requests, " "); got != want {
			t.Errorf("requests = %q, want %q", got, want)
		}
	})
}

// direct points cmd's client straight at srv, bypassing notInAccount.
func direct(t *testing.T, cmd *cobra.Command, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.WithValue(cmd.Context(), cmdutil.KeyClient, client))
	return cmd
}

// TestCancelOutboundHelp_SaysWhatDryRunChecks pins #326: a dry run of
// cancel-outbound for a domain in the account but not leaving it previews
// the cancel, since no endpoint reports an outbound transfer. The help says
// so.
func TestCancelOutboundHelp_SaysWhatDryRunChecks(t *testing.T) {
	if !strings.Contains(cancelOutboundCmd.Long, "but not that a transfer\nout is pending") {
		t.Errorf("help does not say what --dry-run leaves unchecked:\n%s", cancelOutboundCmd.Long)
	}
}

// TestCreate_DryRunOfUnregisteredName pins #362: `transfer create --dry-run`
// quoted a transfer for a name nobody has registered, which `domain check`
// calls available. The dry run asks the availability check, and a name it
// offers as a plain registration fails before any pricing, pointing at
// `domain register`. One offered as an aftermarket purchase is registered to
// someone, so its preview goes on; so does one the check cannot answer.
func TestCreate_DryRunOfUnregisteredName(t *testing.T) {
	for _, tc := range []struct {
		name, check string
		status      int
		refused     bool
	}{
		{"available", `{"results":[{"domainName":"free-zz9.com","purchasable":true,"purchasePrice":12.99}]}`, 200, true},
		{"available, registration type", `{"results":[{"domainName":"free-zz9.com","purchasable":true,"purchaseType":"registration"}]}`, 200, true},
		{"aftermarket", `{"results":[{"domainName":"free-zz9.com","purchasable":true,"purchaseType":"aftermarket_s"}]}`, 200, false},
		{"taken", `{"results":[{"domainName":"free-zz9.com","purchasable":false}]}`, 200, false},
		{"check failed", `{"message":"Server error"}`, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mu       sync.Mutex
				requests []string
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, ":checkAvailability"):
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.check))
				case strings.HasSuffix(r.URL.Path, ":getPricing"):
					_, _ = w.Write([]byte(`{"transferPrice":9.99}`))
				case r.URL.Path == "/core/v1/accountinfo/balance":
					_, _ = w.Write([]byte(`{"balance":100}`))
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				}
			}))
			t.Cleanup(srv.Close)

			cmd := direct(t, cmdForTransferCreate(t, srv), srv)
			if err := cmd.ParseFlags([]string{"--auth-code", "AUTH123"}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { createAuthCode = "" })
			cmd = drifttest.WithDryRun(t, cmd, true)
			err := runCreate(cmd, []string{"free-zz9.com"})
			mu.Lock()
			defer mu.Unlock()
			priced := slices.ContainsFunc(requests, func(r string) bool { return strings.HasSuffix(r, ":getPricing") })
			if !tc.refused {
				if err != nil || !priced {
					t.Fatalf("want the preview, got %v after %v", err, requests)
				}
				return
			}
			u, ok := errors.AsType[*cmdutil.UnavailableError](err)
			if !ok || !strings.Contains(err.Error(), "not registered") || !strings.Contains(u.UserHint(), "namecom domain register free-zz9.com") {
				t.Fatalf("want an unavailable error pointing at domain register, got %v", err)
			}
			if priced {
				t.Errorf("no pricing should be fetched: %v", requests)
			}
		})
	}
}
