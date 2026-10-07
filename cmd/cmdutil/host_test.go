package cmdutil

import (
	"errors"
	"strings"
	"testing"
)

// TestZoneHost pins how a --host is read: relative to the zone, fully
// qualified with or without the trailing dot, and the domain itself as the
// apex. A trailing-dot name outside the zone is absolute, so it is refused
// rather than created under the zone.
func TestZoneHost(t *testing.T) {
	const d = "example.com"
	for in, want := range map[string]string{
		"www":                   "www",
		"@":                     "@",
		"sweep.example.com":     "sweep",
		"sweep.example.com.":    "sweep",
		"Sweep.Example.COM":     "Sweep",
		"a.b.example.com":       "a.b",
		"example.com":           "@",
		"example.com.":          "@",
		"*.example.com":         "*",
		"sweep.other.com":       "sweep.other.com",
		"example.com.other.org": "example.com.other.org",
		"notexample.com":        "notexample.com",
	} {
		got, err := ZoneHost(in, d)
		if err != nil || got != want {
			t.Errorf("ZoneHost(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, err := ZoneHost("www.bücher.de", "xn--bcher-kva.de"); err != nil || got != "www" {
		t.Errorf("IDN: ZoneHost = %q, %v; want www", got, err)
	}

	for in, want := range map[string]string{
		"x.other.com.":   "is not in example.com",
		"www.":           "is not in example.com",
		".example.com":   "empty label",
		".":              "empty label",
		"a..example.com": "empty label",
		"":               "cannot be empty",
	} {
		_, err := ZoneHost(in, d)
		if _, ok := errors.AsType[*UsageError](err); !ok {
			t.Errorf("ZoneHost(%q) = %v, want a usage error", in, err)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ZoneHost(%q) = %q, want it to say %q", in, err, want)
		}
	}
}
