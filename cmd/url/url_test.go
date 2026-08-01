package url

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
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// ensure context import used across all tests
var _ = context.Background

func neverCalledServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API should not be called for pre-flight validation failure: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cmdForURLCreate(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	cmd.Flags().StringVar(&createForwardsTo, "to", "", "")
	cmd.Flags().StringVar(&createType, "type", "redirect", "")
	cmd.Flags().StringVar(&createHost, "host", "@", "")
	cmd.Flags().StringVar(&createTitle, "title", "", "")
	cmd.Flags().StringVar(&createMeta, "meta", "", "")
	return cmd
}

// ---- url list ---------------------------------------------------------------

func cmdForURLList(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	t.Cleanup(func() { listAll = false })
	return cmd
}

func TestURLList_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLList(t, srv)
	if err := runList(cmd, []string{"nodot"}); err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestURLList_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"urlForwarding":[],"nextPage":0}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForURLList(t, srv)
	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
}

// ---- url get ----------------------------------------------------------------

func cmdForURLGet(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd
}

func TestURLGet_BadID(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLGet(t, srv)
	if err := runGet(cmd, []string{"example.com", "notanumber"}); err == nil {
		t.Fatal("expected error for non-integer ID, got nil")
	}
}

func TestURLGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"host":"@","forwardsTo":"https://example.com","type":"redirect"}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForURLGet(t, srv)
	if err := runGet(cmd, []string{"example.com", "7"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
}

func TestURLCreate_MissingDestURL(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLCreate(t, srv)
	createForwardsTo, createType, createHost = "", "redirect", "@"

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error when --to is empty, got nil")
	}
}

func TestURLCreate_DestURLNoScheme(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLCreate(t, srv)
	// Mark --to as Changed so the validation path runs (non-interactive).
	if err := cmd.ParseFlags([]string{"--to", "example.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	createType = "redirect"

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for URL without http:// scheme, got nil")
	}
	if !strings.Contains(err.Error(), "http") {
		t.Errorf("expected 'http' in error, got: %v", err)
	}
}

func TestURLCreate_InvalidType(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://example.com", "--type", "permanent"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for invalid forwarding type, got nil")
	}
	if !strings.Contains(err.Error(), "redirect") && !strings.Contains(err.Error(), "302") {
		t.Errorf("expected valid types listed in error, got: %v", err)
	}
}

