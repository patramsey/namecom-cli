package sdkspike

import (
	"context"
	"net/http"

	coreapigo "github.com/namedotcom/core-api-go"
	sdk "github.com/namedotcom/core-api-go/client"
	"github.com/namedotcom/core-api-go/option"
)

// Workarounds for two defects in core-api-go v1.33.2. Both are written up in
// docs/upstream/ and neither is filed yet.
//
//  1. option.WithoutRetries() is silently ignored at client scope, so the SDK
//     retries a POST on a 5xx even when the client asked it not to.
//  2. The retry backoff sleeps with a bare time.Sleep, so it ignores the
//     request context and can outlast the caller's deadline.
//
// They interact in a way that is not obvious and cost a measurement to find:
// disabling retries does NOT skip the sleep. Retrier.run sleeps BEFORE it
// checks the attempt counter, so a call with retries disabled still waits the
// full Retry-After and only then declines to retry. Against a 429 carrying
// "Retry-After: 30" that is thirty seconds of dead time for one request.
//
// So the two defects need two separate workarounds, and neither substitutes for
// the other.

// safeCallOptions are appended to every SDK call.
//
// WithoutRetries has to be per-call because the client-scoped form does not
// work — see defect 1. That is the whole reason Client below exists: an option
// that must be repeated at every call site is an option someone will eventually
// forget, and forgetting it silently restores POST-on-5xx retries against
// endpoints that do not honour idempotency keys.
func safeCallOptions() []option.RequestOption {
	return []option.RequestOption{option.WithoutRetries()}
}

// stripRetryAfter removes the Retry-After header from responses on their way
// back to the SDK.
//
// This is the workaround for defect 2, and it is only safe because of where it
// sits. By the time a response leaves our RoundTripper, our own transport has
// already read Retry-After, made its retry decision, and — per #41 — either
// waited on a context-aware timer or declined to wait at all. The header has no
// remaining consumer inside the process. What it still has is the SDK's dead
// sleep downstream, which reads it and blocks for up to maxRetryDelay (60s)
// with no way to interrupt.
//
// Deleting it caps that dead sleep at the SDK's minRetryDelay of one second.
// Measured against a 429 carrying "Retry-After: 30": 30s before, 1s after, with
// *core.APIError still carrying status 429 and the response body in both cases.
//
// It is deliberately NOT applied to the shared transport — only to the client
// handed to the SDK — so that `namecom api` and anything else reading raw
// responses still sees the header the server sent.
type stripRetryAfter struct{ base http.RoundTripper }

func (s stripRetryAfter) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := s.base.RoundTrip(req)
	if resp != nil {
		resp.Header.Del("Retry-After")
	}
	return resp, err
}

// sdkHTTPClient wraps our own client for handing to the SDK, adding the
// Retry-After strip and nothing else. The rate limiter, the POST-on-5xx
// refusal, and the deadline guard all live in base's transport and are
// untouched.
func sdkHTTPClient(base *http.Client) *http.Client {
	wrapped := *base // copy: the caller's client keeps its own transport
	inner := base.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	wrapped.Transport = stripRetryAfter{base: inner}
	return &wrapped
}

// Client is the only supported way to reach the SDK from this package.
//
// It keeps *sdk.Namecom unexported on purpose. The per-call WithoutRetries
// workaround cannot be enforced by convention across ~53 call sites, so it is
// enforced by construction instead: there is no way to issue a request that
// skips safeCallOptions without editing this file.
//
// If defect 1 is fixed upstream, this type collapses to a plain client
// constructor and the methods go away.
type Client struct {
	sdk *sdk.Namecom
}

// NewGuarded builds a client with both workarounds applied.
func NewGuarded(baseURL, username, token string, httpClient *http.Client) *Client {
	return &Client{
		sdk: sdk.NewNamecom(
			option.WithBaseURL(baseURL),
			option.WithBasicAuth(username, token),
			option.WithHTTPClient(sdkHTTPClient(httpClient)),
			// Kept even though it does not work at client scope: it is the
			// declaration of intent, and it starts working the day the SDK is
			// fixed. safeCallOptions is what actually holds today.
			option.WithoutRetries(),
		),
	}
}

// ListRecords lists one page of DNS records.
func (c *Client) ListRecords(ctx context.Context, req *coreapigo.ListRecordsRequest) (*coreapigo.ListRecordsResponse, error) {
	return c.sdk.DNS.ListRecords(ctx, req, safeCallOptions()...)
}

// GetRecord reads a single DNS record.
func (c *Client) GetRecord(ctx context.Context, req *coreapigo.GetRecordRequest) (*coreapigo.Record, error) {
	return c.sdk.DNS.GetRecord(ctx, req, safeCallOptions()...)
}

// CreateRecord creates a DNS record.
func (c *Client) CreateRecord(ctx context.Context, req *coreapigo.DNSCreateRecordBody) (*coreapigo.Record, error) {
	return c.sdk.DNS.CreateRecord(ctx, req, safeCallOptions()...)
}

// UpdateRecord replaces a DNS record.
func (c *Client) UpdateRecord(ctx context.Context, req *coreapigo.DNSUpdateRecordBody) (*coreapigo.Record, error) {
	return c.sdk.DNS.UpdateRecord(ctx, req, safeCallOptions()...)
}

// DeleteRecord removes a DNS record.
func (c *Client) DeleteRecord(ctx context.Context, req *coreapigo.DeleteRecordRequest) error {
	return c.sdk.DNS.DeleteRecord(ctx, req, safeCallOptions()...)
}
