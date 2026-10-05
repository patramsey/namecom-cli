package dns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	coreapigo "github.com/namedotcom/core-api-go"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// inputRecord is one record read from a `dns import` or `dns sync` file, in
// the shape the API takes: Host relative to the zone ("@" for the apex), and
// Answer as the API stores it — a fully qualified target without its trailing
// dot, the MX or SRV priority carried separately in Priority, and TXT as one
// unquoted string.
type inputRecord struct {
	// Source locates the record in the file for error messages: "record 3"
	// for JSON, "line 12" for a zone file.
	Source   string
	Type     string
	Host     string
	Answer   string
	TTL      int64
	Priority *int64
}

// Input formats, as readRecordsFile reports them.
const (
	formatJSON = "json"
	formatZone = "zone"
)

// readRecordsFile reads the records in path ("-" for stdin) for domain. The
// file is either the JSON that `dns export` writes or a BIND zone file, such
// as `dns export --zone` writes. A .json extension means JSON; otherwise the
// content decides — JSON starts with "[" or "{", and a zone file cannot. Any
// problem with the file is a usage error: the input is wrong, not the API.
func readRecordsFile(path, domain string) ([]inputRecord, string, error) {
	data, err := readImportData(path)
	if err != nil {
		return nil, "", fmt.Errorf("reading import file: %w", err)
	}
	data, err = decodeImportData(data)
	if err != nil {
		return nil, "", cmdutil.NewUsageError(fmt.Errorf("decoding import file: %w", err))
	}
	trimmed := bytes.TrimSpace(data)
	isJSON := strings.EqualFold(filepath.Ext(path), ".json") ||
		len(trimmed) == 0 || trimmed[0] == '[' || trimmed[0] == '{' || string(trimmed) == "null"
	if isJSON {
		recs, err := parseRecordsJSON(trimmed)
		if err != nil {
			return nil, "", cmdutil.NewUsageError(fmt.Errorf("parsing import file: %w", err))
		}
		return recs, formatJSON, nil
	}
	recs, err := parseZone(string(data), domain)
	if err != nil {
		return nil, "", cmdutil.NewUsageError(fmt.Errorf("parsing zone file: %w", err))
	}
	return recs, formatZone, nil
}