func TestURLCreate_ValidTypes(t *testing.T) {
	for _, fwdType := range []string{"redirect", "302", "masked"} {
		t.Run(fwdType, func(t *testing.T) {
			var called bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"host":"@","forwardsTo":"https://example.com","type":"` + fwdType + `"}`))
			}))
			t.Cleanup(srv.Close)

			cmd := cmdForURLCreate(t, srv)
			if err := cmd.ParseFlags([]string{"--to", "https://example.com", "--type", fwdType}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}

			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runCreate with type %q: %v", fwdType, err)
			}
			if !called {
				t.Errorf("expected API to be called for valid type %q", fwdType)
			}
		})
	}
}

func TestURLCreate_BadDomainArg(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://example.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

// ---- url update -------------------------------------------------------------

func cmdForURLUpdate(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	cmd.Flags().StringVar(&updateForwardsTo, "to", "", "")
	cmd.Flags().StringVar(&updateType, "type", "redirect", "")
	cmd.Flags().StringVar(&updateTitle, "title", "", "")
	cmd.Flags().StringVar(&updateMeta, "meta", "", "")
	t.Cleanup(func() { updateForwardsTo = ""; updateType = "redirect"; updateTitle = ""; updateMeta = "" })
	return cmd
}

func TestURLUpdate_BadID(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://dest.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runUpdate(cmd, []string{"example.com", "notanumber"})
	if err == nil {
		t.Fatal("expected error for non-integer ID, got nil")
	}
}

func TestURLUpdate_BadDestURL(t *testing.T) {
	getResponse := `{"id":1,"host":"@","forwardsTo":"https://old.com","type":"redirect"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(getResponse))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "no-scheme.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runUpdate(cmd, []string{"example.com", "1"})
	if err == nil {
		t.Fatal("expected error for URL without http:// scheme, got nil")
	}
	if !strings.Contains(err.Error(), "http") {
		t.Errorf("expected 'http' in error, got: %v", err)
	}
}

func TestURLUpdate_InvalidType(t *testing.T) {
	getResponse := `{"id":1,"host":"@","forwardsTo":"https://old.com","type":"redirect"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Error("PUT should not be called when type validation fails")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(getResponse))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://dest.com", "--type", "permanent"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runUpdate(cmd, []string{"example.com", "1"})
	if err == nil {
		t.Fatal("expected error for invalid forwarding type, got nil")
	}
}

// captureUpdateBody serves getResponse for the GET and records the decoded
// body of the subsequent write request, so tests can assert on what we
// actually send rather than only that no error came back.
func captureUpdateBody(t *testing.T, getResponse string, got *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(getResponse))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Errorf("decoding %s body: %v", r.Method, err)
		}
		_, _ = w.Write([]byte(getResponse))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestURLUpdate_PreservesUnsetFieldsFromCurrent guards a regression where
// runUpdate fetched the current entry — its comment promising that "unset
// flags preserve existing values (type, title, meta)" — but only ever read
// current.Type back. Title and Meta have no `omitempty` in the request body,
// so a nil pointer serializes as an explicit `"title":null`, and the server
// reads that as a deliberate clear rather than an omission.
func TestURLUpdate_PreservesUnsetFieldsFromCurrent(t *testing.T) {
	const title = "My Site"
	const meta = "<meta name='keywords' content='fish, denver'>"
	getResponse := `{"id":1,"host":"@","forwardsTo":"https://old.com","type":"masked","title":"` + title + `","meta":"` + meta + `"}`

	var gotBody map[string]any
	srv := captureUpdateBody(t, getResponse, &gotBody)

	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://new.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com", "1"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}

	if gotBody == nil {
		t.Fatal("update request was never sent")
	}
	if got := gotBody["title"]; got != title {
		t.Errorf("title should be preserved: expected %q, got %#v", title, got)
	}
	if got := gotBody["meta"]; got != meta {
		t.Errorf("meta should be preserved: expected %q, got %#v", meta, got)
	}
	// The type-preservation path already worked; pin it so a fix here can't regress it.
	if got := gotBody["type"]; got != "masked" {
		t.Errorf("type should be preserved: expected %q, got %#v", "masked", got)
	}
}

// TestURLUpdate_ExplicitFlagsOverrideCurrent is the other half: preserving
// unset fields must not stop the user from actually changing them.
func TestURLUpdate_ExplicitFlagsOverrideCurrent(t *testing.T) {
	getResponse := `{"id":1,"host":"@","forwardsTo":"https://old.com","type":"masked","title":"Old Title","meta":"old meta"}`

	var gotBody map[string]any
	srv := captureUpdateBody(t, getResponse, &gotBody)

	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://new.com", "--title", "New Title"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com", "1"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}

	if gotBody == nil {
		t.Fatal("update request was never sent")
	}
	if got := gotBody["title"]; got != "New Title" {
		t.Errorf("explicit --title should win: expected %q, got %#v", "New Title", got)
	}
	if got := gotBody["meta"]; got != "old meta" {
		t.Errorf("unset --meta should still be preserved: expected %q, got %#v", "old meta", got)
	}
}

// ---- url delete -------------------------------------------------------------

func cmdForURLDelete(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	return cmd
}

func TestURLUpdate_Success(t *testing.T) {
	getJSON := `{"id":1,"host":"@","forwardsTo":"https://old.com","type":"redirect"}`
	putJSON := `{"id":1,"host":"@","forwardsTo":"https://new.com","type":"redirect"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			_, _ = w.Write([]byte(putJSON))
		} else {
			_, _ = w.Write([]byte(getJSON))
		}
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://new.com"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com", "1"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
}

func TestURLDelete_Success(t *testing.T) {
	var deletePath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deletePath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForURLDelete(t, srv)
	if err := runDelete(cmd, []string{"example.com", "5"}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if !strings.Contains(deletePath, "5") {
		t.Errorf("expected ID '5' in delete path, got: %q", deletePath)
	}
}

func TestURLDelete_BadID(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLDelete(t, srv)
	err := runDelete(cmd, []string{"example.com", "notanumber"})
	if err == nil {
		t.Fatal("expected error for non-integer ID, got nil")
	}
}

func TestURLDelete_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForURLDelete(t, srv)
	err := runDelete(cmd, []string{"nodot", "1"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

// withDryRun attaches a root command carrying --dry-run=true and --yes=true so
// cmdutil.IsDryRun / IsYes see them, mirroring how the real CLI wires
// persistent flags.
func withDryRun(t *testing.T, child *cobra.Command, dryRun bool) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVar(&yes, "yes", false, "")
	if err := root.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if dryRun {
		if err := root.PersistentFlags().Set("dry-run", "true"); err != nil {
			t.Fatalf("setting dry-run flag: %v", err)
		}
	}
	root.AddCommand(child)
	return child
}

// TestDryRunMatchesRealRequest_URL runs each mutating url command twice — once
// with --dry-run to capture what we PRINT, once against a live test server to
// capture what we SEND — and asserts they agree.
//
// The dry-run strings are hand-written and had drifted badly: update printed
// "PUT /core/v1/domains/{d}/url/forwarding/{id}" while actually sending
// "PATCH /core/v1/urlforwarding/{d}/{id}". --dry-run exists precisely so users
// can trust it before mutating state, so a comparison test is the fix that
// keeps working, not just correcting today's strings.
func TestDryRunMatchesRealRequest_URL(t *testing.T) {
	const getResponse = `{"id":1,"host":"@","forwardsTo":"https://old.com","type":"redirect"}`

	tests := []struct {
		name  string
		setup func(*testing.T, *httptest.Server) *cobra.Command
		args  []string
		run   func(*cobra.Command, []string) error
	}{
		{
			name: "update",
			setup: func(t *testing.T, srv *httptest.Server) *cobra.Command {
				cmd := cmdForURLUpdate(t, srv)
				if err := cmd.ParseFlags([]string{"--to", "https://new.com"}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			},
			args: []string{"example.com", "1"},
			run:  runUpdate,
		},
		{
			name:  "delete",
			setup: func(t *testing.T, srv *httptest.Server) *cobra.Command { return cmdForURLDelete(t, srv) },
			args:  []string{"example.com", "1"},
			run:   runDelete,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			printed := captureDryRunLine(t, func(srv *httptest.Server) (*cobra.Command, error) {
				cmd := withDryRun(t, tc.setup(t, srv), true)
				return cmd, tc.run(cmd, tc.args)
			}, getResponse)

			sent := captureRealRequest(t, func(srv *httptest.Server) (*cobra.Command, error) {
				cmd := withDryRun(t, tc.setup(t, srv), false)
				return cmd, tc.run(cmd, tc.args)
			}, getResponse)

			if printed != sent {
				t.Errorf("--dry-run reports %q but the command actually sends %q", printed, sent)
			}
		})
	}
}

// httpMethods is the set we recognize when scanning dry-run output for the
// "METHOD /path" line, which may be surrounded by other detail lines.
var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// dryRunLine extracts the "METHOD /path" line from captured dry-run output.
func dryRunLine(t *testing.T, s string) string {
	t.Helper()
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && httpMethods[f[0]] && strings.HasPrefix(f[1], "/") {
			return f[0] + " " + f[1]
		}
	}
	t.Fatalf("no dry-run METHOD/path line found in output: %q", s)
	return ""
}

// captureDryRunLine runs the command in dry-run mode and returns what it printed.
func captureDryRunLine(t *testing.T, run func(*httptest.Server) (*cobra.Command, error), getResponse string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(getResponse))
	}))
	t.Cleanup(srv.Close)

	cmd, err := run(srv)
	if err != nil {
		t.Fatalf("dry-run invocation failed: %v", err)
	}
	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	return dryRunLine(t, buf.String())
}

// captureRealRequest runs the command for real and returns the method and path
// of the last request it made — the mutating one, after any read-modify-write GET.
func captureRealRequest(t *testing.T, run func(*httptest.Server) (*cobra.Command, error), getResponse string) string {
	t.Helper()
	var last string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(getResponse))
	}))
	t.Cleanup(srv.Close)

	if _, err := run(srv); err != nil {
		t.Fatalf("live invocation failed: %v", err)
	}
	if last == "" {
		t.Fatal("no request was made")
	}
	return last
}
