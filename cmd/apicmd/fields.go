package apicmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
)

// field is one -f or -F parameter: its key, the value a JSON body carries,
// and the text a query string carries.
type field struct {
	key   string
	value any
	text  string
}

// parseFields parses -f (raw) and -F (typed) parameters, -f first, as gh
// api does. A malformed one is a usage error; an unreadable @file is not.
func parseFields(raw, typed []string) ([]field, error) {
	fields := make([]field, 0, len(raw)+len(typed))
	for _, kv := range raw {
		key, val, err := splitField("-f", kv)
		if err != nil {
			return nil, err
		}
		fields = append(fields, field{key, val, val})
	}
	for _, kv := range typed {
		key, val, err := splitField("-F", kv)
		if err != nil {
			return nil, err
		}
		f := field{key: key, text: val}
		if f.value, err = typedValue(val); err != nil {
			return nil, err
		}
		if s, ok := f.value.(string); ok {
			f.text = s // the contents of an @file
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// splitField splits key=value.
func splitField(flag, kv string) (key, val string, err error) {
	key, val, ok := strings.Cut(kv, "=")
	if !ok || key == "" {
		return "", "", cmdutil.NewUsageError(fmt.Errorf("invalid %s %q: expected key=value", flag, kv))
	}
	return key, val, nil
}

// typedValue is a -F value: true, false and null as themselves, a JSON
// number as a number, @file as the file's contents (@- for stdin), and
// anything else as a string.
func typedValue(s string) (any, error) {
	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	if name, ok := strings.CutPrefix(s, "@"); ok {
		var (
			b   []byte
			err error
		)
		if name == "-" {
			b, err = io.ReadAll(os.Stdin)
		} else {
			b, err = os.ReadFile(name) //nolint:gosec // G304: -F key=@file names the file to read; that is its purpose
		}
		if err != nil {
			return nil, fmt.Errorf("reading -F value %s: %w", s, err)
		}
		return string(b), nil
	}
	// json.Valid alone would also take "[1]" or a quoted string, which -F
	// sends as text, as gh does.
	if s != "" && (s[0] == '-' || (s[0] >= '0' && s[0] <= '9')) && json.Valid([]byte(s)) {
		return json.Number(s), nil
	}
	return s, nil
}

// fieldsBody builds the JSON body fields describe. A key may nest, as
// a[b]=c, and a trailing [] appends to an array, as a[]=x.
func fieldsBody(fields []field) ([]byte, error) {
	root := map[string]any{}
	for _, f := range fields {
		if err := setField(root, f.key, f.value); err != nil {
			return nil, cmdutil.NewUsageError(err)
		}
	}
	return json.Marshal(root)
}

// fieldsQuery is fields as query parameters, for a GET or HEAD. Keys are
// sent as written, a[b] included.
func fieldsQuery(fields []field) url.Values {
	q := url.Values{}
	for _, f := range fields {
		q.Add(f.key, f.text)
	}
	return q
}

// setField sets key, split by splitKey, to val in root.
func setField(root map[string]any, key string, val any) error {
	path, err := splitKey(key)
	if err != nil {
		return err
	}
	m := root
	for i, seg := range path[:len(path)-1] {
		if path[i+1] == "" { // seg[]: splitKey allows it only last
			arr, ok := m[seg].([]any)
			if _, exists := m[seg]; exists && !ok {
				return fmt.Errorf("field %q: %s is already set to something that is not a list", key, seg)
			}
			m[seg] = append(arr, val)
			return nil
		}
		switch v := m[seg].(type) {
		case nil:
			if _, exists := m[seg]; exists {
				return fmt.Errorf("field %q: %s is already set to null", key, seg)
			}
			next := map[string]any{}
			m[seg], m = next, next
		case map[string]any:
			m = v
		default:
			return fmt.Errorf("field %q: %s is already set to something that is not an object", key, seg)
		}
	}
	last := path[len(path)-1]
	if _, exists := m[last]; exists {
		return fmt.Errorf("field %q is given more than once", key)
	}
	m[last] = val
	return nil
}

// splitKey splits a[b][c] into a, b, c, and a[] into a and "". [] may come
// only last.
func splitKey(key string) ([]string, error) {
	name, rest, nested := strings.Cut(key, "[")
	if name == "" {
		return nil, fmt.Errorf("field %q has no name", key)
	}
	path := []string{name}
	if !nested {
		return path, nil
	}
	rest = "[" + rest
	for rest != "" {
		if path[len(path)-1] == "" {
			return nil, fmt.Errorf("field %q: [] may only end a key", key)
		}
		seg, after, ok := strings.Cut(rest, "]")
		if !ok || !strings.HasPrefix(seg, "[") || strings.Contains(seg[1:], "[") {
			return nil, fmt.Errorf("field %q: expected name[key] or name[]", key)
		}
		path = append(path, seg[1:])
		rest = after
	}
	return path, nil
}
