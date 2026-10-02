package cmdutil

import (
	"errors"
	"math"
	"strings"
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

// TestValidDNSCreateType pins that CAA, which the API rejects on create, is a
// usage error naming the reason, while every other known type is accepted.
func TestValidDNSCreateType(t *testing.T) {
	for _, s := range []string{"A", "AAAA", "ANAME", "CNAME", "MX", "NS", "SRV", "TXT", "a"} {
		if err := ValidDNSCreateType(s); err != nil {
			t.Errorf("ValidDNSCreateType(%q) unexpected error: %v", s, err)
		}
	}
	for _, s := range []string{"CAA", "caa"} {
		err := ValidDNSCreateType(s)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "does not accept CAA") {
			t.Errorf("ValidDNSCreateType(%q) = %v, want a usage error saying CAA is not accepted", s, err)
		}
	}
	err := ValidDNSCreateType("BOGUS")
	if err == nil || strings.Contains(err.Error(), "CAA") {
		t.Errorf("ValidDNSCreateType(\"BOGUS\") = %v, want an error that does not offer CAA", err)
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
		"  ",
		"has space",
		"label..double",
		".leading-dot",
		"trailing-dot.",
		"-starts-with-hyphen",
		"ends-with-hyphen-",
		string(make([]byte, 64)) + ".com", // label > 63 chars
		string(make([]byte, 250)) + ".example.com", // total > 253 chars
		// #187: any byte but space and tab used to pass. `dns export --zone`
		// writes the owner name as-is, so `0"` broke the zone file.
		`0"`, "a;b", "a(b", "a)b", "a\rb", "a\nb", "a@b", "a/b", "a*b", "www.*",
	}
	for _, s := range bad {
		if err := ValidDNSHost(s); err == nil {
			t.Errorf("ValidDNSHost(%q) expected error, got nil", s)
		}
	}
}

func TestPositiveID(t *testing.T) {
	for s, want := range map[string]int32{"1": 1, "9911": 9911, "2147483647": math.MaxInt32, "007": 7} {
		if got, ok := PositiveID(s); !ok || got != want {
			t.Errorf("PositiveID(%q) = %d, %v, want %d, true", s, got, ok, want)
		}
	}
	for _, s := range []string{"", "0", "-5", "+5", " 5", "5 ", "abc", "2147483648", "1e3", "0x10"} {
		if got, ok := PositiveID(s); ok {
			t.Errorf("PositiveID(%q) = %d, true, want rejected", s, got)
		}
	}
}

