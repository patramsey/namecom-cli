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
	RPS        float64
	Burst      int
	MaxRetries int
}

// Client is the configured API client.
type Client struct {
	gen        *gen.Client
	baseURL    string
	httpClient *http.Client
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
	if maxRetries == 0 {
		maxRetries = defaultRetries
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
		req.Header.Set("Authorization", authHeader)
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "application/json")
		if key, _ := ctx.Value(idempKeyCtxKey{}).(string); key != "" &&
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
	return &Client{gen: gc, baseURL: baseURL, httpClient: httpClient}, nil
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
