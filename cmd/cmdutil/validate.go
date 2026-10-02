package cmdutil

import (
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// usagef builds a UsageError. Every failure in this file is an invocation
// mistake — a malformed flag value, an out-of-range count, an argument that
// cannot be what it claims — so each one maps to exit code 2 in the documented
// table. They used to return bare fmt.Errorf values, which collapsed to exit 1
// and made `--type ZZZ` indistinguishable from a 500 to a calling script.
func usagef(format string, a ...any) error {
	return NewUsageError(fmt.Errorf(format, a...))
}

// ValidDate checks that s is a valid YYYY-MM-DD date.
func ValidDate(s, flagName string) error {
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return usagef("--%s: %q is not a valid date — expected YYYY-MM-DD", flagName, s)
	}
	return nil
}

var validDNSTypes = map[string]bool{
	"A": true, "AAAA": true, "ANAME": true, "CAA": true,
	"CNAME": true, "MX": true, "NS": true, "SRV": true, "TXT": true,
}

// ValidDNSType checks that t is a supported DNS record type.
func ValidDNSType(t string) error {
	if !validDNSTypes[strings.ToUpper(t)] {
		return usagef("unknown record type %q — must be one of: A, AAAA, ANAME, CAA, CNAME, MX, NS, SRV, TXT", t)
	}
	return nil
}

// ValidDNSCreateType is ValidDNSType for a record about to be created. The API
// rejects CAA on create — it is not in the server's list of allowed types — so
// refuse it here with a clear message rather than send a request that can only
// fail. CAA stays in validDNSTypes for the rest of the CLI.
func ValidDNSCreateType(t string) error {
	if strings.EqualFold(t, "CAA") {
		return usagef("name.com's API does not accept CAA records — --type must be one of: A, AAAA, ANAME, CNAME, MX, NS, SRV, TXT")
	}
	if !validDNSTypes[strings.ToUpper(t)] {
		return usagef("unknown record type %q — must be one of: A, AAAA, ANAME, CNAME, MX, NS, SRV, TXT", t)
	}
	return nil
}

// ValidDNSHost checks that host is a valid relative DNS label (@ and wildcards allowed).
//
// An empty or whitespace-only host is refused rather than taken as the apex.
// The API treats "" and "@" as different hosts; for a URL forwarding, "" also
// replaces every apex A record.
func ValidDNSHost(host string) error {
	if strings.TrimSpace(host) == "" {
		return usagef("--host cannot be empty (use @ for the zone apex)")
	}
	if host == "@" || host == "*" {
		return nil
	}
	check := strings.TrimPrefix(host, "*.")
	if strings.ContainsAny(check, " \t") {
		return usagef("--host %q must not contain spaces", host)
	}
	// Only hostname characters (#187). `dns export --zone` writes the host as
	// the owner name unescaped, so a `"`, `;` or `(` the API stored broke the
	// zone file. A wildcard is only meaningful as the whole leftmost label,
	// which is trimmed above. Non-ASCII is left to the server.
	for i := 0; i < len(check); i++ {
		if c := check[i]; c < utf8.RuneSelf && !isHostnameByte(c) {
			return usagef("--host %q must not contain %q", host, check[i:i+1])
		}
	}
	if len(check) > 253 {
		return usagef("--host %q exceeds maximum DNS name length (253 chars)", host)
	}
	for label := range strings.SplitSeq(check, ".") {
		if label == "" {
			return usagef("--host %q has an empty label (double dot or leading/trailing dot)", host)
		}
		if len(label) > 63 {
			return usagef("--host %q label %q exceeds 63 characters", host, label)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return usagef("--host %q label %q must not start or end with a hyphen", host, label)
		}
	}
	return nil
}