// parseRecordsJSON reads the `dns export` JSON: an array of records. An
// object whose "data" or "records" key holds that array is accepted too, so
// a list printed through the JSON envelope can be fed back in. An empty file
// or `null` is no records.
func parseRecordsJSON(data []byte) ([]inputRecord, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var records []*coreapigo.Record
	if data[0] == '{' {
		var env map[string]json.RawMessage
		if err := json.Unmarshal(data, &env); err != nil {
			return nil, err
		}
		inner, ok := env["data"]
		if !ok {
			inner, ok = env["records"]
		}
		if !ok {
			return nil, errors.New("expected a JSON array of records, as 'dns export' writes")
		}
		data = inner
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	out := make([]inputRecord, 0, len(records))
	for i, r := range records {
		if r == nil {
			return nil, fmt.Errorf("record %d is null", i+1)
		}
		// The API returns the apex host as "", which is what `dns export`
		// writes; it is sent as "@", the spelling `dns create --host`
		// defaults to. A record with no ttl decodes as 0, which the server
		// rejects; it gets the default `dns create --ttl` has.
		host := derefStr(r.Host)
		if host == "" {
			host = "@"
		}
		ttl := r.TTL
		if ttl == 0 {
			ttl = defaultTTL
		}
		out = append(out, inputRecord{
			Source:   fmt.Sprintf("record %d", i+1),
			Type:     strings.ToUpper(derefStr(r.Type)),
			Host:     host,
			Answer:   derefStr(r.Answer),
			TTL:      ttl,
			Priority: r.Priority,
		})
	}
	return out, nil
}

// anameCommentPrefix starts the comment `dns export --zone` writes in place
// of an ANAME record, which is name.com's own type and not one a zone parser
// accepts. parseZone reads the record back out of that comment, so a zone
// exported and synced again does not lose its ANAME records — which, with
// --prune, would delete them.
const anameCommentPrefix = "; ANAME not representable in a zone file: "

// zoneToken is one field of a zone-file entry. Quoted records whether it was
// written in double quotes, which matters only for TXT and CAA rdata.
type zoneToken struct {
	text   string
	quoted bool
}

// zoneEntry is one logical zone-file line: its tokens, the physical line it
// starts on, and whether it began with whitespace (no owner name, so the
// previous owner applies).
type zoneEntry struct {
	line       int
	blankOwner bool
	tokens     []zoneToken
}

// parseZone reads an RFC 1035 master file for domain. It supports what `dns
// export --zone` writes and the forms common in hand-written zones: $ORIGIN
// and $TTL, "@", relative and absolute names, a blank owner repeating the
// previous one, an optional TTL (with BIND units such as 1h) and IN class in
// either order, quoted TXT character-strings, comments, and parentheses
// spanning lines. SOA records are skipped, since name.com manages the SOA.
// $INCLUDE, $GENERATE, classes other than IN, and types name.com does not
// host are errors rather than being skipped, so nothing in the file is
// silently ignored.
func parseZone(text, domain string) ([]inputRecord, error) {
	entries, err := zoneEntries(text)
	if err != nil {
		return nil, err
	}
	zone := strings.ToLower(strings.TrimSuffix(domain, ".")) + "."
	origin := zone
	var (
		defTTL    int64 = -1 // $TTL
		lastTTL   int64 = -1 // the last explicit TTL, which RFC 1035 says repeats
		lastOwner string
		records   []inputRecord
	)
	for _, e := range entries {
		t := e.tokens
		where := func(format string, a ...any) error {
			return fmt.Errorf("line %d: "+format, append([]any{e.line}, a...)...)
		}
		if !e.blankOwner && strings.HasPrefix(t[0].text, "$") && !t[0].quoted {
			switch strings.ToUpper(t[0].text) {
			case "$ORIGIN":
				if len(t) != 2 {
					return nil, where("$ORIGIN takes one name")
				}
				o := absName(t[1].text, origin)
				if o != zone && !strings.HasSuffix(o, "."+zone) {
					return nil, where("$ORIGIN %s is outside the zone %s", o, zone)
				}
				origin = o
			case "$TTL":
				if len(t) != 2 {
					return nil, where("$TTL takes one value")
				}
				ttl, ok := parseTTL(t[1].text)
				if !ok {
					return nil, where("invalid $TTL %q", t[1].text)
				}
				defTTL = ttl
			default:
				return nil, where("%s is not supported", t[0].text)
			}
			continue
		}

		var owner string
		if e.blankOwner {
			if lastOwner == "" {
				return nil, where("the first record must name its owner")
			}
			owner = lastOwner
		} else {
			if t[0].quoted || t[0].text == "" || strings.HasPrefix(t[0].text, ".") {
				return nil, where("invalid owner name %q", t[0].text)
			}
			owner = absName(t[0].text, origin)
			t = t[1:]
		}
		lastOwner = owner

		// An optional TTL and an optional class, in either order.
		ttl := int64(-1)
		class := false
	fields:
		for len(t) > 0 && !t[0].quoted {
			if n, ok := parseTTL(t[0].text); ok && ttl < 0 {
				ttl = n
				t = t[1:]
				continue
			}
			switch strings.ToUpper(t[0].text) {
			case "IN":
				if class {
					break fields
				}
				class = true
				t = t[1:]
				continue
			case "CH", "HS", "CS":
				return nil, where("class %s is not supported; only IN", strings.ToUpper(t[0].text))
			}
			break
		}
		if len(t) == 0 || t[0].quoted {
			return nil, where("missing record type")
		}
		rtype := strings.ToUpper(t[0].text)
		rdata := t[1:]
		switch {
		case ttl >= 0:
			lastTTL = ttl
		case defTTL >= 0:
			ttl = defTTL
		case lastTTL >= 0:
			ttl = lastTTL
		default:
			ttl = defaultTTL
		}

		host, err := relativeHost(owner, zone)
		if err != nil {
			return nil, where("%v", err)
		}
		rec := inputRecord{Source: fmt.Sprintf("line %d", e.line), Type: rtype, Host: host, TTL: ttl}
		if rtype == "SOA" {
			continue
		}
		if err := zoneRdata(&rec, rdata, origin); err != nil {
			return nil, where("%s %s: %v", rtype, displayHost(&host), err)
		}
		records = append(records, rec)
	}
	return records, nil
}

// zoneRdata fills rec's Answer and Priority from the rdata tokens of its type.
func zoneRdata(rec *inputRecord, rdata []zoneToken, origin string) error {
	want := func(n int, form string) error {
		if len(rdata) != n {
			return fmt.Errorf("expected %s, got %d field(s)", form, len(rdata))
		}
		for _, f := range rdata {
			if f.quoted {
				return fmt.Errorf("expected %s, got a quoted string", form)
			}
		}
		return nil
	}
	prio := func(s string) (*int64, error) {
		n, err := strconv.ParseUint(s, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("priority %q must be an integer 0-65535", s)
		}
		p := int64(n)
		return &p, nil
	}
	switch rec.Type {
	case "A", "AAAA":
		if err := want(1, "an address"); err != nil {
			return err
		}
		rec.Answer = rdata[0].text
	case "CNAME", "ANAME", "NS":
		if err := want(1, "a target name"); err != nil {
			return err
		}
		rec.Answer = targetName(rdata[0].text, origin)
	case "MX":
		if err := want(2, "preference and exchange"); err != nil {
			return err
		}
		p, err := prio(rdata[0].text)
		if err != nil {
			return err
		}
		rec.Priority, rec.Answer = p, targetName(rdata[1].text, origin)
	case "SRV":
		if err := want(4, "priority, weight, port and target"); err != nil {
			return err
		}
		p, err := prio(rdata[0].text)
		if err != nil {
			return err
		}
		rec.Priority = p
		rec.Answer = rdata[1].text + " " + rdata[2].text + " " + targetName(rdata[3].text, origin)
	case "TXT":
		if len(rdata) == 0 {
			return errors.New("expected at least one character-string")
		}
		// Several character-strings make one value: that is how a zone
		// file spells a TXT value over 255 bytes, and how `dns export
		// --zone` splits one. The API stores the value whole.
		var b strings.Builder
		for _, f := range rdata {
			b.WriteString(f.text)
		}
		rec.Answer = b.String()
	case "CAA":
		if len(rdata) != 3 {
			return fmt.Errorf("expected flags, tag and value, got %d field(s)", len(rdata))
		}
		rec.Answer = rdata[0].text + " " + rdata[1].text + " " + strconv.Quote(rdata[2].text)
	default:
		return fmt.Errorf("record type %s is not one name.com hosts (A, AAAA, ANAME, CAA, CNAME, MX, NS, SRV, TXT)", rec.Type)
	}
	return nil
}

// absName makes a zone-file name absolute: "@" is the origin, a name ending
// in "." is already absolute, and any other is relative to the origin. The
// result is lowercased and ends in ".".
func absName(name, origin string) string {
	switch {
	case name == "@":
		return origin
	case strings.HasSuffix(name, "."):
		return strings.ToLower(name)
	}
	return strings.ToLower(name) + "." + origin
}

// targetName is a record target in the form the API stores: absolute, with
// no trailing dot. The root, ".", stays as it is (a null MX).
func targetName(name, origin string) string {
	if name == "." {
		return name
	}
	return strings.TrimSuffix(absName(name, origin), ".")
}

// relativeHost turns an absolute owner name into the host the API takes: "@"
// for the zone itself, and the labels before the zone otherwise. An owner
// outside the zone is an error; it cannot be a record of this domain.
func relativeHost(owner, zone string) (string, error) {
	if owner == zone {
		return "@", nil
	}
	if h, ok := strings.CutSuffix(owner, "."+zone); ok {
		return h, nil
	}
	return "", fmt.Errorf("owner %s is outside the zone %s", owner, zone)
}

// parseTTL reads a TTL: whole seconds, or BIND's unit form ("1h30m", "2d",
// "1W"), with each number followed by s, m, h, d or w. It is bounded at
// 2^31-1, the largest TTL RFC 2181 allows.
func parseTTL(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, n >= 0 && n <= math.MaxInt32
	}
	var total, n int64
	digits := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isDigit(c) {
			n = n*10 + int64(c-'0')
			digits = true
			if n > math.MaxInt32 {
				return 0, false
			}
			continue
		}
		if !digits {
			return 0, false
		}
		var unit int64
		switch c | 0x20 {
		case 's':
			unit = 1
		case 'm':
			unit = 60
		case 'h':
			unit = 3600
		case 'd':
			unit = 86400
		case 'w':
			unit = 604800
		default:
			return 0, false
		}
		total += n * unit
		if total > math.MaxInt32 {
			return 0, false
		}
		n, digits = 0, false
	}
	if digits { // a trailing number with no unit, as in "1h30"
		total += n
	}
	return total, total <= math.MaxInt32
}

