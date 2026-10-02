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
	// authOrigin is the scheme, host, and port the credential belongs to.
	// Authorization is sent only on requests to it — see apply.
	authOrigin origin
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
	// The check is an exact origin match — scheme, host, and port — which is
	// deliberately stricter than net/http's rule: this client talks to one
	// origin. net/http compares redirect hosts by name only, so a redirect to
	// another port on the same host, or from https to http, arrives here with
	// Authorization already copied onto it. Leaving a present header alone is
	// not enough, then: off-origin it is removed. That includes one supplied
	// through `namecom api --header`, which is a credential all the same.
	switch {
	case !t.originMatches(req):
		req.Header.Del("Authorization")
	case req.Header.Get("Authorization") == "":
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

// origin is the part of a URL a credential is bound to. Comparing all three
// parts keeps the credential off another service on the same host, and off
// plain http when the API is reached over https.
type origin struct {
	scheme, host, port string
}

// originMatches reports whether req is addressed to the origin the credential
// belongs to. A zero authOrigin means the transport was built without one, in
// which case no credential is sent at all rather than sent everywhere.
func (t *headerTransport) originMatches(req *http.Request) bool {
	if t.authOrigin == (origin{}) || req.URL == nil {
		return false
	}
	return originOfURL(req.URL) == t.authOrigin
}

// originOf extracts the origin from a base URL, for binding the credential to
// it. A URL that will not parse, or lacks a scheme or host, yields the zero
// origin, which makes originMatches refuse to send anything — failing closed
// rather than sending the token everywhere.
func originOf(baseURL string) origin {
	u, err := url.Parse(baseURL)
	if err != nil {
		return origin{}
	}
	o := originOfURL(u)
	if o.scheme == "" || o.host == "" {
		return origin{}
	}
	return o
}

// originOfURL normalises u's origin: scheme and host lower-cased, and an
// omitted port filled in with the scheme's default, so https://api.name.com
// and https://api.name.com:443 compare equal.
func originOfURL(u *url.URL) origin {
	o := origin{
		scheme: strings.ToLower(u.Scheme),
		host:   strings.ToLower(u.Hostname()),
		port:   u.Port(),
	}
	if o.port == "" {
		switch o.scheme {
		case "https":
			o.port = "443"
		case "http":
			o.port = "80"
		}
	}
	return o
}

// sdkUserAgentPrefix is what the Core SDK stamps as its User-Agent, minus the
// version.
const sdkUserAgentPrefix = "github.com/namedotcom/core-api-go/"
