package apicmd

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestAPI_PaginatePageSize pins #290 (A7): --paginate walked the pages at
// the API's default size, or at a -f perPage=5 left over from paging by
// hand, which was 900 requests for one list. Without a perPage of its own
// it asks for the API's maximum, 1000, so a walk is as few requests as it
// can be. A perPage the user gave is kept.
func TestAPI_PaginatePageSize(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		fields     []string
		want       []string
	}{
		{"default", "/core/v1/domains", nil, []string{
			"/core/v1/domains?perPage=1000",
			"/core/v1/domains?page=2&perPage=1000",
			"/core/v1/domains?page=3&perPage=1000",
		}},
		{"path query kept", "/core/v1/domains?sort=asc", nil, []string{
			"/core/v1/domains?sort=asc&perPage=1000",
			"/core/v1/domains?page=2&perPage=1000&sort=asc",
			"/core/v1/domains?page=3&perPage=1000&sort=asc",
		}},
		{"perPage in the path", "/core/v1/domains?perPage=2", nil, []string{
			"/core/v1/domains?perPage=2",
			"/core/v1/domains?page=2&perPage=2",
			"/core/v1/domains?page=3&perPage=2",
		}},
		{"perPage from -f", "/core/v1/domains", []string{"perPage=2"}, []string{
			"/core/v1/domains?perPage=2",
			"/core/v1/domains?page=2&perPage=2",
			"/core/v1/domains?page=3&perPage=2",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, uris := pagedServer(t)
			cmd, _ := apiCmd(t, srv)
			apiPaginate, apiFields = true, tc.fields
			if err := runAPI(cmd, []string{tc.path}); err != nil {
				t.Fatalf("runAPI: %v", err)
			}
			if !reflect.DeepEqual(*uris, tc.want) {
				t.Errorf("requested %q\nwant %q", *uris, tc.want)
			}
		})
	}
}

// TestAPI_PaginateProgress: a long --paginate said nothing until it ended
// (#290, A7). With stderr a terminal it shows the page it is fetching and
// the last page, and clears the line when done; otherwise stderr stays
// empty.
func TestAPI_PaginateProgress(t *testing.T) {
	for _, tty := range []bool{true, false} {
		srv, _ := pagedServer(t)
		cmd, buf := apiCmd(t, srv)
		var stderr bytes.Buffer
		cmdutil.Out(cmd).EWriter = &stderr
		prev := progressTTY
		progressTTY = func() bool { return tty }
		apiPaginate = true
		err := runAPI(cmd, []string{"/core/v1/domains"})
		progressTTY = prev
		if err != nil {
			t.Fatalf("runAPI: %v", err)
		}
		if buf.String() != mergedDomains {
			t.Errorf("printed %s", buf.String())
		}
		got := stderr.String()
		if !tty {
			if got != "" {
				t.Errorf("not a terminal: stderr = %q, want nothing", got)
			}
			continue
		}
		for _, want := range []string{"page 2 of 3", "page 3 of 3"} {
			if !strings.Contains(got, want) {
				t.Errorf("stderr lacks %q: %q", want, got)
			}
		}
		if !strings.HasSuffix(got, "\r\033[K") {
			t.Errorf("progress line not cleared: %q", got)
		}
	}
}

// TestAPI_PaginateMaxPages: --paginate with a small perPage walked every page
// one request at a time — 6,500 of them for -f perPage=1 on a large account —
// with nothing to bound or announce it (ISSUE-06). --max-pages, 100 unless
// given, bounds it: when the first page's lastPage is over the limit no other
// page is fetched, a list with no lastPage stops at the limit, and either is a
// usage error (exit 2) with nothing printed. 0 is no limit.
func TestAPI_PaginateMaxPages(t *testing.T) {
	for _, tc := range []struct {
		name     string
		max      int
		requests int
		err      string // "" for success
	}{
		{"over the limit by lastPage", 2, 1, "--paginate: this list is 3 pages at perPage=2, more than --max-pages 2"},
		{"at the limit", 3, 3, ""},
		{"no limit", 0, 3, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, uris := pagedServer(t)
			cmd, buf := apiCmd(t, srv)
			apiPaginate, apiFields, apiMaxPages = true, []string{"perPage=2"}, tc.max
			err := runAPI(cmd, []string{"/core/v1/domains"})
			if len(*uris) != tc.requests {
				t.Errorf("sent %d requests, want %d: %q", len(*uris), tc.requests, *uris)
			}
			if tc.err == "" {
				if err != nil || buf.String() != mergedDomains {
					t.Errorf("runAPI = %v, printed %s", err, buf.String())
				}
				return
			}
			if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok || err.Error() != tc.err {
				t.Errorf("runAPI = %v, want the usage error %q", err, tc.err)
			}
			if buf.Len() != 0 {
				t.Errorf("printed %s, want nothing", buf.String())
			}
		})
	}
}

// TestAPI_PaginateMaxPagesWithoutLastPage: a list that never says how long
// it is is stopped when the walk reaches --max-pages.
func TestAPI_PaginateMaxPagesWithoutLastPage(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"items":[%d],"nextPage":%d}`, page, max(page, 1)+1)
	}))
	t.Cleanup(srv.Close)
	cmd, buf := apiCmd(t, srv)
	apiPaginate, apiMaxPages = true, 4
	err := runAPI(cmd, []string{"/core/v1/items"})
	want := "--paginate: there are more pages after 4 at perPage=1000, the --max-pages limit"
	if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok || err.Error() != want {
		t.Errorf("runAPI = %v, want the usage error %q", err, want)
	}
	if requests != 4 {
		t.Errorf("sent %d requests, want 4", requests)
	}
	if buf.Len() != 0 {
		t.Errorf("printed %s, want nothing", buf.String())
	}
}

// TestAPI_MaxPagesChecked: --max-pages is checked before any request.
func TestAPI_MaxPagesChecked(t *testing.T) {
	for _, tc := range []struct {
		name     string
		paginate bool
		max      int
	}{
		{"negative", true, -1},
		{"without --paginate", false, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, uris := pagedServer(t)
			cmd, _ := apiCmd(t, srv)
			cmd.Flags().IntVar(&apiMaxPages, "max-pages", defaultMaxPages, "")
			if err := cmd.Flags().Set("max-pages", strconv.Itoa(tc.max)); err != nil {
				t.Fatal(err)
			}
			apiPaginate = tc.paginate
			err := runAPI(cmd, []string{"/core/v1/domains"})
			if _, ok := errors.AsType[*cmdutil.UsageError](err); !ok {
				t.Errorf("runAPI = %v, want a usage error", err)
			}
			if len(*uris) != 0 {
				t.Errorf("sent %q, want nothing", *uris)
			}
		})
	}
}
