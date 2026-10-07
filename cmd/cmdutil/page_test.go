package cmdutil

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/patramsey/namecom-cli/internal/api"
)

// TestPastLastPage covers the rule every paged list uses for a page past the
// end. The API answers one with page 1 again, so each case past the end is
// a reply a real page could not be; the from field, which the API gets wrong
// at perPage 1, is not consulted.
func TestPastLastPage(t *testing.T) {
	one, two := 1, 2
	for _, tc := range []struct {
		name     string
		page     int
		perPage  *int
		n, total int
		lastPage *int
		want     bool
	}{
		{"page 1 is never past the end", 1, &two, 2, 2, nil, false},
		{"page 2 answered with all 5 of a one-page list", 2, nil, 5, 5, nil, true},
		{"page 2 at --limit 5 of 5", 2, intPtr(5), 5, 5, nil, true},
		{"page 2 of 1 a page, 6 in all", 2, &one, 1, 6, intPtr(6), false},
		{"page 3 of 2 a page, 6 in all", 3, &two, 2, 6, nil, false},
		{"page 4 of 2 a page, 6 in all", 4, &two, 2, 6, nil, true},
		{"page past lastPage", 5, nil, 0, 0, intPtr(4), true},
		{"page at lastPage", 4, nil, 1, 0, intPtr(4), false},
		{"no count and no lastPage is taken at its word", 5, &two, 2, 0, nil, false},
		{"page 2 of 1000 at the default page size", 2, nil, 500, 1000, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PastLastPage(tc.page, tc.perPage, tc.n, tc.total, tc.lastPage); got != tc.want {
				t.Errorf("PastLastPage(%d, %v, %d, %d, %v) = %v, want %v", tc.page, tc.perPage, tc.n, tc.total, tc.lastPage, got, tc.want)
			}
		})
	}
}

// TestPageOutOfRange: only the API's 400 for a page past the end, and only
// past page 1, is an empty page.
func TestPageOutOfRange(t *testing.T) {
	outOfRange := &api.APIError{StatusCode: 400, Message: "Page exceeds available pages"}
	for _, tc := range []struct {
		name string
		page int
		err  error
		want bool
	}{
		{"past the end", 8, outOfRange, true},
		{"wrapped", 8, fmt.Errorf("listing: %w", outOfRange), true},
		{"page 1", 1, outOfRange, false},
		{"another 400", 8, &api.APIError{StatusCode: 400, Message: "perPage exceeds maximum of 1000"}, false},
		{"a 500", 8, &api.APIError{StatusCode: 500, Message: "Page exceeds available pages"}, false},
		{"no error", 8, nil, false},
		{"not an API error", 8, errors.New("Page exceeds available pages"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PageOutOfRange(tc.page, tc.err); got != tc.want {
				t.Errorf("PageOutOfRange(%d, %v) = %v, want %v", tc.page, tc.err, got, tc.want)
			}
		})
	}
}

// TestInt32Page covers the narrowing that eight command packages used to carry
// a private copy of, none of them tested. The out-of-range cases are the point:
// they fail silently by design — a dropped `nextPage` rather than an error — so
// nothing else would notice them going wrong.
func TestInt32Page(t *testing.T) {
	tests := []struct {
		name string
		in   *int
		want *int32
	}{
		{"nil stays nil", nil, nil},
		{"ordinary page", intPtr(2), int32Ptr(2)},
		{"zero is passed through, not treated as absent", intPtr(0), int32Ptr(0)},
		{"largest representable page", intPtr(math.MaxInt32), int32Ptr(math.MaxInt32)},
		{"above int32 is dropped rather than wrapped", intPtr(math.MaxInt32 + 1), nil},
		{"below int32 is dropped rather than wrapped", intPtr(math.MinInt32 - 1), nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Int32Page(tc.in)
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("Int32Page(%v) = %d, want nil", deref(tc.in), *got)
			case tc.want != nil && got == nil:
				t.Errorf("Int32Page(%v) = nil, want %d", deref(tc.in), *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Errorf("Int32Page(%v) = %d, want %d", deref(tc.in), *got, *tc.want)
			}
		})
	}
}

// TestInt32Count pins the clamp. A wrapped value here would be printed to the
// user as a fact — "(3 domains)" under a table showing none — which is worse
// than an obviously implausible large number.
func TestInt32Count(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int32
	}{
		{"zero", 0, 0},
		{"ordinary count", 42, 42},
		{"largest representable", math.MaxInt32, math.MaxInt32},
		{"above int32 clamps rather than wrapping", math.MaxInt32 + 1, math.MaxInt32},
		{"negative clamps to zero", -1, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Int32Count(tc.in); got != tc.want {
				t.Errorf("Int32Count(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestNonNil pins #157's list half: a null element in a response list reached
// row builders and --quiet loops as a nil pointer, and each dereferenced it.
func TestNonNil(t *testing.T) {
	a, b := 1, 2
	if got := NonNil([]*int{nil, &a, nil, &b, nil}); len(got) != 2 || got[0] != &a || got[1] != &b {
		t.Errorf("NonNil kept %v, want only the two non-nil elements, in order", got)
	}
	if got := NonNil([]*int{nil}); len(got) != 0 {
		t.Errorf("NonNil([nil]) = %v, want empty", got)
	}
	in := []*int{nil, &a}
	NonNil(in)
	if in[0] != nil || in[1] != &a {
		t.Errorf("NonNil modified its input: %v", in)
	}
	if got := NonNil[int](nil); got != nil {
		t.Errorf("NonNil(nil) = %v, want nil", got)
	}
}

func intPtr(n int) *int       { return &n }
func int32Ptr(n int32) *int32 { return &n }

func deref(p *int) any {
	if p == nil {
		return "nil"
	}
	return *p
}
