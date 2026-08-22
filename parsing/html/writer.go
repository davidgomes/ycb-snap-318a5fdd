package html

import (
	"fmt"
	"html"
	"strings"

	"github.com/tomwright/dasel/v3/model"
	"github.com/tomwright/dasel/v3/parsing"
)

type htmlWriter struct{ options parsing.WriterOptions }

func newHTMLWriter(options parsing.WriterOptions) (parsing.Writer, error) {
	return &htmlWriter{options: options}, nil
}

func (w *htmlWriter) Write(value *model.Value) ([]byte, error) {
	var b strings.Builder
	if err := w.writeValue(&b, "", value, 0); err != nil {
		return nil, err
	}
	if !w.options.Compact {
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

func (w *htmlWriter) writeValue(b *strings.Builder, key string, v *model.Value, depth int) error {
	if v.IsBranch() || v.IsSpread() {
		return v.RangeSlice(func(_ int, item *model.Value) error { return w.writeValue(b, key, item, depth) })
	}
	if v.Type() == model.TypeSlice {
		return v.RangeSlice(func(_ int, item *model.Value) error { return w.writeValue(b, key, item, depth) })
	}
	if key == "" {
		return fmt.Errorf("html writer requires an element key")
	}
	indent := func() {
		if !w.options.Compact {
			b.WriteString(strings.Repeat(w.options.Indent, depth))
		}
	}
	indent()
	b.WriteByte('<')
	b.WriteString(strings.ToLower(key))
	if v.Type() == model.TypeString {
		text, err := valueToString(v)
		if err != nil {
			return err
		}
		b.WriteByte('>')
		b.WriteString(html.EscapeString(text))
		b.WriteString("</")
		b.WriteString(strings.ToLower(key))
		b.WriteByte('>')
		if !w.options.Compact {
			b.WriteByte('\n')
		}
		return nil
	}
	if v.Type() != model.TypeMap {
		return fmt.Errorf("html writer does not support value type: %s", v.Type())
	}
	kvs, err := v.MapKeyValues()
	if err != nil {
		return err
	}
	for _, kv := range kvs {
		if strings.HasPrefix(kv.Key, "-") {
			val, e := valueToString(kv.Value)
			if e != nil {
				return e
			}
			b.WriteByte(' ')
			b.WriteString(strings.ToLower(kv.Key[1:]))
			b.WriteString(`="`)
			b.WriteString(html.EscapeString(val))
			b.WriteByte('"')
		}
	}
	void := isVoid(key)
	children := make([]struct {
		key string
		val *model.Value
	}, 0)
	text := ""
	for _, kv := range kvs {
		if strings.HasPrefix(kv.Key, "-") {
			continue
		}
		if kv.Key == "#text" {
			text, err = valueToString(kv.Value)
			if err != nil {
				return err
			}
			continue
		}
		children = append(children, struct {
			key string
			val *model.Value
		}{kv.Key, kv.Value})
	}
	if void {
		b.WriteString("/>")
		if !w.options.Compact {
			b.WriteByte('\n')
		}
		return nil
	}
	b.WriteByte('>')
	if text != "" {
		b.WriteString(html.EscapeString(text))
	}
	if len(children) > 0 && !w.options.Compact {
		b.WriteByte('\n')
	}
	for _, child := range children {
		if err := w.writeValue(b, child.key, child.val, depth+1); err != nil {
			return err
		}
	}
	if len(children) > 0 {
		indent()
	}
	b.WriteString("</")
	b.WriteString(strings.ToLower(key))
	b.WriteByte('>')
	if !w.options.Compact {
		b.WriteByte('\n')
	}
	return nil
}

func isVoid(tag string) bool {
	switch strings.ToLower(tag) {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}

func valueToString(v *model.Value) (string, error) {
	if v.IsNull() {
		return "", nil
	}
	switch v.Type() {
	case model.TypeString:
		return v.StringValue()
	case model.TypeInt:
		i, e := v.IntValue()
		return fmt.Sprintf("%d", i), e
	case model.TypeFloat:
		f, e := v.FloatValue()
		return fmt.Sprintf("%g", f), e
	case model.TypeBool:
		x, e := v.BoolValue()
		return fmt.Sprintf("%t", x), e
	default:
		return "", fmt.Errorf("html writer cannot format type %s to string", v.Type())
	}
}
