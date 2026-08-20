// Package sdkspike is a throwaway evaluation of github.com/namedotcom/core-api-go
// against the `dns` command group. See issue #40.
//
// It exists to answer three questions with running code rather than argument,
// and it is wired the way the evaluation recommends: the SDK supplies the typed
// client, our own transport keeps supplying rate limiting, retry policy, and
// the deadline guard from #41.
//
// Nothing here is imported by cmd/. Delete the package once the decision on #40
// is made either way.
package sdkspike

import (
	"context"
	"fmt"
	"net/http"

	coreapigo "github.com/namedotcom/core-api-go"
	sdk "github.com/namedotcom/core-api-go/client"
	"github.com/namedotcom/core-api-go/option"
)

// New builds an SDK client wired the way the migration would wire it.
//
// httpClient is ours — internal/api builds it with the rate limiter and
// retryTransport inside. WithoutRetries turns the SDK's own retry layer off,
// which is the crux of the recommendation: the SDK's retrier retries POSTs on
// 5xx (it dispatches on status code alone) and sleeps with a bare time.Sleep
// that ignores context. Ours refuses POST retries and returns the response
// rather than sleeping past the deadline. Only one of the two may be live.
func New(baseURL, username, token string, httpClient *http.Client) *Client {
	return NewGuarded(baseURL, username, token, httpClient)
}

// NewWithSDKRetries is the same client with the SDK's retry layer left ON.
// Used only to demonstrate what that layer does; not a supported wiring.
func NewWithSDKRetries(baseURL, username, token string, httpClient *http.Client) *sdk.Namecom {
	return sdk.NewNamecom(
		option.WithBaseURL(baseURL),
		option.WithBasicAuth(username, token),
		option.WithHTTPClient(httpClient),
	)
}

// ListAllRecords walks every page of a zone.
//
// The point of porting this one is the pagination guard from #41. The SDK
// reports nextPage/lastPage as *int where the generated client used *int32, so
// cmdutil.NextPage's signature does not carry over unchanged — this is the
// int-width adaptation the migration needs, isolated so it can be judged.
func ListAllRecords(ctx context.Context, c *Client, domain string) ([]*coreapigo.Record, int, error) {
	var (
		all      []*coreapigo.Record
		page     = 1
		requests int
	)
	for {
		p := page
		resp, err := c.ListRecords(ctx, &coreapigo.ListRecordsRequest{
			DomainName: domain,
			Page:       &p,
		})
		requests++
		if err != nil {
			return nil, requests, fmt.Errorf("listing records for %s: %w", domain, err)
		}
		all = append(all, resp.Records...)

		next, ok := nextPage(page, resp.NextPage, resp.LastPage)
		if !ok {
			return all, requests, nil
		}
		page = next
	}
}

// nextPage is cmdutil.NextPage over int rather than int32.
//
// That it had to be re-typed at all is the finding: the guard is not portable
// as written, and every paginated walk would need the same adaptation. The
// logic is identical — the page must advance, and must not run past lastPage.
func nextPage(current int, next, last *int) (int, bool) {
	if next == nil || *next == 0 {
		return current, false
	}
	if *next <= current {
		return current, false
	}
	if last != nil && *last > 0 && *next > *last {
		return current, false
	}
	return *next, true
}

// Changed carries the "was this flag supplied" signal that cmd/dns/dns.go reads
// from cobra, so the read-modify-write merge can be judged without a cobra
// command in the way. A nil field means the caller did not supply that flag and
// the current value must survive.
type Changed struct {
	Type     *string
	Host     *string
	Answer   *string
	TTL      *int64
	Priority *int64
}

// UpdateRecord mirrors runUpdate in cmd/dns/dns.go against the SDK.
func UpdateRecord(ctx context.Context, c *Client, domain string, id int, ch Changed) (*coreapigo.Record, error) {
	current, err := c.GetRecord(ctx, &coreapigo.GetRecordRequest{
		DomainName: domain,
		ID:         id,
	})
	if err != nil {
		return nil, fmt.Errorf("reading record %d on %s: %w", id, domain, err)
	}

	// The two casts here are the same ones internal/api/gen forces today, and
	// the reason is the same: the spec declares Record.type as a plain string
	// and the update body's type as an enum, and Record.ttl as a value where
	// the body's is a pointer. Both survive the migration unchanged — they come
	// from the spec, not from oapi-codegen.
	body := &coreapigo.DNSUpdateRecordBody{
		DomainName: domain,
		ID:         id,
		Type:       coreapigo.DNSUpdateRecordBodyType(derefStr(current.Type)),
		Answer:     derefStr(current.Answer),
		Host:       current.Host,
		TTL:        &current.TTL,
		Priority:   current.Priority,
	}

	if ch.Type != nil {
		body.Type = coreapigo.DNSUpdateRecordBodyType(*ch.Type)
	}
	if ch.Host != nil {
		body.Host = ch.Host
	}
	if ch.Answer != nil {
		body.Answer = *ch.Answer
	}
	if ch.TTL != nil {
		body.TTL = ch.TTL
	}
	if ch.Priority != nil {
		body.Priority = ch.Priority
	}

	updated, err := c.UpdateRecord(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("updating record %d on %s: %w", id, domain, err)
	}
	return updated, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
