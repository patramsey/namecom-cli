package api

import (
	"net/http"

	sdk "github.com/namedotcom/core-api-go/client"
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
func newSDK(baseURL string, hc *http.Client) *sdk.Namecom {
	// The User-Agent is NOT set here, though it looks like it should be.
	// core/request_option.go's cloneHeader() clones the caller's HTTPHeader and
	// then unconditionally overwrites User-Agent with the library's own string,
	// so option.WithHTTPHeader cannot influence it. headerTransport corrects it
	// afterwards instead — see sdkUserAgentPrefix.
	return sdk.NewNamecom(
		option.WithBaseURL(baseURL),
		option.WithHTTPClient(hc),
		option.WithoutRetries(),
	)
}
