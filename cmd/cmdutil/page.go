package cmdutil

import (
	"fmt"
	"math"

	"github.com/spf13/cobra"
)

// AllUsage is the help for every list's --all.
const AllUsage = "fetch every page, starting at --page"

// AddPageFlags gives a list command --all, --page and --limit, the same on
// every paged list (#236). --page existed only on `domain list`, so
// `order list --page 2` was an unknown flag, and --all was described four
// ways. noun names one result, for --limit's help.
func AddPageFlags(cmd *cobra.Command, all *bool, page, limit *int, noun string) {
	cmd.Flags().BoolVar(all, "all", false, AllUsage)
	cmd.Flags().IntVar(page, "page", 1, "page to fetch, from 1")
	cmd.Flags().IntVar(limit, "limit", 0, noun+"s per page (default: the API's page size)")
}

// ValidPage checks --page and --limit before any request. --page 0 used to
// exit 1, like an API failure, instead of 2.
func ValidPage(page, limit int) error {
	if page < 1 {
		return usagef("--page must be 1 or greater (got %d)", page)
	}
	if limit < 0 {
		return usagef("--limit must be 1 or greater (got %d)", limit)
	}
	return nil
}

// PerPage is --limit as the perPage a list request sends: nil, the API's
// default, when it was not set.
func PerPage(limit int) *int {
	if limit <= 0 {
		return nil
	}
	return &limit
}

// MorePages is the note under a list that stopped before its last page,
// matching the footer `domain list` already printed.
func MorePages(next int) string {
	return fmt.Sprintf("--page %d for more, --all for everything", next)
}

// Int32Page narrows the SDK's *int page number to the *int32 the output
// envelope uses.
//
// The envelope is not widened to match, because its type is part of the JSON
// this CLI emits: `nextPage` is a number in every list command's output, and
// changing the Go type risks changing that shape for a field that is a small
// positive integer in every real response.
//
// A page number that does not fit in an int32 cannot have come from this API.
// It yields nil — the caller then omits `nextPage` rather than printing a
// wrapped, wrong page for the user to pass back to --page.
func Int32Page(p *int) *int32 {
	if p == nil {
		return nil
	}
	if *p > math.MaxInt32 || *p < math.MinInt32 {
		return nil
	}
	v := int32(*p)
	return &v
}

// Int32Count narrows the SDK's int total to the int32 the output envelope uses.
//
// Clamped rather than converted. A bare conversion is an overflow gosec flags,
// and a wrapped negative total would be printed to the user as fact — "3
// domains" when there are billions is a worse failure than "a very large
// number". The clamp is unreachable against this API; it exists so the
// impossible case degrades honestly.
func Int32Count(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < 0 {
		return 0
	}
	return int32(n)
}

// NonNil returns s without its nil elements, leaving s itself unchanged.
//
// The SDK decodes a JSON null inside a response list as a nil pointer, and
// every row builder and --quiet loop dereferenced each element unchecked, so
// one null crashed the command (#157). Apply it where a list is taken from a
// response, so nothing downstream has to check. A nil element carries nothing
// to show, so it is dropped rather than printed as an empty row.
func NonNil[T any](s []*T) []*T {
	for i, v := range s {
		if v != nil {
			continue
		}
		out := append(make([]*T, 0, len(s)-1), s[:i]...)
		for _, v := range s[i+1:] {
			if v != nil {
				out = append(out, v)
			}
		}
		return out
	}
	return s
}