func TestValidPriority(t *testing.T) {
	for _, p := range []int64{0, 10, 65535} {
		if err := ValidPriority(p); err != nil {
			t.Errorf("ValidPriority(%d) = %v, want nil", p, err)
		}
	}
	// #187: -8 was sent, and the exported zone then failed to load.
	for _, p := range []int64{-8, -1, 65536, math.MaxInt64} {
		var ue *UsageError
		if err := ValidPriority(p); !errors.As(err, &ue) {
			t.Errorf("ValidPriority(%d) = %v, want a usage error", p, err)
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
		// #187: hostname targets with empty labels or stray bytes, SRV fields
		// split on CR, and out-of-range SRV numbers all used to pass.
		{"CNAME", "www", ".00", true},
		{"CNAME", "www", "a..example.com", true},
		{"CNAME", "www", "a\"b.example.com", true},
		{"ANAME", "@", "a..example.com", true},
		{"NS", "sub", "ns1..example.com", true},
		{"NS", "sub", "ns1.example.com.", false},
		{"MX", "@", "mail\rexample.com", true},
		{"MX", "@", "mail\n.example.com", true},
		{"MX", "@", "mail..example.com", true},
		{"MX", "@", ".", false}, // null MX (RFC 7505)
		{"SRV", "@", "0\r0 0 target.example.com.", true},
		{"SRV", "@", "10 443 a..example.com", true},
		{"SRV", "@", "-1 443 target.example.com.", true},
		{"SRV", "@", "+1 443 target.example.com.", true},
		{"SRV", "@", "1 65536 target.example.com.", true},
		{"SRV", "@", "65535 65535 target.example.com.", false},
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
		{"A", "::1", 0, false, ""}, // IPv6 addr: isPrivateIP returns false for non-IPv4
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
	ok := []string{"example.com", "sub.example.co.uk", "a.b", strings.Repeat("a", 63) + ".com"}
	for _, s := range ok {
		if err := ValidDomainName(s); err != nil {
			t.Errorf("ValidDomainName(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{"", "nodot", "has space.com", ".leading.com", "trailing.com.", "no dot",
		// #187: an empty label reached the API, and `transfer eligibility
		// bad..com` answered for bad.com. So did labels over 63 bytes.
		"bad..com",
		strings.Repeat("a", 64) + ".com",
		strings.Repeat("a.", 127) + "com", // 257 bytes in all
	}
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
		".leading.example.com",  // leading dot
		"trailing.example.com.", // trailing dot
		// Whitespace (#191): ValidDomainName refused these, ValidNameserver
		// let them through to the API.
		"ns1.example .com",
		"ns1.example\t.com",
		" ns1.example.com",
		"ns1 .example.com",
		// Characters no hostname has; the domain-argument rules refuse them too.
		"*.www",
		"::ffff:1.2.3.4",
		"user@example.com",
		"ns1.exa?mple.com",
		// DNS length limits (#187), the same ones domain arguments now get.
		strings.Repeat("a", 64) + ".example.com",
		strings.Repeat("a.", 127) + "com",
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

// TestValidPrice guards issue #168. --price parses with strconv, so Inf, NaN
// and negative values all reach the command. The callers only checked
// price > 0: NaN and negatives were silently dropped, and Inf was quoted in
// the prompt as "$+Inf" and then failed to marshal.
func TestValidPrice(t *testing.T) {
	for _, p := range []float64{0.01, 12.99, 25000} {
		if err := ValidPrice(p); err != nil {
			t.Errorf("ValidPrice(%v) unexpected error: %v", p, err)
		}
	}
	for _, p := range []float64{0, -1, -0.01, math.Inf(1), math.Inf(-1), math.NaN()} {
		err := ValidPrice(p)
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Errorf("ValidPrice(%v) = %v, want a usage error", p, err)
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
		{"Bücher.COM", "xn--bcher-kva.com", false},
		{"under_score.com", "under_score.com", false},
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

// TestCanonicalDomain pins the normalization every command's domain argument
// goes through: trim, lowercase, and encode Unicode labels as punycode.
//
// The encoding is what issue #160 was about. The API normalizes UTF-8 in a
// request body, but a domain argument usually lands in the URL path, and there
// name.com's edge answers a percent-encoded Unicode name with an HTML 403.
func TestCanonicalDomain(t *testing.T) {
	tests := []struct{ in, want string }{
		{"example.com", "example.com"},
		{"EXAMPLE.COM", "example.com"},
		{"  example.com  ", "example.com"},
		// Unicode is lowercased and encoded to its ASCII form.
		{"CAFÉ.COM", "xn--caf-dma.com"},
		{"bücher.com", "xn--bcher-kva.com"},
		// Already-punycode input is untouched, never double-encoded.
		{"xn--caf-dma.com", "xn--caf-dma.com"},
		{"XN--CAF-DMA.COM", "xn--caf-dma.com"},
		// Not valid IDNA: left as typed (lowercased) for ValidDomainName to
		// reject, rather than encoded into something the user never asked for.
		{"bücher-.com", "bücher-.com"},
	}
	for _, tc := range tests {
		if got := CanonicalDomain(tc.in); got != tc.want {
			t.Errorf("CanonicalDomain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDomainArg_RejectsBadNames pins the other half of #160: a name that cannot
// be a domain is a usage error (exit 2) before anything is sent. A '?', '#' or
// '/' changes what the request path means — `transfer eligibility 'a?x=1.com'`
// reached the server — and invalid IDNA has no ASCII form to send at all.
func TestDomainArg_RejectsBadNames(t *testing.T) {
	for _, in := range []string{
		"a?x=1.com",
		"a#b.com",
		"a/b.com",
		"../x.com",
		"a%2fb.com",
		"bücher-.com",  // hyphen at the end of a label
		"a\u200db.com", // zero-width joiner outside its permitted context
		"\u05d0a.com",  // bidi rule: RTL label containing an LTR letter
	} {
		_, err := DomainArg([]string{in}, 0)
		if err == nil {
			t.Errorf("DomainArg(%q) = nil error, want a usage error", in)
			continue
		}
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("DomainArg(%q) error %v is %T, want *UsageError (exit 2)", in, err, err)
		}
	}
}

// TestCanonicalDomain_Idempotent guards against double-encoding: running the
// result through again must be a no-op, since commands may normalize a name
// that has already been normalized.
func TestCanonicalDomain_Idempotent(t *testing.T) {
	for _, in := range []string{"example.com", "café.com", "xn--caf-dma.com", "ÄPFEL.de"} {
		once := CanonicalDomain(in)
		if twice := CanonicalDomain(once); twice != once {
			t.Errorf("CanonicalDomain not idempotent for %q: %q -> %q", in, once, twice)
		}
	}
}

// TestValidationMessagesAreActionable guards the substance of every validation
// error, not merely that one occurred.
//
// The sibling tables in this file assert `wantErr bool` only. That is a weak
// contract for a validation package, where the message IS the product — a
// review demonstrated it by replacing every fmt.Errorf in validate.go with
// fmt.Errorf("invalid") and watching all 27 tests pass. A user told only
// "invalid" learns nothing; the whole reason these checks run client-side
// instead of letting the API 422 is that they can say what to do instead.
//
// Expected substrings are quoted from the real messages, not invented.
func TestValidationMessagesAreActionable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want []string // every substring must be present
	}{
		{
			name: "CNAME at apex names the alternative",
			err:  ValidDNSAnswer("CNAME", "@", "target.example.com."),
			want: []string{"apex", "ANAME"},
		},
		{
			name: "A record with IPv6 says which family is wanted",
			err:  ValidDNSAnswer("A", "@", "::1"),
			want: []string{"IPv4"},
		},
		{
			name: "AAAA record with IPv4 says which family is wanted",
			err:  ValidDNSAnswer("AAAA", "@", "1.2.3.4"),
			want: []string{"IPv6"},
		},
		{
			name: "SRV format error shows the expected shape",
			err:  ValidDNSAnswer("SRV", "@", "onlyone"),
			want: []string{"SRV", "port", "target"},
		},
		{
			name: "CAA tag error lists the permitted tags",
			err:  ValidDNSAnswer("CAA", "@", "0 badtag letsencrypt.org"),
			want: []string{"issue", "issuewild", "iodef"},
		},
		{
			name: "unknown record type lists the supported types",
			err:  ValidDNSType("BOGUS"),
			want: []string{"BOGUS", "A", "CNAME", "TXT"},
		},
		{
			name: "TTL error states the minimum and what was given",
			err:  ValidTTL(60),
			want: []string{"300", "60"},
		},
		{
			name: "host with spaces quotes the offending value",
			err:  ValidDNSHost("has space"),
			want: []string{"has space", "space"},
		},
		{
			name: "domain without a dot explains what is missing",
			err:  ValidDomainName("nodot"),
			want: []string{"nodot", "dot"},
		},
		{
			name: "nameserver error shows an example",
			err:  ValidNameserver("ns1nodot", 0),
			want: []string{"fully-qualified", "ns1."},
		},
		{
			name: "years error states the permitted range",
			err:  ValidYears(11),
			want: []string{"1", "10", "11"},
		},
		{
			name: "URL without a scheme says which schemes are wanted",
			err:  ValidURL("example.com", "to"),
			want: []string{"--to", "http"},
		},
		{
			name: "auth code error states the minimum length",
			err:  ValidAuthCode("abc"),
			want: []string{"too short", "6"},
		},
		{
			name: "forwarding type error lists valid types",
			err:  ValidURLForwardingType("permanent", "type"),
			want: []string{"redirect", "302", "masked"},
		},
		{
			name: "date error shows the expected format",
			err:  ValidDate("01/02/2026", "since"),
			want: []string{"--since", "YYYY-MM-DD"},
		},
		{
			name: "sort-dir error lists the valid values",
			err:  ValidSortDir("descending"),
			want: []string{"asc", "desc"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("expected an error for this input")
			}
			msg := tc.err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("message should mention %q so the user knows what to do; got: %s", want, msg)
				}
			}
		})
	}
}