// zoneEntries splits a zone file into logical entries: comments dropped,
// parenthesised groups joined across lines, quoted strings unescaped into one
// token each. CR before LF is ignored, so a file saved on Windows reads the
// same.
func zoneEntries(text string) ([]zoneEntry, error) {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		l = strings.TrimSuffix(l, "\r")
		if rest, ok := strings.CutPrefix(l, anameCommentPrefix); ok {
			l = rest
		}
		lines[i] = l
	}

	var entries []zoneEntry
	var cur *zoneEntry
	depth := 0
	for n, l := range lines {
		lineNo := n + 1
		if depth == 0 {
			if cur != nil && len(cur.tokens) > 0 {
				entries = append(entries, *cur)
			}
			cur = &zoneEntry{line: lineNo, blankOwner: l != "" && (l[0] == ' ' || l[0] == '\t')}
		}
		i := 0
		for i < len(l) {
			c := l[i]
			switch c {
			case ' ', '\t':
				i++
			case ';':
				i = len(l)
			case '(':
				depth++
				i++
			case ')':
				if depth == 0 {
					return nil, fmt.Errorf("line %d: unbalanced ')'", lineNo)
				}
				depth--
				i++
			case '"':
				s, next, err := readQuoted(l, i+1)
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", lineNo, err)
				}
				cur.tokens = append(cur.tokens, zoneToken{text: s, quoted: true})
				i = next
			default:
				s, next, err := readBare(l, i)
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", lineNo, err)
				}
				cur.tokens = append(cur.tokens, zoneToken{text: s})
				i = next
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("line %d: unclosed '('", cur.line)
	}
	if cur != nil && len(cur.tokens) > 0 {
		entries = append(entries, *cur)
	}
	return entries, nil
}

