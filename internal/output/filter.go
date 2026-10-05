package output

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/itchyny/gojq"
)

// Filter is what --fields and --jq ask of a command's output (#241). The
// command runs in JSON mode, as `-o json` would run it, into a buffer; when
// it returns, EndFilter projects that document to Fields, runs JQ over it,
// and prints the result in the format that was asked for.
//
// Doing it once here, over the document, rather than in each command, means
// every command that prints JSON supports both flags, and what they filter is
// exactly the document the JSON contract describes.
type Filter struct {
	// Fields are the keys to keep, in order, of each list item or of the
	// object. Empty means keep everything.
	Fields []string
	// JQ is the compiled --jq expression, or nil.
	JQ *gojq.Code

	format Format    // the format asked for, which the result is printed in
	out    io.Writer // the writer the command would have printed to
	buf    bytes.Buffer
}

// FilterError is a --fields or --jq that does not fit the command's output:
// a field no item has, an expression that fails on this document, output
// that is not JSON. The command line is wrong, so cmd reports it as a usage
// error.
type FilterError struct{ msg string }

func (e *FilterError) Error() string { return e.msg }

// ParseFields splits --fields values, which may each hold several names
// separated by commas, into field names: spaces trimmed, empty names and
// repeats dropped.
func ParseFields(vals []string) ([]string, error) {
	var fields []string
	seen := map[string]bool{}
	for _, v := range vals {
		for f := range strings.SplitSeq(v, ",") {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			fields = append(fields, f)
		}
	}
	if len(fields) == 0 {
		return nil, errors.New("--fields needs at least one field name, such as --fields domainName,expireDate")
	}
	return fields, nil
}

// CompileJQ compiles a --jq expression. Its error is gojq's own message.
func CompileJQ(expr string) (*gojq.Code, error) {
	q, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("--jq: %w", err)
	}
	code, err := gojq.Compile(q)
	if err != nil {
		return nil, fmt.Errorf("--jq: %w", err)
	}
	return code, nil
}

// BeginFilter starts f: until EndFilter, c is in JSON mode and prints into
// f's buffer. c.Format is the format the filtered result is printed in.
func (c *Config) BeginFilter(f *Filter) {
	f.format, f.out = c.Format, c.Writer
	c.Format, c.Writer = FormatJSON, &f.buf
	c.filter = f
}

// EndFilter filters what the command printed since BeginFilter and prints
// it, and puts c back in the format that was asked for, so that warnings and
// errors after it come out in that format too. Nothing is printed when the
// command printed nothing. It does nothing when no filter was begun.
//
// When the filter does not fit the output, the result is a *FilterError and
// nothing is printed — unless wrote says the command sent a write. Then the
// write has been made, and a usage error would say the command line stopped
// it, so the document is printed unfiltered with a warning instead: the
// outcome of a write is never thrown away.
func (c *Config) EndFilter(wrote bool) error {
	f := c.filter
	if f == nil {
		return nil
	}
	c.filter = nil
	c.Format, c.Writer = f.format, f.out
	raw := f.buf.Bytes()
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}

	var rendered bytes.Buffer
	err := c.renderFiltered(&rendered, raw, f)
	if err == nil {
		_, err = c.Writer.Write(rendered.Bytes())
		return err
	}
	if !wrote {
		return err
	}
	c.Warn(err.Error() + " — the change was made, so its output is printed unfiltered")
	_, werr := c.Writer.Write(raw)
	return werr
}

// flagNames names the flags in use, for messages.
func (f *Filter) flagNames() string {
	switch {
	case f.JQ != nil && len(f.Fields) > 0:
		return "--fields and --jq"
	case f.JQ != nil:
		return "--jq"
	}
	return "--fields"
}