// ValidDNSAnswer validates the answer field for a given record type and host.
func ValidDNSAnswer(recordType, host, answer string) error {
	if answer == "" {
		return usagef("--answer is required")
	}
	switch strings.ToUpper(recordType) {
	case "A":
		ip := net.ParseIP(answer)
		if ip == nil || ip.To4() == nil {
			return usagef("--answer must be a valid IPv4 address for A records, got %q", answer)
		}
	case "AAAA":
		ip := net.ParseIP(answer)
		if ip == nil || ip.To4() != nil {
			return usagef("--answer must be a valid IPv6 address for AAAA records, got %q", answer)
		}
	case "CNAME":
		if host == "@" || host == "" {
			return usagef("CNAME record cannot be set at the zone apex (@) — use ANAME for apex aliasing")
		}
		return validTarget("CNAME", answer)
	case "ANAME", "NS":
		return validTarget(strings.ToUpper(recordType), answer)
	case "MX":
		if strings.IndexFunc(answer, unicode.IsSpace) >= 0 {
			return usagef("MX record --answer must be a hostname (got %q) — set priority with --priority", answer)
		}
		if answer == "." {
			return nil // a null MX (RFC 7505): the domain accepts no mail
		}
		return validTarget("MX", answer)
	case "SRV":
		// strings.Fields also splits on CR, LF and tab, so "0\r0 0 x" passed
		// as three fields and broke the exported zone (#187). Only spaces may
		// separate them.
		if strings.IndexFunc(answer, func(r rune) bool { return r != ' ' && unicode.IsSpace(r) }) >= 0 {
			return usagef("SRV record --answer fields must be separated by spaces, got %q", answer)
		}
		parts := strings.Fields(answer)
		if len(parts) != 3 {
			return usagef("SRV record --answer must be \"weight port target\" (e.g. \"0 443 target.example.com.\"), got %q", answer)
		}
		if !isUint16(parts[0]) {
			return usagef("SRV record weight (first field) must be an integer 0-65535, got %q", parts[0])
		}
		if !isUint16(parts[1]) {
			return usagef("SRV record port (second field) must be an integer 0-65535, got %q", parts[1])
		}
		return validTarget("SRV", parts[2])
	case "CAA":
		parts := strings.Fields(answer)
		if len(parts) < 3 {
			return usagef("CAA record --answer must be \"flags tag value\" (e.g. `0 issue \"letsencrypt.org\"`), got %q", answer)
		}
		flags, err := strconv.Atoi(parts[0])
		if err != nil || flags < 0 || flags > 255 {
			return usagef("CAA record flags (first field) must be an integer 0-255, got %q", parts[0])
		}
		validTags := map[string]bool{"issue": true, "issuewild": true, "iodef": true}
		if !validTags[parts[1]] {
			return usagef("CAA record tag (second field) must be one of: issue, issuewild, iodef — got %q", parts[1])
		}
	}
	return nil
}

// validTarget checks the hostname a CNAME, ANAME, MX, NS or SRV record points
// at. A target was only checked for spaces, so `.00` or a name with a `"` in it
// was sent, and `dns export --zone` then wrote a line that does not load. One
// trailing dot (an absolute name) is allowed; non-ASCII is left to the server.
func validTarget(rtype, name string) error {
	check := strings.TrimSuffix(name, ".")
	if check == "" {
		return usagef("%s record --answer must be a hostname, got %q", rtype, name)
	}
	for i := 0; i < len(check); i++ {
		if c := check[i]; c < utf8.RuneSelf && !isHostnameByte(c) {
			return usagef("%s record target %q must not contain %q", rtype, name, check[i:i+1])
		}
	}
	if len(check) > 253 {
		return usagef("%s record target %q exceeds maximum DNS name length (253 chars)", rtype, name)
	}
	for label := range strings.SplitSeq(check, ".") {
		if label == "" {
			return usagef("%s record target %q has an empty label (double dot or leading dot)", rtype, name)
		}
		if len(label) > 63 {
			return usagef("%s record target %q label %q exceeds 63 characters", rtype, name, label)
		}
	}
	return nil
}

// isUint16 reports whether s is a plain decimal 0-65535: no sign, no spaces.
func isUint16(s string) bool {
	_, err := strconv.ParseUint(s, 10, 16)
	return err == nil
}

// ValidPriority checks an MX or SRV priority. Both are 16-bit fields, but the
// flag is an int64 and any value was sent; a negative one was stored, and the
// exported zone then failed to load (#187).
func ValidPriority(p int64) error {
	if p < 0 || p > 65535 {
		return usagef("--priority must be between 0 and 65535 (got %d)", p)
	}
	return nil
}

// DNSAnswerWarnings returns soft-warning messages for valid-but-suspicious record values.
// Callers should print each returned string with out.Warn().
func DNSAnswerWarnings(recordType, answer string, priority int64, priorityChanged bool) []string {
	var warnings []string
	switch strings.ToUpper(recordType) {
	case "A":
		if ip := net.ParseIP(answer); ip != nil && isPrivateIP(ip) {
			warnings = append(warnings, fmt.Sprintf(
				"A record answer %q is a private/RFC1918 address — public DNS with private IPs is usually unintentional", answer))
		}
	case "CNAME":
		if !strings.HasSuffix(answer, ".") {
			warnings = append(warnings, fmt.Sprintf(
				"CNAME answer %q has no trailing dot — it resolves relative to the zone (becomes %q + zone); add a trailing dot for an absolute hostname", answer, answer))
		}
	case "MX":
		if !priorityChanged && priority == 0 {
			warnings = append(warnings, "MX record priority is 0 (highest preference) because --priority was not set; use --priority 10 (or higher) unless this is intentional")
		}
	}
	return warnings
}

