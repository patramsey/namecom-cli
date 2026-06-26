package cmdutil

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// ValidDate checks that s is a valid YYYY-MM-DD date.
func ValidDate(s, flagName string) error {
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return fmt.Errorf("--%s: %q is not a valid date — expected YYYY-MM-DD", flagName, s)
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
		return fmt.Errorf("unknown record type %q — must be one of: A, AAAA, ANAME, CAA, CNAME, MX, NS, SRV, TXT", t)
	}
	return nil
}

// ValidDNSHost checks that host is a valid relative DNS label (@ and wildcards allowed).
func ValidDNSHost(host string) error {
	if host == "" {
		return fmt.Errorf("--host cannot be empty (use @ for the zone apex)")
	}
	if host == "@" || host == "*" {
		return nil
	}
	check := strings.TrimPrefix(host, "*.")
	if strings.ContainsAny(check, " \t") {
		return fmt.Errorf("--host %q must not contain spaces", host)
	}
	if len(check) > 253 {
		return fmt.Errorf("--host %q exceeds maximum DNS name length (253 chars)", host)
	}
	for _, label := range strings.Split(check, ".") {
		if label == "" {
			return fmt.Errorf("--host %q has an empty label (double dot or leading/trailing dot)", host)
		}
		if len(label) > 63 {
			return fmt.Errorf("--host %q label %q exceeds 63 characters", host, label)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("--host %q label %q must not start or end with a hyphen", host, label)
		}
	}
	return nil
}

// ValidDNSAnswer validates the answer field for a given record type and host.
func ValidDNSAnswer(recordType, host, answer string) error {
	if answer == "" {
		return fmt.Errorf("--answer is required")
	}
	switch strings.ToUpper(recordType) {
	case "A":
		ip := net.ParseIP(answer)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("A record --answer must be a valid IPv4 address, got %q", answer)
		}
	case "AAAA":
		ip := net.ParseIP(answer)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("AAAA record --answer must be a valid IPv6 address, got %q", answer)
		}
	case "CNAME":
		if host == "@" || host == "" {
			return fmt.Errorf("CNAME record cannot be set at the zone apex (@) — use ANAME for apex aliasing")
		}
	case "MX":
		if strings.ContainsAny(answer, " \t") {
			return fmt.Errorf("MX record --answer must be a hostname (got %q) — set priority with --priority", answer)
		}
	case "SRV":
		parts := strings.Fields(answer)
		if len(parts) != 3 {
			return fmt.Errorf("SRV record --answer must be \"weight port target\" (e.g. \"0 443 target.example.com.\"), got %q", answer)
		}
		if _, err := strconv.Atoi(parts[0]); err != nil {
			return fmt.Errorf("SRV record weight (first field) must be an integer, got %q", parts[0])
		}
		if _, err := strconv.Atoi(parts[1]); err != nil {
			return fmt.Errorf("SRV record port (second field) must be an integer, got %q", parts[1])
		}
	case "CAA":
		parts := strings.Fields(answer)
		if len(parts) < 3 {
			return fmt.Errorf("CAA record --answer must be \"flags tag value\" (e.g. `0 issue \"letsencrypt.org\"`), got %q", answer)
		}
		flags, err := strconv.Atoi(parts[0])
		if err != nil || flags < 0 || flags > 255 {
			return fmt.Errorf("CAA record flags (first field) must be an integer 0-255, got %q", parts[0])
		}
		validTags := map[string]bool{"issue": true, "issuewild": true, "iodef": true}
		if !validTags[parts[1]] {
			return fmt.Errorf("CAA record tag (second field) must be one of: issue, issuewild, iodef — got %q", parts[1])
		}
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
		return fmt.Errorf("--ttl must be at least 300 seconds (got %d)", ttl)
	}
	return nil
}

// ValidDomainName does a basic sanity check on a domain name argument.
func ValidDomainName(domain string) error {
	if strings.Contains(domain, " ") {
		return fmt.Errorf("domain name %q must not contain spaces", domain)
	}
	if !strings.Contains(domain, ".") {
		return fmt.Errorf("domain name %q must contain at least one dot", domain)
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return fmt.Errorf("domain name %q must not start or end with a dot", domain)
	}
	return nil
}

// ValidNameserver checks that ns is a plausible fully-qualified nameserver hostname.
func ValidNameserver(ns string, idx int) error {
	if ns == "" {
		return fmt.Errorf("nameserver %d is empty", idx+1)
	}
	if !strings.Contains(ns, ".") {
		return fmt.Errorf("nameserver %q must be a fully-qualified hostname (e.g. ns1.example.com)", ns)
	}
	if strings.HasPrefix(ns, ".") || strings.HasSuffix(ns, ".") {
		return fmt.Errorf("nameserver %q must not start or end with a dot", ns)
	}
	for _, label := range strings.Split(ns, ".") {
		if label == "" {
			return fmt.Errorf("nameserver %q has an empty label", ns)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("nameserver %q label %q must not start or end with a hyphen", ns, label)
		}
	}
	return nil
}

// ValidYears checks that n is a valid domain registration/renewal period.
func ValidYears(n int) error {
	if n < 1 || n > 10 {
		return fmt.Errorf("--years must be between 1 and 10 (got %d)", n)
	}
	return nil
}

// ValidURL checks that u has an http:// or https:// scheme.
func ValidURL(u, flagName string) error {
	if u == "" {
		return fmt.Errorf("--%s is required", flagName)
	}
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return fmt.Errorf("--%s %q must start with http:// or https://", flagName, u)
	}
	return nil
}

// ValidEmail checks that addr looks like a valid email address.
func ValidEmail(addr, flagName string) error {
	if addr == "" {
		return fmt.Errorf("--%s is required", flagName)
	}
	at := strings.LastIndex(addr, "@")
	if at < 1 || at == len(addr)-1 {
		return fmt.Errorf("--%s %q is not a valid email address — expected user@domain.tld", flagName, addr)
	}
	if !strings.Contains(addr[at+1:], ".") {
		return fmt.Errorf("--%s %q domain part has no dot — expected user@domain.tld", flagName, addr)
	}
	return nil
}

// ValidURLForwardingType checks that t is a supported URL forwarding type.
func ValidURLForwardingType(t, flagName string) error {
	switch t {
	case "redirect", "302", "masked":
		return nil
	}
	return fmt.Errorf("--%s %q is not valid — must be one of: redirect, 302, masked", flagName, t)
}

// ValidEmailLocalPart checks that s is a valid email mailbox local-part.
func ValidEmailLocalPart(s, argName string) error {
	if s == "" {
		return fmt.Errorf("%s must not be empty", argName)
	}
	if strings.Contains(s, "@") {
		return fmt.Errorf("%s %q must not contain '@' — provide only the local part (e.g. 'info', not 'info@example.com')", argName, s)
	}
	if strings.ContainsAny(s, " \t\n") {
		return fmt.Errorf("%s %q must not contain spaces", argName, s)
	}
	return nil
}

// DomainArg validates and normalizes (lowercases) a domain name positional argument.
func DomainArg(args []string, n int) (string, error) {
	d := strings.ToLower(args[n])
	if err := ValidDomainName(d); err != nil {
		return "", err
	}
	return d, nil
}

// ValidAuthCode checks that a transfer auth code is plausibly non-trivial.
func ValidAuthCode(code string) error {
	if code == "" {
		return fmt.Errorf("--auth-code is required")
	}
	if len(code) < 6 {
		return fmt.Errorf("--auth-code is too short (got %d chars, minimum 6); EPP auth codes are typically 8+ characters", len(code))
	}
	return nil
}
