package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// fakeRecord is a record as the fake zone stores and returns it.
type fakeRecord struct {
	ID         int    `json:"id"`
	DomainName string `json:"domainName"`
	Host       string `json:"host"`
	Fqdn       string `json:"fqdn"`
	Type       string `json:"type"`
	Answer     string `json:"answer"`
	TTL        int64  `json:"ttl"`
	Priority   *int64 `json:"priority,omitempty"`
}

// fakeZone is a stub of the records API for example.com that keeps state:
// list, get, create, update and delete behave as the API does, including
// refusing a duplicate record and returning 404 for a missing one, so a
// command can be run twice against it. Any other domain is a 404.
type fakeZone struct {
	t       *testing.T
	mu      sync.Mutex
	records []fakeRecord
	nextID  int
	// writes logs every mutating request, "POST", "PUT 3", "DELETE 4".
	writes []string
	// sent is every mutating request in full: "POST /path {body}".
	sent []string
	// fail, when set, decides whether a write is refused with a 422.
	fail func(method string, r fakeRecord) bool
}

func newFakeZone(t *testing.T, records ...fakeRecord) (*fakeZone, *httptest.Server) {
	t.Helper()
	z := &fakeZone{t: t, nextID: 100}
	for _, r := range records {
		z.records = append(z.records, z.normalize(r))
	}
	srv := httptest.NewServer(z)
	t.Cleanup(srv.Close)
	return z, srv
}

// normalize stores r as the API does: the apex as "", targets without the
// trailing dot.
func (z *fakeZone) normalize(r fakeRecord) fakeRecord {
	if r.Host == "@" {
		r.Host = ""
	}
	if r.Answer != "." {
		r.Answer = strings.TrimSuffix(r.Answer, ".")
	}
	r.DomainName = "example.com"
	r.Fqdn = "example.com."
	if r.Host != "" {
		r.Fqdn = r.Host + ".example.com."
	}
	return r
}

func (z *fakeZone) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	z.mu.Lock()
	defer z.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	const base = "/core/v1/domains/example.com/records"
	rest, ok := strings.CutPrefix(req.URL.Path, base)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		return
	}
	id, _ := strconv.Atoi(strings.TrimPrefix(rest, "/"))
	idx := -1
	for i, r := range z.records {
		if rest != "" && r.ID == id {
			idx = i
		}
	}
	notFound := func() {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}

	raw, _ := io.ReadAll(req.Body)
	var body fakeRecord
	if req.Method == http.MethodPost || req.Method == http.MethodPut {
		if err := json.Unmarshal(raw, &body); err != nil {
			z.t.Errorf("decoding %s body: %v", req.Method, err)
		}
		body = z.normalize(body)
	}
	if req.Method != http.MethodGet {
		z.sent = append(z.sent, strings.TrimSpace(req.Method+" "+req.URL.Path+" "+canonical(string(raw))))
		entry := req.Method
		if rest != "" {
			entry += " " + strconv.Itoa(id)
		}
		z.writes = append(z.writes, entry)
		if z.fail != nil && z.fail(req.Method, body) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Invalid Argument","details":"stub refused it"}`))
			return
		}
	}

	switch {
	case req.Method == http.MethodGet && rest == "":
		out := z.records
		if out == nil {
			out = []fakeRecord{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"records": out})
	case req.Method == http.MethodGet:
		if idx < 0 {
			notFound()
			return
		}
		_ = json.NewEncoder(w).Encode(z.records[idx])
	case req.Method == http.MethodPost:
		for _, r := range z.records {
			if r.Host == body.Host && r.Type == body.Type && r.Answer == body.Answer {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"Parameter Value Error","details":"Record already exists"}`))
				return
			}
		}
		body.ID = z.nextID
		z.nextID++
		z.records = append(z.records, body)
		_ = json.NewEncoder(w).Encode(body)
	case req.Method == http.MethodPut:
		if idx < 0 {
			notFound()
			return
		}
		body.ID = id
		z.records[idx] = body
		_ = json.NewEncoder(w).Encode(body)
	case req.Method == http.MethodDelete:
		if idx < 0 {
			notFound()
			return
		}
		z.records = append(z.records[:idx], z.records[idx+1:]...)
		_, _ = w.Write([]byte(`{}`))
	default:
		z.t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// state is the zone's records as sorted one-line descriptions.
func (z *fakeZone) state() []string {
	z.mu.Lock()
	defer z.mu.Unlock()
	var s []string
	for _, r := range z.records {
		line := fmt.Sprintf("%s %s %s %d", r.Type, displayHost(&r.Host), r.Answer, r.TTL)
		if r.Priority != nil {
			line += fmt.Sprintf(" prio=%d", *r.Priority)
		}
		s = append(s, line)
	}
	return s
}

func (z *fakeZone) writeLog() []string {
	z.mu.Lock()
	defer z.mu.Unlock()
	return append([]string(nil), z.writes...)
}

// runOpts is how a test invokes a command: its output format, and the global
// --dry-run and --yes.
type runOpts struct {
	format output.Format
	dryRun bool
	yes    bool
}

// commandFor wires child, under a root carrying --dry-run and --yes, to srv
// with its output captured.
func commandFor(t *testing.T, srv *httptest.Server, o runOpts) (cmd *cobra.Command, stdout, stderr *bytes.Buffer) {
	t.Helper()
	t.Cleanup(output.StubInteractive(false))
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	if o.format == "" {
		o.format = output.FormatTable
	}
	out := &output.Config{Format: o.format, Color: output.ColorNever, Writer: stdout, EWriter: stderr}
	root := &cobra.Command{Use: "namecom"}
	root.PersistentFlags().Bool("dry-run", o.dryRun, "")
	root.PersistentFlags().Bool("yes", o.yes, "")
	cmd = &cobra.Command{Use: "child"}
	root.AddCommand(cmd)
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
	cmd.SetContext(ctx)
	return cmd, stdout, stderr
}

// writeFile writes content to name in a fresh temporary directory.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

// canonical re-encodes a JSON document so key order and spacing do not
// matter; anything else is returned as it is.
func canonical(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return strings.TrimSpace(s)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (z *fakeZone) sentLog() []string {
	z.mu.Lock()
	defer z.mu.Unlock()
	return append([]string(nil), z.sent...)
}
