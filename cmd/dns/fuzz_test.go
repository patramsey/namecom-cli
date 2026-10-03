package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	coreapigo "github.com/namedotcom/core-api-go"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// parseCharStrings reads zone-file TXT rdata as a sequence of RFC 1035
// quoted character-strings separated by single spaces, undoing \X and \DDD
// escapes (quoteTXT writes control characters as \DDD since #188).
// Strict, it accepts exactly the shape quoteTXT promises: one space between
// strings. Lenient, it accepts any run of spaces and tabs there, as a zone file
// does; that is how quoteTXT must read a value that is already quoted.
func parseCharStrings(s string, lenient bool) ([]string, error) {
	var out []string
	i := 0
	for {
		if i >= len(s) || s[i] != '"' {
			return nil, fmt.Errorf("expected '\"' at %d in %q", i, s)
		}
		i++
		var b strings.Builder
		closed := false
		for i < len(s) {
			c := s[i]
			if c == '\\' {
				if i+1 >= len(s) {
					return nil, errors.New("dangling backslash")
				}
				if d := s[i+1]; d >= '0' && d <= '9' {
					if i+4 > len(s) {
						return nil, errors.New("short \\DDD escape")
					}
					n, err := strconv.ParseUint(s[i+1:i+4], 10, 8)
					if err != nil {
						return nil, fmt.Errorf("bad \\DDD escape %q", s[i:i+4])
					}
					b.WriteByte(byte(n))
					i += 4
					continue
				}
				b.WriteByte(s[i+1])
				i += 2
				continue
			}
			if c == '"' {
				closed = true
				i++
				break
			}
			b.WriteByte(c)
			i++
		}
		if !closed {
			return nil, errors.New("unterminated string")
		}
		out = append(out, b.String())
		if i == len(s) {
			return out, nil
		}
		if s[i] != ' ' && (!lenient || s[i] != '\t') {
			return nil, fmt.Errorf("expected separator at %d in %q", i, s)
		}
		i++
		for lenient && i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
	}
}

// FuzzQuoteTXT checks that quoteTXT's output is a run of character-strings
// that each fit in 255 bytes. Input that is already a well-formed run of
// quoted strings within the limit must keep those strings. Any other input is
// content: its strings must concatenate back to it, without splitting a UTF-8
// character when the input is valid UTF-8.
func FuzzQuoteTXT(f *testing.F) {
	f.Add("v=spf1 include:_spf.example.com ~all")
	f.Add(`say "hi" \ there`)
	f.Add(strings.Repeat("a", 255))
	f.Add(strings.Repeat("a", 256))
	f.Add(strings.Repeat("é", 200))
	f.Add(strings.Repeat("\\", 300))
	f.Add("")
	f.Add(`"already quoted"`)
	f.Add(`"a"b"`)
	f.Add("\"a\" \t \"b\"")
	f.Add(`"\065\999"`)
	f.Add(`"` + strings.Repeat("k", 300) + `"`)
	f.Fuzz(func(t *testing.T, s string) {
		got := quoteTXT(s)
		parts, err := parseCharStrings(got, false)
		if err != nil {
			t.Fatalf("quoteTXT(%q) = %q does not parse: %v", s, got, err)
		}
		for _, p := range parts {
			if len(p) > maxCharString {
				t.Fatalf("chunk of %d bytes exceeds %d", len(p), maxCharString)
			}
		}
		if in, err := parseCharStrings(s, true); err == nil && fitsCharStrings(in) {
			if !slices.Equal(parts, in) {
				t.Fatalf("quoteTXT(%q) changed well-formed strings %q to %q", s, in, parts)
			}
			return
		}
		if strings.Join(parts, "") != s {
			t.Fatalf("quoteTXT(%q) round trip = %q", s, strings.Join(parts, ""))
		}
		for _, p := range parts {
			if utf8.ValidString(s) && !utf8.ValidString(p) {
				t.Fatalf("chunk %q splits a UTF-8 character of %q", p, s)
			}
		}
	})
}

// fitsCharStrings reports whether every string is within the RFC 1035 limit.
func fitsCharStrings(parts []string) bool {
	for _, p := range parts {
		if len(p) > maxCharString {
			return false
		}
	}
	return true
}

// FuzzQualifyTarget checks qualify/qualifyTarget: idempotent, only ever add a
// dot, and leave every field but the last untouched.
func FuzzQualifyTarget(f *testing.F) {
	f.Add("mail.example.com")
	f.Add("0 443 sip.example.com.")
	f.Add("")
	f.Add(".")
	f.Add("0 443 ")
	f.Fuzz(func(t *testing.T, s string) {
		q := qualify(s)
		if qualify(q) != q {
			t.Fatalf("qualify not idempotent on %q", s)
		}
		if q != s && q != s+"." {
			t.Fatalf("qualify(%q) = %q", s, q)
		}
		if s != "" && !strings.HasSuffix(q, ".") {
			t.Fatalf("qualify(%q) = %q not absolute", s, q)
		}
		qt := qualifyTarget(s)
		if qualifyTarget(qt) != qt {
			t.Fatalf("qualifyTarget not idempotent on %q", s)
		}
		if qt != s && qt != s+"." {
			t.Fatalf("qualifyTarget(%q) = %q", s, qt)
		}
	})
}

