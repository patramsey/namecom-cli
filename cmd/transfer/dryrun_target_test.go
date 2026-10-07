package transfer

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/drifttest"
	"github.com/spf13/cobra"
)

// notInAccount answers GET /core/v1/domains/<name> — the read `transfer
// create --dry-run` makes to see whether the domain is already in the account
// — with a 404, and passes every other request to srv. The stubs the transfer
// tests share answer everything with one document, which would read as the
// domain being in the account.
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
