package url

import (
	"strings"
	"testing"
)

// TestValidateDestination guards #239: the create and update forms accepted
// any non-empty destination, so "example dot com" got through, the typing was
// lost, and the failure named a --to flag the user never typed. The form now
// rejects it as it is typed, without naming a flag.
func TestValidateDestination(t *testing.T) {
	for _, tt := range []struct {
		in, wantErr string
	}{
		{"https://example.com", ""},
		{"HTTP://example.com/path?q=1", ""},
		{"  https://example.com  ", ""},
		{"", "required"},
		{"   ", "required"},
		{"example dot com", "must start with http:// or https://"},
		{"example.com", "must start with http:// or https://"},
		{"https://", "not a valid URL"},
		{"https://example dot com", "not a valid URL"},
	} {
		err := validateDestination(tt.in)
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("validateDestination(%q) = %v, want nil", tt.in, err)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("validateDestination(%q) = %v, want an error containing %q", tt.in, err, tt.wantErr)
		case err != nil && strings.Contains(err.Error(), "--"):
			t.Errorf("validateDestination(%q) names a flag: %v", tt.in, err)
		}
	}
}

// TestForwardingName: the form asked where "example.com/@" should forward.
func TestForwardingName(t *testing.T) {
	for host, want := range map[string]string{
		"@":   "example.com",
		"":    "example.com",
		"www": "www.example.com",
	} {
		if got := forwardingName("example.com", host); got != want {
			t.Errorf("forwardingName(example.com, %q) = %q, want %q", host, got, want)
		}
	}
}
