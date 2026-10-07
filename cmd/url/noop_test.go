package url

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestURLUpdate_NothingToChange pins #312: flags that ask for what the
// forwarding already is sent the PATCH anyway and printed "no values
// changed", with no "changed" in JSON. Now only the GET the read-modify-write
// needs is sent, under --dry-run too, and JSON says "changed": false; a real
// change says "changed": true.
func TestURLUpdate_NothingToChange(t *testing.T) {
	const current = `{"id":7,"host":"swfwd","forwardsTo":"https://example.org/b","type":"masked","title":"T"}`
	for name, tc := range map[string]struct {
		flags   []string
		dryRun  bool
		changed bool
		sent    int32
	}{
		"same --to":                 {flags: []string{"--to", "https://example.org/b"}, sent: 1},
		"same --title":              {flags: []string{"--title", "T"}, sent: 1},
		"same --type":               {flags: []string{"--type", "masked"}, sent: 1},
		"same --to under --dry-run": {flags: []string{"--to", "https://example.org/b"}, dryRun: true, sent: 1},
		"a new --to":                {flags: []string{"--to", "https://example.org/c"}, changed: true, sent: 2},
	} {
		t.Run(name, func(t *testing.T) {
			var n atomic.Int32
			srv := fixedServer(t, current, current, &n)
			for _, format := range []output.Format{output.FormatTable, output.FormatJSON} {
				n.Store(0)
				cmd := withDryRun(t, cmdForURLUpdate(t, srv), tc.dryRun)
				out := cmdutil.Out(cmd)
				out.Format = format
				if err := cmd.ParseFlags(tc.flags); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
				if err := runUpdate(cmd, []string{"example.com", "7"}); err != nil {
					t.Fatalf("runUpdate: %v", err)
				}
				if got := n.Load(); got != tc.sent {
					t.Errorf("%s: sent %d requests, want %d", format, got, tc.sent)
				}
				stdout := out.Writer.(*bytes.Buffer).String()
				if tc.dryRun && tc.changed {
					continue
				}
				if format == output.FormatJSON {
					var doc struct {
						ID      int   `json:"id"`
						Changed *bool `json:"changed"`
					}
					if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
						t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
					}
					if doc.ID != 7 || doc.Changed == nil || *doc.Changed != tc.changed {
						t.Errorf("JSON = %s, want the forwarding with \"changed\": %v", stdout, tc.changed)
					}
					continue
				}
				want := "already has these values: nothing to change"
				if tc.changed {
					want = "Updated URL forwarding 7 (swfwd): forwards to https://example.org/b → https://example.org/c"
				}
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout = %q, want %q", stdout, want)
				}
			}
		})
	}
}
