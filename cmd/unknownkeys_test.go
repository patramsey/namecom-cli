package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/config"
)

// TestConfigUnknownKey_WarnsInJSON pins #358 end to end: a profile's
// base_url, which namecom has no such key for, was ignored without a word.
// The command still runs, and JSON mode carries the warning in its
// warnings array.
func TestConfigUnknownKey_WarnsInJSON(t *testing.T) {
	path := withConfig(t, loneProfile+"    base_url: http://127.0.0.1:8080\n")
	config.TakeWarnings()
	args := []string{"domain", "list"}
	resetFlags(t, args)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domains":[]}`))
	}))
	t.Cleanup(srv.Close)

	_, stderr, code := runContract(t, append([]string{"--base-url", srv.URL, "-o", "json"}, args...)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	list, _ := decodeDoc(t, "stderr", stderr)["warnings"].([]any)
	for _, w := range list {
		if s, _ := w.(string); strings.Contains(s, `"base_url"`) && strings.Contains(s, path) && strings.Contains(s, "--base-url") {
			return
		}
	}
	t.Errorf("warnings should name base_url, the file and --base-url, got:\n%s", stderr)
}