func isPrivateIP(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	switch {
	case ip4[0] == 10:
		return true
	case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
		return true
	case ip4[0] == 192 && ip4[1] == 168:
		return true
	}
	return false
}

// ValidTTL checks that ttl meets the API minimum.
func ValidTTL(ttl int64) error {
	if ttl < 300 {
		return usagef("--ttl must be at least 300 seconds (got %d)", ttl)
	}
	return nil
}

// ValidDomainName does a basic sanity check on a domain name argument.
//
// Beyond the shape checks, every ASCII character must be one a hostname can
// hold. A domain argument is interpolated into a URL path, so a '?', '#' or '/'
// would change what that path means rather than name a domain — `transfer
// eligibility 'a?x=1.com'` reached the server. Non-ASCII input must be valid
// IDNA, since CanonicalDomain can only send it as punycode.
func ValidDomainName(domain string) error {
	if strings.Contains(domain, " ") {
		return usagef("domain name %q must not contain spaces", domain)
	}
	if !strings.Contains(domain, ".") {
		return usagef("domain name %q must contain at least one dot", domain)
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return usagef("domain name %q must not start or end with a dot", domain)
	}
	for i := 0; i < len(domain); i++ {
		if c := domain[i]; c < utf8.RuneSelf && !isHostnameByte(c) {
			return usagef("domain name %q must not contain %q", domain, domain[i:i+1])
		}
	}
	ascii := domain
	if !isASCII(domain) {
		a, err := idna.Lookup.ToASCII(domain)
		if err != nil {
			return usagef("domain name %q is not a valid internationalized domain name: %v", domain, err)
		}
		ascii = a
	}
	// Lengths are DNS limits, so they apply to the form that is sent. An empty
	// label used to reach the API, which read `bad..com` as bad.com (#187).
	if len(ascii) > 253 {
		return usagef("domain name %q exceeds the maximum DNS name length (253 characters)", domain)
	}
	for label := range strings.SplitSeq(ascii, ".") {
		if label == "" {
			return usagef("domain name %q has an empty label (double dot)", domain)
		}
		if len(label) > 63 {
			return usagef("domain name %q label %q exceeds 63 characters", domain, label)
		}
	}
	return nil
}

// isHostnameByte reports whether c may appear in a domain name: letters,
// digits, hyphen, dot, and underscore (which DNS allows outside hostnames and
// some registries accept).
func isHostnameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '-' || c == '.' || c == '_'
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// ValidNameserver checks that ns is a plausible fully-qualified nameserver hostname.
func ValidNameserver(ns string, idx int) error {
	if ns == "" {
		return usagef("nameserver %d is empty", idx+1)
	}
	if strings.IndexFunc(ns, unicode.IsSpace) >= 0 {
		return usagef("nameserver %q must not contain spaces", ns)
	}
	// A nameserver is a hostname, so it is held to the same characters as a
	// domain argument: no "*", "@", ":" (an IPv6 address is not a nameserver
	// name), and a non-ASCII name must be valid IDNA.
	for i := 0; i < len(ns); i++ {
		if c := ns[i]; c < utf8.RuneSelf && !isHostnameByte(c) {
			return usagef("nameserver %q must not contain %q", ns, ns[i:i+1])
		}
	}
	ascii := ns
	if !isASCII(ns) {
		a, err := idna.Lookup.ToASCII(ns)
		if err != nil {
			return usagef("nameserver %q is not a valid internationalized hostname: %v", ns, err)
		}
		ascii = a
	}
	if !strings.Contains(ns, ".") {
		return usagef("nameserver %q must be a fully-qualified hostname (e.g. ns1.example.com)", ns)
	}
	if strings.HasPrefix(ns, ".") || strings.HasSuffix(ns, ".") {
		return usagef("nameserver %q must not start or end with a dot", ns)
	}
	// The label rules apply to the form that is sent: IDNA mapping can empty
	// a label (a lone soft hyphen) or change its length.
	if len(ascii) > 253 {
		return usagef("nameserver %q exceeds the maximum DNS name length (253 characters)", ns)
	}
	for label := range strings.SplitSeq(ascii, ".") {
		if label == "" {
			return usagef("nameserver %q has an empty label", ns)
		}
		if len(label) > 63 {
			return usagef("nameserver %q label %q exceeds 63 characters", ns, label)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return usagef("nameserver %q label %q must not start or end with a hyphen", ns, label)
		}
	}
	return nil
}