// fuzzRecord builds one record from fuzz inputs. Only records that the
// create/import validators accept are returned (ok == true): those are the
// ones the API could hold, so export must render them faithfully.
func fuzzRecord(typeIdx uint8, host, answer string, ttl, prio int64) (*coreapigo.Record, bool) {
	types := []string{"A", "AAAA", "ANAME", "CNAME", "MX", "NS", "SRV", "TXT"}
	rtype := types[int(typeIdx)%len(types)]
	// Records arrive as JSON, where invalid UTF-8 cannot survive decoding.
	if !utf8.ValidString(host) || !utf8.ValidString(answer) {
		return nil, false
	}
	if host == "" {
		host = "@"
	}
	if cmdutil.ValidDNSHost(host) != nil || cmdutil.ValidDNSAnswer(rtype, host, answer) != nil || cmdutil.ValidTTL(ttl) != nil {
		return nil, false
	}
	fqdn := "example.com."
	apiHost := host
	if host == "@" {
		apiHost = "" // the API returns the apex as ""
	} else {
		fqdn = host + ".example.com."
	}
	r := &coreapigo.Record{
		Type: &rtype, Host: &apiHost, Answer: &answer, Fqdn: &fqdn, TTL: ttl,
	}
	if rtype == "MX" || rtype == "SRV" {
		if cmdutil.ValidPriority(prio) != nil {
			return nil, false
		}
		r.Priority = &prio
	}
	return r, true
}

// fuzzExporter is one records server and client shared by every iteration of
// a fuzz target: a server per iteration runs the machine out of ephemeral
// ports within seconds.
type fuzzExporter struct {
	mu     sync.Mutex
	body   []byte
	client *api.Client
}

func newFuzzExporter(f *testing.F) *fuzzExporter {
	e := &fuzzExporter{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		e.mu.Lock()
		defer e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(e.body)
	}))
	f.Cleanup(srv.Close)
	client, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		f.Fatal(err)
	}
	e.client = client
	return e
}

