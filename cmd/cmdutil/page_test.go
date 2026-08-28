package cmdutil

import (
	"math"
	"testing"
)

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

func intPtr(n int) *int       { return &n }
func int32Ptr(n int32) *int32 { return &n }

func deref(p *int) any {
	if p == nil {
		return "nil"
	}
	return *p
}
