package cmdutil

import (
	"errors"
	"fmt"
	"testing"

	"github.com/patramsey/namecom-cli/internal/api"
)

// IsNotFound is what turns a bare 404 into "domain not found — run 'namecom
// domain list'" across the whole CLI. It matches through wrapping, so the
// wrapped cases matter as much as the direct one: commands routinely add
// context with %w before the error reaches a caller that checks this.
func TestIsNotFound(t *testing.T) {
	notFound := &api.APIError{StatusCode: 404, Message: "Domain not found"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not a 404", nil, false},
		{"a plain error is not a 404", errors.New("boom"), false},
		{"a direct 404", notFound, true},
		{"a 404 wrapped once", fmt.Errorf("fetching domain: %w", notFound), true},
		{"a 404 wrapped twice", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", notFound)), true},
		{"a 404 behind a UsageError", &UsageError{Err: notFound}, true},
		{"403 is not a 404", &api.APIError{StatusCode: 403, Message: "Forbidden"}, false},
		{"500 is not a 404", &api.APIError{StatusCode: 500}, false},
		{"400 is not a 404", &api.APIError{StatusCode: 400}, false},
		{"an unwrapped error mentioning 404 is not a 404", errors.New("got status 404"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotFound(tt.err); got != tt.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// UsageError marks a problem with how the command was invoked, which the root
// command turns into a different exit code than an API failure. That only
// works if it stays both matchable and unwrappable.
func TestUsageError(t *testing.T) {
	inner := errors.New("bad flag combination")
	ue := &UsageError{Err: inner}

	if got := ue.Error(); got != inner.Error() {
		t.Errorf("Error() = %q, want the inner message %q", got, inner.Error())
	}
	if got := ue.Unwrap(); !errors.Is(got, inner) {
		t.Errorf("Unwrap() = %v, want %v", got, inner)
	}
	if !errors.Is(ue, inner) {
		t.Error("errors.Is(UsageError, inner) = false; the chain is broken")
	}

	var target *UsageError
	if !errors.As(fmt.Errorf("context: %w", ue), &target) {
		t.Error("a wrapped UsageError should still be findable with errors.As")
	}
}

// NewUsageError returns nil for nil so callers can apply it straight to a
// function result. Returning a non-nil *UsageError wrapping nil would make
// `if err != nil` true on success — the classic typed-nil trap.
func TestNewUsageError(t *testing.T) {
	if err := NewUsageError(nil); err != nil {
		t.Errorf("NewUsageError(nil) = %v, want nil", err)
	}

	inner := errors.New("boom")
	err := NewUsageError(inner)
	if err == nil {
		t.Fatal("NewUsageError(non-nil) = nil, want an error")
	}
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("NewUsageError returned %T, want a *UsageError", err)
	}
	if !errors.Is(err, inner) {
		t.Error("NewUsageError should preserve the original error in the chain")
	}
}

// TestClassifyCobraUsage guards the message matching that maps cobra's own
// invocation errors onto exit code 2.
//
// Cobra validates required flags and flag groups inside execute(), after
// SetFlagErrorFunc has had its chance, and exposes no hook for that path. So
// `dns create example.com` with two required flags missing exited 1 while
// `dns list example.com --badflag` right beside it exited 2. Matching the
// message is the only seam available; these cases fail if a cobra upgrade
// rewords one, which is the point of asserting them.
func TestClassifyCobraUsage(t *testing.T) {
	usage := func(err error) bool {
		var u *UsageError
		return errors.As(err, &u)
	}

	cobraMessages := []string{
		`required flag(s) "answer", "type" not set`,
		`if any flags in the group [a b] are set they must all be set; missing [b]`,
		`unknown command "regsiter" for "namecom domain"`,
		`unknown flag: --badflag`,
		`unknown shorthand flag: 'z' in -z`,
		`invalid argument "abc" for "--ttl" flag: strconv.ParseInt: parsing "abc": invalid syntax`,
		`flag needs an argument: --type`,
	}
	for _, msg := range cobraMessages {
		if got := ClassifyCobraUsage(errors.New(msg)); !usage(got) {
			t.Errorf("ClassifyCobraUsage(%q) did not classify as a usage error", msg)
		}
	}

	t.Run("leaves other errors alone", func(t *testing.T) {
		runtime := errors.New("connection refused")
		if got := ClassifyCobraUsage(runtime); usage(got) {
			t.Errorf("misclassified a runtime error as usage: %v", got)
		}
	})

	t.Run("nil stays nil", func(t *testing.T) {
		if got := ClassifyCobraUsage(nil); got != nil {
			t.Errorf("ClassifyCobraUsage(nil) = %v, want nil", got)
		}
	})

	t.Run("does not re-wrap an already-classified error", func(t *testing.T) {
		// An AuthError whose text happens to mention an unknown flag must keep
		// its exit code 3 rather than being demoted to a usage error.
		auth := NewAuthError(errors.New("unknown flag: --token was not usable"))
		got := ClassifyCobraUsage(auth)
		var a *AuthError
		if !errors.As(got, &a) {
			t.Errorf("ClassifyCobraUsage demoted an AuthError: %v", got)
		}
	})
}
