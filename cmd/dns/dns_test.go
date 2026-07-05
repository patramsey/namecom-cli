package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
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
		recID := int32(1)
		recHost := "@"
		recAnswer := "1.2.3.4"
		_ = json.NewEncoder(w).Encode(gen.Record{
			Id:     &recID,
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

func recordServer(t *testing.T, records []gen.Record, nextPage int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.ListRecordsResponseSchema{
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
	records := []gen.Record{{Host: &recHost, Answer: &recAnswer, Type: &recType}}
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
	records := []gen.Record{{Host: &recHost, Answer: &recAnswer, Type: &recType}}
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
	records := []gen.Record{
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
func paginatedRecordServer(t *testing.T, pages [][]gen.Record) *httptest.Server {
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
		var nextPage int32
		if idx+1 < len(pages) {
			nextPage = int32(idx + 2)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.ListRecordsResponseSchema{
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
	pages := [][]gen.Record{
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
	recID := int32(123)
	record := gen.Record{Id: &recID, Type: &recType, Host: &recHost, Answer: &recAnswer}

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

func TestDNSList_JSONEnvelope(t *testing.T) {
	recType := "A"
	recHost := "@"
	recAnswer := "1.2.3.4"
	records := []gen.Record{{Host: &recHost, Answer: &recAnswer, Type: &recType}}
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
