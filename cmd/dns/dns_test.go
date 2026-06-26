package dns

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
		Format: output.FormatTable,
		Color:  output.ColorNever,
		Writer: &bytes.Buffer{},
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
