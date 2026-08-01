package transfer

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var _ = context.Background // ensure context import is used

func neverCalledServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API should not be called for pre-flight validation failure: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cmdForTransferCreate(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	// Register --yes on persistent flags so cmdutil.IsYes returns true (skips confirm).
	var yes bool
	cmd.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	cmd.Flags().StringVar(&createAuthCode, "auth-code", "", "")
	cmd.Flags().BoolVar(&createPrivacy, "privacy", false, "")
	cmd.Flags().Float64Var(&createPrice, "price", 0, "")
	return cmd
}

func TestTransferCreate_AuthCodeEmpty(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForTransferCreate(t, srv)
	createAuthCode, createPrivacy, createPrice = "", false, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for empty auth code, got nil")
	}
	if !strings.Contains(err.Error(), "auth-code") {
		t.Errorf("expected 'auth-code' in error, got: %v", err)
	}
}

func TestTransferCreate_AuthCodeTooShort(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "abc"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for short auth code (< 6 chars), got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("expected 'too short' in error, got: %v", err)
	}
}

func TestTransferCreate_BadDomainArg(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "validcode123"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

// ---- internal-in ------------------------------------------------------------

func cmdForInternalIn(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	cmd.Flags().StringVar(&internalAuthCode, "auth-code", "", "")
	t.Cleanup(func() { internalAuthCode = "" })
	return cmd
}

func TestTransferInternalIn_EmptyAuthCode(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForInternalIn(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", ""}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runInternalIn(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for empty auth code in non-interactive mode, got nil")
	}
}

func TestTransferInternalIn_AuthCodeTooShort(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForInternalIn(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "abc"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runInternalIn(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for short auth code, got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("expected 'too short' in error, got: %v", err)
	}
}

func TestTransferInternalIn_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForInternalIn(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "validcode123"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runInternalIn(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

// ---- transfer list ----------------------------------------------------------

func cmdForTransferList(t *testing.T, srv *httptest.Server) *cobra.Command {
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

func TestTransferList_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transfers":[],"nextPage":0}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForTransferList(t, srv)
	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
}

func TestTransferList_ShowsEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transfers":[{"domainName":"example.com","status":"pending"}],"nextPage":0}`))
	}))
	t.Cleanup(srv.Close)

	var stdout bytes.Buffer
	client, _ := api.New(api.Options{BaseURL: srv.URL})
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &stdout, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	t.Cleanup(func() { listAll = false })

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(stdout.String(), "example.com") {
		t.Errorf("expected 'example.com' in output, got: %q", stdout.String())
	}
}

// ---- transfer get -----------------------------------------------------------

func cmdForTransferGet(t *testing.T, srv *httptest.Server) *cobra.Command {
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

func TestTransferGet_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForTransferGet(t, srv)
	if err := runGet(cmd, []string{"nodot"}); err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestTransferGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","status":"pending"}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForTransferGet(t, srv)
	if err := runGet(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
}

func TestTransferInternalIn_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","status":"pending"}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForInternalIn(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "validcode123"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runInternalIn(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runInternalIn: %v", err)
	}
}

