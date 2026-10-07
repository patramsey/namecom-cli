package cmdutil

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// AllUsage is the help for every list's --all.
const AllUsage = "fetch every page, starting at --page, 1000 per request"

// MaxPerPage is the largest page the API serves: a larger perPage is
// rejected with "perPage exceeds maximum of 1000".
const MaxPerPage = 1000

// AddPageFlags gives a list command --all, --page and --limit, the same on
// every paged list (#236). --page existed only on `domain list`, so
// `order list --page 2` was an unknown flag, and --all was described four
// ways. plural names the results, for --limit's help.
func AddPageFlags(cmd *cobra.Command, all *bool, page, limit *int, plural string) {
	cmd.Flags().BoolVar(all, "all", false, AllUsage)
	cmd.Flags().IntVar(page, "page", 1, "page to fetch, from 1")
	cmd.Flags().IntVar(limit, "limit", 0, plural+" per page, 1 to 1000 (default: the API's page size)")
	MarkList(cmd)
}

// Paging is how a paged list fetches, decided by ListPaging.
type Paging struct {
	// All is whether to fetch every page from --page on, rather than one.
	All bool
	// PerPage is the perPage each request sends: nil for the API's default.
	PerPage *int
}

// ListPaging checks a list's --page and --limit before any request and
// decides how it fetches: every page when AutoPage says so, else the one
// page --page names, of --limit items.
//
// --page 0 used to exit 1, like an API failure, instead of 2 (#236), and
// --limit 0 silently meant the default page while --limit 1001 was a 400 from
// the API (#290). Both are usage errors now.
//
// A list that fetches every page requests MaxPerPage whatever --limit says:
// --limit is how many items a page shows, and when every page is fetched
// it only sets how many requests that takes. `domain list --limit 2 --all`
// sent one request per two domains (#290); it now sends one per thousand,
// and says that --limit did not apply.
func ListPaging(cmd *cobra.Command, all bool, page, limit int) (Paging, error) {
	if page < 1 {
		return Paging{}, usagef("--page must be 1 or greater (got %d)", page)
	}
	limitSet := cmd.Flags().Changed("limit")
	if (limitSet || limit != 0) && (limit < 1 || limit > MaxPerPage) {
		return Paging{}, usagef("--limit must be between 1 and %d (got %d)", MaxPerPage, limit)
	}
	if !AutoPage(cmd, all) {
		if limit == 0 {
			return Paging{}, nil
		}
		return Paging{PerPage: &limit}, nil
	}
	if limitSet {
		Out(cmd).Warn(fmt.Sprintf("--limit does not apply with --all: every page is fetched, %d per request", MaxPerPage))
	}
	n := MaxPerPage
	return Paging{All: true, PerPage: &n}, nil
}

// MorePages is the note under a list that stopped before its last page,
// matching the footer `domain list` already printed.
func MorePages(next int) string {
	return fmt.Sprintf("--page %d for more, --all for everything", next)
}

// AutoPage reports whether a paged list fetches every page rather than one:
// with --all, or with --quiet when neither --page nor --limit was given.
// Nothing else does: a filter narrows the page, it does not fetch more of
// them. domain, order and dns list used to page fully whenever one was set,
// ignoring --page and --limit, so `order list --status failed --limit 2`
// sent 52 requests (#281).
//
// --quiet pages fully by default because the table's "--page N for more"
// footer is not printed under it, so one page would truncate silently (#99).
// It used to do so even with --page or --limit, so `order list -q --limit 1`
// fetched the whole history one order per request (#277). Those flags ask
// for one page, and get it; QuietMorePages says there is more.
func AutoPage(cmd *cobra.Command, all bool) bool {
	if all {
		return true
	}
	return Out(cmd).QuietMode && !cmd.Flags().Changed("page") && !cmd.Flags().Changed("limit")
}

// QuietMorePages is MorePages for a --quiet list that stopped before its last
// page. It goes to stderr whatever the format, so stdout stays one value per
// line, as Footer would print it if quiet mode printed footers.
func QuietMorePages(out *output.Config, next int) {
	fmt.Fprintln(out.EWriter, out.Dim(MorePages(next)))
}

// Showing is the count under a list that stopped before its last page, when
// the API reported where the page sits: "Showing 1–250 of 6,522 domains".
// "1 unverified contact · --page 2 for more" read as the total (#290).
func Showing(from, to, total int, noun string) string {
	return fmt.Sprintf("Showing %s–%s of %s", output.Thousands(from), output.Thousands(to), output.Plural(total, noun))
}

// EmptyPage is out.Empty for a list that came back empty. Past page 1 that
// says nothing about the list, only that --page ran past its end, so the
// usual hint — "add the first one", or for unverified contacts "new ones
// take ~10 minutes to appear" — would be wrong (#290).
func EmptyPage(out *output.Config, page int, headers []string, noun, hint string) {
	if page <= 1 || out.Format == output.FormatTSV {
		out.EmptyTable(headers, noun, hint)
		return
	}
	if out.Format != output.FormatTable || out.QuietMode {
		return
	}
	fmt.Fprintln(out.EWriter, out.Dim(fmt.Sprintf("No %s on page %d.", output.PluralNoun(2, noun), page)))
	out.Hint("That is past the last page; leave out --page to start at the first")
}

// PastLastPage reports whether the page a list asked for, perPage at a time
// (nil for the API's default), is past the end of the list, judged from the
// reply: n items, total the totalCount it reported (0 when it did not) and
// lastPage (nil or 0 when it did not). The caller then shows an empty page,
// whatever the reply held.
//
// Past the end, the API does not answer with an empty page. Where the list
// fits on one page, `domain list` and `order list` answer with page 1 again,
// so a script paging until a page came back empty never stopped. `dns list`
// detected that from the reply's from, which the API gets wrong: at perPage
// 1, page 2 says from:1, so a real page was thrown away. Only the count is
// trusted here. Each test is one a real page cannot fail: no page is past
// lastPage, page p starts after (p-1)*perPage items, and a page after the
// first cannot hold every item.
func PastLastPage(page int, perPage *int, n, total int, lastPage *int) bool {
	if page <= 1 {
		return false
	}
	if lastPage != nil && *lastPage > 0 && page > *lastPage {
		return true
	}
	if total <= 0 {
		return false
	}
	if perPage != nil && (page-1)*(*perPage) >= total {
		return true
	}
	return n >= total
}

// PageOutOfRange reports whether err is how the API refuses a page past the
// end of a list that spans several: a 400, "Page exceeds available pages".
// For page 2 or later that is an empty page, as PastLastPage makes the other
// answers, not a failure: `domain list --page 8` past 7 pages exited 1.
func PageOutOfRange(page int, err error) bool {
	var apiErr *api.APIError
	return page > 1 && errors.As(api.NormalizeError(err), &apiErr) &&
		apiErr.StatusCode == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(apiErr.Message), "page exceeds available pages")
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
