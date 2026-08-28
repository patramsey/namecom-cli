package dns

import (
	"bytes"
	"context"
	"encoding/json"
	coreapigo "github.com/namedotcom/core-api-go"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

func TestDNSCreate_CAABadTag(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "CAA", "@", "0 badtag letsencrypt.org", 300, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for invalid CAA tag, got nil")
	}
	if !strings.Contains(err.Error(), "tag") {
		t.Errorf("expected 'tag' in error, got: %v", err)
	}
}

func TestDNSCreate_CAAFlagsOutOfRange(t *testing.T) {
	srv := neverCalledServer(t)
	cmd := cmdForCreate(t, srv)
	createType, createHost, createAnswer, createTTL, createPriority = "CAA", "@", "256 issue letsencrypt.org", 300, 0

	err := runCreate(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("expected error for CAA flags > 255, got nil")
	}
	if !strings.Contains(err.Error(), "flags") {
		t.Errorf("expected 'flags' in error, got: %v", err)
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
	records := []*coreapigo.Record{{Host: &recHost, Answer: &recAnswer, Type: &recType}}
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
	if !strings.Contains(stdout.String(), "More records") {
		t.Errorf("expected 'More records' hint when hasMore=true, got: %q", stdout.String())
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

func TestDNSList_TypeFilter(t *testing.T) {
	typeA := "A"
	typeMX := "MX"
	hostAt := "@"
	answerA := "1.2.3.4"
	answerMX := "mail.example.com"
	records := []*coreapigo.Record{
		{Host: &hostAt, Answer: &answerA, Type: &typeA},
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
	if strings.Contains(out, "mail.example.com") {
		t.Errorf("MX record should be filtered out, but appears in output: %q", out)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
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
	importDryRun = false
	t.Cleanup(func() { importFile = ""; importDryRun = false })

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
	importFile, importDryRun = path, false
	t.Cleanup(func() { importFile = ""; importDryRun = false })

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
