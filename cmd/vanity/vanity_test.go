package vanity

import (
	"bytes"
	"context"
	"encoding/json"
	coreapigo "github.com/namedotcom/core-api-go"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
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
	var nextPage int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.ListVanityNameserversResponse{NextPage: &nextPage})
	}))
	t.Cleanup(srv.Close)

	cmd := baseCmd(t, srv)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	t.Cleanup(func() { listAll = false })
	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}

	// An account with no vanity nameservers must be told so and pointed at the
	// command that creates one. Exiting 0 with a blank screen reads as a
	// failure to the user, and nothing but this assertion guards the message.
	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	got := buf.String()
	if !strings.Contains(got, "No vanity nameservers found") {
		t.Errorf("empty list should say so explicitly:\n%s", got)
	}
	if !strings.Contains(got, "vanity-ns create") {
		t.Errorf("empty list should point at the create command:\n%s", got)
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
		_ = json.NewEncoder(w).Encode(coreapigo.VanityNameserverResponse{
			Hostname: strPtr("ns1.example.com"),
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
		_ = json.NewEncoder(w).Encode(coreapigo.VanityNameserverResponse{Hostname: strPtr("ns1.example.com")})
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
				_ = json.NewEncoder(w).Encode(coreapigo.VanityNameserverResponse{Hostname: &tc.hostname})
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
		_ = json.NewEncoder(w).Encode(coreapigo.VanityNameserverResponse{
			Hostname: strPtr("ns1.example.com"),
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

// TestVanityList_PagesToTheEnd covers the --all walk across more than one page,
// and the guard that stops it when nextPage never advances.
//
// There was no multi-page test here, which mattered when every list loop was
// rewritten to route through cmdutil.NextPage: the continuation line is what a
// mechanical rewrite gets wrong, and nothing would have caught a loop that
// stopped after page 1 or one that never advanced at all.
func TestVanityList_PagesToTheEnd(t *testing.T) {
	t.Run("walks every page", func(t *testing.T) {
		const maxRequests = 8 // a correct implementation needs exactly 2
		var requests int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			if requests > maxRequests {
				t.Errorf("pagination did not terminate: %d requests", requests)
				http.Error(w, "loop", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`{"vanityNameservers":[{"domainName":"example.com","hostname":"ns2.example.com","ips":["2.2.2.2"]}],"lastPage":2}`))
				return
			}
			_, _ = w.Write([]byte(`{"vanityNameservers":[{"domainName":"example.com","hostname":"ns1.example.com","ips":["1.1.1.1"]}],"nextPage":2,"lastPage":2}`))
		}))
		t.Cleanup(srv.Close)

		cmd := baseCmd(t, srv)
		cmd.Flags().BoolVar(&listAll, "all", false, "")
		t.Cleanup(func() { listAll = false })
		listAll = true
		out := cmdutil.Out(cmd)
		out.Format = output.FormatJSON

		if err := runList(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if requests != 2 {
			t.Errorf("made %d page requests, want exactly 2", requests)
		}

		buf, ok := out.Writer.(*bytes.Buffer)
		if !ok {
			t.Fatal("output writer is not a *bytes.Buffer")
		}
		var env struct {
			Data []struct {
				Hostname string   `json:"hostname"`
				Ips      []string `json:"ips"`
			} `json:"data"`
		}
		if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
			t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
		}
		if len(env.Data) != 2 {
			t.Fatalf("got %d nameservers across 2 pages, want 2: %s", len(env.Data), buf.String())
		}
		// Pairing hostname to its glue IP catches page 2 overwriting page 1's
		// backing array, the aliasing failure this loop shape has produced before.
		want := map[string]string{"ns1.example.com": "1.1.1.1", "ns2.example.com": "2.2.2.2"}
		for _, e := range env.Data {
			if len(e.Ips) != 1 || want[e.Hostname] != e.Ips[0] {
				t.Errorf("%s has ips %v, want [%s] — pages were aliased", e.Hostname, e.Ips, want[e.Hostname])
			}
		}
	})

	t.Run("a non-advancing nextPage terminates the walk", func(t *testing.T) {
		var requests int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			if requests > 10 {
				t.Errorf("walk did not terminate against a non-advancing nextPage")
				http.Error(w, "loop", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"vanityNameservers":[{"domainName":"example.com","hostname":"ns1.example.com","ips":["1.1.1.1"]}],"nextPage":2,"lastPage":99}`))
		}))
		t.Cleanup(srv.Close)

		cmd := baseCmd(t, srv)
		cmd.Flags().BoolVar(&listAll, "all", false, "")
		t.Cleanup(func() { listAll = false })
		listAll = true
		cmdutil.Out(cmd).Format = output.FormatJSON

		if err := runList(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if requests != 2 {
			t.Errorf("made %d requests, want 2 (page 1 -> 2, then the page stops advancing)", requests)
		}
	})
}

func strPtr(s string) *string { return &s }

// TestVanityList_JSONEnvelope covers the JSON and YAML branches of the list
// output. Function-level coverage read as covered because runList is entered
// through the table path; the format switch inside it never ran.
//
// `-o json` is the mode a script consumes, and nextPage is how that script
// learns there is more to fetch — the table path has a human-readable hint
// instead, so this branch is the only place that information exists for an
// automated caller.
func TestVanityList_JSONEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format output.Format
		want   string
	}{
		{"json", output.FormatJSON, `"data"`},
		{"yaml", output.FormatYAML, "data:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"vanityNameservers":[{"hostname":"ns1.example.com","ips":["1.2.3.4"]}],"totalCount":2,"nextPage":2,"lastPage":2}`))
			}))
			t.Cleanup(srv.Close)

			var stdout, stderr bytes.Buffer
			cmd := baseCmd(t, srv)
			out := &output.Config{
				Format: tc.format, Color: output.ColorNever,
				Writer: &stdout, EWriter: &stderr,
			}
			cmd.SetContext(context.WithValue(cmd.Context(), cmdutil.KeyOutput, out))

			if err := runList(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runList: %v", err)
			}
			got := stdout.String()
			if !strings.Contains(got, tc.want) {
				t.Errorf("expected a %s envelope containing %q, got: %q", tc.name, tc.want, got)
			}
			// The payload, not just the envelope: `"data": null` contains
			// `"data"` too, so the key alone passes on an empty result.
			if !strings.Contains(got, "ns1") {
				t.Errorf("%s envelope carried no records: %q", tc.name, got)
			}
			if !strings.Contains(strings.ToLower(got), "nextpage") {
				t.Errorf("%s envelope omitted nextPage despite more results: %q", tc.name, got)
			}
		})
	}
}
