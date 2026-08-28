// Package api wraps the name.com Core SDK with credential injection,
// client-side rate limiting, bounded retries, and error normalization. Command
// code calls SDK() for typed endpoint methods and FromSDKError() to turn an SDK
// error into the *APIError the exit-code mapping understands.
package api

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	sdk "github.com/namedotcom/core-api-go/client"
	"github.com/patramsey/namecom-cli/internal/config"
	"golang.org/x/time/rate"
)

// idempKeyCtxKey is the private context key for an explicitly-supplied
// idempotency key.
type idempKeyCtxKey struct{}

// ContextWithIdempotencyKey pins every write request made with ctx to key.
//
// This is for --idempotency-key, where the user is naming a specific key to
// reuse — typically to make a retried invocation collapse onto an earlier one.
// Leave it unset and each write gets its own generated key instead; see
// idempotencyKeyFor.
func ContextWithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempKeyCtxKey{}, key)
}

// idempotencyKeyFor returns the key to stamp on one outgoing write request.
//
// A pinned key from --idempotency-key wins. Otherwise every write gets a fresh
// one, because a key identifies an OPERATION, not an invocation. It used to be
// minted once per process and reused: `dns import` therefore sent one key
// across every record's POST, and an API honouring keys as documented —
// "reusing the same key returns the original result instead of repeating the
// operation" — would create the first record, echo it back for all the rest,
// and let the CLI report the whole file as imported.
//
// Retries stay safe. The editor runs once, where the generated client builds
// the request; retryTransport replays that same *http.Request with its headers
// already set, so every attempt at one operation carries one key.
func idempotencyKeyFor(ctx context.Context) string {
	if pinned, _ := ctx.Value(idempKeyCtxKey{}).(string); pinned != "" {
		return pinned
	}
	return uuid.NewString()
}

const (
	prodBaseURL    = "https://api.name.com"
	sandboxBaseURL = "https://api.dev.name.com"

	// defaultRPS is deliberately well under the API's 20 req/s limit to leave
	// headroom for other consumers on the same account and for interactive
	// bursts; defaultBurst caps short spikes.
	defaultRPS     = 10
	defaultBurst   = 5
	defaultRetries = 3
	defaultTimeout = 30 * time.Second
)

// Options configures a Client.
type Options struct {
	Creds     config.Credentials
	UserAgent string        // e.g. "namecom-cli/1.2.3"
	Timeout   time.Duration // per-request timeout; 0 uses defaultTimeout
	DebugLog  io.Writer     // when non-nil, log requests/responses here (token redacted)
	BaseURL   string        // override base URL (prod/sandbox inferred from Creds if empty); used in tests

	// OnRetry is called just before sleeping between retry attempts so the
	// caller can surface a "retrying…" message to the user.
	OnRetry func(attempt int, delay time.Duration)

	// Advanced knobs; zero values fall back to the defaults above.
	RPS   float64
	Burst int
	// MaxRetries is the number of retries AFTER the initial attempt.
	// Zero means "use the default"; a negative value disables retries so the
	// first failure is returned immediately. Without the negative case there
	// was no way to express fail-fast, since 0 was already taken by the default.
	MaxRetries int
}

// Client is the configured API client.
type Client struct {
	sdk        *sdk.Namecom
	baseURL    string
	httpClient *http.Client
	// editor applies the standard headers. It is registered on the generated
	// client and also exposed via Prepare for callers that build their own
	// requests; keeping one implementation stops the two paths from drifting.
	editor func(context.Context, *http.Request) error
}

// New builds a Client from the resolved credentials and options.
func New(opts Options) (*Client, error) {
	baseURL := prodBaseURL
	if opts.Creds.Sandbox {
		baseURL = sandboxBaseURL
	}
	if opts.BaseURL != "" {
		baseURL = opts.BaseURL
	}

	rps := opts.RPS
	if rps == 0 {
		rps = defaultRPS
	}
	burst := opts.Burst
	if burst == 0 {
		burst = defaultBurst
	}
	maxRetries := opts.MaxRetries
	switch {
	case maxRetries == 0:
		maxRetries = defaultRetries
	case maxRetries < 0:
		// Explicit fail-fast. Clamp to 0 rather than passing the negative
		// through: the retry loop is `for attempt := 0; attempt <= maxRetries`,
		// so a negative skipped the body entirely and returned (nil, nil) —
		// which net/http reports as "RoundTripper returned a nil *Response with
		// a nil error".
		maxRetries = 0
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ua := opts.UserAgent
	if ua == "" {
		ua = "namecom-cli"
	}
	authHeader := "Basic " + base64.StdEncoding.EncodeToString(
		[]byte(opts.Creds.Username+":"+opts.Creds.Token))

	// headerTransport wraps retryTransport, not the reverse. See its doc
	// comment: the idempotency key must be stamped once, before the retry loop
	// replays the request.
	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &headerTransport{
			authHeader: authHeader,
			userAgent:  ua,
			authHost:   hostOf(baseURL),
			base: &retryTransport{
				base:       http.DefaultTransport,
				limiter:    rate.NewLimiter(rate.Limit(rps), burst),
				maxRetries: maxRetries,
				logw:       opts.DebugLog,
				onRetry:    opts.OnRetry,
			},
		},
	}

	// The header rules now live in headerTransport, which every caller of this
	// HTTP client goes through. This editor stays only so Prepare() keeps
	// working for hand-built requests, and it delegates to the same
	// implementation rather than restating it.
	ht := &headerTransport{authHeader: authHeader, userAgent: ua, authHost: hostOf(baseURL)}
	editor := func(_ context.Context, req *http.Request) error {
		ht.apply(req)
		return nil
	}

	return &Client{
		sdk:        newSDK(baseURL, httpClient),
		baseURL:    baseURL,
		httpClient: httpClient,
		editor:     editor,
	}, nil
}

// Prepare applies the standard headers — auth, User-Agent, Accept, and the
// per-invocation idempotency key — to a hand-built request.
//
// The generated client applies these through a request editor, which only runs
// inside generated endpoint methods. Anything that builds its own request and
// sends it via HTTPClient() (notably `namecom api`) must call this, or the
// request goes out unauthenticated. Headers already present are left alone.
func (c *Client) Prepare(req *http.Request) error {
	if c.editor == nil {
		return nil
	}
	return c.editor(req.Context(), req)
}

// SDK returns the Core SDK client, wired to this Client's transport.
//
// This is the only API client now. It shares the HTTP client built in New, so
// every call goes through headerTransport (auth, User-Agent, idempotency key)
// and retryTransport (rate limiting, retry policy, the deadline guard).
func (c *Client) SDK() *sdk.Namecom { return c.sdk }

// BaseURL reports the base URL in use (production or sandbox).
func (c *Client) BaseURL() string { return c.baseURL }

// HTTPClient returns the underlying http.Client (with auth, rate limiting, and
// retry applied) for raw requests via `namecom api`.
func (c *Client) HTTPClient() *http.Client { return c.httpClient }
