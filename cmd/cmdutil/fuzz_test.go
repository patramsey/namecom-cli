package cmdutil

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// isUsage reports whether err is the UsageError every validator promises.
func isUsage(err error) bool {
	var ue *UsageError
	return errors.As(err, &ue)
}

func checkUsage(t *testing.T, name string, err error) {
	t.Helper()
	if err != nil && !isUsage(err) {
		t.Fatalf("%s returned %T, want *UsageError: %v", name, err, err)
	}
}

// FuzzValidators runs every string validator on arbitrary input: none may
// panic, every failure must be a UsageError (exit code 2), and acceptance must
// imply the structural promise each one makes.
func FuzzValidators(f *testing.F) {
	for _, s := range []string{"", "@", "*", "*.www", "www", "a..b", "-a", "a-",
		"example.com", ".example.com", "example.com.", "ns1.example.com",
		strings.Repeat("a", 64), strings.Repeat("a.", 127) + "a", "1.2.3.4", "::1",
		"::ffff:1.2.3.4", "0 443 x.example.com", "0 issue \"letsencrypt.org\"",
		"2024-02-29", "2023-02-29", "on", "OFF", "user@example.com", "http://x"} {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, s, host string) {
		if err := ValidDNSHost(s); err != nil {
			checkUsage(t, "ValidDNSHost", err)
		} else if s != "@" && s != "*" {
			check := strings.TrimPrefix(s, "*.")
			if len(check) > 253 {
				t.Fatalf("ValidDNSHost accepted %d-byte host", len(check))
			}
			for _, l := range strings.Split(check, ".") {
				if l == "" || len(l) > 63 || strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
					t.Fatalf("ValidDNSHost(%q) accepted bad label %q", s, l)
				}
			}
		}

		for _, typ := range []string{"A", "AAAA", "ANAME", "CAA", "CNAME", "MX", "NS", "SRV", "TXT", "a", "srv", "bogus"} {
			err := ValidDNSAnswer(typ, host, s)
			checkUsage(t, "ValidDNSAnswer", err)
			if err != nil {
				continue
			}
			switch strings.ToUpper(typ) {
			case "A":
				if ip := net.ParseIP(s); ip == nil || ip.To4() == nil {
					t.Fatalf("A accepted %q", s)
				}
			case "AAAA":
				if ip := net.ParseIP(s); ip == nil || ip.To4() != nil {
					t.Fatalf("AAAA accepted %q", s)
				}
			case "SRV":
				if len(strings.Fields(s)) != 3 {
					t.Fatalf("SRV accepted %q", s)
				}
			}
			_ = DNSAnswerWarnings(typ, s, 0, false)
		}

		if err := ValidDomainName(s); err != nil {
			checkUsage(t, "ValidDomainName", err)
		} else if !strings.Contains(s, ".") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
			t.Fatalf("ValidDomainName accepted %q", s)
		}
		// DomainArg is ValidDomainName after CanonicalDomain; canonicalizing
		// twice must change nothing.
		if d, err := DomainArg([]string{s}, 0); err == nil {
			if CanonicalDomain(d) != d {
				t.Fatalf("CanonicalDomain not idempotent on %q", s)
			}
		} else {
			checkUsage(t, "DomainArg", err)
		}

		if err := ValidNameserver(s, 0); err != nil {
			checkUsage(t, "ValidNameserver", err)
		} else if ValidDomainName(s) != nil {
			// A nameserver is a hostname; anything accepted as one is a name.
			t.Fatalf("ValidNameserver accepted %q, which ValidDomainName refuses", s)
		}

		if err := ValidDate(s, "x"); err != nil {
			checkUsage(t, "ValidDate", err)
		} else if d, _ := time.Parse("2006-01-02", s); d.Format("2006-01-02") != s {
			t.Fatalf("ValidDate accepted non-canonical %q", s)
		}

		if on, err := OnOffArg(s); err != nil {
			checkUsage(t, "OnOffArg", err)
		} else if want := strings.EqualFold(s, "on"); on != want || (!strings.EqualFold(s, "on") && !strings.EqualFold(s, "off")) {
			t.Fatalf("OnOffArg(%q) = %v", s, on)
		}

		checkUsage(t, "ValidDNSType", ValidDNSType(s))
		checkUsage(t, "ValidDNSCreateType", ValidDNSCreateType(s))
		checkUsage(t, "ValidSortDir", ValidSortDir(s))
		checkUsage(t, "ValidURL", ValidURL(s, "x"))
		checkUsage(t, "ValidEmail", ValidEmail(s, "x"))
		checkUsage(t, "ValidURLForwardingType", ValidURLForwardingType(s, "x"))
		checkUsage(t, "ValidEmailLocalPart", ValidEmailLocalPart(s, "x"))
		checkUsage(t, "ValidAuthCode", ValidAuthCode(s))
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			checkUsage(t, "ValidTTL", ValidTTL(n))
			checkUsage(t, "ValidYears", ValidYears(int(n)))
		}
	})
}

// FuzzNextPage checks NextPage never reports a page at or before the current
// one, so a pagination loop built on it always terminates.
func FuzzNextPage(f *testing.F) {
	f.Add(int32(1), int32(2), int32(5), true, true)
	f.Add(int32(5), int32(5), int32(5), true, true)
	f.Add(int32(3), int32(0), int32(0), false, false)
	f.Add(int32(2147483647), int32(-2147483648), int32(0), true, false)
	f.Fuzz(func(t *testing.T, cur, next, last int32, hasNext, hasLast bool) {
		var np, lp *int32
		if hasNext {
			np = &next
		}
		if hasLast {
			lp = &last
		}
		got, ok := NextPage(cur, np, lp)
		if ok && got <= cur {
			t.Fatalf("NextPage(%d, %v, %v) = %d, not after current", cur, np, lp, got)
		}
	})
}
