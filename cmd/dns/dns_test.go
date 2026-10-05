package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	coreapigo "github.com/namedotcom/core-api-go"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// neverCalledServer returns a server that marks the test as failed if any
// request reaches it. Use it to assert validation fires before the API call.
func neverCalledServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("API should not be called for pre-flight validation failure: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// cmdForCreate builds a cobra command wired with a test API client and output,
// with the same flags that runCreate inspects via cmd.Flags().Changed().
func cmdForCreate(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	// runCreate branches on output.IsInteractive(). Under `go test` stdin is not
	// a TTY so it happens to be false, but that is ambient state, not a
	// controlled input: every test built from this helper silently depended on
	// the environment. Pin it.
	t.Cleanup(output.StubInteractive(false))
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

	// Register every flag that runCreate checks via cmd.Flags().Changed().
	cmd.Flags().StringVar(&createType, "type", "", "")
	cmd.Flags().StringVar(&createHost, "host", "@", "")
	cmd.Flags().StringVar(&createAnswer, "answer", "", "")
	cmd.Flags().Int64Var(&createTTL, "ttl", 300, "")
	cmd.Flags().Int64Var(&createPriority, "priority", 0, "")
	return cmd
}

func TestDNSCreate_UnknownType(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "BOGUS", "@", "1.2.3.4", 300, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown record type") {
		t.Errorf("expected 'unknown record type' in error, got: %v", err)
	}
}

func TestDNSCreate_CNAMEAtApex(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "CNAME", "@", "target.example.com.", 300, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for CNAME at apex, got nil")
	}
	if !strings.Contains(err.Error(), "apex") {
		t.Errorf("expected 'apex' in error, got: %v", err)
	}
}

func TestDNSCreate_ARecordBadIP(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "A", "@", "not-an-ip", 300, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for non-IP A record answer, got nil")
	}
	if !strings.Contains(err.Error(), "IPv4") {
		t.Errorf("expected 'IPv4' in error, got: %v", err)
	}
}

func TestDNSCreate_ARecordIPv6Answer(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "A", "@", "::1", 300, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for IPv6 answer in A record, got nil")
	}
	if !strings.Contains(err.Error(), "IPv4") {
		t.Errorf("expected 'IPv4' in error, got: %v", err)
	}
}

func TestDNSCreate_AAAARecordIPv4Answer(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "AAAA", "@", "1.2.3.4", 300, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for IPv4 answer in AAAA record, got nil")
	}
	if !strings.Contains(err.Error(), "IPv6") {
		t.Errorf("expected 'IPv6' in error, got: %v", err)
	}
}

func TestDNSCreate_SRVBadFormat(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "SRV", "@", "onlyone", 300, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for malformed SRV answer, got nil")
	}
	if !strings.Contains(err.Error(), "SRV") {
		t.Errorf("expected 'SRV' in error, got: %v", err)
	}
}

func TestDNSCreate_SRVBadPort(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "SRV", "@", "10 notaport target.com.", 300, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for non-integer SRV port, got nil")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Errorf("expected 'port' in error, got: %v", err)
	}
}

