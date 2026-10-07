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
)

const duplicateRecord = `{"message":"Invalid Argument","details":"Parameter Value Error - Duplicate Record"}`

// duplicateServer answers the create with the API's 400 Duplicate Record and
// the forwarding list with list (a 500 when it is ""), recording each
// request as "METHOD /path".
func duplicateServer(t *testing.T, list string, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(duplicateRecord))
		case list == "":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"Invalid Argument"}`))
		default:
			_, _ = w.Write([]byte(list))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestURLCreate_DuplicateRecordThatLanded pins #308: an apex create the
// API answered with 400 Duplicate Record had been stored anyway, and the CLI
// exited 1. One list of the forwardings now finds it: the create succeeds,
// with a warning naming the left-over apex A records as the likely cause,
// and "changed": true in JSON.
func TestURLCreate_DuplicateRecordThatLanded(t *testing.T) {
	list := `{"urlForwarding":[` +
		`{"id":5,"host":"","forwardsTo":"https://example.org/old","type":"redirect"},` +
		`{"id":6,"host":"www","forwardsTo":"https://example.org/apex8","type":"redirect"},` +
		`{"id":9,"host":"","forwardsTo":"https://example.org/apex8","type":"redirect"}],"lastPage":1}`
	for _, format := range []output.Format{output.FormatTable, output.FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			var seen []string
			cmd := cmdForURLCreate(t, duplicateServer(t, list, &seen))
			out := cmdutil.Out(cmd)
			out.Format = format
			if err := cmd.ParseFlags([]string{"--to", "https://example.org/apex8"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			if err := runCreate(cmd, []string{"example.com"}); err != nil {
				t.Fatalf("runCreate = %v, want the create reported as made", err)
			}
			want := []string{
				"POST /core/v1/domains/example.com/url/forwarding",
				"GET /core/v1/urlforwarding/example.com",
			}
			if strings.Join(seen, "\n") != strings.Join(want, "\n") {
				t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
			}

			stdout := out.Writer.(*bytes.Buffer).String()
			if format == output.FormatJSON {
				var doc struct {
					ID      int  `json:"id"`
					Changed bool `json:"changed"`
				}
				if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
					t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
				}
				if doc.ID != 9 || !doc.Changed {
					t.Errorf("stdout = %s, want forwarding 9 with \"changed\": true", stdout)
				}
				warnings := strings.Join(out.TakeWarnings(), "\n")
				for _, w := range []string{"Duplicate Record", "id 9", "--host @ --type A", "namecom dns delete example.com"} {
					if !strings.Contains(warnings, w) {
						t.Errorf("warnings %q should mention %q", warnings, w)
					}
				}
				return
			}
			if !strings.Contains(stdout, "Created URL forwarding (id 9)") {
				t.Errorf("stdout = %q, want the created forwarding", stdout)
			}
			if stderr := out.EWriter.(*bytes.Buffer).String(); !strings.Contains(stderr, "! the API answered 400 Duplicate Record") {
				t.Errorf("stderr = %q, want the warning", stderr)
			}
		})
	}
}

// When the list does not have it, the 400 stands, as a conflict whose hint
// names the likely cause. When the list fails, the hint says the outcome is
// unknown and how to check.
func TestURLCreate_DuplicateRecordNotCreated(t *testing.T) {
	for name, tc := range map[string]struct{ list, hint string }{
		"not in the list": {`{"urlForwarding":[{"id":6,"host":"www","forwardsTo":"https://example.org/apex8","type":"redirect"}],"lastPage":1}`,
			"namecom dns list example.com --host @ --type A"},
		"list fails": {"", "may have been created anyway: run 'namecom url list example.com'"},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			cmd := cmdForURLCreate(t, duplicateServer(t, tc.list, &seen))
			if err := cmd.ParseFlags([]string{"--to", "https://example.org/apex8"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runCreate(cmd, []string{"example.com"})
			conflict, ok := errors.AsType[*cmdutil.ConflictError](err)
			if !ok {
				t.Fatalf("runCreate = %v, want a ConflictError", err)
			}
			if !strings.Contains(err.Error(), "Duplicate Record") || !strings.Contains(conflict.Hint, tc.hint) {
				t.Errorf("error %q, hint %q; want the API's message and a hint with %q", err, conflict.Hint, tc.hint)
			}
			if len(seen) != 2 {
				t.Errorf("sent %v, want the POST and one GET", seen)
			}
		})
	}
}

// Only an apex create is checked: a subdomain's Duplicate Record is not the
// left-over A record case, and costs no extra request.
func TestURLCreate_DuplicateRecordOnSubdomainIsNotChecked(t *testing.T) {
	var seen []string
	cmd := cmdForURLCreate(t, duplicateServer(t, `{"urlForwarding":[]}`, &seen))
	if err := cmd.ParseFlags([]string{"--to", "https://example.org/apex8", "--host", "www"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runCreate(cmd, []string{"example.com"})
	if err == nil || !strings.Contains(err.Error(), "Duplicate Record") {
		t.Fatalf("runCreate = %v, want the API's error", err)
	}
	if _, ok := errors.AsType[*cmdutil.ConflictError](err); ok {
		t.Errorf("a subdomain's Duplicate Record was turned into a conflict: %v", err)
	}
	if len(seen) != 1 {
		t.Errorf("sent %v, want only the POST", seen)
	}
}