// export runs `dns export` over records and returns what it printed.
func (e *fuzzExporter) export(t *testing.T, records []*coreapigo.Record, format output.Format) []byte {
	t.Helper()
	body, err := json.Marshal(coreapigo.ListRecordsResponse{Records: records})
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.body = body
	e.mu.Unlock()
	var buf bytes.Buffer
	out := &output.Config{Format: format, Color: output.ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	cmd := &cobra.Command{}
	ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
	ctx = context.WithValue(ctx, cmdutil.KeyClient, e.client)
	cmd.SetContext(ctx)
	if err := runExport(cmd, []string{"example.com"}); err != nil {
		t.Fatalf("runExport: %v", err)
	}
	return buf.Bytes()
}

// FuzzExportImportRoundTrip exports one valid record as JSON, feeds that
// export to `dns import --dry-run`, and checks the planned create carries the
// same type, host, answer, TTL and priority.
func FuzzExportImportRoundTrip(f *testing.F) {
	exp := newFuzzExporter(f)
	f.Add(uint8(0), "www", "1.2.3.4", int64(300), int64(0))
	f.Add(uint8(4), "@", "mail.example.com", int64(3600), int64(10))
	f.Add(uint8(6), "_sip._tcp", "1 5061 sip.example.org", int64(300), int64(5))
	f.Add(uint8(7), "@", `v=spf1 "quoted" \ ~all`, int64(300), int64(0))
	f.Fuzz(func(t *testing.T, typeIdx uint8, host, answer string, ttl, prio int64) {
		rec, ok := fuzzRecord(typeIdx, host, answer, ttl, prio)
		if !ok {
			return
		}
		exportZone = false
		exported := exp.export(t, []*coreapigo.Record{rec}, output.FormatJSON)

		path := filepath.Join(t.TempDir(), "records.json")
		if err := os.WriteFile(path, exported, 0o600); err != nil {
			t.Fatal(err)
		}
		client, err := api.New(api.Options{BaseURL: "http://127.0.0.1:1"})
		if err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		out := &output.Config{Format: output.FormatJSON, Color: output.ColorNever, Writer: &stdout, EWriter: &bytes.Buffer{}}
		icmd := &cobra.Command{}
		ctx := context.WithValue(context.Background(), cmdutil.KeyOutput, out)
		ctx = context.WithValue(ctx, cmdutil.KeyClient, client)
		icmd.SetContext(ctx)
		icmd.PersistentFlags().Bool("dry-run", true, "")
		importFile = path
		defer func() { importFile = "" }()
		if err := runImport(icmd, []string{"example.com"}); err != nil {
			t.Fatalf("import rejected its own export of %+v: %v\nexport: %s", rec, err, exported)
		}
		var plan []struct {
			Body struct {
				Type     string `json:"type"`
				Host     string `json:"host"`
				Answer   string `json:"answer"`
				TTL      *int64 `json:"ttl"`
				Priority *int64 `json:"priority"`
			} `json:"body"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
			t.Fatalf("dry-run output is not JSON: %v\n%s", err, stdout.String())
		}
		if len(plan) != 1 {
			t.Fatalf("want 1 planned request, got %d", len(plan))
		}
		b := plan[0].Body
		wantHost := *rec.Host
		if wantHost == "" {
			wantHost = "@"
		}
		if b.Type != *rec.Type || b.Host != wantHost || b.Answer != *rec.Answer ||
			b.TTL == nil || *b.TTL != rec.TTL {
			t.Fatalf("round trip changed the record:\n in: %s %q %q ttl=%d\nout: %s %q %q ttl=%v",
				*rec.Type, *rec.Host, *rec.Answer, rec.TTL, b.Type, b.Host, b.Answer, b.TTL)
		}
		if rec.Priority != nil && (b.Priority == nil || *b.Priority != *rec.Priority) {
			t.Fatalf("priority %d lost in round trip: %v", *rec.Priority, b.Priority)
		}
	})
}

// FuzzExportZoneChecks renders one valid record with `dns export --zone` and
// asks BIND's named-checkzone whether the result is a loadable zone. Skipped
// when named-checkzone is not installed.
func FuzzExportZoneChecks(f *testing.F) {
	checker, err := exec.LookPath("named-checkzone")
	if err != nil {
		f.Skip("named-checkzone not installed")
	}
	exp := newFuzzExporter(f)
	f.Add(uint8(0), "www", "1.2.3.4", int64(300), int64(0))
	f.Add(uint8(4), "@", "mail.example.com", int64(3600), int64(10))
	f.Add(uint8(6), "_sip._tcp", "1 5061 sip.example.org", int64(300), int64(5))
	f.Add(uint8(7), "@", `v=spf1 "quoted" \ ~all`, int64(300), int64(0))
	f.Add(uint8(7), "txt", strings.Repeat("k", 600), int64(300), int64(0))
	f.Add(uint8(7), "txt", `"a"b"`, int64(300), int64(0))
	f.Add(uint8(7), "txt", `"`+strings.Repeat("k", 300)+`"`, int64(300), int64(0))
	f.Fuzz(func(t *testing.T, typeIdx uint8, host, answer string, ttl, prio int64) {
		rec, ok := fuzzRecord(typeIdx, host, answer, ttl, prio)
		if !ok || *rec.Type == "ANAME" {
			return // ANAME is exported as a comment
		}
		// Owner names and hostname targets are written unescaped. The
		// validators hold ASCII to the hostname alphabet but leave non-ASCII
		// to the server, so keep names to that alphabet here and fuzz the
		// free-text rdata instead.
		if *rec.Host != "" && !plainName(*rec.Host) {
			return
		}
		// BIND refuses a wildcard NS owner on content grounds, not syntax.
		// Nor will it load an in-zone NS target that has no address record.
		inZone := func(s string) bool {
			s = strings.ToLower(strings.TrimSuffix(s, "."))
			return s == "example.com" || strings.HasSuffix(s, ".example.com")
		}
		if *rec.Type == "NS" && (strings.Contains(*rec.Host, "*") || inZone(*rec.Answer)) {
			return
		}
		switch *rec.Type {
		case "CNAME", "NS", "MX":
			if !plainName(*rec.Answer) {
				return
			}
		case "SRV":
			if !plainName(strings.Fields(*rec.Answer)[2]) {
				return
			}
		}
		exportZone = true
		defer func() { exportZone = false }()
		line := string(exp.export(t, []*coreapigo.Record{rec}, output.FormatTable))
		zone := "$ORIGIN example.com.\n$TTL 300\n" +
			"@ 300 IN SOA ns1.example.net. hostmaster.example.net. 1 3600 600 86400 300\n" +
			"@ 300 IN NS ns1.example.net.\n" + line
		path := filepath.Join(t.TempDir(), "zone")
		if err := os.WriteFile(path, []byte(zone), 0o600); err != nil {
			t.Fatal(err)
		}
		// -i none: integrity checks (e.g. MX target has an address) are about
		// the zone's content, not whether the exported syntax loads.
		// G204: the binary is named-checkzone from PATH and the arguments are fixed.
		outb, err := exec.Command(checker, "-i", "none", "-k", "ignore", "-n", "ignore", //nolint:gosec
			"-m", "ignore", "-M", "ignore", "-S", "ignore", "-r", "ignore",
			"example.com", path).CombinedOutput()
		if err != nil {
			t.Fatalf("named-checkzone rejected the export of %s %q %q:\n%s\nzone line: %q",
				*rec.Type, *rec.Host, *rec.Answer, outb, line)
		}
	})
}

// hostnameAlphabet reports whether s uses only the characters a DNS name in a
// zone file can carry without escaping (letters, digits, '-', '_', '.', '*').
func hostnameAlphabet(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '*':
		default:
			return false
		}
	}
	return true
}

// plainName is a hostname in the unescaped alphabet with no empty label,
// optionally absolute.
func plainName(s string) bool {
	return hostnameAlphabet(s) && cmdutil.ValidDNSHost(strings.TrimSuffix(s, ".")) == nil
}
