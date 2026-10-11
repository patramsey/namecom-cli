package cmd

import (
	"encoding/base64"
	"strings"
	"testing"
)

// redactProfile has a token long enough to be scrubbed by value.
const redactProfile = "profiles:\n  work:\n    username: workuser\n    token: work-token-0123456789\n"

// runRedacted is runCounted with redactProfile's credentials.
func runRedacted(t *testing.T, routes map[string]reply, args ...string) (stdout, stderr string, code int, requests int32) {
	t.Helper()
	withConfig(t, redactProfile)
	resetFlags(t, args)
	srv, n := countingServer(t, routes)
	stdout, stderr, code = runContract(t, append([]string{"--base-url", srv.URL}, args...)...)
	return stdout, stderr, code, n.Load()
}

// TestSecretsRedactedByValue pins #341's third part: secrets were redacted by
// the name of the field or header carrying them, so a reply that put one
// somewhere else — a proxy echoing the request headers, an error quoting the
// auth code — printed it in the error, its details and the --debug log.
func TestSecretsRedactedByValue(t *testing.T) {
	stubStart(t, func(string) error { return nil })
	basic := base64.StdEncoding.EncodeToString([]byte("workuser:work-token-0123456789"))
	tests := []struct {
		name     string
		routes   map[string]reply
		args     []string
		secrets  []string
		requests int32
	}{
		{
			name: "an echoed Authorization header",
			routes: map[string]reply{"GET /echo": {400,
				`{"message":"bad request from Basic ` + basic + `","details":"token work-token-0123456789",` +
					`"headers":{"Authorization":"Basic ` + basic + `"}}`}},
			args:     []string{"api", "GET", "/echo", "-o", "json", "--debug"},
			secrets:  []string{basic, "work-token-0123456789"},
			requests: 1,
		},
		{
			name: "an error quoting the auth code",
			routes: map[string]reply{"POST /core/v1/transfers": {400,
				`{"message":"Invalid auth code SECRETCODE123","details":"code SECRETCODE123 was refused"}`}},
			args:    []string{"transfer", "create", "example.com", "--auth-code", "SECRETCODE123", "--yes", "-o", "json", "--debug"},
			secrets: []string{"SECRETCODE123"},
			// The pricing GET, then the POST, as in TestRequestCounts.
			requests: 2,
		},
		{
			name: "an api field's auth code",
			routes: map[string]reply{"POST /core/v1/transfers": {400,
				`{"message":"Invalid auth code SECRETCODE123"}`}},
			args:     []string{"api", "POST", "/core/v1/transfers", "-f", "authCode=SECRETCODE123", "--yes", "-o", "json", "--debug"},
			secrets:  []string{"SECRETCODE123"},
			requests: 1,
		},
		{
			name:     "an api dry run's body",
			args:     []string{"api", "POST", "/core/v1/transfers", "-f", "domainName=example.com", "-f", "authCode=SECRETCODE123", "--dry-run", "-o", "json"},
			secrets:  []string{"SECRETCODE123"},
			requests: 0,
		},
		{
			name:     "a secret query parameter in the debug request line",
			args:     []string{"api", "GET", "/core/v1/hello?access_token=QUERYSECRET1&page=1", "-o", "json", "--debug"},
			secrets:  []string{"QUERYSECRET1"},
			requests: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, _, n := runRedacted(t, tc.routes, tc.args...)
			for _, s := range tc.secrets {
				// Every reply here is an error or a dry run: the api
				// passthrough's stdout copy of a 2xx reply is not covered.
				if strings.Contains(stdout+stderr, s) {
					t.Errorf("%q printed:\nstdout: %s\nstderr: %s", s, stdout, stderr)
				}
			}
			if !strings.Contains(stdout+stderr, "[redacted]") {
				t.Errorf("nothing says [redacted]:\nstdout: %s\nstderr: %s", stdout, stderr)
			}
			if n != tc.requests {
				t.Errorf("sent %d requests, want %d", n, tc.requests)
			}
		})
	}
}

// TestDebugRedactsBaseURLUserinfo: a user name and password in --base-url
// were logged in every --debug request line (#341).
func TestDebugRedactsBaseURLUserinfo(t *testing.T) {
	withConfig(t, redactProfile)
	args := []string{"api", "GET", "/core/v1/hello", "-o", "json", "--debug"}
	resetFlags(t, args)
	srv, n := countingServer(t, nil)
	withUser := strings.Replace(srv.URL, "http://", "http://someone:URLPASSWORD1@", 1)
	_, stderr, code := runContract(t, append([]string{"--base-url", withUser}, args...)...)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, stderr)
	}
	if strings.Contains(stderr, "URLPASSWORD1") || strings.Contains(stderr, "someone") {
		t.Errorf("the userinfo was logged:\n%s", stderr)
	}
	if !strings.Contains(stderr, "http://[redacted]@") {
		t.Errorf("the request line does not show the userinfo redacted:\n%s", stderr)
	}
	if n.Load() != 1 {
		t.Errorf("sent %d requests, want 1", n.Load())
	}
}