// TestDNSCreate_MissingIDIsUnexpectedResponse pins #187 (suggested on #185):
// a redirected POST answered as a GET printed "Created A record (id 0)" and
// exited 0 having created nothing. A 2xx with no ID is an unexpected response.
func TestDNSCreate_MissingIDIsUnexpectedResponse(t *testing.T) {
	for _, resp := range []string{`{"type":"A","host":"www","answer":"1.2.3.4","ttl":300}`, `{"id":0}`, `{"records":[]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(resp))
		}))
		t.Cleanup(srv.Close)
		cmd := cmdForCreate(t, srv)
		createType, createHost, createAnswer, createTTL, createPriority = "A", "www", "1.2.3.4", 300, 0
		err := runCreate(cmd, []string{"example.com"})
		if _, ok := errors.AsType[*api.UnexpectedResponseError](err); !ok {
			t.Errorf("response %s: runCreate = %v, want *api.UnexpectedResponseError", resp, err)
		}
	}
}

// TestDNSWrites_PriorityOutOfRange pins #187: --priority had no range check,
// so -8 was sent and the exported zone then failed to load. Create, update and
// import all refuse it as a usage error before any request.
func TestDNSWrites_PriorityOutOfRange(t *testing.T) {
	var ue *cmdutil.UsageError
	t.Cleanup(func() { createPriority = 0 })

	cmd := cmdForCreate(t, neverCalledServer(t))
	createType, createHost, createAnswer, createTTL = "MX", "@", "mail.example.com.", 300
	if err := cmd.Flags().Set("priority", "-8"); err != nil {
		t.Fatal(err)
	}
	if err := runCreate(cmd, []string{"example.com"}); !errors.As(err, &ue) {
		t.Errorf("create --priority -8 = %v, want a usage error", err)
	}

	cmd = cmdForUpdate(t, neverCalledServer(t))
	if err := cmd.Flags().Set("priority", "65536"); err != nil {
		t.Fatal(err)
	}
	if err := runUpdate(cmd, []string{"example.com", "1"}); !errors.As(err, &ue) {
		t.Errorf("update --priority 65536 = %v, want a usage error", err)
	}

	path := filepath.Join(t.TempDir(), "records.json")
	payload := `[{"type":"MX","host":"@","answer":"mail.example.com.","ttl":300,"priority":-8}]`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := api.New(api.Options{BaseURL: neverCalledServer(t).URL})
	if err != nil {
		t.Fatal(err)
	}
	icmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, &output.Config{
		Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}})
	icmd.SetContext(context.WithValue(ctx, cmdutil.KeyClient, client))
	importFile = path
	t.Cleanup(func() { importFile = "" })
	if err := runImport(icmd, []string{"example.com"}); !errors.As(err, &ue) {
		t.Errorf("import with priority -8 = %v, want a usage error", err)
	}
}

// TestDNSCreate_CAAIsRefused guards #128: the API rejects CAA on create (it is
// not in the server's list of allowed types), so the CLI must refuse it as a
// usage error instead of sending a request that can only fail.
func TestDNSCreate_CAAIsRefused(t *testing.T) {
	for _, typ := range []string{"CAA", "caa"} {
		t.Run(typ, func(t *testing.T) {
			srv := neverCalledServer(t)
			cmd := cmdForCreate(t, srv)
			createType, createHost, createAnswer, createTTL, createPriority = typ, "@", `0 issue "letsencrypt.org"`, 300, 0

			err := runCreate(cmd, []string{"example.com"})
			if err == nil {
				t.Fatal("expected --type CAA to be refused, got nil")
			}
			var ue *cmdutil.UsageError
			if !errors.As(err, &ue) {
				t.Errorf("expected a usage error (exit 2), got: %v", err)
			}
			if !strings.Contains(err.Error(), "does not accept CAA") {
				t.Errorf("error should say the API does not accept CAA, got: %v", err)
			}
		})
	}
}

// TestDNSCreate_TypeListsOmitCAA pins that CAA is not offered anywhere on the
// create path: the --type help and the "--type is required" message. The help
// names it only to say it cannot be created (#237).
func TestDNSCreate_TypeListsOmitCAA(t *testing.T) {
	usage := createCmd.Flags().Lookup("type").Usage
	if !strings.Contains(usage, "CAA is read-only") {
		t.Errorf("dns create --type help does not say CAA is read-only: %q", usage)
	}
	if strings.Contains(strings.Replace(usage, "CAA is read-only", "", 1), "CAA") {
		t.Errorf("dns create --type help offers CAA: %q", usage)
	}
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "", "@", "1.2.3.4", 300, 0
	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected an error for a missing --type")
	}
	if strings.Contains(err.Error(), "CAA") {
		t.Errorf("--type is required message offers CAA: %v", err)
	}
}

// TestDNSImport_CAAIsRefused pins that import, which creates records through
// the same endpoint, refuses CAA before writing anything.
func TestDNSImport_CAAIsRefused(t *testing.T) {
	payload := `[
	  {"type":"A","host":"one","answer":"1.1.1.1","ttl":300},
	  {"type":"CAA","host":"@","answer":"0 issue \"letsencrypt.org\"","ttl":300}
	]`
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing import file: %v", err)
	}
	srv := neverCalledServer(t)
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever,
		Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	importFile = path
	t.Cleanup(func() { importFile = "" })

	err = runImport(cmd, []string{"example.com"})
	if err == nil || !strings.Contains(err.Error(), "does not accept CAA") {
		t.Errorf("expected the CAA record to be refused, got: %v", err)
	}
}

func TestDNSCreate_TTLTooLow(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "A", "@", "1.2.3.4", 60, 0
	// Mark --ttl as explicitly changed so the TTL check runs.
	if err := cmd.ParseFlags([]string{"--ttl", "60"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for TTL < 300, got nil")
	}
	if !strings.Contains(err.Error(), "300") {
		t.Errorf("expected '300' in error, got: %v", err)
	}
}

func TestDNSCreate_InvalidHost(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "A", "has space", "1.2.3.4", 300, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for host with spaces, got nil")
	}
	if !strings.Contains(err.Error(), "space") {
		t.Errorf("expected 'space' in error, got: %v", err)
	}
}

func TestDNSCreate_BadDomainArg(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "A", "@", "1.2.3.4", 300, 0

	err := runCreate(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
	if !strings.Contains(err.Error(), "dot") {
		t.Errorf("expected 'dot' in error, got: %v", err)
	}
}

func TestDNSCreate_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		recType := "A"
		recID := 1
		recHost := "@"
		recAnswer := "1.2.3.4"
		_ = json.NewEncoder(w).Encode(coreapigo.Record{
			ID:     &recID,
			Type:   &recType,
			Host:   &recHost,
			Answer: &recAnswer,
		})
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "A", "@", "1.2.3.4", 300, 0

	if err := runCreate(cmd, []string{"EXAMPLE.COM"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected normalized domain 'example.com' in request path, got %q", receivedPath)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain was not lowercased in request path: %q", receivedPath)
	}
}

// ---- dns list ---------------------------------------------------------------

func recordServer(t *testing.T, records []*coreapigo.Record, nextPage int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.ListRecordsResponse{
			Records:  records,
			NextPage: &nextPage,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cmdForList(t *testing.T, srv *httptest.Server, stdout *bytes.Buffer) *cobra.Command {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{
		Format:  output.FormatTable,
		Color:   output.ColorNever,
		Writer:  stdout,
		EWriter: &bytes.Buffer{},
	}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	cmd.Flags().StringVar(&listType, "type", "", "")
	return cmd
}

func TestDNSList_ShowsRecords(t *testing.T) {
	recType := "A"
	recHost := "www"
	recAnswer := "1.2.3.4"
	// A second record of a different type, carrying a priority: TYPE and
	// PRIORITY are columns the single A record cannot exercise. "A" is also a
	// poor thing to assert on — it occurs inside the ANSWER header — whereas
	// "MX" and its priority appear only where the record renders them.
	mxType := "MX"
	mxHost := "@"
	mxAnswer := "mail.example.com"
	mxPriority := int64(10)
	records := []*coreapigo.Record{
		{Host: &recHost, Answer: &recAnswer, Type: &recType},
		{Host: &mxHost, Answer: &mxAnswer, Type: &mxType, Priority: &mxPriority},
	}
	srv := recordServer(t, records, 0)

	var stdout bytes.Buffer
	cmd := cmdForList(t, srv, &stdout)
	listAll, listType = false, ""

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(stdout.String(), "www") {
		t.Errorf("expected 'www' in output, got: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "1.2.3.4") {
		t.Errorf("expected '1.2.3.4' in output, got: %q", stdout.String())
	}
	// The record type decides what the answer means; a blank TYPE column makes
	// an MX and a CNAME to the same host indistinguishable.
	if !strings.Contains(stdout.String(), "MX") {
		t.Errorf("expected the record type 'MX' in output, got: %q", stdout.String())
	}
	// Priority is what orders MX delivery; it renders in its own column.
	if !strings.Contains(stdout.String(), "10") {
		t.Errorf("expected the MX priority '10' in output, got: %q", stdout.String())
	}
}

// TestDNSList_NullBodyIsAnError pins #157: a 200 whose body is `null` made the
// SDK return a nil response with a nil error, and the page loop dereferenced it.
func TestDNSList_NullBodyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("null\n"))
	}))
	t.Cleanup(srv.Close)
	var stdout bytes.Buffer
	cmd := cmdForList(t, srv, &stdout)
	listAll, listType = false, ""

	err := runList(cmd, []string{"example.com"})
	if _, ok := errors.AsType[*api.UnexpectedResponseError](err); !ok {
		t.Fatalf("runList = %v, want an *api.UnexpectedResponseError", err)
	}
}

// TestDNSList_NullRecordIsSkipped pins #157's list half: a null element in
// `records` crashed the table and --quiet loops. It is skipped, not shown as
// an empty row or counted.
func TestDNSList_NullRecordIsSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"records":[null,{"id":7,"host":"www","type":"A","answer":"192.0.2.1"},null]}`))
	}))
	t.Cleanup(srv.Close)

	for _, quiet := range []bool{false, true} {
		var stdout bytes.Buffer
		cmd := cmdForList(t, srv, &stdout)
		cmdutil.Out(cmd).QuietMode = quiet
		listAll, listType = false, ""

		if err := runList(cmd, []string{"example.com"}); err != nil {
			t.Fatalf("quiet=%v: runList: %v", quiet, err)
		}
		got := stdout.String()
		if quiet {
			if got != "7\n" {
				t.Errorf("--quiet output = %q, want only the one record's ID", got)
			}
		} else if stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String(); !strings.Contains(got, "192.0.2.1") || !strings.Contains(stderr, "1 record\n") {
			t.Errorf("table should show the one record and count only it:\n%s\nstderr:\n%s", got, stderr)
		}
	}
}

func TestDNSList_BadDomainArg(t *testing.T) {
	srv := neverCalledServer(t)
	var stdout bytes.Buffer
	cmd := cmdForList(t, srv, &stdout)
	listAll, listType = false, ""

	err := runList(cmd, []string{"nodot"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestDNSList_HasMoreHint(t *testing.T) {
	recType := "A"
	recHost := "@"
	recAnswer := "1.2.3.4"
	records := []*coreapigo.Record{{Host: &recHost, Answer: &recAnswer, Type: &recType}}
	// nextPage=2 signals there are more pages.
	srv := recordServer(t, records, 2)

	var stdout bytes.Buffer
	cmd := cmdForList(t, srv, &stdout)
	listAll, listType = false, ""

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	// The note is part of the count footer, on stderr, so it stays out of a
	// table redirected to a file.
	if stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String(); !strings.Contains(stderr, "--page 2 for more, --all for everything") {
		t.Errorf("expected a '--page 2 for more' note when hasMore=true, got: %q", stderr)
	}
	if strings.Contains(stdout.String(), "--all") {
		t.Errorf("the note leaked onto stdout: %q", stdout.String())
	}
}

func TestDNSList_EmptyRecords(t *testing.T) {
	srv := recordServer(t, nil, 0)

	var stdout bytes.Buffer
	cmd := cmdForList(t, srv, &stdout)
	listAll, listType = false, ""

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
}

// An empty zone lists as `[]`, not `null`, so `jq '.data[]'` iterates nothing
// rather than failing. Also covers a --type filter that matches nothing, which
// reslices records and must not turn [] back into null.
func TestDNSList_EmptyIsArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalCount":0,"from":0,"to":0,"records":[]}`))
	}))
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		format output.Format
		typ    string
		want   string
	}{
		{output.FormatJSON, "", `"data": []`},
		{output.FormatYAML, "", "data: []"},
		{output.FormatJSON, "MX", `"data": []`},
	} {
		t.Run(string(tc.format)+tc.typ, func(t *testing.T) {
			var stdout bytes.Buffer
			cmd := cmdForList(t, srv, &stdout)
			listAll, listType = false, tc.typ
			t.Cleanup(func() { listType = "" })
			cmdutil.Out(cmd).Format = tc.format
			if err := runList(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runList: %v", err)
			}
			if got := stdout.String(); !strings.Contains(got, tc.want) {
				t.Errorf("empty list should print %s, got:\n%s", tc.want, got)
			}
		})
	}
}

