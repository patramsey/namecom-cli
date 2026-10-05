package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPipedTable pins what `-o table` puts on stdout when stdout is not a
// terminal (#233): `domain list -o table 2>/dev/null` still ended with
// "(2 domains)" and "→ Run …", inside box-drawing borders. stdout now holds
// the rows alone, as aligned columns; the count and hints go to stderr.
func TestPipedTable(t *testing.T) {
	withConfig(t, loneProfile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domains":[` +
			`{"domainName":"example.com","locked":true,"autorenewEnabled":true},` +
			`{"domainName":"example.org"}],"totalCount":2}`))
	}))
	t.Cleanup(srv.Close)

	stdout := runCapturingStdout(t, []string{"--base-url", srv.URL, "-o", "table", "domain", "list"})

	if strings.ContainsAny(stdout, "╭│─╰") {
		t.Errorf("piped table kept its borders:\n%s", stdout)
	}
	for _, leak := range []string{"2 domains", "→"} {
		if strings.Contains(stdout, leak) {
			t.Errorf("stdout holds %q, which belongs on stderr:\n%s", leak, stdout)
		}
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "DOMAIN ") ||
		!strings.HasPrefix(lines[1], "example.com ") || !strings.HasPrefix(lines[2], "example.org ") {
		t.Errorf("want a header and two rows, got:\n%s", stdout)
	}
}
