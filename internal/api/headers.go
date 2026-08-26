package api

import (
	"net/http"
	"net/url"
	"strings"
)

// headerTransport stamps the standard headers on every outgoing request:
// auth, User-Agent, Accept, and the idempotency key for writes.
//
// This lives in the transport rather than in a gen.RequestEditorFn because a
// request editor only runs inside the generated client's endpoint methods.
// Anything else that shares this HTTP client — the Core SDK, which builds its
// own requests, and `namecom api`, which hand-rolls them — would otherwise need
// its own copy of this logic, and three copies of an auth rule is how one of
// them ends up sending an unauthenticated request.
//
// It is placed outside retryTransport so the debug log written by the retry
// layer shows the headers as sent. Key stability across retries does not depend
// on that ordering, which is worth stating because it looks like it should:
// retryTransport replays the same *http.Request, and apply only fills headers
// that are absent, so the key set on the first attempt survives into the rest
// either way. Verified by inverting the nesting and watching
// TestIdempotencyKeyStableAcrossRetries still pass.
type headerTransport struct {
	base       http.RoundTripper
	authHeader string
	userAgent  string
	// authHost is the hostname the credential belongs to. Authorization is
	// stamped only on requests to it — see apply.
	authHost string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.apply(req)
	return t.base.RoundTrip(req)
}

// apply stamps the headers on req. Exposed separately so Prepare() can reuse it
// for hand-built requests instead of carrying a second copy of these rules.
//
// Headers already present are left alone. `namecom api --header
// 'Authorization: …'` is a supported escape hatch, and for that path apply runs
// twice — once via Prepare, once in RoundTrip — which is why it must be
// idempotent rather than unconditional.
func (t *headerTransport) apply(req *http.Request) {
	// Bind the credential to the API's host. net/http strips Authorization
	// when it follows a redirect to a different host, but a transport runs
	// again for the redirected request, so an unconditional Set here puts the
	// token back and hands it to whatever host the redirect named. That is a
	// credential leak to an arbitrary server, and it is exactly what
	// TestAPI_CredentialNotForwardedOnCrossHostRedirect exists to catch —
	// which it did, when this logic first moved out of the request editor.
	//
	// The check is an exact hostname match, deliberately stricter than the
	// subdomain rule net/http uses: this client talks to one host.
	if req.Header.Get("Authorization") == "" && t.hostMatches(req) {
		req.Header.Set("Authorization", t.authHeader)
	}
	// Absent, or stamped by the SDK. The Core SDK's cloneHeader() overwrites
	// User-Agent unconditionally after cloning any header the caller supplied,
	// so option.WithHTTPHeader cannot set it and this is the only place left to
	// correct it. Requests would otherwise identify the library rather than the
	// CLI, which is the string name.com sees when attributing traffic — and the
	// point of running both clients on one transport is that a request looks the
	// same whichever one built it.
	//
	// Matched by prefix rather than exact string so a version bump upstream
	// does not silently reintroduce it. A user-supplied User-Agent, including
	// `namecom api --header`, is still left alone. Upstream can still identify
	// the library: the SDK also sets X-Fern-SDK-Name and X-Fern-SDK-Version,
	// which are untouched.
	if ua := req.Header.Get("User-Agent"); ua == "" || strings.HasPrefix(ua, sdkUserAgentPrefix) {
		req.Header.Set("User-Agent", t.userAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if req.Header.Get("X-Idempotency-Key") == "" && isWrite(req.Method) {
		req.Header.Set("X-Idempotency-Key", idempotencyKeyFor(req.Context()))
	}
}

// isWrite reports whether a method carries an idempotency key.
//
// PATCH is deliberately absent, matching what the editor did before this moved:
// the Core API declares X-Idempotency-Key on five operations, none of them
// PATCH. Adding it here would send a header the API does not document for those
// endpoints.
func isWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// hostMatches reports whether req is addressed to the host the credential
// belongs to. An empty authHost means the transport was built without one, in
// which case no credential is stamped at all rather than stamped everywhere.
func (t *headerTransport) hostMatches(req *http.Request) bool {
	if t.authHost == "" || req.URL == nil {
		return false
	}
	return strings.EqualFold(req.URL.Hostname(), t.authHost)
}

// hostOf extracts the hostname from a base URL, for binding the credential to
// it. A URL that will not parse yields "", which makes hostMatches refuse to
// stamp anything — failing closed rather than sending the token everywhere.
func hostOf(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// sdkUserAgentPrefix is what the Core SDK stamps as its User-Agent, minus the
// version.
const sdkUserAgentPrefix = "github.com/namedotcom/core-api-go/"
