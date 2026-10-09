package cmdutil

import (
	"fmt"
	"strings"

	"github.com/patramsey/namecom-cli/internal/output"
)

// ZoneHost is a --host value in the form the API takes, validated and with
// Unicode labels in punycode: relative to the zone, and "@" for the apex.
// "www", "www.example.com" and "www.example.com." are all www, and
// "example.com" is the apex, as `dns create` reads them (#285). `url create`
// sent a fully qualified host as typed, and the API made a forwarding for
// www.example.com.example.com.
//
// A trailing dot makes a name absolute, so one outside the zone, such as
// "www.other.org.", is a usage error rather than a host under the zone. Its
// dot used to be dropped and the name created inside the zone.
func ZoneHost(h, domain string) (string, error) {
	rel, err := relZoneHost(h, domain)
	if err != nil {
		return "", err
	}
	if err := ValidDNSHost(rel); err != nil {
		return "", err
	}
	a, err := ASCIIHostname(rel, "--host")
	if err != nil {
		return "", err
	}
	if a != rel {
		if err := ValidDNSHost(a); err != nil {
			return "", err
		}
	}
	return a, nil
}

// relZoneHost strips the zone from a fully qualified host, with or without
// its trailing dot, and makes the domain itself "@". A relative host is
// returned without a trailing dot, and is left to ZoneHost to validate.
func relZoneHost(h, domain string) (string, error) {
	t := strings.TrimSuffix(h, ".")
	if t == "" {
		return h, nil
	}
	a, err := ASCIIHostname(t, "--host")
	if err != nil {
		return t, nil
	}
	switch la := strings.ToLower(a); {
	case la == domain:
		return "@", nil
	case strings.HasSuffix(la, "."+domain):
		rel := a[:len(a)-len(domain)-1]
		if rel == "" {
			// ".example.com": the leading dot, not an empty --host.
			return "", usagef("--host %q has an empty label (double dot or leading/trailing dot)", h)
		}
		return rel, nil
	case t != h:
		return "", OutOfZone("--host", h, domain)
	}
	return t, nil
}

// OutOfZone is the usage error for h, an absolute name — ending in "." —
// that is not domain or under it. what names where h came from: "--host",
// or "host" for a record read from a file, which has no flag. `dns` and
// `url` used to word it two ways (#323).
func OutOfZone(what, h, domain string) error {
	return NewUsageErrorHint(fmt.Errorf("%s %q is not in %s", what, h, domain),
		fmt.Sprintf("a trailing dot makes a name absolute; without it, %q is a host under %s", strings.TrimSuffix(h, "."), domain))
}

// SideEffectNote tells the user about DNS records name.com adds or leaves
// for a forwarding: a dim note in a table, and a warning in every other
// format, so that JSON and YAML carry it in {"warnings": […]} on stderr. As
// a plain Note it printed only in a table, and a script never learned of the
// apex A record a deleted forwarding leaves. --quiet prints nothing.
func SideEffectNote(out *output.Config, msg string) {
	if out.QuietMode {
		return
	}
	if out.Format == output.FormatTable {
		out.Note(msg)
		return
	}
	out.Warn(msg)
}
