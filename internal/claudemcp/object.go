package claudemcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// object is a top-level JSON object kept as raw member values in their file
// order, so a rewrite changes only the member it sets: every other value is
// written back byte for byte.
type object struct {
	keys    []string
	values  map[string]json.RawMessage
	newline bool // the file ended with a line break
}

// decodeObject decodes data, which must hold exactly one JSON object.
func decodeObject(data []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not an object")
	}
	o := &object{values: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("bad key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if _, seen := o.values[key]; !seen {
			o.keys = append(o.keys, key)
		}
		o.values[key] = raw
	}
	if _, err := dec.Token(); err != nil { // the closing '}'
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("data after the object")
	}
	o.newline = bytes.HasSuffix(bytes.TrimRight(data, " \t\r"), []byte("\n"))
	return o, nil
}

func (o *object) get(key string) (json.RawMessage, bool) {
	v, ok := o.values[key]
	return v, ok
}

// set replaces key's value in place, or appends the key.
func (o *object) set(key string, value json.RawMessage) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *object) remove(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// encode writes the object two-space indented (as Claude Code writes it), each
// member value as its raw bytes.
func (o *object) encode() []byte {
	var b bytes.Buffer
	if len(o.keys) == 0 {
		b.WriteString("{}")
	} else {
		b.WriteString("{\n")
		for i, k := range o.keys {
			name, _ := json.Marshal(k)
			b.WriteString("  ")
			b.Write(name)
			b.WriteString(": ")
			b.Write(o.values[k])
			if i < len(o.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteByte('}')
	}
	if o.newline {
		b.WriteByte('\n')
	}
	return b.Bytes()
}