func TestTransferCreate_DomainNormalized(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","status":"pending","order":1,"totalPaid":9.99}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--auth-code", "validcode123", "--yes"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runCreate(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	body := string(receivedBody)
	if !strings.Contains(body, `"example.com"`) {
		t.Errorf("expected normalized 'example.com' in request body, got %q", body)
	}
	if strings.Contains(body, "EXAMPLE") {
		t.Errorf("domain was not lowercased in request body: %q", body)
	}
}

// TestIsTerminalTransferStatus pins the terminal/non-terminal split against the
// spec's own classification. The previous hardcoded set treated `rejected` as
// terminal and omitted `canceled_pending_refund`, so `--watch` exited early on
// a rejection (which the registry may still follow with a retry) and polled
// forever on a cancellation that had already finished.
//
// Spec (TransferStatus description):
//
//	Terminal (finished; will not change):
//	  completed, failed, canceled, canceled_pending_refund
//	Non-terminal (in progress; may change):
//	  pending, submitting_transfer, pending_new_auth_code, pending_unlock,
//	  pending_registry_unlock, pending_transfer, pending_insert, rejected
func TestIsTerminalTransferStatus(t *testing.T) {
	terminal := []string{"completed", "failed", "canceled", "canceled_pending_refund"}
	nonTerminal := []string{
		"pending", "submitting_transfer", "pending_new_auth_code", "pending_unlock",
		"pending_registry_unlock", "pending_transfer", "pending_insert", "rejected",
	}

	for _, s := range terminal {
		if !isTerminalTransferStatus(s) {
			t.Errorf("%q is terminal per the spec, got non-terminal (--watch would poll forever)", s)
		}
	}
	for _, s := range nonTerminal {
		if isTerminalTransferStatus(s) {
			t.Errorf("%q is non-terminal per the spec, got terminal (--watch would exit early)", s)
		}
	}

	// Every value in the generated enum must be classified. The previous version
	// counted the test's OWN literals (4+8 is always 12), so adding a 13th value
	// to the generated enum did not fail it — the guard could not do what its
	// comment claimed. Enumerate the generated constants instead, so a spec
	// regen that introduces a status forces a decision here rather than letting
	// it silently default to non-terminal (i.e. --watch polls forever).
	classified := map[string]bool{}
	for _, s := range append(append([]string{}, terminal...), nonTerminal...) {
		classified[s] = true
	}
	for _, s := range allTransferStatuses() {
		if !classified[s] {
			t.Errorf("TransferStatus %q is not classified as terminal or non-terminal — "+
				"--watch would treat it as non-terminal and poll indefinitely", s)
		}
	}
}

// ---- dry-run / real request drift -------------------------------------------

var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

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

// withDryRun attaches a root carrying --dry-run and --yes so cmdutil.IsDryRun /
// IsYes observe them, as the real CLI wires persistent flags.
func withDryRun(t *testing.T, child *cobra.Command, dryRun bool) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
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

// TestDryRunMatchesRealRequest_Transfer asserts that what --dry-run prints is
// what the command actually sends. Three of these four had drifted: cancel
// printed DELETE /core/v1/transfers/{d} while sending POST
// /core/v1/transfers/{d}:cancel, cancel-outbound printed a
// /core/v1/domains/{d}:cancel-transfer-out path that does not exist, and
// internal-in printed /core/v1/transfers:internal-in instead of
// /core/v1/transfers/internal/in.
func TestDryRunMatchesRealRequest_Transfer(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *httptest.Server) *cobra.Command
		args  []string
		run   func(*cobra.Command, []string) error
	}{
		{"cancel", cmdForTransferGet, []string{"example.com"}, runCancel},
		{"cancel-outbound", cmdForTransferGet, []string{"example.com"}, runCancelOutbound},
		{
			name: "internal-in",
			setup: func(t *testing.T, srv *httptest.Server) *cobra.Command {
				cmd := cmdForInternalIn(t, srv)
				if err := cmd.ParseFlags([]string{"--auth-code", "ABC123"}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				return cmd
			},
			args: []string{"example.com"},
			run:  runInternalIn,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const resp = `{"domainName":"example.com","status":"pending"}`

			// dry-run: capture what we print
			dsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp))
			}))
			t.Cleanup(dsrv.Close)
			dcmd := withDryRun(t, tc.setup(t, dsrv), true)
			if err := tc.run(dcmd, tc.args); err != nil {
				t.Fatalf("dry-run invocation: %v", err)
			}
			buf, ok := cmdutil.Out(dcmd).Writer.(*bytes.Buffer)
			if !ok {
				t.Fatal("output writer is not a *bytes.Buffer")
			}
			printed := dryRunLine(t, buf.String())

			// live: capture what we send
			var last string
			lsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				last = r.Method + " " + r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp))
			}))
			t.Cleanup(lsrv.Close)
			lcmd := withDryRun(t, tc.setup(t, lsrv), false)
			if err := tc.run(lcmd, tc.args); err != nil {
				t.Fatalf("live invocation: %v", err)
			}
			if last == "" {
				t.Fatal("no request was made")
			}

			if printed != last {
				t.Errorf("--dry-run reports %q but the command actually sends %q", printed, last)
			}
		})
	}
}

