package url

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// wwwZone has address records on www, a record of another type there, and
// an A record on another host.
const wwwZone = `{"records":[
	{"id":1,"host":"www","type":"A","answer":"192.0.2.10","ttl":300},
	{"id":2,"host":"WWW","type":"AAAA","answer":"2001:db8::10","ttl":300},
	{"id":3,"host":"www","type":"TXT","answer":"keep me","ttl":300},
	{"id":4,"host":"api","type":"A","answer":"192.0.2.20","ttl":300}
],"lastPage":1}`

const (
	wantAEffect    = "name.com replaces A www 192.0.2.10 with its forwarding record, and deleting the forwarding does not restore it"
	wantAAAAEffect = "name.com replaces AAAA www 2001:db8::10 with its forwarding record, and deleting the forwarding does not restore it"
	recordsPath    = "GET /core/v1/domains/example.com/records"
	createPath     = "POST /core/v1/domains/example.com/url/forwarding"
)

// zoneServer answers the records list with zone and a create with a new
// forwarding, recording each request as "METHOD /path".
func zoneServer(t *testing.T, zone string, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(zone))
			return
		}
		_, _ = w.Write([]byte(`{"id":7,"host":"www","forwardsTo":"https://example.org","type":"redirect"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// flagged gives child a root carrying --dry-run and --yes, set as asked.
func flagged(t *testing.T, child *cobra.Command, dryRun, yes bool) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "namecom"}
	root.PersistentFlags().Bool("dry-run", dryRun, "")
	root.PersistentFlags().Bool("yes", yes, "")
	root.AddCommand(child)
	return child
}

// TestURLCreateDryRun_NamesReplacedRecords pins #358: `url create --host
// www` replaced www's A record without a word, and the dry run showed only
// the POST. One records read now names each A, AAAA and CNAME record on the
// host, in JSON's sideEffects and on "Would also" lines in table mode.
func TestURLCreateDryRun_NamesReplacedRecords(t *testing.T) {
	defer output.StubInteractive(false)()
	for name, format := range map[string]output.Format{"json": output.FormatJSON, "table": output.FormatTable} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			cmd := flagged(t, cmdForURLCreate(t, zoneServer(t, wwwZone, &seen)), true, false)
			out := cmdutil.Out(cmd)
			out.Format = format
			if err := cmd.ParseFlags([]string{"--to", "https://example.org", "--host", "www"}); err != nil {
				t.Fatal(err)
			}
			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if strings.Join(seen, ",") != recordsPath {
				t.Errorf("sent %v, want only %s", seen, recordsPath)
			}
			stdout := out.Writer.(*bytes.Buffer).String()
			stderr := out.EWriter.(*bytes.Buffer).String()
			if format == output.FormatJSON {
				var doc struct {
					SideEffects []string `json:"sideEffects"`
				}
				if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
					t.Fatalf("stdout %q: %v", stdout, err)
				}
				if strings.Join(doc.SideEffects, "\n") != wantAEffect+"\n"+wantAAAAEffect {
					t.Errorf("sideEffects = %q\nwant [%q %q]", doc.SideEffects, wantAEffect, wantAAAAEffect)
				}
				return
			}
			for _, want := range []string{"Would also: " + wantAEffect, "Would also: " + wantAAAAEffect} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr = %q\nwant %q", stderr, want)
				}
			}
			if strings.Contains(stderr, "keep me") || strings.Contains(stderr, "192.0.2.20") {
				t.Errorf("stderr = %q names a record forwarding leaves alone", stderr)
			}
		})
	}
}

// TestURLCreate_ReplacingRecordsNeedsYes: a create that replaces records
// asks first, so a script without --yes gets the confirmation error (exit
// 2) naming them and nothing is created. A host with no address records is
// not asked about, and the apex is not read at all.
func TestURLCreate_ReplacingRecordsNeedsYes(t *testing.T) {
	defer output.StubInteractive(false)()
	for name, tc := range map[string]struct {
		host     string
		yes      bool
		wantSent string
		wantErr  bool
	}{
		"www, no --yes":   {"www", false, recordsPath, true},
		"www, --yes":      {"www", true, recordsPath + "," + createPath, false},
		"bare host":       {"blog", false, recordsPath + "," + createPath, false},
		"apex, no --yes":  {"@", false, createPath, false},
		"fqdn, no --yes":  {"www.example.com", false, recordsPath, true},
		"upper, no --yes": {"WWW", false, recordsPath, true},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			cmd := flagged(t, cmdForURLCreate(t, zoneServer(t, wwwZone, &seen)), false, tc.yes)
			if err := cmd.ParseFlags([]string{"--to", "https://example.org", "--host", tc.host}); err != nil {
				t.Fatal(err)
			}
			err := runCreate(cmd, []string{"example.com"})
			if strings.Join(seen, ",") != tc.wantSent {
				t.Errorf("sent %v, want %s", seen, tc.wantSent)
			}
			if !tc.wantErr {
				if err != nil {
					t.Errorf("runCreate = %v, want success", err)
				}
				return
			}
			if _, ok := errors.AsType[*cmdutil.ConfirmationRequiredError](err); !ok {
				t.Fatalf("runCreate = %v, want a confirmation error", err)
			}
			usage, ok := errors.AsType[*cmdutil.UsageError](err) // exit 2
			if !ok {
				t.Fatalf("runCreate = %v, want a usage error", err)
			}
			for _, want := range []string{"A www 192.0.2.10", "AAAA www 2001:db8::10", "--yes"} {
				if !strings.Contains(err.Error()+" "+usage.UserHint(), want) {
					t.Errorf("error = %q (hint %q), want it to name %q", err, usage.UserHint(), want)
				}
			}
		})
	}
}
