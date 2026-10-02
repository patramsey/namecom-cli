package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
)

// TestQuietContract pins the --quiet contract documented on
// output.Config.QuietMode (#173, #174), through the real root command:
//
//   - a create prints the new resource's identifier and nothing else;
//   - an update or delete prints nothing;
//   - a read of one object prints its chosen identifying value.
//
// Each case runs with -o table and -o json: quiet wins over the format, which
// is what `ID=$(namecom dns create … -q)` in a pipe relies on, since a pipe
// defaults to JSON. Hints were the other leak — `-o table -q` printed only the
// "→ Run 'namecom dns list …'" line.
func TestQuietContract(t *testing.T) {
	withConfig(t, loneProfile)

	const (
		domainJSON = `{"domainName":"example.com","locked":true}`
		recordJSON = `{"id":42,"domainName":"example.com","host":"www","fqdn":"www.example.com.","type":"A","answer":"192.0.2.1","ttl":300}`
	)
	tests := []struct {
		name   string
		args   []string
		routes map[string]string // "METHOD /path" → response body; anything else gets {}
		want   string
	}{
		// Creates: the new identifier.
		{
			name:   "dns create",
			args:   []string{"dns", "create", "example.com", "--type", "A", "--host", "www", "--answer", "192.0.2.1"},
			routes: map[string]string{"POST /core/v1/domains/example.com/records": recordJSON},
			want:   "42\n",
		},
		{
			name:   "url create",
			args:   []string{"url", "create", "example.com", "--host", "go", "--to", "https://example.org"},
			routes: map[string]string{"POST /core/v1/domains/example.com/url/forwarding": `{"id":7,"host":"go.example.com","forwardsTo":"https://example.org","type":"redirect"}`},
			want:   "7\n",
		},
		{
			name:   "vanity-ns create",
			args:   []string{"vanity-ns", "create", "example.com", "--hostname", "ns1.example.com", "--ips", "192.0.2.1"},
			routes: map[string]string{"POST /core/v1/domains/example.com/vanity_nameservers": `{"hostname":"ns1.example.com","ips":["192.0.2.1"]}`},
			want:   "ns1.example.com\n",
		},
		{
			name:   "email create",
			args:   []string{"email", "create", "example.com", "info", "--to", "owner@example.org"},
			routes: map[string]string{"POST /core/v1/domains/example.com/email/forwarding": `{"domainName":"example.com","emailBox":"info","emailTo":"owner@example.org"}`},
			want:   "info\n",
		},
		{
			name: "transfer create",
			args: []string{"transfer", "create", "example.com", "--auth-code", "abc123"},
			routes: map[string]string{
				"POST /core/v1/transfers": `{"order":1,"totalPaid":12.99,"transfer":{"domainName":"example.com","status":"pending"}}`,
			},
			want: "example.com\n",
		},

		// Updates and deletes: nothing.
		{
			name: "dns update",
			args: []string{"dns", "update", "example.com", "42", "--answer", "192.0.2.2"},
			routes: map[string]string{
				"GET /core/v1/domains/example.com/records/42": recordJSON,
				"PUT /core/v1/domains/example.com/records/42": recordJSON,
			},
			want: "",
		},
		{
			name: "dns delete",
			args: []string{"dns", "delete", "example.com", "42"},
			want: "",
		},
		{
			name:   "domain update",
			args:   []string{"domain", "update", "example.com", "--autorenew=true"},
			routes: map[string]string{"GET /core/v1/domains/example.com": domainJSON, "PATCH /core/v1/domains/example.com": domainJSON},
			want:   "",
		},

		// Reads of one object: the chosen value.
		{
			name:   "domain get",
			args:   []string{"domain", "get", "example.com"},
			routes: map[string]string{"GET /core/v1/domains/example.com": domainJSON},
			want:   "example.com\n",
		},
		{
			name:   "domain pricing",
			args:   []string{"domain", "pricing", "example.com"},
			routes: map[string]string{"GET /core/v1/domains/example.com:getPricing": `{"purchasePrice":12.99,"renewalPrice":14.99,"transferPrice":9.99}`},
			want:   "12.99\n",
		},
		{
			name:   "domain contacts get",
			args:   []string{"domain", "contacts", "get", "example.com"},
			routes: map[string]string{"GET /core/v1/domains/example.com": `{"domainName":"example.com","contacts":{"registrant":{"email":"owner@example.org"},"admin":{"email":"admin@example.org"}}}`},
			want:   "owner@example.org\n",
		},
		{
			name:   "transfer eligibility, eligible",
			args:   []string{"transfer", "eligibility", "example.com"},
			routes: map[string]string{"GET /core/v1/transfers/eligibility/example.com": `{"domainName":"example.com","atName":true,"supportsInternalTransfer":true}`},
			want:   "example.com\n",
		},
		{
			name:   "transfer eligibility, not eligible",
			args:   []string{"transfer", "eligibility", "example.com"},
			routes: map[string]string{"GET /core/v1/transfers/eligibility/example.com": `{"domainName":"example.com","atName":false,"supportsInternalTransfer":true}`},
			want:   "",
		},
		{
			name:   "order get",
			args:   []string{"order", "get", "5"},
			routes: map[string]string{"GET /core/v1/orders/5": `{"id":5,"status":"success"}`},
			want:   "5\n",
		},
		{
			name:   "email get",
			args:   []string{"email", "get", "example.com", "info"},
			routes: map[string]string{"GET /core/v1/domains/example.com/email/forwarding/info": `{"domainName":"example.com","emailBox":"info","emailTo":"owner@example.org"}`},
			want:   "info\n",
		},
		{
			// Every ListDomains call gets the same page, so the expiry query
			// reports old.example as expired; that is the line quiet prints.
			name:   "status",
			args:   []string{"status"},
			routes: map[string]string{"GET /core/v1/domains": `{"domains":[{"domainName":"old.example","expireDate":"2020-01-01T00:00:00Z"}],"totalCount":1}`},
			want:   "old.example\n",
		},
		{
			name: "auth status",
			args: []string{"auth", "status"},
			want: "workuser\n",
		},
		{
			name: "config show",
			args: []string{"config", "show"},
			want: "work\n",
		},
		{
			name: "config list-profiles",
			args: []string{"config", "list-profiles"},
			want: "work\n",
		},
		{
			name: "version",
			args: []string{"version"},
			want: gatherBuildInfo().Version + "\n",
		},
	}

	for _, tc := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			body, ok := tc.routes[r.Method+" "+r.URL.Path]
			if !ok {
				body = `{}`
			}
			_, _ = w.Write([]byte(body))
		}))
		for _, format := range []string{"table", "json"} {
			t.Run(tc.name+" -o "+format, func(t *testing.T) {
				args := append([]string{"--base-url", srv.URL, "--yes", "-q", "-o", format}, tc.args...)
				stdout := runCapturingStdout(t, args)
				if stdout != tc.want {
					t.Errorf("namecom %s -q -o %s printed %q, want %q",
						strings.Join(tc.args, " "), format, stdout, tc.want)
				}
			})
		}
		srv.Close()
	}
}

// runCapturingStdout runs `namecom <args>` through the real root and returns
// what it wrote to stdout. stderr is discarded: warnings belong there in quiet
// mode, and this test is about what a script captures.
func runCapturingStdout(t *testing.T, args []string) string {
	t.Helper()
	t.Cleanup(output.StubInteractive(false))

	dir := t.TempDir()
	out, err := os.Create(filepath.Join(dir, "stdout")) //nolint:gosec // a path under t.TempDir()
	if err != nil {
		t.Fatalf("creating stdout file: %v", err)
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, devnull
	restore := func() { os.Stdout, os.Stderr = stdout, stderr }
	t.Cleanup(func() { restore(); _ = out.Close(); _ = devnull.Close() })

	prev := gf
	t.Cleanup(func() { gf = prev; rootCmd.SetArgs(nil) })
	rootCmd.SetArgs(args)
	runErr := rootCmd.ExecuteContext(context.Background())
	restore()
	if runErr != nil {
		t.Fatalf("namecom %s: %v", strings.Join(args, " "), runErr)
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatalf("reading stdout: %v", err)
	}
	return string(data)
}
