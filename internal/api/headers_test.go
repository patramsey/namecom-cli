package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCredentialNotForwardedToOtherPort is the redirect case the hostname-only
// check missed. Two httptest servers share 127.0.0.1, so a redirect from one to
// the other keeps the hostname and changes only the port. net/http compares
// redirect hosts by name and copies Authorization across, and the old check
// then saw a matching host and left it there — handing the credential to
// whatever was listening on the other port.
func TestCredentialNotForwardedToOtherPort(t *testing.T) {
	var otherSawAuth string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherSawAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer other.Close()

	var originSawAuth string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originSawAuth = r.Header.Get("Authorization")
		http.Redirect(w, r, other.URL+"/elsewhere", http.StatusFound)
	}))
	defer origin.Close()

	c, err := New(Options{BaseURL: origin.URL, MaxRetries: -1})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	resp, err := c.HTTPClient().Get(origin.URL + "/core/v1/domains")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()

	if originSawAuth == "" {
		t.Fatal("the API origin itself got no Authorization header; the test proves nothing")
	}
	if otherSawAuth != "" {
		t.Errorf("Authorization followed a redirect to another port on the same host: %q", otherSawAuth)
	}
}

// TestCredentialBoundToOrigin pins the origin rule directly, including the
// cases an httptest server cannot easily stage (https → http on one host).
func TestCredentialBoundToOrigin(t *testing.T) {
	tests := []struct {
		name, base, target string
		want               bool
	}{
		{"same origin", "https://api.name.com", "https://api.name.com/core/v1/domains", true},
		{"host compared case-insensitively", "https://api.name.com", "https://API.Name.com/x", true},
		{"explicit default port", "https://api.name.com", "https://api.name.com:443/x", true},
		{"base with explicit default port", "https://api.name.com:443", "https://api.name.com/x", true},
		{"plain http base stays usable", "http://127.0.0.1:8080", "http://127.0.0.1:8080/x", true},
		{"https to http on the same host", "https://api.name.com", "http://api.name.com/x", false},
		{"https to http on port 443", "https://api.name.com", "http://api.name.com:443/x", false},
		{"other port on the same host", "https://api.name.com", "https://api.name.com:8443/x", false},
		{"other host", "https://api.name.com", "https://evil.example/x", false},
		{"subdomain", "https://api.name.com", "https://x.api.name.com/x", false},
		{"unparseable base fails closed", "://bad", "https://api.name.com/x", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ht := &headerTransport{authHeader: "Basic secret", userAgent: "ua", authOrigin: originOf(tc.base)}

			// Absent: stamped only for the credential's origin.
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			req.Header.Del("Authorization")
			ht.apply(req)
			if got := req.Header.Get("Authorization") != ""; got != tc.want {
				t.Errorf("stamped Authorization = %v, want %v", got, tc.want)
			}

			// Present — which is how a redirect arrives, because net/http
			// copies it from the original request when only the port or
			// scheme changed. It must be removed, not merely left alone.
			req = httptest.NewRequest(http.MethodGet, tc.target, nil)
			req.Header.Set("Authorization", "Basic secret")
			ht.apply(req)
			if got := req.Header.Get("Authorization") != ""; got != tc.want {
				t.Errorf("kept a copied Authorization = %v, want %v", got, tc.want)
			}
		})
	}
}
