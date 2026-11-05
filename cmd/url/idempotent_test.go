package url

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
)

// routedServer answers "METHOD /path" from routes, records each request, and
// fails the test on one it has no route for.
func routedServer(t *testing.T, routes map[string]struct {
	status int
	body   string
}, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		*seen = append(*seen, key)
		reply, ok := routes[key]
		if !ok {
			t.Errorf("unexpected request %s", key)
			http.Error(w, "unexpected", http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.status)
		_, _ = w.Write([]byte(reply.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type reply = struct {
	status int
	body   string
}

// TestURLCreate_Type301 pins #361: the help calls the default a 301, and
// --type 302 was accepted, but --type 301 was a usage error. It is the
// default type, redirect, and is sent as that.
func TestURLCreate_Type301(t *testing.T) {
	var sent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"host":"@","forwardsTo":"https://example.org","type":"redirect"}`))
	}))
	t.Cleanup(srv.Close)
	cmd := cmdForURLCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://example.org", "--type", "301"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if !strings.Contains(string(sent), `"type":"redirect"`) {
		t.Errorf("sent %s, want type redirect", sent)
	}
}

// TestURLCreate_DuplicateHintsUpdate pins #361: a second create for a host
// was the API's bare 409; dns hints --if-not-exists and email hints `email
// update`. It now hints `url update`, still as a conflict.
func TestURLCreate_DuplicateHintsUpdate(t *testing.T) {
	var seen []string
	srv := routedServer(t, map[string]reply{
		"POST /core/v1/domains/example.com/url/forwarding": {http.StatusConflict, `{"message":"Conflict"}`},
	}, &seen)
	cmd := cmdForURLCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://example.org"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runCreate(flagged(t, cmd, false, true), []string{"example.com"})
	apiErr, ok := errors.AsType[*api.APIError](err)
	if !ok || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("err = %v, want the API's 409", err)
	}
	h, ok := errors.AsType[*existsError](err)
	if !ok || !strings.Contains(h.hint, "namecom url update example.com <id>") || !strings.Contains(h.hint, "--if-not-exists") {
		t.Errorf("hint = %v, want one naming url update and --if-not-exists", err)
	}
}

// TestURLCreate_IfNotExists pins #361's --if-not-exists, which matches `dns
// create --if-not-exists`: one list of the domain's forwardings first; a
// forwarding on the host that does what was asked is reported with
// "changed": false and nothing is sent; one that forwards elsewhere fails as
// a conflict naming its ID; none is created as usual.
func TestURLCreate_IfNotExists(t *testing.T) {
	const list = "GET /core/v1/urlforwarding/example.com"
	const post = "POST /core/v1/domains/example.com/url/forwarding"
	for _, tc := range []struct {
		name     string
		listBody string
		want     []string
		conflict bool
		changed  bool
	}{
		{"there", `{"urlForwarding":[{"id":9,"host":"example.com","forwardsTo":"https://example.org","type":"redirect"}],"lastPage":1}`,
			[]string{list}, false, false},
		{"elsewhere", `{"urlForwarding":[{"id":9,"host":"","forwardsTo":"https://other.example","type":"redirect"}],"lastPage":1}`,
			[]string{list}, true, false},
		{"other type", `{"urlForwarding":[{"id":9,"host":"@","forwardsTo":"https://example.org","type":"302"}],"lastPage":1}`,
			[]string{list}, true, false},
		{"none", `{"urlForwarding":[{"id":9,"host":"www","forwardsTo":"https://example.org","type":"redirect"}],"lastPage":1}`,
			[]string{list, post}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			createIfNotExists = true
			t.Cleanup(func() { createIfNotExists = false })
			var seen []string
			srv := routedServer(t, map[string]reply{
				list: {200, tc.listBody},
				post: {200, `{"id":10,"host":"@","forwardsTo":"https://example.org","type":"redirect"}`},
			}, &seen)
			cmd := cmdForURLCreate(t, srv)
			out := cmdutil.Out(cmd)
			out.Format = output.FormatJSON
			if err := cmd.ParseFlags([]string{"--to", "https://example.org"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := runCreate(flagged(t, cmd, false, true), []string{"example.com"})
			if strings.Join(seen, ", ") != strings.Join(tc.want, ", ") {
				t.Errorf("requests = %q, want %q", seen, tc.want)
			}
			if tc.conflict {
				c, ok := errors.AsType[*cmdutil.ConflictError](err)
				if !ok || !strings.Contains(c.Hint, "namecom url update example.com 9 --to https://example.org --type redirect") {
					t.Fatalf("err = %v, want a conflict naming the update of forwarding 9", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("runCreate: %v", err)
			}
			var doc struct {
				ID      int  `json:"id"`
				Changed bool `json:"changed"`
			}
			if err := json.Unmarshal(out.Writer.(*bytes.Buffer).Bytes(), &doc); err != nil {
				t.Fatalf("stdout: %v", err)
			}
			if doc.Changed != tc.changed {
				t.Errorf("changed = %v, want %v", doc.Changed, tc.changed)
			}
		})
	}
}

// TestURLCreateDryRun_ApexChecksTheDomain pins #361: a dry run of an apex
// create read nothing, so for a domain not in the account it previewed the
// POST and exited 0 where the create fails not_found, as other dry runs
// now do (#362).
func TestURLCreateDryRun_ApexChecksTheDomain(t *testing.T) {
	var seen []string
	srv := routedServer(t, map[string]reply{
		"GET /core/v1/domains/example.com": {http.StatusNotFound, `{"message":"Not Found"}`},
	}, &seen)
	cmd := cmdForURLCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--to", "https://example.org"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := runCreate(flagged(t, cmd, true, false), []string{"example.com"})
	if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), `domain "example.com" not found`) {
		t.Fatalf("err = %v, want the domain not found", err)
	}
}

// TestURLUpdate_NothingToUpdateFirst pins #361: `url update D 7` with no
// flags sent a GET before failing "--to is required unless …"; it now fails
// first, worded as `dns update` words it.
func TestURLUpdate_NothingToUpdateFirst(t *testing.T) {
	cmd := cmdForURLUpdate(t, neverCalledServer(t))
	err := runUpdate(cmd, []string{"example.com", "7"})
	if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok ||
		!strings.Contains(err.Error(), "nothing to update — pass at least one of --to, --type, --title, --meta") {
		t.Fatalf("err = %v, want the nothing-to-update usage error", err)
	}
}

// TestURLDelete_SeveralAndIfExists pins #361: url delete took one ID and had
// no --if-exists. It takes several, as `dns delete` does, read from one list,
// and --if-exists skips the missing ones with "changed": false items.
func TestURLDelete_SeveralAndIfExists(t *testing.T) {
	const list = "GET /core/v1/urlforwarding/example.com"
	listBody := `{"urlForwarding":[{"id":7,"host":"www","forwardsTo":"https://a.example","type":"redirect"},` +
		`{"id":8,"host":"go","forwardsTo":"https://b.example","type":"302"}],"lastPage":1}`

	t.Run("missing without --if-exists", func(t *testing.T) {
		var seen []string
		srv := routedServer(t, map[string]reply{list: {200, listBody}}, &seen)
		err := runDelete(cmdForURLDelete(t, srv), []string{"example.com", "7", "9"})
		if !cmdutil.IsNotFound(err) || !strings.Contains(err.Error(), "URL forwarding 9 not found on example.com") {
			t.Fatalf("err = %v, want 9 not found", err)
		}
		if len(seen) != 1 {
			t.Errorf("requests = %q, want the list alone", seen)
		}
	})

	t.Run("--if-exists", func(t *testing.T) {
		deleteIfExists = true
		t.Cleanup(func() { deleteIfExists = false })
		var seen []string
		srv := routedServer(t, map[string]reply{
			list: {200, listBody},
			"DELETE /core/v1/urlforwarding/example.com/7": {200, `{}`},
			"DELETE /core/v1/urlforwarding/example.com/8": {200, `{}`},
		}, &seen)
		cmd := cmdForURLDelete(t, srv)
		out := cmdutil.Out(cmd)
		out.Format = output.FormatJSON
		if err := runDelete(cmd, []string{"example.com", "8", "9", "7", "8"}); err != nil {
			t.Fatalf("runDelete: %v", err)
		}
		want := []string{list, "DELETE /core/v1/urlforwarding/example.com/8", "DELETE /core/v1/urlforwarding/example.com/7"}
		if strings.Join(seen, ", ") != strings.Join(want, ", ") {
			t.Errorf("requests = %q, want %q", seen, want)
		}
		var doc struct {
			Changed bool `json:"changed"`
			Data    []struct {
				ID      int  `json:"id"`
				Changed bool `json:"changed"`
			} `json:"data"`
		}
		if err := json.Unmarshal(out.Writer.(*bytes.Buffer).Bytes(), &doc); err != nil {
			t.Fatalf("stdout: %v\n%s", err, out.Writer)
		}
		if !doc.Changed || len(doc.Data) != 3 || doc.Data[0].ID != 8 || !doc.Data[0].Changed ||
			doc.Data[1].ID != 9 || doc.Data[1].Changed || doc.Data[2].ID != 7 || !doc.Data[2].Changed {
			t.Errorf("JSON = %s", out.Writer)
		}
	})

	t.Run("--if-exists, one gone", func(t *testing.T) {
		deleteIfExists = true
		t.Cleanup(func() { deleteIfExists = false })
		var seen []string
		srv := routedServer(t, map[string]reply{
			"GET /core/v1/urlforwarding/example.com/9": {http.StatusNotFound, `{"message":"Not Found"}`},
			list: {200, listBody},
		}, &seen)
		cmd := cmdForURLDelete(t, srv)
		out := cmdutil.Out(cmd)
		out.Format = output.FormatJSON
		if err := runDelete(cmd, []string{"example.com", "9"}); err != nil {
			t.Fatalf("runDelete: %v", err)
		}
		if got := out.Writer.(*bytes.Buffer).String(); !strings.Contains(got, `"changed": false`) {
			t.Errorf("stdout = %s, want changed false", got)
		}
		if len(seen) != 2 {
			t.Errorf("requests = %q, want the GET and a one-entry list", seen)
		}
	})
}
