package domain

import (
	"bytes"
	"encoding/json"
	"fmt"

	coreapigo "github.com/namedotcom/core-api-go"
)

// domainDoc is a domain as `domain get -o json` and `-o yaml` print it: the
// API's keys in its order, except contacts, which goes last. Four contacts
// are some fifty lines of YAML, and they came before createDate, expireDate
// and renewalPrice, the values a reader most often looks for (#364). The
// embedded payload keeps --fields' list of known keys.
type domainDoc struct {
	*coreapigo.DomainResponsePayload
}

func (d domainDoc) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(d.DomainResponsePayload)
	if err != nil {
		return nil, err
	}
	return keyLast(b, "contacts")
}

// domainDocs wraps each of ds as a domainDoc.
func domainDocs(ds []*coreapigo.DomainResponsePayload) []domainDoc {
	docs := make([]domainDoc, len(ds))
	for i, d := range ds {
		docs[i] = domainDoc{d}
	}
	return docs
}

// keyLast returns the JSON object b with key, if present, moved after the
// others. The other keys keep their order.
func keyLast(b []byte, key string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("moving %q last: not a JSON object", key)
	}
	var out bytes.Buffer
	var last json.RawMessage
	out.WriteByte('{')
	n := 0
	write := func(k string, v json.RawMessage) {
		if n > 0 {
			out.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		out.Write(kb)
		out.WriteByte(':')
		out.Write(v)
		n++
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if k == key {
			last = v
			continue
		}
		write(k, v)
	}
	if last != nil {
		write(key, last)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