func TestDNSList_TypeFilter(t *testing.T) {
	typeA := "A"
	typeMX := "MX"
	// A named host rather than "@": the filtered view renders its own HOST
	// column, and a single "@" cannot be told apart from an empty cell.
	hostWWW := "www"
	hostAt := "@"
	answerA := "1.2.3.4"
	answerMX := "mail.example.com"
	records := []*coreapigo.Record{
		{Host: &hostWWW, Answer: &answerA, Type: &typeA},
		{Host: &hostAt, Answer: &answerMX, Type: &typeMX},
	}
	srv := recordServer(t, records, 0)

	var stdout bytes.Buffer
	cmd := cmdForList(t, srv, &stdout)
	listAll = false
	if err := cmd.ParseFlags([]string{"--type", "A"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	t.Cleanup(func() { listType = "" })

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "1.2.3.4") {
		t.Errorf("expected A record answer in filtered output, got: %q", out)
	}
	if !strings.Contains(out, "www") {
		t.Errorf("expected the A record host in filtered output, got: %q", out)
	}
	if strings.Contains(out, "mail.example.com") {
		t.Errorf("MX record should be filtered out, but appears in output: %q", out)
	}
}

// TestDNSList_FilteredViewShowsTypeColumn covers the TYPE column of the flat
// (filtered) table. The unfiltered listing groups by type and renders the label
// as a section header via recordRowsNoType, so it never exercises this column;
// only `--type` reaches recordRows. Filtering on MX rather than A is deliberate
// — "A" occurs inside the "ANSWER" header, so asserting on it would pass
// against an empty column.
func TestDNSList_FilteredViewShowsTypeColumn(t *testing.T) {
	typeMX := "MX"
	hostAt := "@"
	answerMX := "mail.example.com"
	priority := int64(10)
	records := []*coreapigo.Record{
		{Host: &hostAt, Answer: &answerMX, Type: &typeMX, Priority: &priority},
	}
	srv := recordServer(t, records, 0)

	var stdout bytes.Buffer
	cmd := cmdForList(t, srv, &stdout)
	listAll = false
	if err := cmd.ParseFlags([]string{"--type", "MX"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	t.Cleanup(func() { listType = "" })

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "MX") {
		t.Errorf("expected the record type 'MX' in the filtered table, got: %q", out)
	}
	if !strings.Contains(out, "mail.example.com") {
		t.Errorf("expected the MX answer in the filtered table, got: %q", out)
	}
}

// paginatedRecordServer serves multiple pages of DNS records. It routes
// by the ?page= query param; pages[0] = page 1, pages[1] = page 2, etc.
func paginatedRecordServer(t *testing.T, pages [][]*coreapigo.Record) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageNum := 1
		if p := r.URL.Query().Get("page"); p != "" {
			if n, err := strconv.Atoi(p); err == nil {
				pageNum = n
			}
		}
		idx := pageNum - 1
		if idx < 0 || idx >= len(pages) {
			http.Error(w, "page out of range", http.StatusNotFound)
			return
		}
		var nextPage int
		if idx+1 < len(pages) {
			nextPage = idx + 2
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(coreapigo.ListRecordsResponse{
			Records:  pages[idx],
			NextPage: &nextPage,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDNSList_AllFetchesAllPages(t *testing.T) {
	typeA := "A"
	host1 := "www"
	host2 := "mail"
	ans1 := "1.2.3.4"
	ans2 := "5.6.7.8"
	pages := [][]*coreapigo.Record{
		{{Host: &host1, Answer: &ans1, Type: &typeA}}, // page 1 — NextPage=2
		{{Host: &host2, Answer: &ans2, Type: &typeA}}, // page 2 — NextPage=0
	}
	srv := paginatedRecordServer(t, pages)

	var stdout bytes.Buffer
	cmd := cmdForList(t, srv, &stdout)
	listType = ""
	if err := cmd.ParseFlags([]string{"--all"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	t.Cleanup(func() { listAll = false })

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "1.2.3.4") {
		t.Errorf("output missing page 1 record: %q", out)
	}
	if !strings.Contains(out, "5.6.7.8") {
		t.Errorf("output missing page 2 record: %q", out)
	}
	if strings.Contains(out, "More records") {
		t.Errorf("should not show 'More records' hint when --all fetches everything: %q", out)
	}
}

// ---- dns delete -------------------------------------------------------------

func cmdForDelete(t *testing.T, srv *httptest.Server) *cobra.Command {
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

func TestDNSDelete_BadID(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForDelete(t, srv)
	err := runDelete(cmd, []string{"example.com", "notanumber"})
	if err == nil {
		t.Fatal("expected error for non-integer record ID, got nil")
	}
}

func TestDNSDelete_BadDomain(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForDelete(t, srv)
	err := runDelete(cmd, []string{"nodot", "123"})
	if err == nil {
		t.Fatal("expected error for domain without dot, got nil")
	}
}

func TestDNSDelete_DomainNormalized(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForDelete(t, srv)
	if err := runDelete(cmd, []string{"EXAMPLE.COM", "123"}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if strings.Contains(receivedPath, "EXAMPLE") {
		t.Errorf("domain not normalized in DELETE path: %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "example.com") {
		t.Errorf("expected 'example.com' in DELETE path, got: %q", receivedPath)
	}
}

// ---- dns update -------------------------------------------------------------

func cmdForUpdate(t *testing.T, srv *httptest.Server) *cobra.Command {
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
	cmd.Flags().StringVar(&updateType, "type", "", "")
	cmd.Flags().StringVar(&updateHost, "host", "@", "")
	cmd.Flags().StringVar(&updateAnswer, "answer", "", "")
	cmd.Flags().Int64Var(&updateTTL, "ttl", 300, "")
	cmd.Flags().Int64Var(&updatePriority, "priority", 0, "")
	t.Cleanup(func() { updateType = ""; updateHost = "@"; updateAnswer = ""; updateTTL = 300; updatePriority = 0 })
	return cmd
}

// TestDNSUpdate_TypeChangeRejectedByExistingAnswer verifies that changing
// --type without --answer validates the existing answer against the new type.
// An A record's IPv4 answer must be rejected when the type is changed to AAAA.
func TestDNSUpdate_TypeChangeRejectedByExistingAnswer(t *testing.T) {
	recType := "A"
	recHost := "@"
	recAnswer := "1.2.3.4"
	recID := int(123)
	record := coreapigo.Record{ID: &recID, Type: &recType, Host: &recHost, Answer: &recAnswer}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Error("PUT should not be called when pre-flight validation fails")
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(record)
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--type", "AAAA"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	updateAnswer = "" // not changed

	err := runUpdate(cmd, []string{"example.com", "123"})
	if err == nil {
		t.Fatal("expected error when changing type to AAAA with existing IPv4 answer, got nil")
	}
	if !strings.Contains(err.Error(), "AAAA") {
		t.Errorf("expected 'AAAA' in error, got: %v", err)
	}
}

// TestDNSUpdate_CAAIsRefused guards #169: create and import refuse CAA as a
// usage error because the API rejects it, but update still accepted it, so
// --dry-run previewed a PUT that could only fail.
func TestDNSUpdate_CAAIsRefused(t *testing.T) {
	recType, recHost, recAnswer, recID := "A", "@", "1.2.3.4", 123
	record := coreapigo.Record{ID: &recID, Type: &recType, Host: &recHost, Answer: &recAnswer}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Error("PUT should not be sent for --type CAA")
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(record)
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--type", "CAA", "--answer", `0 issue "letsencrypt.org"`}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	err := runUpdate(cmd, []string{"example.com", "123"})
	if err == nil {
		t.Fatal("expected --type CAA to be refused, got nil")
	}
	var ue *cmdutil.UsageError
	if !errors.As(err, &ue) {
		t.Errorf("expected a usage error (exit 2), got: %v", err)
	}
	if !strings.Contains(err.Error(), "does not accept CAA") {
		t.Errorf("error should say the API does not accept CAA, got: %v", err)
	}
}

func TestDNSUpdate_SuccessPath(t *testing.T) {
	recType := "A"
	recHost := "www"
	recAnswer := "1.2.3.4"
	recID := int(42)
	// A deliberately realistic record: the API replaces the whole record on
	// PUT, so every field the user did not pass has to survive the round trip.
	// ttl is 3600 rather than 300 because 300 is --ttl's default — a fixture
	// using it cannot tell "preserved the record's TTL" apart from "ignored the
	// record and sent the flag default", which is the bug worth catching.
	record := coreapigo.Record{ID: &recID, Type: &recType, Host: &recHost, Answer: &recAnswer, TTL: 3600}

	var putPath string
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			putPath = r.URL.Path
			putBody, _ = io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(record)
			return
		}
		_ = json.NewEncoder(w).Encode(record)
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForUpdate(t, srv)
	if err := cmd.ParseFlags([]string{"--answer", "5.6.7.8"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if err := runUpdate(cmd, []string{"example.com", "42"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if putPath == "" {
		t.Fatal("PUT request was never made")
	}
	if !strings.Contains(putPath, "example.com") || !strings.Contains(putPath, "42") {
		t.Errorf("unexpected PUT path: %q", putPath)
	}
	var sent struct {
		Type   string `json:"type"`
		Host   string `json:"host"`
		Answer string `json:"answer"`
		TTL    int64  `json:"ttl"`
	}
	if err := json.Unmarshal(putBody, &sent); err != nil {
		t.Fatalf("PUT body was not JSON: %v (%s)", err, putBody)
	}
	if sent.Answer != "5.6.7.8" {
		t.Errorf("--answer must reach the wire: sent answer %q, want 5.6.7.8", sent.Answer)
	}
	// Only --answer was passed. Anything else arriving as a zero value or a
	// flag default means this PUT silently rewrote a field the user never
	// mentioned — on DNS, that is a live outage rather than a cosmetic bug.
	if sent.TTL != 3600 {
		t.Errorf("ttl was not passed and must be preserved, got %d", sent.TTL)
	}
	if sent.Host != "www" {
		t.Errorf("host was not passed and must be preserved, got %q", sent.Host)
	}
	if sent.Type != "A" {
		t.Errorf("type was not passed and must be preserved, got %q", sent.Type)
	}
}

// TestDNSList_YAMLEnvelope is the YAML counterpart of the JSON test below.
//
// The two branches are separate statements, so covering one leaves the other
// cold — and `-o yaml` is a documented output mode, not an alias. Splitting a
// format switch is exactly where a change lands in one arm and not the other.
func TestDNSList_YAMLEnvelope(t *testing.T) {
	recType := "A"
	recHost := "@"
	recAnswer := "1.2.3.4"
	records := []*coreapigo.Record{{Host: &recHost, Answer: &recAnswer, Type: &recType}}
	srv := recordServer(t, records, 0)

	var stdout bytes.Buffer
	cmd := cmdForList(t, srv, &stdout)
	out := &output.Config{
		Format:  output.FormatYAML,
		Color:   output.ColorNever,
		Writer:  &stdout,
		EWriter: &bytes.Buffer{},
	}
	cmd.SetContext(context.WithValue(cmd.Context(), cmdutil.KeyOutput, out))

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, "data:") {
		t.Errorf("expected a YAML list envelope with a data key, got: %q", got)
	}
	// The payload, not just the envelope: an empty list still emits `data:`.
	if !strings.Contains(got, "1.2.3.4") {
		t.Errorf("YAML envelope carried no records: %q", got)
	}
}

func TestDNSList_JSONEnvelope(t *testing.T) {
	recType := "A"
	recHost := "@"
	recAnswer := "1.2.3.4"
	records := []*coreapigo.Record{{Host: &recHost, Answer: &recAnswer, Type: &recType}}
	srv := recordServer(t, records, 0)

	var stdout bytes.Buffer
	client, _ := api.New(api.Options{BaseURL: srv.URL})
	out := &output.Config{
		Format:  output.FormatJSON,
		Color:   output.ColorNever,
		Writer:  &stdout,
		EWriter: &bytes.Buffer{},
	}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	cmd.Flags().BoolVar(&listAll, "all", false, "")
	cmd.Flags().StringVar(&listType, "type", "", "")
	listAll, listType = false, ""

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	body := stdout.String()
	if !strings.Contains(body, `"data"`) {
		t.Errorf("expected JSON list envelope with 'data' key, got: %q", body)
	}
}

// TestDNSCreate_ExplicitZeroPriority guards a regression where runCreate gated
// the body on `if createPriority != 0`, deciding by value, while the warning
// immediately above it decided by cmd.Flags().Changed("priority"). Priority 0
// is a perfectly normal MX/SRV preference, so `--priority 0` was both dropped
// from the request and silently robbed of the warning that would have said so.
// runUpdate and runImport already gate on Changed(); only create didn't.
func TestDNSCreate_ExplicitZeroPriority(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding create body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL = "MX", "@", "mail.example.com.", 300
	if err := cmd.ParseFlags([]string{"--priority", "0"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if gotBody == nil {
		t.Fatal("create request was never sent")
	}
	got, ok := gotBody["priority"]
	if !ok {
		t.Fatalf("explicit --priority 0 must be sent, body was: %#v", gotBody)
	}
	if got != float64(0) {
		t.Errorf("expected priority 0, got %#v", got)
	}
}

// TestDNSCreate_OmittedPriorityStaysOmitted is the other half: not passing
// --priority at all must still leave the field off the request.
func TestDNSCreate_OmittedPriorityStaysOmitted(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding create body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "A", "@", "1.2.3.4", 300, 0
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if gotBody == nil {
		t.Fatal("create request was never sent")
	}
	if _, ok := gotBody["priority"]; ok {
		t.Errorf("unset --priority should be omitted, got: %#v", gotBody)
	}
}

// TestDNSCreateForm_PriorityIsSent guards the interactive form dropping the
// MX/SRV priority. dnsCreateForm stored the entered value in createPriority but
// marked only type and answer as changed, and runCreate attaches a priority
// only when Changed("priority") — so the value was discarded, and the user was
// warned "priority is 0" right after typing 10. The huh form itself needs a
// terminal; this drives the step after it, which is where the bug was.
func TestDNSCreateForm_PriorityIsSent(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding create body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	ew := &bytes.Buffer{}
	cmdutil.Out(cmd).EWriter = ew
	// What the form leaves behind for "MX, priority 10".
	createType, createHost, createAnswer, createTTL = "MX", "@", "mail.example.com.", 300
	markFormFlags(cmd, "10")

	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if got := gotBody["priority"]; got != float64(10) {
		t.Errorf("priority entered in the form must be sent: got %#v, body %#v", got, gotBody)
	}
	if strings.Contains(ew.String(), "priority is 0") {
		t.Errorf("warned about priority 0 after the user entered 10; stderr: %q", ew.String())
	}
}

// TestDNSCreateForm_NoPriorityStaysUnset pins that a form with no priority
// (any type other than MX/SRV, or a blank entry) leaves the flag unset.
func TestDNSCreateForm_NoPriorityStaysUnset(t *testing.T) {
	cmd := cmdForCreate(t, neverCalledServer(t))
	createType, createAnswer = "A", "1.2.3.4"
	markFormFlags(cmd, "")
	if cmd.Flags().Changed("priority") {
		t.Error("a blank priority must not mark --priority as set")
	}
	if !cmd.Flags().Changed("type") || !cmd.Flags().Changed("answer") {
		t.Error("type and answer from the form must be marked as set")
	}
}

// cmdForUpdateCapturing is cmdForUpdate with the stderr buffer exposed, so
// tests can assert on warnings rather than only on the request body.
func cmdForUpdateCapturing(t *testing.T, srv *httptest.Server) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := cmdForUpdate(t, srv)
	ew := &bytes.Buffer{}
	cmdutil.Out(cmd).EWriter = ew
	return cmd, ew
}

// TestDNSUpdate_NoFalsePriorityWarning guards a regression where runUpdate
// passed the *flag* variable updatePriority (unset, therefore 0) to
// DNSAnswerWarnings rather than the value it had already merged into
// body.Priority from the existing record. Changing only --answer on a
// priority-10 MX record warned "priority is 0" while the PUT correctly carried
// priority 10 — a renderer reading a value that code path never populated.
func TestDNSUpdate_NoFalsePriorityWarning(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":1,"type":"MX","host":"@","answer":"mail.example.com.","ttl":3600,"priority":10}`))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding update body: %v", err)
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	cmd, ew := cmdForUpdateCapturing(t, srv)
	if err := cmd.ParseFlags([]string{"--answer", "mail2.example.com."}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com", "1"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}

	if strings.Contains(ew.String(), "priority is 0") {
		t.Errorf("warned about priority 0 while sending the record's real priority; stderr: %q", ew.String())
	}
	if got := gotBody["priority"]; got != float64(10) {
		t.Errorf("existing priority should be preserved: expected 10, got %#v", got)
	}
}

// TestDNSUpdate_WarnsWhenPriorityGenuinelyZero pins the warning still firing
// when it should — an MX record that really does have priority 0.
func TestDNSUpdate_WarnsWhenPriorityGenuinelyZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":1,"type":"MX","host":"@","answer":"mail.example.com.","ttl":3600}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	cmd, ew := cmdForUpdateCapturing(t, srv)
	if err := cmd.ParseFlags([]string{"--answer", "mail2.example.com."}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com", "1"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !strings.Contains(ew.String(), "priority is 0") {
		t.Errorf("expected the priority-0 warning for a record with no priority; stderr: %q", ew.String())
	}
}

// TestDNSImport_ReadsStdin covers `--file -`, which both help examples advertise
// (`namecom dns export old.com | namecom dns import new.com --file -`) but which
// os.ReadFile never handled — it failed with "open -: no such file or directory".
func TestDNSImport_ReadsStdin(t *testing.T) {
	const payload = `[{"type":"A","host":"@","answer":"1.2.3.4","ttl":300}]`

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	go func() {
		defer func() { _ = w.Close() }()
		_, _ = w.WriteString(payload)
	}()

	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig; _ = r.Close() })

	got, err := readImportData("-")
	if err != nil {
		t.Fatalf("readImportData(\"-\"): %v", err)
	}
	if string(got) != payload {
		t.Errorf("expected stdin payload %q, got %q", payload, string(got))
	}
}

// TestDNSImport_ReadsFile pins that ordinary paths still work.
func TestDNSImport_ReadsFile(t *testing.T) {
	const payload = `[{"type":"A","host":"@","answer":"1.2.3.4","ttl":300}]`
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	got, err := readImportData(path)
	if err != nil {
		t.Fatalf("readImportData(%q): %v", path, err)
	}
	if string(got) != payload {
		t.Errorf("expected file payload %q, got %q", payload, string(got))
	}
}

// utf16Bytes encodes s as UTF-16 with a leading byte-order mark.
func utf16Bytes(s string, bigEndian bool) []byte {
	var b []byte
	for _, u := range append([]uint16{0xFEFF}, utf16.Encode([]rune(s))...) {
		if bigEndian {
			b = append(b, byte(u>>8), byte(u))
		} else {
			b = append(b, byte(u), byte(u>>8))
		}
	}
	return b
}

// TestDNSImport_DecodesBOMAndUTF16 guards #182. Windows PowerShell 5.1's `>`
// writes UTF-16LE with a byte-order mark, and other editors write a UTF-8 BOM,
// so `dns export X > records.json` then `dns import` failed there with
// "invalid character". The BOM decides the encoding.
func TestDNSImport_DecodesBOMAndUTF16(t *testing.T) {
	const payload = `[{"type":"TXT","host":"@","answer":"héllo","ttl":300}]` + "\r\n"
	cases := map[string][]byte{
		"utf-8":     []byte(payload),
		"utf-8 bom": append([]byte{0xEF, 0xBB, 0xBF}, payload...),
		"utf-16le":  utf16Bytes(payload, false),
		"utf-16be":  utf16Bytes(payload, true),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			got := runImportDryRun(t, output.FormatJSON, string(data))
			if !strings.Contains(got, `"héllo"`) {
				t.Errorf("expected the decoded answer in the preview, got: %q", got)
			}
		})
	}
}

// TestDNSImport_MalformedFileIsUsageError pins that a file that is not a JSON
// array of records exits 2: the input is wrong, not the API or the network.
func TestDNSImport_MalformedFileIsUsageError(t *testing.T) {
	cases := map[string]string{
		"object":         `{"type":"A"}`,
		"trailing comma": `[{"type":"A",}]`,
		"odd utf-16":     "\xff\xfe[",
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "records.json")
			if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
				t.Fatalf("writing import file: %v", err)
			}
			client, err := api.New(api.Options{BaseURL: neverCalledServer(t).URL})
			if err != nil {
				t.Fatalf("api.New: %v", err)
			}
			out := &output.Config{Format: output.FormatTable, Color: output.ColorNever,
				Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
			cmd := &cobra.Command{}
			ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
			ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
			cmd.SetContext(ctx)
			cmd.PersistentFlags().Bool("dry-run", true, "")
			importFile = path
			t.Cleanup(func() { importFile = "" })

			err = runImport(cmd, []string{"example.com"})
			var ue *cmdutil.UsageError
			if !errors.As(err, &ue) {
				t.Errorf("expected a usage error (exit 2), got: %v", err)
			}
		})
	}
}

// cmdForExport builds an export command whose output goes to the returned buffer.
func cmdForExport(t *testing.T, srv *httptest.Server, format output.Format) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var buf bytes.Buffer
	out := &output.Config{Format: format, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd, &buf
}

func recordsServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDNSExport_RespectsYAMLFormat guards runExport calling out.JSON
// unconditionally: `dns export -o yaml` silently emitted JSON. Every other
// command in the package switches on out.Format.
func TestDNSExport_RespectsYAMLFormat(t *testing.T) {
	srv := recordsServer(t, `{"records":[{"id":1,"type":"A","host":"@","fqdn":"example.com.","answer":"1.2.3.4","ttl":300}],"nextPage":0}`)
	cmd, buf := cmdForExport(t, srv, output.FormatYAML)
	t.Cleanup(func() { exportZone = false })

	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, `"answer"`) || strings.HasPrefix(strings.TrimSpace(got), "[") {
		t.Errorf("-o yaml emitted JSON, got: %q", got)
	}
	if !strings.Contains(got, "answer:") {
		t.Errorf("expected YAML mapping syntax, got: %q", got)
	}
}

// TestDNSExport_EmptyZoneIsEmptyList guards an empty zone exporting as `null`.
// fetchAllRecords appends each page to a nil slice, so a zone with no records
// handed out.JSON / out.YAML a nil slice. #112 fixed this for list commands
// through the list envelope, which `dns export` now uses too.
func TestDNSExport_EmptyZoneIsEmptyList(t *testing.T) {
	// The API's empty-list shape, as the sandbox returns it for other lists.
	const empty = `{"totalCount":0,"from":0,"to":0,"records":[]}`
	for _, tc := range []struct {
		name   string
		format output.Format
	}{
		{"json", output.FormatJSON},
		{"yaml", output.FormatYAML},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := cmdForExport(t, recordsServer(t, empty), tc.format)
			if err := runExport(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runExport: %v", err)
			}
			// In the {"data": [...]} envelope every list uses now (#240).
			got := strings.TrimSpace(buf.String())
			if got != "{\n  \"data\": []\n}" && got != "data: []" {
				t.Errorf("an empty zone must export as an empty data list, got %q", got)
			}
		})
	}
}

// TestDNSImport_EmptyFileIsNoOp pins that an empty export imports cleanly:
// `[]` as exported now, and `null` as exported before the fix above.
func TestDNSImport_EmptyFileIsNoOp(t *testing.T) {
	for _, payload := range []string{"[]", "null", "null\n"} {
		t.Run(strings.TrimSpace(payload), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "records.json")
			if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
				t.Fatalf("writing import file: %v", err)
			}
			srv := neverCalledServer(t)
			client, err := api.New(api.Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("api.New: %v", err)
			}
			var stdout bytes.Buffer
			out := &output.Config{Format: output.FormatTable, Color: output.ColorNever,
				Writer: &stdout, EWriter: &bytes.Buffer{}}
			cmd := &cobra.Command{}
			ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
			ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
			cmd.SetContext(ctx)
			importFile = path
			t.Cleanup(func() { importFile = "" })

			if err := runImport(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("importing %q must be a no-op, got: %v", payload, err)
			}
			if !strings.Contains(stdout.String(), "Imported 0 records") {
				t.Errorf("expected a zero-record import, got: %q", stdout.String())
			}
		})
	}
}