// renderFiltered writes raw, one or more JSON documents, to w, filtered by f
// and in f.format.
func (c *Config) renderFiltered(w *bytes.Buffer, raw []byte, f *Filter) error {
	docs, err := decodeOrdered(raw)
	if err != nil {
		return &FilterError{fmt.Sprintf("%s needs JSON output, and this command printed something else", f.flagNames())}
	}
	for _, doc := range docs {
		if len(f.Fields) > 0 {
			if doc, err = project(doc, f.Fields); err != nil {
				return err
			}
		}
		if f.JQ != nil {
			if err := runJQ(w, f.JQ, doc); err != nil {
				return err
			}
			continue
		}
		switch f.format {
		case FormatJSON:
			err = encodeJSON(w, doc)
		case FormatYAML:
			err = writeYAML(w, doc)
		default:
			err = c.fieldTable(w, doc, f.Fields)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// runJQ writes each result of code run over doc on its own line: a string as
// itself, with no quotes, as `jq -r` and gh's --jq print it, and anything
// else as compact JSON.
func runJQ(w io.Writer, code *gojq.Code, doc any) error {
	iter := code.RunWithContext(context.Background(), plain(doc))
	for {
		v, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, ok := v.(error); ok {
			if h, ok := errors.AsType[*gojq.HaltError](err); ok && h.Value() == nil {
				return nil
			}
			return &FilterError{"--jq: " + err.Error()}
		}
		if s, ok := v.(string); ok {
			fmt.Fprintln(w, s)
			continue
		}
		b, err := gojq.Marshal(v)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s\n", b)
	}
}

// fieldTable prints doc, already projected to fields, as a table or as TSV:
// fields are the columns, in order, and each list item — or the object, for a
// document that is not a list — is a row.
func (c *Config) fieldTable(w io.Writer, doc any, fields []string) error {
	items, _ := listItems(doc)
	if items == nil {
		items = []any{doc}
	}
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		obj, _ := it.(*object)
		row := make([]string, len(fields))
		for i, f := range fields {
			if obj != nil {
				row[i] = cellString(obj.vals[f])
			}
		}
		rows = append(rows, row)
	}
	prev := c.Writer
	c.Writer = w
	defer func() { c.Writer = prev }()
	if c.Format == FormatTSV {
		c.writeTSV(fields, rows)
		return nil
	}
	if len(rows) > 0 {
		c.Table(fields, rows)
	}
	return nil
}

// cellString is a JSON value as a table cell: a string as itself, null as
// nothing, a number or boolean as JSON writes it, and an object or array as
// compact JSON.
func cellString(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(unescapeHTML(b))
}

// listItems returns the items of a list-shaped document — {"data": [...]},
// whatever else is beside it, or a bare array — and whether it is one.
func listItems(doc any) ([]any, bool) {
	switch d := doc.(type) {
	case []any:
		return d, true
	case *object:
		if items, ok := d.vals["data"].([]any); ok {
			return items, true
		}
	}
	return nil, false
}

// project keeps fields, in that order, of each item of a list-shaped
// document, leaving the envelope's own keys (nextPage, total, changed) alone,
// or of the object itself. An item without a field gets null for it.
//
// A field that no item has is an error naming the fields there are. Absent
// from some items is not: the API leaves out empty values, so the first item
// alone is no guide to what a list holds. An empty list has nothing to check
// against, and passes.
func project(doc any, fields []string) (any, error) {
	if items, ok := listItems(doc); ok {
		var avail []string
		seen := map[string]bool{}
		out := make([]any, len(items))
		for i, it := range items {
			obj, ok := it.(*object)
			if !ok {
				return nil, &FilterError{"--fields picks keys of objects, and this list's items are not objects; use --jq"}
			}
			for _, k := range obj.keys {
				if !seen[k] {
					seen[k] = true
					avail = append(avail, k)
				}
			}
			out[i] = obj.pick(fields)
		}
		if len(items) > 0 {
			if err := checkFields(fields, seen, avail); err != nil {
				return nil, err
			}
		}
		if arr, ok := doc.([]any); ok && arr != nil {
			return out, nil
		}
		return doc.(*object).with("data", out), nil
	}
	obj, ok := doc.(*object)
	if !ok {
		return nil, &FilterError{"--fields picks keys of an object or of a list's items, and this output is neither; use --jq"}
	}
	seen := make(map[string]bool, len(obj.keys))
	for _, k := range obj.keys {
		seen[k] = true
	}
	if err := checkFields(fields, seen, obj.keys); err != nil {
		return nil, err
	}
	return obj.pick(fields), nil
}

// checkFields returns an error naming any of fields not in seen, and the
// fields available.
func checkFields(fields []string, seen map[string]bool, avail []string) error {
	var unknown []string
	for _, f := range fields {
		if !seen[f] {
			unknown = append(unknown, fmt.Sprintf("%q", f))
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return &FilterError{fmt.Sprintf("unknown %s %s for --fields; this output has: %s",
		PluralNoun(len(unknown), "field"), strings.Join(unknown, ", "), strings.Join(avail, ", "))}
}

// object is a decoded JSON object that remembers its key order, so filtered
// output lists keys as the command printed them, or as --fields named them.
type object struct {
	keys []string
	vals map[string]any
}

// pick returns a new object with fields, in order, null where o has none.
func (o *object) pick(fields []string) *object {
	p := &object{keys: fields, vals: make(map[string]any, len(fields))}
	for _, f := range fields {
		p.vals[f] = o.vals[f]
	}
	return p
}

// with returns a copy of o with key set to v.
func (o *object) with(key string, v any) *object {
	c := &object{keys: o.keys, vals: make(map[string]any, len(o.vals))}
	for k, val := range o.vals {
		c.vals[k] = val
	}
	c.vals[key] = v
	return c
}

// MarshalJSON writes o's keys in order.
func (o *object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// decodeOrdered decodes every JSON document in raw, objects as *object and
// numbers as json.Number, so nothing is reordered or rounded.
func decodeOrdered(raw []byte) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var docs []any
	for {
		v, err := decodeValue(dec)
		if err == io.EOF {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		docs = append(docs, v)
	}
}

// decodeValue consumes one JSON value from dec.
func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch d {
	case '{':
		o := &object{vals: map[string]any{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k, _ := kt.(string)
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			if _, dup := o.vals[k]; !dup {
				o.keys = append(o.keys, k)
			}
			o.vals[k] = v
		}
		_, err := dec.Token()
		return o, err
	case '[':
		arr := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	}
	return nil, fmt.Errorf("unexpected %v", d)
}

// plain converts a decoded document to the types gojq takes: map[string]any
// for an object. json.Number it takes as it is.
func plain(v any) any {
	switch v := v.(type) {
	case *object:
		m := make(map[string]any, len(v.vals))
		for k, val := range v.vals {
			m[k] = plain(val)
		}
		return m
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = plain(val)
		}
		return out
	}
	return v
}

// tsvEscaper escapes a TSV cell so that every row is one line and every tab
// separates two cells: a backslash, tab, line feed or carriage return becomes
// \\, \t, \n or \r. It is the "linear TSV" convention `mlr` and PostgreSQL's
// text COPY format read.
var tsvEscaper = strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)

// writeTSV prints headers (unless nil or --no-header) and rows as
// tab-separated lines, each cell escaped by tsvEscaper.
func (c *Config) writeTSV(headers []string, rows [][]string) {
	line := func(cells []string) {
		esc := make([]string, len(cells))
		for i, v := range cells {
			esc[i] = tsvEscaper.Replace(v)
		}
		fmt.Fprintln(c.Writer, strings.Join(esc, "\t"))
	}
	if headers != nil && !c.NoHeader {
		line(headers)
	}
	for _, r := range rows {
		line(r)
	}
}

// tsvCells returns a table's cells as TSV values: styling stripped, and the
// dash a table shows for a missing value turned back into nothing.
func tsvCells(rows [][]string) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = make([]string, len(r))
		for j, v := range r {
			if v = ansi.Strip(v); v != None {
				out[i][j] = v
			}
		}
	}
	return out
}
