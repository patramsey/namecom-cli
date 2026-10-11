package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Redaction by value.
//
// RedactBody and redactHeaders find secrets by where they are: a field or a
// header with a secret's name. That misses a secret the server puts back
// somewhere else — a proxy or test server that echoes the request headers
// into its reply, or an API error that quotes the auth code it refused — and
// those replies became the error message, the JSON envelope's details and the
// --debug log (#341). So the values themselves are also registered, as this
// invocation learns them (the credential when the client is built, an auth
// code when a command reads one), and replaced wherever an error or the debug
// log is written. Command output on stdout is not scrubbed: `domain
// auth-code` exists to print one.

// minSecretLen is the shortest value AddSecret registers. Replacing a short
// string everywhere it occurs would mangle unrelated text, and nothing it
// protects is that short: ValidAuthCode requires 6 characters, and an API
// token is 40.
const minSecretLen = 6

var (
	secretsMu sync.RWMutex
	secrets   []string // every registered form, longest first
)

// AddSecret registers s, a value that must not appear in an error or the
// debug log, together with the forms it takes on the way there: escaped in a
// JSON string and in a URL query. Values shorter than minSecretLen are
// ignored.
func AddSecret(s string) {
	if len(s) < minSecretLen {
		return
	}
	forms := []string{s, url.QueryEscape(s), url.PathEscape(s)}
	for _, html := range []bool{false, true} {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(html)
		if enc.Encode(s) == nil {
			forms = append(forms, strings.TrimSuffix(strings.TrimSuffix(buf.String(), "\n"), `"`)[1:])
		}
	}
	secretsMu.Lock()
	defer secretsMu.Unlock()
	for _, f := range forms {
		if len(f) >= minSecretLen && !containsString(secrets, f) {
			secrets = append(secrets, f)
		}
	}
	// Longest first, so a form that contains another is replaced whole.
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// resetSecrets forgets every registered value. For tests.
func resetSecrets() {
	secretsMu.Lock()
	secrets = nil
	secretsMu.Unlock()
}

// ScrubSecrets returns s with every registered secret replaced by Redacted.
func ScrubSecrets(s string) string {
	secretsMu.RLock()
	defer secretsMu.RUnlock()
	for _, v := range secrets {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, Redacted)
		}
	}
	return s
}

// scrubBytes is ScrubSecrets for a byte slice. It returns b itself when there
// is nothing to replace.
func scrubBytes(b []byte) []byte {
	secretsMu.RLock()
	defer secretsMu.RUnlock()
	for _, v := range secrets {
		if bytes.Contains(b, []byte(v)) {
			b = bytes.ReplaceAll(b, []byte(v), []byte(Redacted))
		}
	}
	return b
}

// scrubWriter scrubs registered secrets from everything written through it.
// The debug log writes each line in one call, so a secret is never split
// across two writes.
type scrubWriter struct{ w io.Writer }

func (s scrubWriter) Write(p []byte) (int, error) {
	if _, err := s.w.Write(scrubBytes(p)); err != nil {
		return 0, err
	}
	// Report p's length: the caller wrote all of it, and a shorter count
	// from a replacement would read as a short write.
	return len(p), nil
}

// detailer matches output's interface for errors that carry the JSON
// envelope's details.
type detailer interface{ ErrorDetails() any }

// redactedError is an error whose message and details had registered secrets
// replaced. It unwraps to the original, so its exit code, type, hint,
// suggestions and idempotency key are unchanged.
type redactedError struct {
	msg     string
	details any
	err     error
}

func (e *redactedError) Error() string     { return e.msg }
func (e *redactedError) Unwrap() error     { return e.err }
func (e *redactedError) ErrorDetails() any { return e.details }

// RedactError returns err with every registered secret replaced in its
// message and in the details the error envelope prints, or err itself when
// neither holds one.
func RedactError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	scrubbed := ScrubSecrets(msg)
	var details any
	detailsChanged := false
	if d := detailer(nil); errors.As(err, &d) {
		details = d.ErrorDetails()
		if details != nil {
			if raw, merr := json.Marshal(details); merr == nil {
				if clean := scrubBytes(raw); !bytes.Equal(clean, raw) {
					details, detailsChanged = json.RawMessage(clean), true
				}
			}
		}
	}
	if scrubbed == msg && !detailsChanged {
		return err
	}
	return &redactedError{msg: scrubbed, details: details, err: err}
}

// redactURL is u as the debug log's request line shows it: userinfo replaced
// by Redacted, and the value of every query parameter whose name looks like a
// secret (see isSecretParam) too. The query keeps its order and encoding
// otherwise, so the line still shows what was sent.
func redactURL(u *url.URL) string {
	c := *u
	c.User = nil
	if c.RawQuery != "" {
		parts := strings.Split(c.RawQuery, "&")
		for i, p := range parts {
			k, _, hasValue := strings.Cut(p, "=")
			name, err := url.QueryUnescape(k)
			if err != nil {
				name = k
			}
			if hasValue && isSecretParam(name) {
				parts[i] = k + "=" + Redacted
			}
		}
		c.RawQuery = strings.Join(parts, "&")
	}
	s := c.String()
	if u.User != nil {
		if scheme, rest, ok := strings.Cut(s, "://"); ok {
			s = scheme + "://" + Redacted + "@" + rest
		}
	}
	return s
}

// isSecretParam reports whether a query parameter's value must not be logged:
// a name that ends in one of secretFields, as for headers, so token,
// access_token and api_key are all caught.
func isSecretParam(name string) bool {
	k := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(name))
	if k == "key" || strings.HasSuffix(k, "apikey") {
		return true
	}
	for field := range secretFields {
		if strings.HasSuffix(k, field) {
			return true
		}
	}
	return false
}
