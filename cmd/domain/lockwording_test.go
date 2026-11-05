package domain

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestUpdate_UnchangedWordedAsTheToggles pins #364: `domain update --lock`
// said "example.com already has lock on" where `domain lock on` says
// "Transfer lock is already on for example.com".
func TestUpdate_UnchangedWordedAsTheToggles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"domainName":"example.com","locked":true,"autorenewEnabled":false}`))
	}))
	t.Cleanup(srv.Close)
	cmd := cmdForUpdate(t, srv)
	for flag, v := range map[string]string{"lock": "true", "autorenew": "false"} {
		if err := cmd.Flags().Set(flag, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := runUpdate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	out := cmdutil.Out(cmd)
	got := out.Writer.(*bytes.Buffer).String() + out.EWriter.(*bytes.Buffer).String()
	want := "Transfer lock is already on and auto-renewal is already off for example.com; nothing to change"
	if !strings.Contains(got, want) {
		t.Errorf("got %q\nwant it to contain %q", got, want)
	}
}
