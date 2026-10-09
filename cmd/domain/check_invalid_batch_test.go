package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
)

// invalidBatchServer answers ZoneCheck and CheckAvailability as the API does
// for names in a TLD it does not know: left out of a reply that has valid
// names in it, and a 422 "None of the submitted domains are valid" for a
// request holding nothing else. Every valid name is available. It counts the
// requests to each path.
func invalidBatchServer(t *testing.T) (*httptest.Server, func() map[string]int) {
	t.Helper()
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		var body struct {
			DomainNames []string `json:"domainNames"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var rows []string
		for _, n := range body.DomainNames {
			if strings.HasSuffix(n, ".notatld") {
				continue
			}
			if r.URL.Path == "/core/v1/zonecheck" {
				rows = append(rows, fmt.Sprintf(`{"domainName":%q,"available":true}`, n))
			} else {
				rows = append(rows, fmt.Sprintf(`{"domainName":%q,"purchasable":true,"purchasePrice":12.99}`, n))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if len(rows) == 0 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"None of the submitted domains are valid"}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[` + strings.Join(rows, ",") + `]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		return counts
	}
}

// TestCheck_BatchOfOnlyInvalidNamesIsUnknown pins #322 item 2: names are
// sent 50 a request, and a request holding only names the API cannot answer
// gets a 422. That failed the whole command and threw away every other
// batch's answers, so the result depended on where a bad name fell. The
// batch's names are unknown instead, as in a mixed batch: every row prints,
// and the command exits 1 naming them.
func TestCheck_BatchOfOnlyInvalidNamesIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		authoritative bool
		want          map[string]int
	}{
		// The registry pass: one request per batch.
		{true, map[string]int{"/core/v1/domains:checkAvailability": 2}},
		// ZoneCheck and then the registry for each batch; the registry is
		// still asked about names ZoneCheck refused, as it is for names
		// ZoneCheck leaves out.
		{false, map[string]int{"/core/v1/zonecheck": 2, "/core/v1/domains:checkAvailability": 2}},
	} {
		t.Run(fmt.Sprintf("authoritative=%v", tc.authoritative), func(t *testing.T) {
			srv, counts := invalidBatchServer(t)
			cmd, buf := cmdForCheckJSON(t, srv)
			checkAuthoritative = tc.authoritative
			names := append(bulkNames(50), "foo.notatld")
			err := runCheck(cmd, names)
			if err == nil || !strings.Contains(err.Error(), "availability unknown for foo.notatld") {
				t.Fatalf("want the unknown name to fail the check, got %v", err)
			}
			if _, isAPI := err.(*api.APIError); isAPI {
				t.Errorf("the 422 must not be the command's error: %v", err)
			}
			var got []map[string]any
			if err := unmarshalData(buf.Bytes(), &got); err != nil || len(got) != 51 {
				t.Fatalf("want 51 rows: %v\n%s", err, buf.String())
			}
			if got[0]["purchasable"] != true {
				t.Errorf("the first batch's answers must print, got %v", got[0])
			}
			if v, ok := got[50]["purchasable"]; !ok || v != nil || got[50]["domainName"] != "foo.notatld" {
				t.Errorf("want foo.notatld unknown (purchasable null), got %v", got[50])
			}
			for path, n := range tc.want {
				if counts()[path] != n {
					t.Errorf("%s: want %d requests, got %v", path, n, counts())
				}
			}
		})
	}
}

// A single unanswerable name was the bare 422 api error, without the
// "check the name and its TLD" the mixed case gives.
func TestCheck_SingleInvalidNameGetsTheHint(t *testing.T) {
	srv, counts := invalidBatchServer(t)
	cmd := cmdForCheckSandbox(t, srv)
	err := runCheck(cmd, []string{"foo.notatld"})
	if err == nil || !strings.Contains(err.Error(), "availability unknown for foo.notatld") {
		t.Fatalf("want availability unknown, got %v", err)
	}
	stderr := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String()
	if !strings.Contains(stderr, "check the name and its TLD") {
		t.Errorf("want the TLD hint, got:\n%s", stderr)
	}
	stdout := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "foo.notatld") || !strings.Contains(stdout, "unknown") {
		t.Errorf("want an unknown row, got:\n%s", stdout)
	}
	if n := counts()["/core/v1/domains:checkAvailability"]; n != 1 {
		t.Errorf("want 1 request, got %v", counts())
	}
}

// Any other refusal is still the command's error: only the API's "none of
// these are valid" answers for the names.
func TestCheck_Other422StillFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Something else"}`))
	}))
	t.Cleanup(srv.Close)
	cmd, buf := cmdForCheckJSON(t, srv)
	checkAuthoritative = true
	err := runCheck(cmd, []string{"example.com"})
	if err == nil || !strings.Contains(err.Error(), "Something else") {
		t.Fatalf("want the API's error, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("want no rows, got %s", buf.String())
	}
}
