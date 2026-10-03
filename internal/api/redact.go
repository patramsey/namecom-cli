package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Redacted is what a secret is shown as wherever a body is displayed rather
// than sent: the --debug log here, and the --dry-run previews that use it.
const Redacted = "[redacted]"

// secretFields are the JSON keys whose values are never logged, compared after
// lower-casing and dropping '_' and '-' so auth_code and authCode both match.
//
// authCode is the one that matters most: it is the secret that authorises
// moving a domain away, and it travels in transfer create and internal-in
// request bodies and in the auth-code endpoint's response. The rest are the
// other credential-shaped fields in the Core SDK's types (an account's
// password, the apiToken returned on account creation), plus the generic
// names, so a field this list has not met yet is more likely to be caught.
var secretFields = map[string]bool{
	"authcode":     true,
	"eppcode":      true,
	"password":     true,
	"apitoken":     true,
	"token":        true,
	"secret":       true,
	"clientsecret": true,
}

func isSecretField(key string) bool {
	k := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	return secretFields[k]
}

// credentialHeaders are the headers whose values are never logged, in
// canonical form. Authorization carries the Basic-encoded token, which is the
// token itself to anyone holding a base64 decoder.
var credentialHeaders = map[string]bool{
	"Authorization":       true,
	"Proxy-Authorization": true,
	"Cookie":              true,
	"Set-Cookie":          true,
}

// isCredentialHeader reports whether a header's value must be redacted: one of
// credentialHeaders, or a name that ends in one of the secret field names, so
// `namecom api --header 'X-Api-Token: …'` is caught too.
func isCredentialHeader(name string) bool {
	if credentialHeaders[http.CanonicalHeaderKey(name)] {
		return true
	}
	k := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
	for field := range secretFields {
		if strings.HasSuffix(k, field) {
			return true
		}
	}
	return false
}

// redactHeaders returns h's headers as "Name: value" lines, sorted by name,
// with the value of every credential header replaced by Redacted. keep, when
// non-nil, limits the result to the headers it accepts.
func redactHeaders(h http.Header, keep func(name string) bool) []string {
	names := make([]string, 0, len(h))
	for name := range h {
		if keep == nil || keep(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		for _, v := range h[name] {
			if isCredentialHeader(name) {
				v = Redacted
			}
			lines = append(lines, name+": "+v)
		}
	}
	return lines
}

// RedactBody returns body with the value of every secret field, at any depth,
// replaced by Redacted. A body with nothing to redact comes back unchanged,
// byte for byte, so the log still shows what went over the wire.
//
// A body that is not valid JSON — including one cut short — still gets the
// string-valued secret fields replaced textually, so a malformed or partial
// body cannot carry a secret past this.
func RedactBody(body []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err == nil && dec.Decode(new(any)) == io.EOF {
		if !redactValue(v) {
			return body
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err == nil {
			return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
		}
	}
	return secretStringRE.ReplaceAll(body, []byte(`${1}"`+Redacted+`"`))
}

// redactValue replaces secret fields in v in place, reporting whether it
// replaced any.
func redactValue(v any) bool {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if isSecretField(k) && child != nil {
				t[k] = Redacted
				changed = true
				continue
			}
			changed = redactValue(child) || changed
		}
	case []any:
		for _, child := range t {
			changed = redactValue(child) || changed
		}
	}
	return changed
}

// secretStringRE is the fallback for bodies that do not parse: a secret key
// followed by a string value, whose closing quote may be missing if the body
// was truncated mid-value.
var secretStringRE = regexp.MustCompile(
	`(?i)("(?:auth[_-]?code|epp[_-]?code|password|api[_-]?token|token|secret|client[_-]?secret)"\s*:\s*)"(?:[^"\\]|\\.)*"?`)
