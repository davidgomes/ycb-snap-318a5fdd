package html

import (
	"bytes"
	"strings"

	"github.com/tomwright/dasel/v3/model"
	"github.com/tomwright/dasel/v3/parsing"
	xhtml "golang.org/x/net/html"
)

type htmlReader struct{ structured bool }

func newHTMLReader(options parsing.ReaderOptions) (parsing.Reader, error) {
	return &htmlReader{structured: options.Ext["html-mode"] == "structured"}, nil
}

func (r *htmlReader) Read(data []byte) (*model.Value, error) {
	doc, err := xhtml.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	root := convert(doc)
	if r.structured {
		return structured(root)
	}
	head, body := &node{name: "head"}, &node{name: "body"}
	var orphan []*node
	for _, child := range root.children {
		if child.name == "html" {
			for _, c := range child.children {
				switch c.name {
				case "head":
					head = c
				case "body":
					body = c
				}
			}
		} else {
			orphan = append(orphan, child)
		}
	}
	body.children = append(body.children, orphan...)
	res := model.NewMapValue()
	h, err := friendlyChildren(head)
	if err != nil {
		return nil, err
	}
	b, err := friendlyChildren(body)
	if err != nil {
		return nil, err
	}
	if err = res.SetMapKey("head", h); err != nil {
		return nil, err
	}
	if err = res.SetMapKey("body", b); err != nil {
		return nil, err
	}
	return res, nil
}

type node struct {
	name, text string
	attrs      map[string]string
	children   []*node
}

func convert(n *xhtml.Node) *node {
	out := &node{name: strings.ToLower(n.Data), attrs: map[string]string{}}
	if n.Type == xhtml.DocumentNode {
		out.name = ""
	}
	if n.Type == xhtml.ElementNode {
		for _, a := range n.Attr {
			out.attrs[strings.ToLower(a.Key)] = a.Val
		}
	}
	if n.Type == xhtml.TextNode {
		out.text = n.Data
		return out
	}
	if n.Type == xhtml.CommentNode || n.Type == xhtml.DoctypeNode {
		return out
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.CommentNode || c.Type == xhtml.DoctypeNode {
			continue
		}
		if c.Type == xhtml.TextNode {
			if strings.TrimSpace(c.Data) != "" {
				out.text += c.Data
			}
			continue
		}
		out.children = append(out.children, convert(c))
	}
	if out.name == "script" || out.name == "style" {
		out.text = rawText(n)
	}
	return out
}

func rawText(n *xhtml.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}

func friendlyChildren(n *node) (*model.Value, error) {
	res := model.NewMapValue()
	groups := map[string][]*node{}
	order := []string{}
	for _, c := range n.children {
		if _, ok := groups[c.name]; !ok {
			order = append(order, c.name)
		}
		groups[c.name] = append(groups[c.name], c)
	}
	for _, key := range order {
		values := groups[key]
		if len(values) == 1 {
			v, err := friendly(values[0])
			if err != nil {
				return nil, err
			}
			if err = res.SetMapKey(key, v); err != nil {
				return nil, err
			}
			continue
		}
		s := model.NewSliceValue()
		for _, c := range values {
			v, err := friendly(c)
			if err != nil {
				return nil, err
			}
			if err = s.Append(v); err != nil {
				return nil, err
			}
		}
		if err := res.SetMapKey(key, s); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func friendly(n *node) (*model.Value, error) {
	if len(n.attrs) == 0 && len(n.children) == 0 {
		return model.NewStringValue(strings.TrimSpace(n.text)), nil
	}
	res := model.NewMapValue()
	for k, v := range n.attrs {
		if err := res.SetMapKey("-"+k, model.NewStringValue(v)); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(n.text) != "" {
		if err := res.SetMapKey("#text", model.NewStringValue(strings.TrimSpace(n.text))); err != nil {
			return nil, err
		}
	}
	children, err := friendlyChildren(n)
	if err != nil {
		return nil, err
	}
	kvs, _ := children.MapKeyValues()
	for _, kv := range kvs {
		if err := res.SetMapKey(kv.Key, kv.Value); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func structured(n *node) (*model.Value, error) {
	res := model.NewMapValue()
	for _, k := range []string{"tag", "attrs", "text", "children"} {
		switch k {
		case "tag":
			if err := res.SetMapKey(k, model.NewStringValue(n.name)); err != nil {
				return nil, err
			}
		case "text":
			if err := res.SetMapKey(k, model.NewStringValue(n.text)); err != nil {
				return nil, err
			}
		case "attrs":
			a := model.NewMapValue()
			for key, val := range n.attrs {
				if err := a.SetMapKey(key, model.NewStringValue(val)); err != nil {
					return nil, err
				}
			}
			if err := res.SetMapKey(k, a); err != nil {
				return nil, err
			}
		case "children":
			s := model.NewSliceValue()
			for _, c := range n.children {
				v, err := structured(c)
				if err != nil {
					return nil, err
				}
				if err = s.Append(v); err != nil {
					return nil, err
				}
			}
			if err := res.SetMapKey(k, s); err != nil {
				return nil, err
			}
		}
	}
	return res, nil
}