// ValidSortDir checks a --sort-dir value. Unlike `sort`, the spec DOES
// enumerate this one — "Possible values are 'asc' (default) or 'desc'" — so a
// typo is worth catching before the round trip.
//
// There is deliberately no equivalent for `sort`: the spec declares it as a
// bare string with no enum, and an invented allowlist here blocked fields the
// server accepts while claiming they were invalid.
func ValidSortDir(dir string) error {
	switch dir {
	case "", "asc", "desc":
		return nil
	}
	return usagef("invalid --sort-dir %q: valid values are asc, desc", dir)
}

// ValidYears checks that n is a valid domain registration/renewal period.
func ValidYears(n int) error {
	if n < 1 || n > 10 {
		return usagef("--years must be between 1 and 10 (got %d)", n)
	}
	return nil
}

// ValidPrice checks a --price value. The flag parses with strconv, which
// accepts Inf and NaN: NaN and negatives used to be dropped silently, and Inf
// was quoted in the prompt and then failed to marshal on send (#168).
func ValidPrice(p float64) error {
	if math.IsNaN(p) || math.IsInf(p, 0) || p <= 0 {
		return usagef("--price must be a positive amount in USD (got %v)", p)
	}
	return nil
}

// ValidURL checks that u has an http:// or https:// scheme.
func ValidURL(u, flagName string) error {
	if u == "" {
		return usagef("--%s is required", flagName)
	}
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return usagef("--%s %q must start with http:// or https://", flagName, u)
	}
	return nil
}

// ValidEmail checks that addr looks like a valid email address.
func ValidEmail(addr, flagName string) error {
	if addr == "" {
		return usagef("--%s is required", flagName)
	}
	at := strings.LastIndex(addr, "@")
	if at < 1 || at == len(addr)-1 {
		return usagef("--%s %q is not a valid email address — expected user@domain.tld", flagName, addr)
	}
	if !strings.Contains(addr[at+1:], ".") {
		return usagef("--%s %q domain part has no dot — expected user@domain.tld", flagName, addr)
	}
	return nil
}

// ValidURLForwardingType checks that t is a supported URL forwarding type.
func ValidURLForwardingType(t, flagName string) error {
	switch t {
	case "redirect", "302", "masked":
		return nil
	}
	return usagef("--%s %q is not valid — must be one of: redirect, 302, masked", flagName, t)
}

// ValidEmailLocalPart checks that s is a valid email mailbox local-part.
func ValidEmailLocalPart(s, argName string) error {
	if s == "" {
		return usagef("%s must not be empty", argName)
	}
	if strings.Contains(s, "@") {
		return usagef("%s %q must not contain '@' — provide only the local part (e.g. 'info', not 'info@example.com')", argName, s)
	}
	if strings.ContainsAny(s, " \t\n") {
		return usagef("%s %q must not contain spaces", argName, s)
	}
	return nil
}

// PositiveID parses a resource ID argument: a plain decimal number from 1 to
// the int32 maximum, the range every ID this API issues falls in. ParseInt
// alone let `0`, `-5` and `+5` through to the request (#187). Callers wrap
// ok == false in a usage error that names the kind of ID.
func PositiveID(s string) (int32, bool) {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil || n < 1 {
		return 0, false
	}
	return int32(n), true
}

// DomainArg validates and normalizes (lowercases) a domain name positional argument.
func DomainArg(args []string, n int) (string, error) {
	d := CanonicalDomain(args[n])
	if err := ValidDomainName(d); err != nil {
		return "", err
	}
	return d, nil
}

// OnOffArg parses the on|off positional argument of the domain toggles
// (lock, autorenew, privacy), case-insensitively.
func OnOffArg(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, usagef("expected 'on' or 'off', got %q", s)
}

// CanonicalDomain normalizes a domain name for comparison and transmission.
//
// It trims, lowercases, and converts Unicode labels to punycode (IDNA lookup
// rules), so bücher.com becomes xn--bcher-kva.com. The API does normalize UTF-8
// in a request body, but most commands put the domain in the URL path, and
// there name.com's edge answers a percent-encoded Unicode name with an HTML 403
// instead of reaching the API. Converting here gives every command — and its
// --dry-run preview — the same ASCII form, which is also the form the API
// replies with.
//
// A name that is not valid IDNA is returned lowercased but otherwise as typed;
// ValidDomainName rejects it with the reason.
func CanonicalDomain(s string) string {
	d := strings.ToLower(strings.TrimSpace(s))
	if isASCII(d) {
		return d
	}
	if a, err := idna.Lookup.ToASCII(d); err == nil {
		return a
	}
	return d
}

// ValidAuthCode checks that a transfer auth code is plausibly non-trivial.
func ValidAuthCode(code string) error {
	if code == "" {
		return usagef("--auth-code is required")
	}
	if len(code) < 6 {
		return usagef("--auth-code is too short (got %d chars, minimum 6); EPP auth codes are typically 8+ characters", len(code))
	}
	return nil
}