// TestTransferCreate_ShowsAPIWarnings guards a dropped field: the create
// response carries an optional Warnings object ("non-blocking registry statuses
// detected") that table output never printed, so a TTY user saw a clean success
// while `-o json` showed the warning. The status is surfaced too — it was also
// dropped.
func TestTransferCreate_ShowsAPIWarnings(t *testing.T) {
	const resp = `{
		"order": 12345,
		"totalPaid": 9.99,
		"transfer": {"domainName":"example.com","status":"pending_unlock"},
		"warnings": {
			"message": "Domain has clientTransferProhibited; unlock at your current registrar",
			"statuses": ["clientTransferProhibited","serverTransferProhibited"]
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if err := cmd.ParseFlags([]string{"--auth-code", "ABC123"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	ew := &bytes.Buffer{}
	cmdutil.Out(cmd).EWriter = ew

	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}

	buf, ok := cmdutil.Out(cmd).Writer.(*bytes.Buffer)
	if !ok {
		t.Fatal("output writer is not a *bytes.Buffer")
	}
	combined := buf.String() + ew.String()

	if !strings.Contains(combined, "clientTransferProhibited") {
		t.Errorf("registry statuses from the API warning must be shown, got: %q", combined)
	}
	if !strings.Contains(combined, "unlock at your current registrar") {
		t.Errorf("warning message must be shown, got: %q", combined)
	}
	if !strings.Contains(combined, "pending_unlock") {
		t.Errorf("transfer status must be shown, got: %q", combined)
	}
}

// TestTransferCreate_QuotesPriceBeforeCharging guards a money-transparency gap.
// Unlike `domain register` and `domain renew`, which call GetPricingForDomain
// and put the amount in the confirmation prompt, `transfer create` asked only
// "Initiate transfer of example.com?" — so the user approved a charge they had
// never been shown. (The total appears in the success line, but that is printed
// after the money has already moved.)
//
// The property under test is ordering: pricing must be fetched BEFORE the
// create request, so the figure can reach the prompt.
func TestTransferCreate_QuotesPriceBeforeCharging(t *testing.T) {
	defer output.StubInteractive(false)()

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getPricing") {
			calls = append(calls, "pricing")
			_, _ = w.Write([]byte(`{"transferPrice":11.99,"renewalPrice":13.99}`))
			return
		}
		calls = append(calls, "create")
		_, _ = w.Write([]byte(`{"order":1,"totalPaid":11.99,"transfer":{"domainName":"example.com","status":"pending"}}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForTransferCreate(t, srv)
	if err := cmd.PersistentFlags().Set("yes", "true"); err != nil {
		t.Fatalf("setting yes flag: %v", err)
	}
	if err := cmd.ParseFlags([]string{"--auth-code", "ABC123"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}

	if len(calls) < 2 || calls[0] != "pricing" {
		t.Errorf("transfer price must be fetched before the transfer is created, call order was: %v", calls)
	}
}

// allTransferStatuses lists every value of the generated TransferStatus enum.
//
// Go has no reflection over untyped constant sets, so this is maintained by
// hand — but it is verified against the generated source at test time by
// countTransferStatusConstants below, which fails if the two drift.
func allTransferStatuses() []string {
	return []string{
		string(gen.TransferStatusCanceled),
		string(gen.TransferStatusCanceledPendingRefund),
		string(gen.TransferStatusCompleted),
		string(gen.TransferStatusFailed),
		string(gen.TransferStatusPending),
		string(gen.TransferStatusPendingInsert),
		string(gen.TransferStatusPendingNewAuthCode),
		string(gen.TransferStatusPendingRegistryUnlock),
		string(gen.TransferStatusPendingTransfer),
		string(gen.TransferStatusPendingUnlock),
		string(gen.TransferStatusRejected),
		string(gen.TransferStatusSubmittingTransfer),
	}
}

// TestAllTransferStatusesIsComplete keeps allTransferStatuses honest by
// counting the TransferStatus constants in the generated source. Without this,
// a regen that adds a status would leave the hand-written list short and the
// classification guard would silently stop covering it.
func TestAllTransferStatusesIsComplete(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "internal", "api", "gen", "zz_generated.go"))
	if err != nil {
		t.Fatalf("reading generated source: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\tTransferStatus\w+\s+TransferStatus = `)
	got := len(re.FindAllString(string(src), -1))
	if got == 0 {
		t.Fatal("found no TransferStatus constants — has the generated layout changed?")
	}
	if want := len(allTransferStatuses()); got != want {
		t.Errorf("generated source declares %d TransferStatus constants but allTransferStatuses lists %d — "+
			"a spec regen added or removed one; classify it in TestIsTerminalTransferStatus", got, want)
	}
}

// TestWatchTransfer_ProgressDoesNotCorruptStructuredOutput guards the stream
// --watch writes into.
//
// The format switch in runCreate was changed to fall through to watchTransfer
// so that --watch works in JSON/YAML mode — the automation case the flag exists
// for. But watchTransfer wrote its progress lines to stdout, which already
// carries the create response as a structured document, so the combined output
// was a JSON object followed by "Watching transfer status…" and a series of
// timestamped status lines.
func TestWatchTransfer_ProgressDoesNotCorruptStructuredOutput(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			out := &output.Config{Format: format, Color: output.ColorNever,
				Writer: &stdout, EWriter: &stderr}

			// Cancel immediately: the banner is written before the first tick, so
			// this exercises the stream choice without waiting on the 5m ticker.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			cmd := &cobra.Command{}
			cmd.SetContext(ctx)

			if err := watchTransfer(cmd, out, nil, "example.com"); err != nil {
				t.Fatalf("watchTransfer: %v", err)
			}
			if stdout.Len() != 0 {
				t.Errorf("--watch wrote progress to stdout in %s mode, corrupting the document:\n%s",
					format, stdout.String())
			}
			if !strings.Contains(stderr.String(), "Watching transfer status") {
				t.Errorf("progress should still be visible on stderr, got: %q", stderr.String())
			}
		})
	}
}