// TestDNSExport_ZoneQuotesTXT pins that TXT rdata is quoted in zone output.
// Unquoted, an SPF/DKIM value with spaces parses as several separate
// character-strings, so the exported zone does not describe the same record.
func TestDNSExport_ZoneQuotesTXT(t *testing.T) {
	srv := recordsServer(t, `{"records":[{"id":1,"type":"TXT","host":"@","fqdn":"example.com.","answer":"v=spf1 include:_spf.google.com ~all","ttl":300}],"nextPage":0}`)
	cmd, buf := cmdForExport(t, srv, output.FormatTable)
	exportZone = true
	t.Cleanup(func() { exportZone = false })

	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if !strings.Contains(got, `"v=spf1 include:_spf.google.com ~all"`) {
		t.Errorf("TXT rdata must be quoted in zone output, got: %q", got)
	}
}

// TestQuoteTXT_SplitsLongValues guards zone output that standard parsers
// reject. RFC 1035 caps a character-string at 255 bytes, and a 2048-bit DKIM
// key is about 400; quoteTXT wrote it as a single quoted string. It must be
// split into several, each at most 255 bytes of the unescaped value, and the
// split must fall between characters, never inside an escape sequence.
func TestQuoteTXT_SplitsLongValues(t *testing.T) {
	// A quote lands at byte 254, so a split that escaped first and cut the
	// escaped text at 255 would separate its backslash from the quote.
	value := strings.Repeat("a", 254) + `"` + strings.Repeat("b", 300)

	got := quoteTXT(value)

	var chunks []string
	for rest := got; rest != ""; {
		if rest[0] != '"' {
			t.Fatalf("expected a quoted character-string at %q in %q", rest, got)
		}
		end := 1
		for ; end < len(rest); end++ {
			if rest[end] == '\\' {
				end++
				continue
			}
			if rest[end] == '"' {
				break
			}
		}
		if end >= len(rest) {
			t.Fatalf("unterminated character-string in %q", got)
		}
		inner := rest[1:end]
		unescaped := strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(inner)
		if len(unescaped) > 255 {
			t.Errorf("character-string is %d bytes, max 255", len(unescaped))
		}
		chunks = append(chunks, unescaped)
		rest = strings.TrimPrefix(rest[end+1:], " ")
	}
	if len(chunks) != 3 {
		t.Errorf("expected 3 character-strings for a %d-byte value, got %d: %q", len(value), len(chunks), got)
	}
	if joined := strings.Join(chunks, ""); joined != value {
		t.Errorf("split changed the value:\n got %q\nwant %q", joined, value)
	}
}

