package dns

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/patramsey/namecom-cli/internal/output"
)

// lineReader hands out one line per Read. huh's accessible mode wraps the
// reader in a fresh bufio.Scanner for every field, and a scanner given the
// whole input at once would swallow the answers meant for the fields after it.
type lineReader struct{ lines []string }

func (r *lineReader) Read(p []byte) (int, error) {
	if len(r.lines) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.lines[0]+"\n")
	r.lines = r.lines[1:]
	return n, nil
}

// answerForms makes dnsCreateForm read its answers from lines, through huh's
// accessible (line-based) mode, instead of from a terminal.
func answerForms(t *testing.T, lines ...string) {
	t.Helper()
	in := &lineReader{lines: lines}
	prev := runForm
	runForm = func(f *huh.Form) error {
		return f.WithAccessible(true).WithInput(in).WithOutput(io.Discard).Run()
	}
	t.Cleanup(func() {
		runForm = prev
		if len(in.lines) != 0 {
			t.Errorf("form did not ask for every answer given; unread: %q", in.lines)
		}
	})
}

// TestDNSCreate_InteractiveFormEndToEnd drives the guided `dns create` form
// (#105) through to the request it sends, where before only markFormFlags —
// the step after the form — was tested. An MX record exercises both forms:
// the priority is asked separately, and must reach the body.
func TestDNSCreate_InteractiveFormEndToEnd(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"domainName":"example.com","host":"","fqdn":"example.com.","type":"MX","answer":"mx.example.com","ttl":3600,"priority":10}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	t.Cleanup(output.StubInteractive(true))
	// Type (5 = MX), host, answer, TTL; then the priority form.
	answerForms(t, "5", "@", "mx.example.com", "3600", "10")

	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("dns create via the form: %v", err)
	}
	want := map[string]any{
		"type": "MX", "host": "@", "answer": "mx.example.com",
		"ttl": float64(3600), "priority": float64(10),
	}
	if !reflect.DeepEqual(sent, want) {
		t.Errorf("form sent %v, want %v", sent, want)
	}
}

// TestDNSCreate_InteractiveFormRevalidates: an answer the form's validator
// rejects is asked for again rather than sent.
func TestDNSCreate_InteractiveFormRevalidates(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":8}`))
	}))
	t.Cleanup(srv.Close)

	cmd := cmdForCreate(t, srv)
	t.Cleanup(output.StubInteractive(true))
	// Type 1 = A; "not-an-ip" is refused and the answer asked for again.
	answerForms(t, "1", "www", "not-an-ip", "192.0.2.1", "300")

	if err := runCreate(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("dns create via the form: %v", err)
	}
	if sent["answer"] != "192.0.2.1" || sent["type"] != "A" || sent["host"] != "www" {
		t.Errorf("form sent %v, want the A record for www with the corrected answer", sent)
	}
	if _, ok := sent["priority"]; ok {
		t.Errorf("an A record must not be sent a priority: %v", sent)
	}
}
