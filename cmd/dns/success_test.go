package dns

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// TestDNSUpdate_SuccessNamesTheChange: "Updated record 12345" said neither
// which record nor what changed, and every write ended with a
// "→ Run 'namecom dns list …'" hint (#238).
func TestDNSUpdate_SuccessNamesTheChange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":12345,"type":"A","host":"www","answer":"192.0.2.1","ttl":300}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":12345}`))
	}))
	t.Cleanup(srv.Close)

	cmd, ew := cmdForUpdateCapturing(t, srv)
	stdout := &bytes.Buffer{}
	cmdutil.Out(cmd).Writer = stdout
	if err := cmd.ParseFlags([]string{"--answer", "192.0.2.2", "--ttl", "600"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runUpdate(cmd, []string{"example.com", "12345"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	want := "✓ Updated A www.example.com (id 12345): answer 192.0.2.1 → 192.0.2.2, ttl 300 → 600\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if strings.Contains(ew.String(), "dns list") {
		t.Errorf("boilerplate hint still printed: %q", ew.String())
	}
}

// "Created A record (id 12345)" did not say which name or value (#238).
func TestDNSCreate_SuccessNamesTheRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":12345,"type":"A","host":"uxtest","answer":"192.0.2.10","ttl":300}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	if err := cmd.ParseFlags([]string{"--type", "A", "--host", "uxtest", "--answer", "192.0.2.10"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	got := cmdutil.Out(cmd).Writer.(*bytes.Buffer).String()
	if want := "✓ Created A uxtest.example.com → 192.0.2.10 (id 12345)\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if e := cmdutil.Out(cmd).EWriter.(*bytes.Buffer).String(); strings.Contains(e, "dns list") {
		t.Errorf("boilerplate hint still printed: %q", e)
	}
}

func TestRecordName(t *testing.T) {
	for host, want := range map[string]string{"": "example.com", "@": "example.com", "www": "www.example.com"} {
		if got := recordName(host, "example.com"); got != want {
			t.Errorf("recordName(%q) = %q, want %q", host, got, want)
		}
	}
}
