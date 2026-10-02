package vanity

import (
	"strings"
	"testing"
)

// FuzzVanityHostname checks that every spelling vanityHostname accepts names
// a host under the domain, and that feeding its result back in is a fixed
// point — get/update/delete and create must agree on what a hostname means.
func FuzzVanityHostname(f *testing.F) {
	for _, s := range []string{"ns1", "NS1", "ns1.example.com", "ns1.example.com.",
		" ns1 ", "example.com", ".example.com", "ns1.other.com", "a.b.example.com",
		"ns1.example.com..", "xexample.com", "ns1..example.com"} {
		f.Add(s, "example.com")
	}
	f.Fuzz(func(t *testing.T, hostname, domain string) {
		// The domain reaches here through DomainArg: lowercased, trimmed,
		// containing a dot and not starting or ending with one.
		domain = strings.ToLower(strings.TrimSpace(domain))
		if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, " ") {
			return
		}
		fqdn, err := vanityHostname(hostname, domain)
		if err != nil {
			return
		}
		if !strings.HasSuffix(fqdn, "."+domain) {
			t.Fatalf("vanityHostname(%q, %q) = %q, not under the domain", hostname, domain, fqdn)
		}
		label := strings.TrimSuffix(fqdn, "."+domain)
		if label == "" || strings.Contains(fqdn, "..") {
			t.Fatalf("vanityHostname(%q, %q) = %q has an empty label", hostname, domain, fqdn)
		}
		again, err := vanityHostname(fqdn, domain)
		if err != nil || again != fqdn {
			t.Fatalf("vanityHostname not a fixed point: %q -> %q -> %q (%v)", hostname, fqdn, again, err)
		}
		l2, err := vanityLabel("--hostname", hostname, domain)
		if err != nil || l2+"."+domain != fqdn {
			t.Fatalf("vanityLabel(%q) = %q, disagrees with vanityHostname %q (%v)", hostname, l2, fqdn, err)
		}
	})
}

// FuzzSplitIPs checks --ips parsing: never nil, no blank or padded entries.
func FuzzSplitIPs(f *testing.F) {
	for _, s := range []string{"", ",", "1.2.3.4", " 1.2.3.4 , ::1 ,", ",,,"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ips := splitIPs(s)
		if ips == nil {
			t.Fatal("splitIPs returned nil")
		}
		for _, ip := range ips {
			if ip == "" || ip != strings.TrimSpace(ip) || strings.Contains(ip, ",") {
				t.Fatalf("splitIPs(%q) produced entry %q", s, ip)
			}
		}
	})
}
