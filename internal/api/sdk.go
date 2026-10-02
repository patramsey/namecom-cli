package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	coreapigo "github.com/namedotcom/core-api-go"
	sdk "github.com/namedotcom/core-api-go/client"
	sdkcore "github.com/namedotcom/core-api-go/core"
	"github.com/namedotcom/core-api-go/option"
)

// newSDK builds the Core SDK client on the same *http.Client as the generated
// one, so a single transport keeps serving both during the migration in #40.
//
// No credentials are passed. headerTransport stamps Authorization, User-Agent,
// Accept, and the idempotency key on everything that goes through this HTTP
// client, so handing the SDK its own copy of the token would create a second
// credential path that could drift from the first — and the first is the one
// `namecom api` and the generated client already rely on.
//
// option.WithoutRetries() is the load-bearing line. The SDK's retrier
// dispatches on status code with no method check, so with retries enabled it
// replays a POST on a 5xx — against endpoints that do not deduplicate, since
// the Core API declares X-Idempotency-Key on only five operations and record
// creation is not one of them. Our retryTransport already implements the
// policy we want, including refusing exactly that replay.
//
// This option was silently ignored at client scope through v1.33.2
// (namedotcom/core-api-go#3) and is fixed in v1.33.3. That fix is what makes
// this wiring safe, and internal/sdkspike guards it.
//
// It is not enough on its own, though: see finalResponseClient.
func newSDK(baseURL string, hc *http.Client) *sdk.Namecom {
	// The User-Agent is NOT set here, though it looks like it should be.
	// core/request_option.go's cloneHeader() clones the caller's HTTPHeader and
	// then unconditionally overwrites User-Agent with the library's own string,
	// so option.WithHTTPHeader cannot influence it. headerTransport corrects it
	// afterwards instead — see sdkUserAgentPrefix.
	return sdk.NewNamecom(
		option.WithBaseURL(baseURL),
		option.WithHTTPClient(finalResponseClient{hc}),
		option.WithoutRetries(),
	)
}

// finalResponseClient keeps the SDK's retrier from ever seeing a status it
// would sleep on.
//
// With retries disabled, that retrier still sleeps on a 429, 408 or 5xx before
// returning it (namedotcom/core-api-go#12, as of v1.34.0): it honours
// Retry-After up to 60s, or backs off a second or more, and only then notices
// it has no attempts left. By the time a response reaches it, retryTransport
// has already retried what it should and declined to wait past the deadline,
// so the sleep is pure delay. A 429 with "Retry-After: 600" under --timeout 5s
// hung for 60s; with "Retry-After: 20" the sleep outlived the deadline and the
// body read failed, turning exit 5 into exit 1 with "request canceled".
//
// The retrier returns a transport error immediately, without sleeping, so the
// response is decoded here, exactly as the SDK's own error decoder would, and
// handed back as that error. Callers still get *core.APIError, or the typed
// error for the status (*TooManyRequestsError, …), with the body and headers
// intact. Remove this once the upstream fix ships.
type finalResponseClient struct {
	hc *http.Client
}

func (c finalResponseClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.hc.Do(req) //nolint:gosec // G704: the SDK's own request to the configured base URL, passed through
	if err != nil || !sdkRetrierSleepsOn(resp.StatusCode) {
		return resp, err
	}
	return nil, decodeSDKError(resp)
}

// sdkRetrierSleepsOn mirrors the SDK retrier's shouldRetry.
func sdkRetrierSleepsOn(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusRequestTimeout ||
		status >= http.StatusInternalServerError
}

// decodeSDKError builds the error the SDK's error decoder
// (internal.NewErrorDecoder over coreapigo.ErrorCodes, which every endpoint
// uses) would have returned for resp, reading and closing its body.
func decodeSDKError(resp *http.Response) error {
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("failed to read error from response body: %w", err)
	}
	apiErr := sdkcore.NewAPIError(resp.StatusCode, resp.Header, errors.New(string(raw)))
	newTyped, ok := coreapigo.ErrorCodes[resp.StatusCode]
	if !ok {
		return apiErr
	}
	typed := newTyped(apiErr)
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(typed); err != nil {
		return apiErr
	}
	return typed
}
