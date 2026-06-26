package cmdutil

import (
	"testing"
)

func TestValidDate(t *testing.T) {
	ok := []string{"2024-01-01", "2099-12-31", "2000-02-29"}
	for _, s := range ok {
		if err := ValidDate(s, "since"); err != nil {
			t.Errorf("ValidDate(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{"", "01-01-2024", "2024/01/01", "2024-13-01", "not-a-date", "2024-1-1"}
	for _, s := range bad {
		if err := ValidDate(s, "since"); err == nil {
			t.Errorf("ValidDate(%q) expected error, got nil", s)
		}
	}
}

func TestValidDNSType(t *testing.T) {
	ok := []string{"A", "AAAA", "ANAME", "CAA", "CNAME", "MX", "NS", "SRV", "TXT", "a", "mx"}
	for _, s := range ok {
		if err := ValidDNSType(s); err != nil {
			t.Errorf("ValidDNSType(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{"", "PTR", "DNSKEY", "SOA", "BOGUS"}
	for _, s := range bad {
		if err := ValidDNSType(s); err == nil {
			t.Errorf("ValidDNSType(%q) expected error, got nil", s)
		}
	}
}

func TestValidDNSHost(t *testing.T) {
	ok := []string{"@", "*", "www", "*.sub", "sub.domain", "a-b", "mail"}
	for _, s := range ok {
		if err := ValidDNSHost(s); err != nil {
			t.Errorf("ValidDNSHost(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{
		"",
		"has space",
		"label..double",
		".leading-dot",
		"trailing-dot.",
		"-starts-with-hyphen",
		"ends-with-hyphen-",
	}
	for _, s := range bad {
		if err := ValidDNSHost(s); err == nil {
			t.Errorf("ValidDNSHost(%q) expected error, got nil", s)
		}
	}
}

func TestValidDNSAnswer(t *testing.T) {
	tests := []struct {
		rtype, host, answer string
		wantErr             bool
	}{
		{"A", "@", "1.2.3.4", false},
		{"A", "@", "256.0.0.1", true},
		{"A", "@", "not-an-ip", true},
		{"A", "@", "::1", true}, // IPv6 for A record
		{"AAAA", "@", "::1", false},
		{"AAAA", "@", "1.2.3.4", true}, // IPv4 for AAAA record
		{"AAAA", "@", "not-an-ip", true},
		{"CNAME", "www", "target.example.com.", false},
		{"CNAME", "@", "target.example.com.", true}, // apex CNAME
		{"CNAME", "", "target.example.com.", true},  // empty host treated as apex
		{"MX", "@", "mail.example.com", false},
		{"MX", "@", "mail.example.com badextra", true}, // space in MX answer
		{"SRV", "@", "10 443 target.example.com.", false},
		{"SRV", "@", "onlyone", true},
		{"SRV", "@", "notint 443 target.com.", true},
		{"SRV", "@", "10 notint target.com.", true},
		{"CAA", "@", "0 issue letsencrypt.org", false},
		{"CAA", "@", "0 issuewild letsencrypt.org", false},
		{"CAA", "@", "0 iodef mailto:admin@example.com", false},
		{"CAA", "@", "256 issue letsencrypt.org", true}, // flags > 255
		{"CAA", "@", "notint issue letsencrypt.org", true},
		{"CAA", "@", "0 badtag letsencrypt.org", true},
		{"CAA", "@", "0 issue", true}, // only 2 parts
		{"TXT", "@", "v=spf1 include:_spf.example.com ~all", false},
		{"A", "@", "", true}, // empty answer
	}
	for _, tt := range tests {
		err := ValidDNSAnswer(tt.rtype, tt.host, tt.answer)
		if tt.wantErr && err == nil {
			t.Errorf("ValidDNSAnswer(%q, %q, %q) expected error, got nil", tt.rtype, tt.host, tt.answer)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("ValidDNSAnswer(%q, %q, %q) unexpected error: %v", tt.rtype, tt.host, tt.answer, err)
		}
	}
}

func TestDNSAnswerWarnings(t *testing.T) {
	tests := []struct {
		rtype, answer   string
		priority        int64
		priChanged      bool
		wantWarnContain string
	}{
		{"A", "10.0.0.1", 0, false, "private"},
		{"A", "172.16.0.1", 0, false, "private"},
		{"A", "192.168.1.1", 0, false, "private"},
		{"A", "8.8.8.8", 0, false, ""},
		{"CNAME", "target.example.com", 0, false, "trailing dot"},
		{"CNAME", "target.example.com.", 0, false, ""},
		{"MX", "mail.example.com", 0, false, "priority"},
		{"MX", "mail.example.com", 0, true, ""},   // priority explicitly set
		{"MX", "mail.example.com", 10, false, ""}, // non-zero priority
		{"TXT", "v=spf1 ~all", 0, false, ""},
	}
	for _, tt := range tests {
		warns := DNSAnswerWarnings(tt.rtype, tt.answer, tt.priority, tt.priChanged)
		if tt.wantWarnContain == "" {
			if len(warns) != 0 {
				t.Errorf("DNSAnswerWarnings(%q, %q) expected no warnings, got %v", tt.rtype, tt.answer, warns)
			}
		} else {
			found := false
			for _, w := range warns {
				if contains(w, tt.wantWarnContain) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("DNSAnswerWarnings(%q, %q) expected warning containing %q, got %v", tt.rtype, tt.answer, tt.wantWarnContain, warns)
			}
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func TestValidTTL(t *testing.T) {
	ok := []int64{300, 3600, 86400}
	for _, n := range ok {
		if err := ValidTTL(n); err != nil {
			t.Errorf("ValidTTL(%d) unexpected error: %v", n, err)
		}
	}
	bad := []int64{0, 1, 60, 299}
	for _, n := range bad {
		if err := ValidTTL(n); err == nil {
			t.Errorf("ValidTTL(%d) expected error, got nil", n)
		}
	}
}

func TestValidDomainName(t *testing.T) {
	ok := []string{"example.com", "sub.example.co.uk", "a.b"}
	for _, s := range ok {
		if err := ValidDomainName(s); err != nil {
			t.Errorf("ValidDomainName(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{"", "nodot", "has space.com", ".leading.com", "trailing.com.", "no dot"}
	for _, s := range bad {
		if err := ValidDomainName(s); err == nil {
			t.Errorf("ValidDomainName(%q) expected error, got nil", s)
		}
	}
}

func TestValidNameserver(t *testing.T) {
	ok := []string{"ns1.example.com", "a.b.c.d"}
	for _, s := range ok {
		if err := ValidNameserver(s, 0); err != nil {
			t.Errorf("ValidNameserver(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{
		"",
		"nodot",
		"-start.example.com",
		"end-.example.com",
		"has..double.dot",
	}
	for _, s := range bad {
		if err := ValidNameserver(s, 0); err == nil {
			t.Errorf("ValidNameserver(%q) expected error, got nil", s)
		}
	}
}

func TestValidYears(t *testing.T) {
	for _, n := range []int{1, 5, 10} {
		if err := ValidYears(n); err != nil {
			t.Errorf("ValidYears(%d) unexpected error: %v", n, err)
		}
	}
	for _, n := range []int{0, -1, 11, 100} {
		if err := ValidYears(n); err == nil {
			t.Errorf("ValidYears(%d) expected error, got nil", n)
		}
	}
}

func TestValidURL(t *testing.T) {
	ok := []string{"http://example.com", "https://example.com", "HTTPS://example.com"}
	for _, s := range ok {
		if err := ValidURL(s, "to"); err != nil {
			t.Errorf("ValidURL(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{"", "example.com", "ftp://example.com", "//example.com"}
	for _, s := range bad {
		if err := ValidURL(s, "to"); err == nil {
			t.Errorf("ValidURL(%q) expected error, got nil", s)
		}
	}
}

func TestValidEmail(t *testing.T) {
	ok := []string{"user@example.com", "a@b.c", "user+tag@sub.domain.com"}
	for _, s := range ok {
		if err := ValidEmail(s, "to"); err != nil {
			t.Errorf("ValidEmail(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{
		"",
		"nodomain",
		"@example.com",
		"user@",
		"user@nodot",
	}
	for _, s := range bad {
		if err := ValidEmail(s, "to"); err == nil {
			t.Errorf("ValidEmail(%q) expected error, got nil", s)
		}
	}
}

func TestValidURLForwardingType(t *testing.T) {
	for _, s := range []string{"redirect", "302", "masked"} {
		if err := ValidURLForwardingType(s, "type"); err != nil {
			t.Errorf("ValidURLForwardingType(%q) unexpected error: %v", s, err)
		}
	}
	for _, s := range []string{"", "301", "permanent", "iframe", "REDIRECT"} {
		if err := ValidURLForwardingType(s, "type"); err == nil {
			t.Errorf("ValidURLForwardingType(%q) expected error, got nil", s)
		}
	}
}

func TestValidEmailLocalPart(t *testing.T) {
	ok := []string{"info", "support", "hello-world", "user123"}
	for _, s := range ok {
		if err := ValidEmailLocalPart(s, "mailbox"); err != nil {
			t.Errorf("ValidEmailLocalPart(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{"", "info@example.com", "has space", "tab\there"}
	for _, s := range bad {
		if err := ValidEmailLocalPart(s, "mailbox"); err == nil {
			t.Errorf("ValidEmailLocalPart(%q) expected error, got nil", s)
		}
	}
}

func TestValidAuthCode(t *testing.T) {
	ok := []string{"abc123", "12345678", "SuperSecret!"}
	for _, s := range ok {
		if err := ValidAuthCode(s); err != nil {
			t.Errorf("ValidAuthCode(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{"", "a", "abc", "12345"}
	for _, s := range bad {
		if err := ValidAuthCode(s); err == nil {
			t.Errorf("ValidAuthCode(%q) expected error, got nil", s)
		}
	}
}

func TestDomainArg_Normalization(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"example.com", "example.com", false},
		{"EXAMPLE.COM", "example.com", false},
		{"My-Site.Co.UK", "my-site.co.uk", false},
		{"nodot", "", true},
		{"has space.com", "", true},
		{".leading.com", "", true},
	}
	for _, tt := range tests {
		got, err := DomainArg([]string{tt.input}, 0)
		if tt.wantErr {
			if err == nil {
				t.Errorf("DomainArg(%q) expected error, got %q", tt.input, got)
			}
		} else {
			if err != nil {
				t.Errorf("DomainArg(%q) unexpected error: %v", tt.input, err)
			} else if got != tt.want {
				t.Errorf("DomainArg(%q) = %q, want %q", tt.input, got, tt.want)
			}
		}
	}
}
