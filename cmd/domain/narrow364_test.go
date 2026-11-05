package domain

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestNarrowTables_KeepDomainNamesWhole pins #364: at 60 columns `domain
// list` cut two domains to "namecom-smoke-37de95…" and
// "namecom-smoke2-37de9…", which could no longer be told apart, while
// AUTO-RENEW and LOCKED stayed; `domain check` cut the name it checked. A
// domain name now keeps its full width, and the flags give way first.
func TestNarrowTables_KeepDomainNamesWhole(t *testing.T) {
	names := []string{"namecom-smoke-37de95c1a2b4.com", "namecom-smoke2-37de95c1a2b4.com"}

	t.Run("list", func(t *testing.T) {
		for _, width := range []int{60, 70} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"domains":[` +
					`{"domainName":"` + names[0] + `","expireDate":"2027-01-01T00:00:00Z","locked":true,"autorenewEnabled":true},` +
					`{"domainName":"` + names[1] + `","expireDate":"2027-01-01T00:00:00Z"}],"totalCount":2}`))
			}))
			var stdout, stderr bytes.Buffer
			cmd := cmdForDomainList(t, srv, &stdout, &stderr)
			listAll, listFilter, listTLD, listExpiringAfter, listExpiringBefore, listPage = false, "", "", "", "", 1
			cmdutil.Out(cmd).MaxWidth = width
			if err := runList(cmd, nil); err != nil {
				t.Fatalf("runList: %v", err)
			}
			srv.Close()
			assertWhole(t, width, stdout.String(), names)
			if !strings.Contains(stderr.String(), "hidden") {
				t.Errorf("width %d: want a footer naming the hidden columns, got:\n%s", width, stderr.String())
			}
		}
	})

	t.Run("check", func(t *testing.T) {
		long := "namecom-smoke-check-37de95c1a2b4c5d6.com"
		for _, width := range []int{60, 70} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"results":[{"domainName":"` + long + `","sld":"x","tld":"com",` +
					`"purchasable":true,"purchasePrice":17.99,"purchaseType":"registration","renewalPrice":19.99}]}`))
			}))
			cmd := cmdForCheckSandbox(t, srv)
			cmdutil.Out(cmd).MaxWidth = width
			if err := runCheck(cmd, []string{long}); err != nil {
				t.Fatalf("runCheck: %v", err)
			}
			srv.Close()
			assertWhole(t, width, cmdutil.Out(cmd).Writer.(*bytes.Buffer).String(), []string{long})
		}
	})
}

func assertWhole(t *testing.T, width int, table string, names []string) {
	t.Helper()
	for _, n := range names {
		if !strings.Contains(table, n) {
			t.Errorf("width %d: %q was cut:\n%s", width, n, table)
		}
	}
	for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n") {
		if w := len([]rune(line)); w > width {
			t.Errorf("width %d: line is %d wide:\n%s", width, w, table)
		}
	}
}
