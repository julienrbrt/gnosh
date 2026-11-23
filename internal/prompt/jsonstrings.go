package prompt

import (
	"bytes"
	"encoding/json"
)

// MayHoldControl reports whether raw, JSON text, may hold a control
// sequence Defuse would change: it holds a "<", written as it is or as a
// JSON escape (\u003c, in either letter case).
func MayHoldControl(raw []byte) bool {
	return bytes.IndexByte(raw, '<') >= 0 || bytes.Contains(bytes.ToLower(raw), []byte(`\u003c`))
}

// RewriteJSONStrings returns raw, one JSON value, with f applied to every
// string in it, keys included, read with its escapes. The value is written
// again token by token, so that the order of an object's keys stays, and
// an object that names a key twice still names it twice: the loop's own
// check for that comes afterwards and must see it. needed is asked first
// whether raw can hold anything f would change; when it says no, when f
// changes nothing, or when raw cannot be read, ok is false and raw is to
// be used as it is.
func RewriteJSONStrings(raw json.RawMessage, f func(string) string, needed func([]byte) bool) (json.RawMessage, bool) {
	if len(raw) == 0 || !needed(raw) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	str := func(s string) error {
		if err := enc.Encode(s); err != nil {
			return err
		}
		out.Truncate(out.Len() - 1) // the line break Encode adds
		return nil
	}
	type frame struct {
		object bool
		n      int  // how many members or items have been started
		key    bool // the next string is a key
	}
	var stack []*frame
	// value is called before a value is written: it writes the comma that
	// parts the items of a list, and notes that an object's next string
	// is a key again.
	value := func() {
		if len(stack) == 0 {
			return
		}
		top := stack[len(stack)-1]
		if top.object {
			top.key = true
			return
		}
		if top.n > 0 {
			out.WriteByte(',')
		}
		top.n++
	}
	changed := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				value()
				out.WriteByte(byte(v))
				stack = append(stack, &frame{object: v == '{', key: v == '{'})
			default:
				if len(stack) == 0 {
					return nil, false
				}
				stack = stack[:len(stack)-1]
				out.WriteByte(byte(v))
			}
		case string:
			s := f(v)
			changed = changed || s != v
			if n := len(stack); n > 0 && stack[n-1].object && stack[n-1].key {
				top := stack[n-1]
				if top.n > 0 {
					out.WriteByte(',')
				}
				top.n++
				top.key = false
				if err := str(s); err != nil {
					return nil, false
				}
				out.WriteByte(':')
				continue
			}
			value()
			if err := str(s); err != nil {
				return nil, false
			}
		case json.Number:
			value()
			out.WriteString(v.String())
		case bool:
			value()
			if v {
				out.WriteString("true")
			} else {
				out.WriteString("false")
			}
		case nil:
			value()
			out.WriteString("null")
		}
	}
	if len(stack) != 0 || !changed || !json.Valid(out.Bytes()) {
		return nil, false
	}
	return out.Bytes(), true
}
