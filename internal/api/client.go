// Package api wraps the generated name.com Core API client with credential
// injection, client-side rate limiting, bounded retries, and error
// normalization. Command code calls Gen() for typed endpoint methods and
// Decode() to turn responses into model values or *APIError.
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/config"
	"golang.org/x/time/rate"
)

// idempKeyCtxKey is the private context key for per-request idempotency keys.
type idempKeyCtxKey struct{}

// ContextWithIdempotencyKey attaches key to ctx; the Client's request editor
// will set X-Idempotency-Key on all write requests (POST/PUT/DELETE) that
// use this context.
func ContextWithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempKeyCtxKey{}, key)
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
	gen        *gen.Client
	baseURL    string
	httpClient *http.Client
	// editor applies the standard headers. It is registered on the generated
	// client and also exposed via Prepare for callers that build their own
	// requests; keeping one implementation stops the two paths from drifting.
	editor gen.RequestEditorFn
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

	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &retryTransport{
			base:       http.DefaultTransport,
			limiter:    rate.NewLimiter(rate.Limit(rps), burst),
			maxRetries: maxRetries,
			logw:       opts.DebugLog,
			onRetry:    opts.OnRetry,
		},
	}

	ua := opts.UserAgent
	if ua == "" {
		ua = "namecom-cli"
	}
	authHeader := "Basic " + base64.StdEncoding.EncodeToString(
		[]byte(opts.Creds.Username+":"+opts.Creds.Token))

	editor := func(ctx context.Context, req *http.Request) error {
		// Don't clobber headers the caller set deliberately — `namecom api
		// --header 'Authorization: …'` is a supported escape hatch.
		if req.Header.Get("Authorization") == "" {
			req.Header.Set("Authorization", authHeader)
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", ua)
		}
		if req.Header.Get("Accept") == "" {
			req.Header.Set("Accept", "application/json")
		}
		// Only supply the per-invocation key when the caller hasn't set one.
		// Editors run AFTER the generated request builder, so an unconditional
		// Set() here silently overwrote a key the user passed explicitly —
		// meaning a retried write sent a different key each attempt and could
		// double-charge, which is precisely what the key exists to prevent.
		if key, _ := ctx.Value(idempKeyCtxKey{}).(string); key != "" &&
			req.Header.Get("X-Idempotency-Key") == "" &&
			(req.Method == http.MethodPost || req.Method == http.MethodPut || req.Method == http.MethodDelete) {
			req.Header.Set("X-Idempotency-Key", key)
		}
		return nil
	}

	gc, err := gen.NewClient(baseURL,
		gen.WithHTTPClient(httpClient),
		gen.WithRequestEditorFn(editor),
	)
	if err != nil {
		return nil, fmt.Errorf("building API client: %w", err)
	}
	return &Client{gen: gc, baseURL: baseURL, httpClient: httpClient, editor: editor}, nil
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

// Gen returns the underlying generated client for calling typed endpoint
// methods. The configured HTTP client (auth, rate limit, retries) is applied
// to every call.
func (c *Client) Gen() *gen.Client { return c.gen }

// BaseURL reports the base URL in use (production or sandbox).
func (c *Client) BaseURL() string { return c.baseURL }

// HTTPClient returns the underlying http.Client (with auth, rate limiting, and
// retry applied) for raw requests via `namecom api`.
func (c *Client) HTTPClient() *http.Client { return c.httpClient }

// Decode reads a response from a generated endpoint method: on a 2xx status it
// unmarshals the JSON body into out (which may be nil to discard the body); on
// any other status it returns a normalized *APIError. It always closes the
// body.
func Decode(resp *http.Response, out any) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseError(resp)
	}
	defer func() { _ = resp.Body.Close() }()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
