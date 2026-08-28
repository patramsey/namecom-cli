package url

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	// An empty result must still guide the user — the point of the empty state
	// is that someone with no records is told what to do next, not shown a
	// blank screen. Asserting err == nil alone cannot see that.
	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	got := buf.String()
	if !strings.Contains(got, "No URL forwardings found") {
		t.Errorf("empty-state output should contain %q, got:\n%s", "No URL forwardings found", got)
	}
	if !strings.Contains(got, "url create") {
		t.Errorf("empty-state output should contain %q, got:\n%s", "url create", got)
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

	// Assert on what was rendered, not merely that no error came back. Checking
	// err == nil alone passes while the renderer emits an empty table, so the
	// data the command exists to display can go missing silently.
	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	got := buf.String()
	for _, want := range []string{"7", "https://example.com", "redirect"} {
		if !strings.Contains(got, want) {
			t.Errorf("get output is missing %q:\n%s", want, got)
		}
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
			var sentBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				sentBody, _ = io.ReadAll(r.Body)
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
			// Accepting the flag is only half of it: the type has to survive the
			// cast to the generated named type and reach the wire. A mapping bug
			// there would create a redirect when the user asked for masked, and
			// "the command exited 0" would never notice.
			var sent struct {
				Type       string `json:"type"`
				ForwardsTo string `json:"forwardsTo"`
			}
			if err := json.Unmarshal(sentBody, &sent); err != nil {
				t.Fatalf("request body was not JSON: %v (%s)", err, sentBody)
			}
			if sent.Type != fwdType {
				t.Errorf("forwarding type sent to the API = %q, want %q", sent.Type, fwdType)
			}
			if sent.ForwardsTo != "https://example.com" {
				t.Errorf("destination sent to the API = %q, want https://example.com", sent.ForwardsTo)
			}
			// And the created entry must be rendered back, not silently dropped.
			buf, bok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
			if !bok {
				t.Fatal("output writer is not a *bytes.Buffer")
			}
			if got := buf.String(); !strings.Contains(got, "https://example.com") {
				t.Errorf("create output should confirm the new forwarding:\n%s", got)
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
		// Guard on "anything that is not the read" rather than naming a verb:
		// this endpoint updates via PATCH, so a check for PUT never fired and
		// a regression that sent the write anyway would have gone unnoticed.
		if r.Method != http.MethodGet {
			t.Errorf("%s must not be sent when type validation fails", r.Method)
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
	// The existing type is deliberately NOT "redirect": that is the --type flag's
	// default, so a fixture using it cannot tell "preserved the current type"
	// apart from "silently fell back to the default and converted the record".
	getJSON := `{"id":1,"host":"@","forwardsTo":"https://old.com","type":"masked"}`
	putJSON := `{"id":1,"host":"@","forwardsTo":"https://new.com","type":"masked"}`
	var sentBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The update is a PATCH, not a PUT. Branching on PUT made this arm dead
		// code: the update response was never served and the stub replied with
		// the pre-update record, so any assertion on the new value would have
		// been testing the old one.
		if r.Method == http.MethodPatch {
			sentBody, _ = io.ReadAll(r.Body)
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

	// This endpoint replaces the record rather than patching a field, so the
	// command fetches first and merges only the flags that changed. The failure
	// mode is silent: any field the user did not pass gets sent as its zero
	// value and is wiped. Only --to was given here, so host and type must come
	// back from the GET untouched.
	// host and domainName are readOnly on this body — the record is identified
	// by the path — so they are deliberately absent, not dropped.
	var sent struct {
		ForwardsTo string  `json:"forwardsTo"`
		Type       string  `json:"type"`
		Title      *string `json:"title"`
	}
	if err := json.Unmarshal(sentBody, &sent); err != nil {
		t.Fatalf("update body was not JSON: %v (%s)", err, sentBody)
	}
	if sent.ForwardsTo != "https://new.com" {
		t.Errorf("--to must reach the wire: sent forwardsTo %q, want https://new.com", sent.ForwardsTo)
	}
	if sent.Type != "masked" {
		t.Errorf("type was not passed and must be preserved from the existing record, got %q (a masked forwarding silently became a 301)", sent.Type)
	}
	// title has no omitempty, so a nil here is transmitted as an explicit
	// `"title":null` and the server reads it as a deliberate clear.
	if sent.Title != nil {
		t.Errorf("title was absent from the existing record and must not be invented, got %q", *sent.Title)
	}

	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	if got := buf.String(); !strings.Contains(got, "Updated") {
		t.Errorf("update should confirm it happened:\n%s", got)
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
		{
			// create was missing from this table, so its dry-run line was the one
			// URL path with no drift protection — in the very test that exists to
			// prevent drift.
			name: "create",
			setup: func(t *testing.T, srv *httptest.Server) *cobra.Command {
				cmd := cmdForURLCreate(t, srv)
				if err := cmd.ParseFlags([]string{"--to", "https://dest.example"}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			},
			args: []string{"example.com"},
			run:  runCreate,
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

// TestURLList_PaginationIsNotAliased guards two defects that share one root
// cause: the decode target was declared OUTSIDE the page loop and every page
// was decoded into it.
//
//  1. Wrong data. json.Unmarshal reuses the existing slice backing array and
//     the *string pointers inside it, so decoding page 2 overwrote the values
//     page 1's already-appended structs still point at. The list then displayed
//     page 1's rows carrying page 2's IDs — and a user copying that ID into
//     `url delete` removes the wrong redirect.
//  2. Infinite loop. The spec says nextPage "is only populated if there is
//     another page", so the final page OMITS the key. Decoding into a reused
//     target leaves the previous page's non-nil pointer in place, so the
//     nextPage==nil exit condition never fires and --all refetches forever.
//
// cmd/dns/dns.go uses a fresh variable per iteration and documents exactly this
// hazard; the other five list commands did not.
func TestURLList_PaginationIsNotAliased(t *testing.T) {
	const maxRequests = 8 // generous bound; a correct impl needs exactly 2
	var requests int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests > maxRequests {
			t.Errorf("pagination did not terminate: %d requests", requests)
			http.Error(w, "loop", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "2":
			// Final page: nextPage is OMITTED, exactly as the spec describes.
			_, _ = w.Write([]byte(`{"urlForwarding":[{"id":222,"host":"bbb","forwardsTo":"https://two.example","type":"masked"}]}`))
		default:
			_, _ = w.Write([]byte(`{"urlForwarding":[{"id":111,"host":"aaa","forwardsTo":"https://one.example","type":"redirect"}],"nextPage":2}`))
		}
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatJSON, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	listAll = true
	t.Cleanup(func() { listAll = false })

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}

	var env struct {
		Data []struct {
			ID   int32  `json:"id"`
			Host string `json:"host"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(env.Data) != 2 {
		t.Fatalf("expected 2 forwardings across 2 pages, got %d: %s", len(env.Data), buf.String())
	}
	if requests != 2 {
		t.Errorf("expected exactly 2 page requests, got %d", requests)
	}

	want := map[string]int32{"aaa": 111, "bbb": 222}
	for _, e := range env.Data {
		if want[e.Host] != e.ID {
			t.Errorf("host %q should have id %d, got %d — page 2 overwrote page 1's data",
				e.Host, want[e.Host], e.ID)
		}
	}
}

// TestURLUpdate_TypeOnlyWithoutTo guards a scripting dead end: runUpdate
// required --to whenever stdin was not a TTY, so
// `namecom url update example.com 1 --type masked` failed in any script with
// "--to is required" — even though the command is already a read-modify-write
// that fetches the current entry and carefully preserves type/title/meta.
// The --to help reads "new destination URL", not "(required)".
func TestURLUpdate_TypeOnlyWithoutTo(t *testing.T) {
	defer output.StubInteractive(false)()

	const getResponse = `{"id":1,"host":"@","forwardsTo":"https://keep-me.example","type":"redirect","title":"T","meta":"M"}`
	var gotBody map[string]any
	srv := captureUpdateBody(t, getResponse, &gotBody)

	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--type", "masked"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com", "1"}); err != nil {
		t.Fatalf("changing only --type should not require --to, got: %v", err)
	}
	if gotBody == nil {
		t.Fatal("update request was never sent")
	}
	if got := gotBody["type"]; got != "masked" {
		t.Errorf("expected type masked, got %#v", got)
	}
	if got := gotBody["forwardsTo"]; got != "https://keep-me.example" {
		t.Errorf("existing destination must be preserved, got %#v", got)
	}
}

// TestURLUpdate_TitleOnlyPreservesType covers a case the old "--to is required"
// guard made unreachable. The type-selection logic keyed off
// `!cmd.Flags().Changed("to")` as a proxy for "the interactive form ran", so
// with --title alone it fell through to updateType — the flag's DEFAULT of
// "redirect" — silently converting a masked forwarding into a 301.
func TestURLUpdate_TitleOnlyPreservesType(t *testing.T) {
	defer output.StubInteractive(false)()

	const getResponse = `{"id":1,"host":"@","forwardsTo":"https://keep.example","type":"masked","title":"Old","meta":"M"}`
	var gotBody map[string]any
	srv := captureUpdateBody(t, getResponse, &gotBody)

	cmd := cmdForURLUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--title", "New Title"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com", "1"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if gotBody == nil {
		t.Fatal("update request was never sent")
	}
	if got := gotBody["type"]; got != "masked" {
		t.Errorf("changing only --title must not reset the forwarding type: got %#v, want masked", got)
	}
	if got := gotBody["title"]; got != "New Title" {
		t.Errorf("expected the new title, got %#v", got)
	}
}

// TestURLList_JSONEnvelope covers the JSON and YAML branches of the list
// output. Function-level coverage read as covered because runList is entered
// through the table path; the format switch inside it never ran.
//
// `-o json` is the mode a script consumes, and nextPage is how that script
// learns there is more to fetch — the table path has a human-readable hint
// instead, so this branch is the only place that information exists for an
// automated caller.
func TestURLList_JSONEnvelope(t *testing.T) {
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
				_, _ = w.Write([]byte(`{"urlForwarding":[{"id":7,"host":"@","forwardsTo":"https://example.org","type":"redirect"}],"totalCount":2,"nextPage":2,"lastPage":2}`))
			}))
			t.Cleanup(srv.Close)

			var stdout, stderr bytes.Buffer
			cmd := cmdForURLList(t, srv)
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
			if !strings.Contains(got, "example.org") {
				t.Errorf("%s envelope carried no records: %q", tc.name, got)
			}
			if !strings.Contains(strings.ToLower(got), "nextpage") {
				t.Errorf("%s envelope omitted nextPage despite more results: %q", tc.name, got)
			}
		})
	}
}