// TestQuoteTXT_ShortValueIsOneString pins that a value under the limit is
// still written as exactly one quoted string.
func TestQuoteTXT_ShortValueIsOneString(t *testing.T) {
	if got, want := quoteTXT(`say "hi"`), `"say \"hi\""`; got != want {
		t.Errorf("quoteTXT = %q, want %q", got, want)
	}
}

// TestQuoteTXT_MalformedQuotedIsRequoted guards #187: a value that starts and
// ends with a quote was passed through as zone syntax unchecked, so `"a"b"`
// (unbalanced) or a single quoted string over 255 bytes was written as-is and
// the zone did not load. Only a well-formed run of character-strings, each
// within the limit, is kept as zone syntax; anything else is content, and is
// quoted and split like any other value.
func TestQuoteTXT_MalformedQuotedIsRequoted(t *testing.T) {
	long := strings.Repeat("k", 300)
	tests := []struct{ name, in, want string }{
		{"unbalanced", `"a"b"`, `"\"a\"b\""`},
		{"dangling escape", `"a\"`, `"\"a\\\""`},
		{"bad decimal escape", `"\999"`, `"\"\\999\""`},
		{"over 255 bytes", `"` + long + `"`,
			`"\"` + strings.Repeat("k", 254) + `" "` + strings.Repeat("k", 46) + `\""`},
		{"well-formed pair is kept", `"a b" "c"`, `"a b" "c"`},
		{"extra separator space is normalized", `"a"  "b"`, `"a" "b"`},
		{"255 bytes is kept", `"` + strings.Repeat("k", 255) + `"`, `"` + strings.Repeat("k", 255) + `"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteTXT(tc.in); got != tc.want {
				t.Errorf("quoteTXT(%q) =\n %q\nwant\n %q", tc.in, got, tc.want)
			}
		})
	}
}

// txtControlCases are TXT answers holding characters that cannot appear raw in
// a zone-file string, and how quoteTXT must write them (#188).
var txtControlCases = []struct{ name, in, want string }{
	{"newline", "a\nb", `"a\010b"`},
	{"tab and CR", "a\tb\r", `"a\009b\013"`},
	{"DEL", "a\x7fb", `"a\127b"`},
	{"already quoted", "\"\n\"", `"\010"`},
	{"already quoted, escaped newline", "\"a\\\nb\"", `"a\010b"`},
	{"already quoted, escaped backslash", "\"a\\\\\nb\"", `"a\\\010b"`},
}

// TestQuoteTXT_EscapesControlCharacters guards #188: quoteTXT escaped only
// backslash and quote, so a newline in a TXT value was written raw inside the
// quoted string and the whole zone failed to load ("unbalanced quotes"). Such
// characters must become RFC 1035 \DDD decimal escapes, which keep the value.
func TestQuoteTXT_EscapesControlCharacters(t *testing.T) {
	for _, tc := range txtControlCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteTXT(tc.in); got != tc.want {
				t.Errorf("quoteTXT(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDNSExport_ZoneControlCharactersLoad checks the same values end to end
// with BIND's named-checkzone: the exported zone must load, and its TXT
// records must hold the original bytes.
func TestDNSExport_ZoneControlCharactersLoad(t *testing.T) {
	checkzone, err := exec.LookPath("named-checkzone")
	if err != nil {
		t.Skip("named-checkzone not installed")
	}
	var recs []string
	for i, tc := range txtControlCases {
		answer, _ := json.Marshal(tc.in)
		recs = append(recs, fmt.Sprintf(`{"id":%d,"type":"TXT","host":"t%d","fqdn":"t%d.example.com.","answer":%s,"ttl":300}`, i, i, i, answer))
	}
	srv := recordsServer(t, `{"records":[`+strings.Join(recs, ",")+`],"nextPage":0}`)
	cmd, buf := cmdForExport(t, srv, output.FormatTable)
	exportZone = true
	t.Cleanup(func() { exportZone = false })
	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	zone := "$ORIGIN example.com.\n" +
		"@\t300\tIN\tSOA\tns1.example.com. hostmaster.example.com. 1 3600 600 86400 300\n" +
		"@\t300\tIN\tNS\tns1.example.com.\n" +
		"ns1\t300\tIN\tA\t192.0.2.1\n" + buf.String()
	path := filepath.Join(t.TempDir(), "example.com.zone")
	if err := os.WriteFile(path, []byte(zone), 0o600); err != nil {
		t.Fatalf("writing zone: %v", err)
	}
	dump, err := exec.Command(checkzone, "-q", "-D", "-o", "-", "example.com", path).CombinedOutput() //nolint:gosec
	if err != nil {
		t.Fatalf("named-checkzone rejected the zone: %v\n%s\nzone:\n%s", err, dump, zone)
	}
	for _, tc := range txtControlCases {
		if !strings.Contains(string(dump), tc.want) {
			t.Errorf("%s: loaded zone lacks %s:\n%s", tc.name, tc.want, dump)
		}
	}
}

// TestDNSExport_ZoneANAMEIsComment pins that ANAME, a name.com-specific type
// with no standard RR, is written as a comment. Written as a record, the line
// made the whole file unparseable by BIND, NSD, and miekg/dns.
func TestDNSExport_ZoneANAMEIsComment(t *testing.T) {
	srv := recordsServer(t, `{"records":[`+
		`{"id":1,"type":"ANAME","host":"","fqdn":"example.com.","answer":"target.example.net.","ttl":300},`+
		`{"id":2,"type":"A","host":"www","fqdn":"www.example.com.","answer":"1.2.3.4","ttl":300}`+
		`],"nextPage":0}`)
	cmd, buf := cmdForExport(t, srv, output.FormatTable)
	exportZone = true
	t.Cleanup(func() { exportZone = false })

	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], ";") {
		t.Errorf("ANAME must be written as a comment, got: %q", lines[0])
	}
	if !strings.Contains(lines[0], "target.example.net.") {
		t.Errorf("the ANAME comment should keep the record's target, got: %q", lines[0])
	}
	if strings.HasPrefix(lines[1], ";") {
		t.Errorf("an A record must not be commented out, got: %q", lines[1])
	}
}

// TestDNSExport_ZoneMXWithoutPriority pins that an MX record whose priority is
// absent still emits a priority field. Omitting it produces a zone line with
// the wrong number of fields, which parsers reject.
func TestDNSExport_ZoneMXWithoutPriority(t *testing.T) {
	srv := recordsServer(t, `{"records":[{"id":1,"type":"MX","host":"@","fqdn":"example.com.","answer":"mail.example.com.","ttl":300}],"nextPage":0}`)
	cmd, buf := cmdForExport(t, srv, output.FormatTable)
	exportZone = true
	t.Cleanup(func() { exportZone = false })

	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	fields := strings.Fields(got)
	// name ttl IN MX <priority> <target>
	if len(fields) != 6 {
		t.Fatalf("expected 6 zone fields for an MX record, got %d: %q", len(fields), got)
	}
	if fields[4] != "0" {
		t.Errorf("expected default priority 0, got %q in %q", fields[4], got)
	}
}

// TestDNSExport_ZoneQualifiesTargets guards hostname targets written relative
// to the origin. The API strips the trailing dot on storage, so a CNAME to
// example.net comes back as "example.net", and a zone file reads that as
// example.net.<origin>. The records are a real sandbox listing.
func TestDNSExport_ZoneQualifiesTargets(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "sandbox_list_records.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := recordsServer(t, string(body))
	cmd, buf := cmdForExport(t, srv, output.FormatTable)
	exportZone = true
	t.Cleanup(func() { exportZone = false })

	if err := runExport(cmd, []string{"namecom-smoke-37de95.com"}); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"blog.namecom-smoke-37de95.com.\t300\tIN\tCNAME\texample.net.\n",
		"_sip._tcp.namecom-smoke-37de95.com.\t300\tIN\tSRV\t20 10 5060 sip.example.net.\n",
		"namecom-smoke-37de95.com.\t300\tIN\tMX\t10 mx6.name.com.\n",
		"; ANAME not representable in a zone file: api.namecom-smoke-37de95.com.\t300\tIN\tANAME\texample.org.\n",
		// Not hostnames: left exactly as they were.
		"www.namecom-smoke-37de95.com.\t300\tIN\tA\t192.0.2.1\n",
		"namecom-smoke-37de95.com.\t300\tIN\tTXT\t\"v=spf1 a mx ~all\"\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing zone line %q in:\n%s", want, got)
		}
	}
}

// TestDNSExport_ZoneQualifiesTargetsOnce pins the edge cases around the
// trailing dot: a target that already has one is not given a second, and the
// root "." (null MX, RFC 7505; "no service" SRV, RFC 2782) stays ".".
func TestDNSExport_ZoneQualifiesTargetsOnce(t *testing.T) {
	srv := recordsServer(t, `{"records":[`+
		`{"id":1,"type":"NS","host":"sub","fqdn":"sub.example.com.","answer":"ns1.example.net","ttl":300},`+
		`{"id":2,"type":"CNAME","host":"www","fqdn":"www.example.com.","answer":"example.net.","ttl":300},`+
		`{"id":3,"type":"MX","host":"","fqdn":"example.com.","answer":".","priority":0,"ttl":300},`+
		`{"id":4,"type":"SRV","host":"_x._tcp","fqdn":"_x._tcp.example.com.","answer":"0 0 .","priority":0,"ttl":300},`+
		`{"id":5,"type":"AAAA","host":"v6","fqdn":"v6.example.com.","answer":"2001:db8::1","ttl":300}`+
		`],"nextPage":0}`)
	cmd, buf := cmdForExport(t, srv, output.FormatTable)
	exportZone = true
	t.Cleanup(func() { exportZone = false })

	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	want := "sub.example.com.\t300\tIN\tNS\tns1.example.net.\n" +
		"www.example.com.\t300\tIN\tCNAME\texample.net.\n" +
		"example.com.\t300\tIN\tMX\t0 .\n" +
		"_x._tcp.example.com.\t300\tIN\tSRV\t0 0 0 .\n" +
		"v6.example.com.\t300\tIN\tAAAA\t2001:db8::1\n"
	if got := buf.String(); got != want {
		t.Errorf("zone output:\n got %q\nwant %q", got, want)
	}
}

// TestDNSDelete_DryRunWorksNonInteractively guards an ordering bug that made
// --dry-run unusable in exactly the setting it exists for. confirmDelete ran
// BEFORE the dryRun branch, and cmdutil.Confirm errors out when stdin is not a
// TTY and --yes was not passed. So a scripted `dns delete … --dry-run` exited
// nonzero with "pass --yes to confirm in non-interactive mode" — demanding the
// user consent to an action the dry run was never going to perform.
//
// Interactively it was just as wrong: it prompted a human about a deletion that
// would not happen. `domain privacy on` already checked dryRun first, which is
// what makes this an inconsistency rather than a convention.
func TestDNSDelete_DryRunWorksNonInteractively(t *testing.T) {
	defer output.StubInteractive(false)()

	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// delete looks the record up first, to show it and to fail early
		// when it is missing (#235); only the DELETE is the real request.
		if r.Method != http.MethodGet {
			called = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var buf bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)

	root := &cobra.Command{Use: "namecom"}
	var dr, yes bool
	root.PersistentFlags().BoolVar(&dr, "dry-run", false, "")
	root.PersistentFlags().BoolVarP(&yes, "yes", "y", false, "")
	if err := root.PersistentFlags().Set("dry-run", "true"); err != nil {
		t.Fatalf("setting dry-run flag: %v", err)
	}
	root.AddCommand(cmd)

	// No --yes: a dry run must not require consent for something it won't do.
	if err := runDelete(cmd, []string{"example.com", "123"}); err != nil {
		t.Fatalf("--dry-run without --yes should succeed non-interactively, got: %v", err)
	}
	if called {
		t.Error("--dry-run issued a real request")
	}
	if !strings.Contains(buf.String(), "/core/v1/domains/example.com/records/123") {
		t.Errorf("expected the dry-run request line, got: %q", buf.String())
	}
}

// TestDNSImport_PartialFailureReportsProgress guards a data-safety gap: the
// import loop returned immediately on the first API error and discarded the
// `created` count, so a failure on record 3 of 5 surfaced as a bare
// "creating A www: ..." with no indication that two records were already
// written. Re-running the same file then duplicated them.
//
// The user needs to know exactly what landed before they retry.
func TestDNSImport_PartialFailureReportsProgress(t *testing.T) {
	payload := `[
	  {"type":"A","host":"one","answer":"1.1.1.1","ttl":300},
	  {"type":"A","host":"two","answer":"2.2.2.2","ttl":300},
	  {"type":"A","host":"boom","answer":"3.3.3.3","ttl":300},
	  {"type":"A","host":"four","answer":"4.4.4.4","ttl":300}
	]`
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing import file: %v", err)
	}

	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 3 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Invalid answer"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var stdout, stderr bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &stdout, EWriter: &stderr}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	importFile = path
	t.Cleanup(func() { importFile = "" })

	err = runImport(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected an error when a record fails to import")
	}

	combined := err.Error() + stdout.String() + stderr.String()
	// The two records that succeeded before the failure must be reported.
	if !strings.Contains(combined, "2") {
		t.Errorf("partial import must report how many records were already created; got error %q and output %q",
			err.Error(), stdout.String()+stderr.String())
	}
	if !strings.Contains(combined, "boom") {
		t.Errorf("error should name the record that failed, got: %q", err.Error())
	}
}

// TestDNSList_TypeFilterSearchesAllPages guards a wrong-results bug: --type
// filters client-side (dns.go) but did NOT imply auto-pagination, so it only
// ever saw page 1. `domain list` and `order list` both auto-page when any
// filter is active; dns did not.
//
// The failure is silent and actively misleading: a zone whose MX records live
// on page 2 reported "No DNS records found." plus a hint to create "the first
// record" — three false statements at once.
func TestDNSList_TypeFilterSearchesAllPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"records":[{"id":22,"type":"MX","host":"@","answer":"mail.example.com.","ttl":300,"priority":10}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"records":[{"id":11,"type":"A","host":"@","answer":"1.2.3.4","ttl":300}],"nextPage":2}`))
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
	cmd.Flags().StringVar(&listType, "type", "", "")
	if err := cmd.Flags().Set("type", "MX"); err != nil {
		t.Fatalf("setting type flag: %v", err)
	}
	listType = "MX"
	t.Cleanup(func() { listAll = false; listType = "" })

	if err := runList(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runList: %v", err)
	}

	var env struct {
		Data []struct {
			ID int32 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(env.Data) != 1 || env.Data[0].ID != 22 {
		t.Errorf("--type MX must find the MX record on page 2, got: %s", buf.String())
	}
}

// TestDNSImport_ValidatesBeforeWriting guards the one bulk-write path that
// performed no client-side validation. dns create checks type/host/answer
// before sending; the import loop sent whatever the file contained.
//
// That combines badly with import not being transactional: a file whose 4th
// record is malformed writes 3 records, then fails on a server-side 422. Every
// record is validated up front so a bad file is rejected before anything is
// written.
func TestDNSImport_ValidatesBeforeWriting(t *testing.T) {
	// Third record has an invalid A answer; the first two are fine.
	payload := `[
	  {"type":"A","host":"one","answer":"1.1.1.1","ttl":300},
	  {"type":"A","host":"two","answer":"2.2.2.2","ttl":300},
	  {"type":"A","host":"bad","answer":"not-an-ip","ttl":300}
	]`
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing import file: %v", err)
	}

	var writes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writes++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever,
		Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	importFile = path
	t.Cleanup(func() { importFile = "" })

	err = runImport(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected an error for a malformed record")
	}
	if writes != 0 {
		t.Errorf("a malformed file must be rejected before any record is written, got %d write(s)", writes)
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error should identify the offending record, got: %v", err)
	}
}

// notFoundServer answers every request with the API's 404 envelope.
func notFoundServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDNSNotFound_KeepsExitCodeUnderFriendlyMessage covers both dns sites that
// replace a 404 with a friendlier message. They used fmt.Errorf, which kept the
// text and dropped the status code, so the command exited 1 instead of the
// documented 4. Each case asserts both halves: the message a person reads,
// and the 404 a script's exit-code check depends on.
func TestDNSNotFound_KeepsExitCodeUnderFriendlyMessage(t *testing.T) {
	t.Run("dns list on an unknown domain", func(t *testing.T) {
		var stdout bytes.Buffer
		cmd := cmdForList(t, notFoundServer(t), &stdout)
		listAll, listType = false, ""
		err := runList(cmd, []string{"example.com"})
		assertFriendlyNotFound(t, err, `domain "example.com" not found`)
	})
	t.Run("dns update on an unknown record", func(t *testing.T) {
		cmd := cmdForUpdate(t, notFoundServer(t))
		err := runUpdate(cmd, []string{"example.com", "42"})
		assertFriendlyNotFound(t, err, "record 42 not found on example.com")
	})
}

func assertFriendlyNotFound(t *testing.T, err error, wantMsg string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a not-found error, got nil")
	}
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("message = %q, want it to contain %q", err.Error(), wantMsg)
	}
	if !cmdutil.IsNotFound(err) {
		t.Errorf("error %q no longer carries the 404, so the command exits 1 instead of 4", err)
	}
}

