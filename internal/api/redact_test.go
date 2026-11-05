package api

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/config"
)

func TestRedactBody(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"top level", `{"domainName":"example.com","authCode":"s3cret"}`,
			`{"authCode":"[redacted]","domainName":"example.com"}`},
		{"nested in an object", `{"transfer":{"authCode":"s3cret","status":"pending"}}`,
			`{"transfer":{"authCode":"[redacted]","status":"pending"}}`},
		{"nested in an array", `{"items":[{"password":"s3cret"},{"apiToken":"s3cret"}]}`,
			`{"items":[{"password":"[redacted]"},{"apiToken":"[redacted]"}]}`},
		{"spelling variants", `{"auth_code":"s3cret","AUTHCODE":"s3cret","client-secret":"s3cret"}`,
			`{"AUTHCODE":"[redacted]","auth_code":"[redacted]","client-secret":"[redacted]"}`},
		{"numbers keep their form", `{"authCode":"s3cret","totalPaid":12.990}`,
			`{"authCode":"[redacted]","totalPaid":12.990}`},
		{"names are not secrets", `{"apiTokenName":"ci","keyTag":1}`,
			`{"apiTokenName":"ci","keyTag":1}`},
		{"untouched body keeps its bytes", "{ \"b\": 1,\n  \"a\": 2 }",
			"{ \"b\": 1,\n  \"a\": 2 }"},
		{"truncated JSON", `{"domainName":"example.com","authCode":"s3cr`,
			`{"domainName":"example.com","authCode":"[redacted]"`},
		{"not JSON", `authCode=s3cret`, `authCode=s3cret`},
		{"empty", ``, ``},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(RedactBody([]byte(tc.in))); got != tc.want {
				t.Errorf("RedactBody(%q)\n got %s\nwant %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestDebugLogRedactsSecretAtTheLogLimit covers a secret whose value straddles
// the 4 KiB cap on a logged request body: no part of it may survive the cut.
func TestDebugLogRedactsSecretAtTheLogLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var log bytes.Buffer
	c, err := New(Options{BaseURL: srv.URL, DebugLog: &log})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	const secret = "VISIBLEPREFIX-hidden-tail"
	pad := strings.Repeat("x", 4096-len(`{"pad":"","authCode":"VISIBLEPREFIX`))
	body := `{"pad":"` + pad + `","authCode":"` + secret + `"}`
	resp, err := c.HTTPClient().Post(srv.URL+"/core/v1/transfers", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()

	if strings.Contains(log.String(), "VISIBLEPREFIX") {
		t.Errorf("debug log carries the start of a secret cut at the log limit:\n%s", log.String())
	}
}

// TestDebugLogEscapesControlCharacters pins #359: --debug printed request
// and response bodies with their escape sequences raw, so a 502 page holding
// ESC[2J cleared the terminal and a domain name could set its title. Secrets
// were redacted there; control characters are now escaped as well, after the
// secrets are replaced, so an escaped form cannot hide one.
func TestDebugLogEscapesControlCharacters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>\x1b[2Jbad gateway \x1b]0;PWNED\a s3cretdebugtoken</html>"))
	}))
	defer srv.Close()

	var log bytes.Buffer
	c, err := New(Options{
		BaseURL:    srv.URL,
		DebugLog:   &log,
		Creds:      config.Credentials{Username: "u", Token: "s3cretdebugtoken"},
		MaxRetries: -1,
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	resp, err := c.HTTPClient().Post(srv.URL+"/core/v1/domains", "text/plain",
		strings.NewReader("raw\x1b]0;REQ\a\x9b2J"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()

	got := log.String()
	if strings.ContainsAny(got, "\x1b\a") || strings.Contains(got, "\u009b") {
		t.Errorf("debug log holds a raw control character:\n%q", got)
	}
	for _, want := range []string{`raw\x1b]0;REQ\x07\x9b2J`, `\x1b[2Jbad gateway \x1b]0;PWNED\x07 [redacted]`} {
		if !strings.Contains(got, want) {
			t.Errorf("debug log does not show %q:\n%q", want, got)
		}
	}
	if strings.Contains(got, "s3cretdebugtoken") {
		t.Errorf("debug log holds the token:\n%q", got)
	}
}

// TestDebugLogTimestampsAndHeaders pins #187: the --debug log had no
// timestamps and no headers, though headers are most of what a failed request
// needs to be diagnosed. Each entry now starts with an RFC 3339 timestamp, the
// response carries the round-trip time, and both directions show headers —
// with every credential header redacted, so neither the token nor the Basic
// value it is encoded into reaches a log that gets pasted into bug reports.
func TestDebugLogTimestampsAndHeaders(t *testing.T) {
	const token = "tok-SECRET-0123456789"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-42")
		w.Header().Set("Set-Cookie", "session=COOKIESECRET")
		w.Header().Set("X-Unrelated", "noise")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var log bytes.Buffer
	c, err := New(Options{
		BaseURL:  srv.URL,
		Creds:    config.Credentials{Username: "alice", Token: token},
		DebugLog: &log,
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/core/v1/domains/example.com", nil)
	req.Header.Set("Cookie", "session=COOKIESECRET")
	req.Header.Set("X-Api-Token", "HEADERSECRET")
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	got := log.String()

	stamp := `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}(Z|[+-]\d{2}:\d{2})`
	for _, pattern := range []string{
		`(?m)^` + stamp + ` → GET http://\S+/core/v1/domains/example\.com$`,
		`(?m)^` + stamp + ` ← 200 OK \(\d+(\.\d+)?(µs|ms|s)\)$`,
		`(?m)^  Authorization: \[redacted\]$`,
		`(?m)^  Cookie: \[redacted\]$`,
		`(?m)^  X-Api-Token: \[redacted\]$`,
		`(?m)^  User-Agent: \S+`,
		`(?m)^  Accept: application/json$`,
		`(?m)^  Content-Type: application/json$`,
		`(?m)^  X-Request-Id: req-42$`,
	} {
		if !regexp.MustCompile(pattern).MatchString(got) {
			t.Errorf("debug log does not match %s:\n%s", pattern, got)
		}
	}
	basic := base64.StdEncoding.EncodeToString([]byte("alice:" + token))
	for _, secret := range []string{token, basic, "COOKIESECRET", "HEADERSECRET"} {
		if strings.Contains(got, secret) {
			t.Errorf("debug log contains the secret %q:\n%s", secret, got)
		}
	}
	// Response headers are filtered to the ones that help debug a request;
	// the rest would bury them.
	if strings.Contains(got, "X-Unrelated") {
		t.Errorf("debug log shows an unrelated response header:\n%s", got)
	}
}
