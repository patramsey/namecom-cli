package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