// TestRecordRows_ApexHostShowsAt pins that the apex renders as "@", the same
// spelling `dns create --host` takes and defaults to. The API returns the apex
// host as "", which rendered as an empty cell — indistinguishable from a
// column that failed to render.
func TestRecordRows_ApexHostShowsAt(t *testing.T) {
	apex, typ, answer := "", "A", "1.2.3.4"
	rec := []*coreapigo.Record{{Host: &apex, Type: &typ, Answer: &answer, TTL: 300}}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}

	if got := recordRows(out, rec)[0][2]; got != "@" {
		t.Errorf("flat view HOST = %q, want %q", got, "@")
	}
	if got := recordRowsNoType(out, rec)[0][1]; got != "@" {
		t.Errorf("grouped view HOST = %q, want %q", got, "@")
	}
}

// runImportCapturing runs `dns import` against payload and returns the decoded
// body of every create request that reached the server.
func runImportCapturing(t *testing.T, payload string) ([]map[string]any, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing import file: %v", err)
	}

	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Errorf("decoding create body: %v", err)
		}
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever,
		Writer: &bytes.Buffer{}, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	importFile = path
	t.Cleanup(func() { importFile = "" })

	err = runImport(cmd, []string{"example.com"})
	return bodies, err
}

// TestDNSImport_ApexFromExport guards the documented `dns export | dns import`
// round trip. The API returns the apex host as "" — not "@" — and import ran
// every record through ValidDNSHost, which rejects "". Any zone with an apex
// record aborted the import. The fixture is shaped the way export writes it;
// the older import fixtures used "@", which is why they never caught this.
func TestDNSImport_ApexFromExport(t *testing.T) {
	payload := `[
	  {"id":1,"domainName":"old.com","host":"","fqdn":"old.com.","type":"A","answer":"1.2.3.4","ttl":300},
	  {"id":2,"domainName":"old.com","host":"www","fqdn":"www.old.com.","type":"A","answer":"1.2.3.4","ttl":300}
	]`
	bodies, err := runImportCapturing(t, payload)
	if err != nil {
		t.Fatalf("importing an exported apex record: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 create requests, got %d", len(bodies))
	}
	// Sent as "@", the same spelling `dns create --host` defaults to.
	if got := bodies[0]["host"]; got != "@" {
		t.Errorf("apex host: got %#v, want %q", got, "@")
	}
	if got := bodies[1]["host"]; got != "www" {
		t.Errorf("non-apex host: got %#v, want %q", got, "www")
	}
}