// readQuoted reads a quoted string from l starting just after its opening
// quote, and returns its unescaped text and the index after the closing
// quote. A string cannot span lines.
func readQuoted(l string, i int) (string, int, error) {
	var b strings.Builder
	for i < len(l) {
		switch c := l[i]; c {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			r, next, err := readEscape(l, i)
			if err != nil {
				return "", 0, err
			}
			b.WriteByte(r)
			i = next
		default:
			b.WriteByte(c)
			i++
		}
	}
	return "", 0, errors.New("unterminated quoted string")
}

// readBare reads an unquoted field, which ends at whitespace, a comment,
// a parenthesis or a quote. Escapes are decoded as in a quoted string.
func readBare(l string, i int) (string, int, error) {
	var b strings.Builder
	for i < len(l) {
		c := l[i]
		switch c {
		case ' ', '\t', ';', '(', ')', '"':
			return b.String(), i, nil
		case '\\':
			r, next, err := readEscape(l, i)
			if err != nil {
				return "", 0, err
			}
			b.WriteByte(r)
			i = next
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), i, nil
}

// readEscape decodes the escape at l[i] (a backslash): \DDD, a decimal byte
// value, or \X, the character X itself.
func readEscape(l string, i int) (byte, int, error) {
	if i+1 >= len(l) {
		return 0, 0, errors.New("backslash at end of line")
	}
	if !isDigit(l[i+1]) {
		return l[i+1], i + 2, nil
	}
	if i+4 > len(l) || !isDigit(l[i+2]) || !isDigit(l[i+3]) {
		return 0, 0, errors.New(`incomplete \DDD escape`)
	}
	n, err := strconv.ParseUint(l[i+1:i+4], 10, 8)
	if err != nil {
		return 0, 0, fmt.Errorf(`\%s is not a byte value`, l[i+1:i+4])
	}
	return byte(n), i + 4, nil
}
