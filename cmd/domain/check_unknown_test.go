package domain

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestCheck_DuplicateNamesCheckedOnce pins #288: `domain check example.com
// EXAMPLE.com` sent both, the API answered once, and the second copy was
// reported unanswered — exit 1 for a name that was checked. A name given
// twice, in any case, is checked and shown once, in first-given order.
func TestCheck_DuplicateNamesCheckedOnce(t *testing.T) {
	var requests int
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			DomainNames []string `json:"domainNames"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent = body.DomainNames
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"domainName":"example.com","purchasable":false},` +
			`{"domainName":"example.net","purchasable":false}]}`))
	}))
	t.Cleanup(srv.Close)

	cmd, buf := cmdForCheckJSON(t, srv)
	if err := cmd.ParseFlags([]string{"--authoritative"}); err != nil {
		t.Fatal(err)
	}
	if err := runCheck(cmd, []string{"example.com", "EXAMPLE.com", "example.net", "example.com"}); err != nil {
		t.Fatalf("a duplicated name must not fail the check: %v", err)
	}
	if requests != 1 {
		t.Errorf("want 1 request, got %d", requests)
	}
	if strings.Join(sent, ",") != "example.com,example.net" {
		t.Errorf("want each name sent once, got %v", sent)
	}
	var got []*coreapigo.SearchResult
	if err := unmarshalData(buf.Bytes(), &got); err != nil || len(got) != 2 {
		t.Fatalf("want one row per distinct name: %v\n%s", err, buf.String())
	}
}

// TestCheck_UnansweredIsUnknownNotTaken pins #288: a name the registry did
// not answer was shown as "taken" and, in JSON, "purchasable": false —
// identical to a taken name. It reads "unknown", and null.
func TestCheck_UnansweredIsUnknownNotTaken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"domainName":"example.com","purchasable":false}]}`))
	}))
	t.Cleanup(srv.Close)

	t.Run("json", func(t *testing.T) {
		cmd, buf := cmdForCheckJSON(t, srv)
		if err := cmd.ParseFlags([]string{"--authoritative"}); err != nil {
			t.Fatal(err)
		}
		if err := runCheck(cmd, []string{"example.com", "foo.notatld"}); err == nil {
			t.Fatal("want an error for the unanswered name")
		}
		var got []map[string]any
		if err := unmarshalData(buf.Bytes(), &got); err != nil || len(got) != 2 {
			t.Fatalf("want two rows: %v\n%s", err, buf.String())
		}
		if v, ok := got[0]["purchasable"]; !ok || v != false {
			t.Errorf("a taken name is purchasable false, got %v", v)
		}
		if v, ok := got[1]["purchasable"]; !ok || v != nil {
			t.Errorf("an unanswered name is purchasable null, got %v (present %v)", v, ok)
		}
		if got[1]["domainName"] != "foo.notatld" {
			t.Errorf("the unanswered row must name it: %v", got[1])
		}
	})

	t.Run("table", func(t *testing.T) {
		cmd := cmdForCheck(t, srv)
		if err := cmd.ParseFlags([]string{"--authoritative"}); err != nil {
			t.Fatal(err)
		}
		_ = runCheck(cmd, []string{"example.com", "foo.notatld"})
		stdout := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
		if !strings.Contains(stdout, "foo.notatld") {
			t.Fatalf("want a row for foo.notatld, got:\n%s", stdout)
		}
		for line := range strings.SplitSeq(stdout, "\n") {
			if strings.Contains(line, "foo.notatld") && (!strings.Contains(line, "unknown") || strings.Contains(line, "taken")) {
				t.Errorf("want foo.notatld shown as unknown, got %q", line)
			}
		}
	})
}