// TestDNSImport_MissingTTLDefaults pins that a record with no ttl is sent with
// the same 300 that `dns create --ttl` defaults to. It used to be sent as 0,
// which the server rejects — after the records before it were already created.
func TestDNSImport_MissingTTLDefaults(t *testing.T) {
	bodies, err := runImportCapturing(t, `[{"type":"A","host":"www","answer":"1.2.3.4"}]`)
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("expected 1 create request, got %d", len(bodies))
	}
	if got := bodies[0]["ttl"]; got != float64(300) {
		t.Errorf("missing ttl: got %#v, want 300", got)
	}
}

// TestDNSImport_ValidatesTTLBeforeWriting is the other half: a TTL that is
// present but invalid is rejected before any record is written, like every
// other field in the validate-first loop.
func TestDNSImport_ValidatesTTLBeforeWriting(t *testing.T) {
	payload := `[
	  {"type":"A","host":"one","answer":"1.1.1.1","ttl":300},
	  {"type":"A","host":"short","answer":"2.2.2.2","ttl":60}
	]`
	bodies, err := runImportCapturing(t, payload)
	if err == nil {
		t.Fatal("expected an error for a TTL below 300")
	}
	if len(bodies) != 0 {
		t.Errorf("an invalid TTL must be rejected before any record is written, got %d write(s)", len(bodies))
	}
	if !strings.Contains(err.Error(), "short") {
		t.Errorf("error should identify the offending record, got: %v", err)
	}
}
