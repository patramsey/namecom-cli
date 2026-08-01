package vanity

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

func neverCalledServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API should not be called for pre-flight validation failure: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func baseCmd(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{
		Format:  output.FormatTable,
		Color:   output.ColorNever,
		Writer:  &bytes.Buffer{},
		EWriter: &bytes.Buffer{},
	}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd
}

func cmdForCreate(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().StringVar(&createHostname, "hostname", "", "")
	cmd.Flags().StringVar(&createIPs, "ips", "", "")
	t.Cleanup(func() { createHostname = ""; createIPs = "" })
	return cmd
}

func cmdForUpdate(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	cmd.Flags().StringVar(&updateIPs, "ips", "", "")
	t.Cleanup(func() { updateIPs = "" })
	return cmd
}

func cmdForDelete(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	cmd := baseCmd(t, srv)
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	return cmd
}

// ---- list -------------------------------------------------------------------

func TestVanityList_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := baseCmd(t, srv)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	t.Cleanup(func() { listAll = false })
	err := runList(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestVanityList_Empty(t *testing.T) {
	var nextPage int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.ListVanityNameserversResponseSchema{NextPage: &nextPage})
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	t.Cleanup(func() { listAll = false })
	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
}

// ---- get --------------------------------------------------------------------

func TestVanityGet_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := baseCmd(t, srv)
	if err := runGet(cmd, []string{"nodot", "ns1.example.com"}); err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestVanityGet_ReturnsEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.VanityNameserverResponseSchema{
			Hostname: "ns1.example.com",
		})
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	if err := runGet(cmd, []string{"example.com", "ns1.example.com"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	// See the note in cmd/url: err == nil says nothing about what was rendered.
	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	if got := buf.String(); !strings.Contains(got, "ns1.example.com") {
		t.Errorf("get output is missing the nameserver hostname:\n%s", got)
	}
}

// ---- create -----------------------------------------------------------------

func TestVanityCreate_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createHostname, createIPs = "ns1.example.com", "1.2.3.4"
	err := runCreate(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestVanityCreate_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.VanityNameserverResponseSchema{Hostname: "ns1.example.com"})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	createHostname, createIPs = "ns1.example.com", "1.2.3.4"
	if err := runCreate(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain not normalized in create path: %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected 'example.com' in create path, got: %q", receivedPath)
	}
}

// TestVanityCreate_HostnameSentAsLabel guards a mismatch between our help text
// and the API. CreateVanityNameserverBody.hostname is documented as "The
// subdomain portion of the nameserver hostname. The domain portion will be
// taken from the URL path… to create 'ns1.example.com', specify 'ns1'" —
// but every CLI example says --hostname ns1.example.com, and we sent it
// verbatim, yielding a 400 or 'ns1.example.com.example.com'.
//
// The get/update/delete path parameter genuinely IS the FQDN, so the CLI's
// own examples are self-consistent; only create needs the label. We accept
// both spellings rather than making create the odd command out.
func TestVanityCreate_HostnameSentAsLabel(t *testing.T) {
	tests := []struct {
		name, hostname, want string
	}{
		{"fqdn as documented in help", "ns1.example.com", "ns1"},
		{"bare label as the API documents", "ns1", "ns1"},
		{"multi-level subdomain", "ns1.a.example.com", "ns1.a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
					t.Errorf("decoding create body: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(gen.VanityNameserverResponseSchema{Hostname: tc.hostname})
			}))
			t.Cleanup(srv.Close)

			cmd := cmdForCreate(t, srv)
			createHostname, createIPs = tc.hostname, "1.2.3.4"
			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runCreate: %v", err)
			}
			if gotBody == nil {
				t.Fatal("create request was never sent")
			}
			if got := gotBody["hostname"]; got != tc.want {
				t.Errorf("expected hostname %q sent to API, got %#v", tc.want, got)
			}
		})
	}
}

// TestVanityCreate_HostnameWrongDomain rejects a hostname that isn't under the
// domain being operated on. Stripping the suffix would otherwise silently turn
// 'ns1.other.com' into a nameserver named 'ns1.other.com' under example.com.
func TestVanityCreate_HostnameWrongDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createHostname, createIPs = "ns1.other.com", "1.2.3.4"
	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for hostname outside the target domain, got nil")
	}
	if !strings.Contains(err.Error(), "example.com") {
		t.Errorf("error should name the expected domain, got: %v", err)
	}
}

// ---- update -----------------------------------------------------------------

func TestVanityUpdate_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForUpdate(t, srv)
	updateIPs = "1.2.3.4"
	err := runUpdate(cmd, []string{"nodot", "ns1.example.com"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestVanityUpdate_Success(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.VanityNameserverResponseSchema{
			Hostname: "ns1.example.com",
		})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForUpdate(t, srv)
	updateIPs = "5.6.7.8"
	if err := runUpdate(cmd, []string{"example.com", "ns1.example.com"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected 'example.com' in request path, got: %q", receivedPath)
	}
}

// ---- delete -----------------------------------------------------------------

func TestVanityDelete_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForDelete(t, srv)
	err := runDelete(cmd, []string{"nodot", "ns1.example.com"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestVanityDelete_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForDelete(t, srv)
	if err := runDelete(cmd, []string{"EXAMPLE.COM", "ns1.example.com"}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain not normalized in delete path: %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected 'example.com' in delete path, got: %q", receivedPath)
	}
}

// TestSplitIPs guards two related defects. strings.Split("", ",") returns
// [""], not an empty slice, so `--ips ""` — the documented way to clear glue
// records ("Providing an empty array will remove all existing IPs") — sent
// [""] and the registry rejected it. Blank entries from a trailing comma had
// the same problem.
func TestSplitIPs(t *testing.T) {
	tests := []struct {
		name, in string
		want     []string
	}{
		{"empty clears all IPs", "", []string{}},
		{"whitespace only clears all IPs", "   ", []string{}},
		{"single IP", "1.2.3.4", []string{"1.2.3.4"}},
		{"multiple IPs", "1.2.3.4,5.6.7.8", []string{"1.2.3.4", "5.6.7.8"}},
		{"surrounding spaces trimmed", " 1.2.3.4 , 5.6.7.8 ", []string{"1.2.3.4", "5.6.7.8"}},
		{"trailing comma drops the blank", "1.2.3.4,", []string{"1.2.3.4"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitIPs(tc.in)
			if got == nil {
				t.Fatal("splitIPs must return a non-nil slice so it marshals as [] not null")
			}
			if len(got) != len(tc.want) {
				t.Fatalf("expected %#v, got %#v", tc.want, got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: expected %q, got %q", i, tc.want[i], got[i])
				}
			}
		})
	}
}
