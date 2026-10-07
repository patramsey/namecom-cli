package apicmd

import (
	"bytes"
	"reflect"
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
