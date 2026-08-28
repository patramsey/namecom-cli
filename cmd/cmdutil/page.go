package cmdutil

import "math"

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
