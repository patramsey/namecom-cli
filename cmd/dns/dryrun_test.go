package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/drifttest"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

const importPayload = `[
  {"id":1,"domainName":"old.com","host":"","fqdn":"old.com.","type":"A","answer":"1.2.3.4","ttl":300},
  {"id":2,"domainName":"old.com","host":"mail","fqdn":"mail.old.com.","type":"MX","answer":"mx.old.com","ttl":3600,"priority":10}
]`

// runImportDryRun runs `dns import --dry-run` in format against payload and
// returns what it printed. The server fails the test if it sees any request.
func runImportDryRun(t *testing.T, format output.Format, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing import file: %v", err)
	}
	client, err := api.New(api.Options{BaseURL: neverCalledServer(t).URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	var buf bytes.Buffer
	out := &output.Config{Format: format, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	cmd.PersistentFlags().Bool("dry-run", true, "")
	importFile = path
	t.Cleanup(func() { importFile = "" })

	if err := runImport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("dns import --dry-run: %v", err)
	}
	return buf.String()
}

// TestDNSImport_DryRunJSON pins issue #137 for `dns import`, which previews one
// request per record: in JSON mode the plan is a single array of dry-run
// documents, and each body is the body the live import sends for that record.
func TestDNSImport_DryRunJSON(t *testing.T) {
	printed := runImportDryRun(t, output.FormatJSON, importPayload)

	var docs []struct {
		DryRun bool           `json:"dry_run"`
		Method string         `json:"method"`
		Path   string         `json:"path"`
		Body   map[string]any `json:"body"`
	}
	if err := json.Unmarshal([]byte(printed), &docs); err != nil {
		t.Fatalf("dns import --dry-run -o json is not a JSON array: %v\n%s", err, printed)
	}

	sent, err := runImportCapturing(t, importPayload)
	if err != nil {
		t.Fatalf("live import: %v", err)
	}
	if len(docs) != len(sent) {
		t.Fatalf("previewed %d requests, sent %d", len(docs), len(sent))
	}
	for i, d := range docs {
		if !d.DryRun || d.Method != "POST" || d.Path != "/core/v1/domains/example.com/records" {
			t.Errorf("preview %d = %v %s %s, want a dry-run POST to the records path", i, d.DryRun, d.Method, d.Path)
		}
		if !reflect.DeepEqual(d.Body, sent[i]) {
			t.Errorf("preview %d body = %v, but the import sends %v", i, d.Body, sent[i])
		}
	}
}

// TestDNSImport_DryRunTable keeps the human preview: a request line per record.
func TestDNSImport_DryRunTable(t *testing.T) {
	printed := runImportDryRun(t, output.FormatTable, importPayload)
	if n := strings.Count(printed, "POST /core/v1/domains/example.com/records\n"); n != 2 {
		t.Errorf("want 2 request lines, got %d in:\n%s", n, printed)
	}
}

// TestDryRunMatchesRealRequest_DNSCreateJSON runs the body drift check with
// -o json, where --dry-run prints a document rather than a request line, so
// the structured preview is held to the same standard as the text one.
func TestDryRunMatchesRealRequest_DNSCreateJSON(t *testing.T) {
	setup := func(t *testing.T, srv *httptest.Server) *cobra.Command {
		cmd := cmdForCreate(t, srv)
		cmdutil.Out(cmd).Format = output.FormatJSON
		if err := cmd.ParseFlags([]string{"--type", "MX", "--host", "@", "--answer", "mx.example.com", "--priority", "10"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		return cmd
	}
	drifttest.AssertDryRunMatches(t, setup, runCreate, []string{"example.com"}, `{"id":1}`)
	drifttest.AssertDryRunBodyMatches(t, setup, runCreate, []string{"example.com"}, `{"id":1}`)
}
