package dns

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
)

// TestDNSList_EscapesControlCharacters pins #337: a record answer holding an
// escape sequence was written to the terminal as it came, so it set the
// window title and cleared the screen. A table and TSV show it as \x1b; JSON
// carries the value unchanged, escaped as JSON escapes it.
func TestDNSList_EscapesControlCharacters(t *testing.T) {
	const answer = "\x1b]0;PWNED-TITLE\a\x1b[2J\x1b[31mred\x1b[0m"
	id, typ, host := 1, "TXT", "x"
	ans := answer
	records := []*coreapigo.Record{{ID: &id, Type: &typ, Host: &host, Answer: &ans}}

	for _, format := range []output.Format{output.FormatTable, output.FormatTSV, output.FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(coreapigo.ListRecordsResponse{Records: records})
			}))
			t.Cleanup(srv.Close)

			var stdout bytes.Buffer
			cmd := cmdForList(t, srv, &stdout)
			cmdutil.Out(cmd).Format = format
			listAll, listType = false, ""
			if err := runList(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runList: %v", err)
			}
			if n := requests.Load(); n != 1 {
				t.Errorf("sent %d requests, want 1", n)
			}
			got := stdout.String()

			if format == output.FormatJSON {
				var doc struct {
					Data []struct {
						Answer string `json:"answer"`
					} `json:"data"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
					t.Fatalf("JSON does not parse: %v\n%s", err, got)
				}
				if len(doc.Data) != 1 || doc.Data[0].Answer != answer {
					t.Errorf("JSON answer = %+v, want %q unchanged", doc.Data, answer)
				}
				return
			}
			if strings.ContainsAny(got, "\x1b\a") {
				t.Errorf("%s output holds a raw control character:\n%q", format, got)
			}
			want := `\x1b]0;PWNED-TITLE\x07\x1b[2J\x1b[31mred\x1b[0m`
			if !strings.Contains(got, want) {
				t.Errorf("%s output does not show the answer escaped as %s:\n%q", format, want, got)
			}
		})
	}
}

// TestPrintPlan_EscapesControlCharacters: the sync plan prints records from
// the zone and the file with Fprintf, and a zone file's \DDD escapes can put
// any byte in a name (#337).
func TestPrintPlan_EscapesControlCharacters(t *testing.T) {
	var stdout bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &stdout, EWriter: &bytes.Buffer{}}
	p := &syncPlan{
		Domain:  "example.com",
		Creates: []planRecord{{Type: "TXT", Host: "a\x1b[2J", Answer: "x", TTL: 300}},
		Deletes: []planRecord{{ID: 1, Type: "TXT", Host: "b", Answer: "\x1b]0;t\a", TTL: 300}},
	}
	if err := printPlan(out, p); err != nil {
		t.Fatalf("printPlan: %v", err)
	}
	got := stdout.String()
	if strings.ContainsAny(got, "\x1b\a") {
		t.Errorf("plan holds a raw control character:\n%q", got)
	}
	for _, want := range []string{`a\x1b[2J`, `\x1b]0;t\x07`} {
		if !strings.Contains(got, want) {
			t.Errorf("plan missing %s:\n%q", want, got)
		}
	}
}

// TestPrintPlan_OneLinePerChange pins #359: a line feed in an answer printed
// a line of its own in the plan — the summary --prune is confirmed on — so a
// hostile record could add a delete or a create the plan does not make.
func TestPrintPlan_OneLinePerChange(t *testing.T) {
	var stdout bytes.Buffer
	out := &output.Config{Format: output.FormatTable, Color: output.ColorNever, Writer: &stdout, EWriter: &bytes.Buffer{}}
	fake := "x\n  delete A @ → 192.0.2.1 (TTL 300) [id 9]"
	p := &syncPlan{
		Domain:  "example.com",
		Prune:   true,
		Creates: []planRecord{{Type: "TXT", Host: "a", Answer: fake, TTL: 300}},
		Updates: []planUpdate{{
			planRecord: planRecord{ID: 2, Type: "TXT", Host: "u", Answer: "new", TTL: 300},
			Before:     planRecord{ID: 2, Type: "TXT", Host: "u", Answer: "old\tHIDDEN", TTL: 300},
			Changes:    []string{"answer old\tHIDDEN → new"},
		}},
		Deletes: []planRecord{{ID: 1, Type: "TXT", Host: "b", Answer: fake, TTL: 300}},
		Kept:    []planKept{{planRecord: planRecord{ID: 3, Type: "NS", Host: "@", Answer: fake, TTL: 300}, Reason: "the zone's own"}},
	}
	if err := printPlan(out, p); err != nil {
		t.Fatalf("printPlan: %v", err)
	}
	got := stdout.String()
	if n := strings.Count(got, "\n"); n != 5 {
		t.Errorf("plan is %d lines, want 5 (header, create, update, delete, keep):\n%s", n, got)
	}
	if strings.Contains(got, "\t") {
		t.Errorf("plan holds a raw tab:\n%q", got)
	}
	for _, want := range []string{`x\n  delete A @`, `old\tHIDDEN`} {
		if !strings.Contains(got, want) {
			t.Errorf("plan missing %s:\n%s", want, got)
		}
	}
}
