package apicmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

// errNotObject is a page that is not a JSON object, so it has no nextPage.
var errNotObject = errors.New("not a JSON object")

// pages merges the pages of a list endpoint into one document, as
// --paginate prints it. The API's lists are objects such as
// {"domains": [...], "totalCount": 9, "from": 1, "to": 3, "nextPage": 2,
// "lastPage": 3}. In the merged document every array is the arrays of all
// the pages end to end, nextPage and lastPage are gone (there is no next
// page of it), "to" is the last page's, and everything else is the first
// page's. Keys keep the order the first page gave them.
type pages struct {
	keys   []string
	vals   map[string]json.RawMessage
	arrays map[string][]json.RawMessage
}

// add merges one page and returns its nextPage, or 0 when it has none.
func (p *pages) add(page []byte) (next int, err error) {
	dec := json.NewDecoder(bytes.NewReader(page))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return 0, errNotObject
	}
	if p.vals == nil {
		p.vals, p.arrays = map[string]json.RawMessage{}, map[string][]json.RawMessage{}
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return 0, err
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return 0, err
		}
		_, scalar := p.vals[key]
		_, array := p.arrays[key]
		switch {
		case key == "nextPage":
			if string(raw) == "null" {
				continue
			}
			if next, err = strconv.Atoi(string(raw)); err != nil {
				return 0, fmt.Errorf("nextPage is %s, not a page number", raw)
			}
		case key == "lastPage":
		case raw[0] == '[' && !scalar:
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err != nil {
				return 0, err
			}
			if !array {
				p.keys = append(p.keys, key)
			}
			p.arrays[key] = append(p.arrays[key], items...)
		case !scalar && !array:
			p.keys = append(p.keys, key)
			p.vals[key] = raw
		case key == "to" && scalar:
			p.vals[key] = raw
		}
	}
	return next, nil
}

// bytes returns the merged document, compact.
func (p *pages) bytes() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range p.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(k)
		b.Write(name)
		b.WriteByte(':')
		if raw, ok := p.vals[k]; ok {
			b.Write(raw)
			continue
		}
		b.WriteByte('[')
		for j, item := range p.arrays[k] {
			if j > 0 {
				b.WriteByte(',')
			}
			b.Write(item)
		}
		b.WriteByte(']')
	}
	b.WriteByte('}')
	var out bytes.Buffer
	if err := json.Compact(&out, b.Bytes()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// withPage returns target with its page query parameter set to n.
func withPage(target string, n int) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(n))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// startPage is the page target asks for: its page query parameter, or 1.
func startPage(target string) int {
	u, err := url.Parse(target)
	if err != nil {
		return 1
	}
	if n, err := strconv.Atoi(u.Query().Get("page")); err == nil {
		return n
	}
	return 1
}
