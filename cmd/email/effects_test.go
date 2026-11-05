package email

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

// The zone of the #358 sweep: an apex MX of the domain's own, a subdomain MX
// forwarding leaves alone, and an SPF record of its own.
const (
	zoneElsewhere = `{"records":[
		{"id":1,"host":"","type":"MX","answer":"mail2.example.net","priority":1,"ttl":300},
		{"id":2,"host":"sub","type":"MX","answer":"mx.sub.example.net","priority":5,"ttl":300},
		{"id":3,"host":"@","type":"TXT","answer":"v=spf1 include:_spf.google.com ~all","ttl":300},
		{"id":4,"host":"","type":"TXT","answer":"google-site-verification=abc","ttl":300}
	],"lastPage":1}`
	// A zone forwarding has already set up: nothing more would change.
	zoneForwarding = `{"records":[
		{"id":5,"host":"","type":"MX","answer":"mx3.name.com","priority":10,"ttl":300},
		{"id":6,"host":"","type":"MX","answer":"MX4.name.com.","priority":10,"ttl":300},
		{"id":7,"host":"","type":"TXT","answer":"\"v=spf1 a mx ~all\"","ttl":300}
	],"lastPage":1}`
)

// effectsServer answers the records list with zone, and every other request
// with the created mailbox, recording each as "METHOD /path".
func effectsServer(t *testing.T, zone string, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/records"):
			_, _ = w.Write([]byte(zone))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		default:
			_, _ = w.Write([]byte(`{"domainName":"example.com","emailBox":"info","emailTo":"me@example.org"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withFlags gives child a root carrying --dry-run and --yes, set as asked.
func withFlags(t *testing.T, child *cobra.Command, dryRun, yes bool) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "namecom"}
	root.PersistentFlags().Bool("dry-run", dryRun, "")
	root.PersistentFlags().Bool("yes", yes, "")
	root.AddCommand(child)
	return child
}

const (
	wantMXEffect  = "name.com replaces MX @ mail2.example.net (priority 1) with its own MX records (mx3.name.com–mx8.name.com)"
	wantSPFEffect = `name.com adds the SPF record "v=spf1 a mx ~all" beside TXT @ v=spf1 include:_spf.google.com ~all, and two SPF records make SPF checks fail (RFC 7208 §4.5)`
)

// TestEmailCreateDryRun_NamesReplacedRecords pins #358: a create replaced
// the apex MX records of the domain's own and added a second SPF record,
// and the dry run showed only the POST. It now names both, in the JSON
// document's sideEffects and on "Would also" lines in table mode, from one
// records read; the subdomain MX and the non-SPF TXT are not named.
func TestEmailCreateDryRun_NamesReplacedRecords(t *testing.T) {
	defer output.StubInteractive(false)()
	for name, format := range map[string]output.Format{"json": output.FormatJSON, "table": output.FormatTable} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			cmd := withFlags(t, cmdForEmailCreate(t, effectsServer(t, zoneElsewhere, &seen)), true, false)
			out := cmdutil.Out(cmd)
			out.Format = format
			if err := cmd.ParseFlags([]string{"--to", "me@example.org"}); err != nil {
				t.Fatal(err)
			}
			if err := runCreate(cmd, []string{"example.com", "info"}); err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if want := getInfo + "," + getRecords; strings.Join(seen, ",") != want {
				t.Errorf("sent %v, want %s", seen, want)
			}
			stdout := out.Writer.(*bytes.Buffer).String()
			stderr := out.EWriter.(*bytes.Buffer).String()
			if format == output.FormatJSON {
				var doc struct {
					Method      string   `json:"method"`
					SideEffects []string `json:"sideEffects"`
				}
				if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
					t.Fatalf("stdout %q: %v", stdout, err)
				}
				if doc.Method != "POST" || strings.Join(doc.SideEffects, "\n") != wantMXEffect+"\n"+wantSPFEffect {
					t.Errorf("dry run = %+v\nwant the POST with sideEffects [%q %q]", doc, wantMXEffect, wantSPFEffect)
				}
				return
			}
			for _, want := range []string{"Would also: " + wantMXEffect, "Would also: " + wantSPFEffect} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr = %q\nwant %q", stderr, want)
				}
			}
			if strings.Contains(stderr, "sub") {
				t.Errorf("stderr = %q names the subdomain MX, which forwarding leaves alone", stderr)
			}
		})
	}
}

// TestEmailCreate_ReplacingRecordsNeedsYes pins #358's confirmation: a create
// that would replace records asks first, so a script without --yes gets the
// usual confirmation error (exit 2) naming them, and nothing is created.
// --yes creates it; a zone forwarding has already set up is not asked about.
func TestEmailCreate_ReplacingRecordsNeedsYes(t *testing.T) {
	defer output.StubInteractive(false)()
	const post = "POST /core/v1/domains/example.com/email/forwarding"
	for name, tc := range map[string]struct {
		zone     string
		yes      bool
		wantSent string
		wantErr  bool
	}{
		"no --yes":           {zoneElsewhere, false, getRecords, true},
		"--yes":              {zoneElsewhere, true, getRecords + "," + post, false},
		"nothing to replace": {zoneForwarding, false, getRecords + "," + post, false},
		"empty zone":         {`{"records":[]}`, false, getRecords + "," + post, false},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			cmd := withFlags(t, cmdForEmailCreate(t, effectsServer(t, tc.zone, &seen)), false, tc.yes)
			if err := cmd.ParseFlags([]string{"--to", "me@example.org"}); err != nil {
				t.Fatal(err)
			}
			err := runCreate(cmd, []string{"example.com", "info"})
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
			for _, want := range []string{"mail2.example.net", "v=spf1 include:_spf.google.com", "--yes"} {
				if !strings.Contains(err.Error()+" "+usage.UserHint(), want) {
					t.Errorf("error = %q (hint %q), want it to name %q", err, usage.UserHint(), want)
				}
			}
		})
	}
}
