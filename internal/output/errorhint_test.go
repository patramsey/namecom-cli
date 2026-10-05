package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// TestErrorHint_NilUnwrap pins issue #156. errorHint walked the Unwrap() chain
// and assigned whatever came back, and both *net.DNSError and
// *json.UnmarshalTypeError have an Unwrap method that returns nil. The next
// err.Error() then panicked, so an offline machine, and every response the SDK
// could not decode, crashed the CLI with exit 2.
func TestErrorHint_NilUnwrap(t *testing.T) {
	dnsErr := fmt.Errorf("getting domain: %w", &url.Error{
		Op:  "Get",
		URL: "http://nonexistent.invalid/core/v1/domains/x.com",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{
			Err: "no such host", Name: "nonexistent.invalid", IsNotFound: true,
		}},
	})
	decodeErr := fmt.Errorf("getting requirements: %w", &json.UnmarshalTypeError{
		Value: "array", Type: reflect.TypeFor[map[string]any](), Field: "requirements.fields",
	})

	t.Run("dns failure gets the network hint", func(t *testing.T) {
		if got := errorHint(dnsErr); !strings.Contains(got, "could not reach the API") {
			t.Errorf("errorHint = %q, want the could-not-reach-the-API hint", got)
		}
	})

	// A DNS failure that is not "no such host" — a resolver timing out, say —
	// says nothing the message match recognises, but it is still the network.
	t.Run("other dns failure gets the network hint", func(t *testing.T) {
		err := fmt.Errorf("getting domain: %w", &net.DNSError{Err: "server misbehaving", Name: "api.name.com"})
		if got := errorHint(err); !strings.Contains(got, "could not reach the API") {
			t.Errorf("errorHint = %q, want the could-not-reach-the-API hint", got)
		}
	})

	t.Run("decode failure has no hint", func(t *testing.T) {
		if got := errorHint(decodeErr); got != "" {
			t.Errorf("errorHint = %q, want none", got)
		}
	})

	// Through Error, which is where the panic surfaced, in every format.
	for _, f := range []Format{FormatTable, FormatJSON, FormatYAML} {
		t.Run("Error renders "+string(f), func(t *testing.T) {
			var ew bytes.Buffer
			c := &Config{Format: f, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &ew}
			c.Error(dnsErr)
			c.Error(decodeErr)
			if !strings.Contains(ew.String(), "no such host") || !strings.Contains(ew.String(), "requirements.fields") {
				t.Errorf("both errors should be rendered, got:\n%s", ew.String())
			}
		})
	}
}

type hinted struct {
	msg, hint string
	err       error
}

func (e *hinted) Error() string    { return e.msg }
func (e *hinted) Unwrap() error    { return e.err }
func (e *hinted) UserHint() string { return e.hint }

// TestErrorHint_OutermostDecides pins #234: a wrapper whose message already
// says what to do returned an empty hint, and errorHint walked past it to the
// generic hint underneath, so the advice was printed twice.
func TestErrorHint_OutermostDecides(t *testing.T) {
	inner := &hinted{msg: "Not Found", hint: "generic advice"}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"empty outer hint suppresses the inner one", &hinted{msg: "order 1 not found — run 'namecom order list'", err: inner}, ""},
		{"through plain wrapping too", fmt.Errorf("ctx: %w", &hinted{msg: "x", err: inner}), ""},
		{"outer hint replaces the inner one", &hinted{msg: "x", hint: "specific", err: inner}, "specific"},
		{"bare inner hint still found", fmt.Errorf("ctx: %w", inner), "generic advice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorHint(tt.err); got != tt.want {
				t.Errorf("errorHint = %q, want %q", got, tt.want)
			}
			var ew bytes.Buffer
			c := &Config{Format: FormatTable, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &ew}
			c.Error(tt.err)
			want := 0
			if tt.want != "" {
				want = 1
			}
			if n := strings.Count(ew.String(), "→ "); n != want {
				t.Errorf("rendered %d hint lines, want %d:\n%s", n, want, ew.String())
			}
		})
	}
}
