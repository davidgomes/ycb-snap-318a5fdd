package orderedmap

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

// SyntaxError describes an invalid JSONPath expression.
type SyntaxError struct {
	Message  string
	Position int
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("syntax error at position %d: %s", e.Position, e.Message)
}

type jpStep struct {
	kind, key string
	keys      []string
	indexes   []int
	recursive bool
	filter    string
}

// Query evaluates the JSONPath expression path against doc.
func Query(doc interface{}, path string) ([]interface{}, error) {
	steps, err := parseJSONPath(path)
	if err != nil {
		return nil, err
	}
	cur := []interface{}{doc}
	for _, s := range steps {
		var next []interface{}
		for _, v := range cur {
			next = append(next, applyStep(v, s)...)
		}
		cur = next
	}
	return cur, nil
}
func QueryOne(doc interface{}, path string) (interface{}, bool, error) {
	r, e := Query(doc, path)
	if e != nil || len(r) == 0 {
		return nil, false, e
	}
	return r[0], true, nil
}

func parseJSONPath(p string) ([]jpStep, error) {
	if p == "" || p[0] != '$' {
		return nil, &SyntaxError{"path must start with $", 0}
	}
	var out []jpStep
	recursiveBracket := false
	i := 1
	for i < len(p) {
		if p[i] == '.' {
			recursive := false
			i++
			if i < len(p) && p[i] == '.' {
				recursive = true
				i++
			}
			if i < len(p) && p[i] == '*' {
				out = append(out, jpStep{kind: "wild", recursive: recursive})
				i++
				continue
			}
			if recursive && i < len(p) && p[i] == '[' {
				recursiveBracket = true
				continue
			}
			start := i
			for i < len(p) && (unicode.IsLetter(rune(p[i])) || unicode.IsDigit(rune(p[i])) || p[i] == '_' || p[i] == '-') {
				i++
			}
			if start == i {
				return nil, &SyntaxError{"expected key", i}
			}
			out = append(out, jpStep{kind: "key", key: p[start:i], recursive: recursive})
			continue
		}
		if p[i] != '[' {
			return nil, &SyntaxError{"expected selector", i}
		}
		start := i
		i++
		if i < len(p) && p[i] == '?' {
			end := strings.IndexByte(p[i:], ']')
			if end < 0 {
				return nil, &SyntaxError{"unterminated filter", start}
			}
			out = append(out, jpStep{kind: "filter", filter: p[i+1 : i+end]})
			i += end + 1
			continue
		}
		if i < len(p) && p[i] == '(' {
			end := strings.IndexByte(p[i:], ')')
			if end < 0 {
				return nil, &SyntaxError{"unterminated script", start}
			}
			out = append(out, jpStep{kind: "script", key: p[i+1 : i+end]})
			i += end + 1
			if i >= len(p) || p[i] != ']' {
				return nil, &SyntaxError{"expected ]", i}
			}
			i++
			continue
		}
		var vals []string
		var nums []int
		for {
			for i < len(p) && (p[i] == ' ' || p[i] == '\t') {
				i++
			}
			if i >= len(p) {
				return nil, &SyntaxError{"unterminated bracket", start}
			}
			if p[i] == '\'' || p[i] == '"' {
				q := p[i]
				i++
				var b strings.Builder
				for i < len(p) && p[i] != q {
					if p[i] == '\\' && i+1 < len(p) {
						i++
						b.WriteByte(p[i])
					} else {
						b.WriteByte(p[i])
					}
					i++
				}
				if i >= len(p) {
					return nil, &SyntaxError{"unterminated string", i}
				}
				i++
				vals = append(vals, b.String())
			} else {
				j := i
				for i < len(p) && (p[i] == '-' || unicode.IsDigit(rune(p[i]))) {
					i++
				}
				if j == i {
					return nil, &SyntaxError{"expected key, index, or string", i}
				}
				n, e := strconv.Atoi(p[j:i])
				if e != nil {
					return nil, &SyntaxError{"invalid index", j}
				}
				nums = append(nums, n)
			}
			for i < len(p) && (p[i] == ' ' || p[i] == '\t') {
				i++
			}
			if i < len(p) && p[i] == ',' {
				i++
				continue
			}
			break
		}
		if i >= len(p) || p[i] != ']' {
			return nil, &SyntaxError{"expected ]", i}
		}
		i++
		if len(vals) > 0 {
			out = append(out, jpStep{kind: "keyunion", keys: vals, recursive: recursiveBracket})
		} else {
			out = append(out, jpStep{kind: "idxunion", indexes: nums})
		}
		recursiveBracket = false
	}
	return out, nil
}

func children(v interface{}) []interface{} {
	switch x := v.(type) {
	case *Map:
		r := []interface{}{}
		x.Iterate(func(_, v interface{}) { r = append(r, v) })
		return r
	case []interface{}:
		return x
	case map[string]interface{}:
		r := []interface{}{}
		for _, v := range x {
			r = append(r, v)
		}
		return r
	}
	return nil
}
func get(v interface{}, k string) (interface{}, bool) {
	switch x := v.(type) {
	case *Map:
		return x.Get(k)
	case map[string]interface{}:
		r, ok := x[k]
		return r, ok
	}
	return nil, false
}
func applyStep(v interface{}, s jpStep) []interface{} {
	if s.recursive {
		var r []interface{}
		var walk func(interface{})
		walk = func(x interface{}) {
			if s.kind == "wild" {
				r = append(r, x)
			} else if s.kind == "keyunion" {
				for _, k := range s.keys {
					if y, ok := get(x, k); ok {
						r = append(r, y)
					}
				}
			} else if y, ok := get(x, s.key); ok {
				r = append(r, y)
			}
			for _, c := range children(x) {
				walk(c)
			}
		}
		walk(v)
		return r
	}
	if s.kind == "wild" {
		return children(v)
	}
	if s.kind == "key" {
		if s.key == "length()" {
			if n, ok := length(v); ok {
				return []interface{}{n}
			}
		}
		if x, ok := get(v, s.key); ok {
			return []interface{}{x}
		}
		return nil
	}
	if s.kind == "keyunion" {
		r := []interface{}{}
		for _, k := range s.keys {
			if x, ok := get(v, k); ok {
				r = append(r, x)
			}
		}
		return r
	}
	if s.kind == "idxunion" {
		r := []interface{}{}
		a, ok := v.([]interface{})
		if !ok {
			return nil
		}
		for _, n := range s.indexes {
			if n < 0 {
				n += len(a)
			}
			if n >= 0 && n < len(a) {
				r = append(r, a[n])
			}
		}
		return r
	}
	if s.kind == "script" {
		a, ok := v.([]interface{})
		if !ok {
			return nil
		}
		parts := strings.Split(strings.ReplaceAll(s.key, " ", ""), "-")
		if len(parts) == 2 && parts[0] == "@.length" {
			n, _ := strconv.Atoi(parts[1])
			i := len(a) - n
			if i >= 0 && i < len(a) {
				return []interface{}{a[i]}
			}
		}
		return nil
	}
	if s.kind == "filter" {
		r := []interface{}{}
		for _, x := range children(v) {
			if evalFilter(x, s.filter) {
				r = append(r, x)
			}
		}
		return r
	}
	return nil
}
func length(v interface{}) (int, bool) {
	switch x := v.(type) {
	case []interface{}:
		return len(x), true
	case *Map:
		return x.Len(), true
	case map[string]interface{}:
		return len(x), true
	case string:
		return len(x), true
	}
	return 0, false
}
func truth(v interface{}) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []interface{}:
		return len(x) > 0
	case *Map:
		return x.Len() > 0
	}
	return true
}
func evalFilter(v interface{}, f string) bool {
	f = strings.TrimSpace(f)
	if parts := strings.Split(f, "||"); len(parts) > 1 {
		for _, p := range parts {
			if evalFilter(v, p) {
				return true
			}
		}
		return false
	}
	if parts := strings.Split(f, "&&"); len(parts) > 1 {
		for _, p := range parts {
			if !evalFilter(v, p) {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(f, "(@.") {
		f = f[2:]
	} else if strings.HasPrefix(f, "@.") {
		f = f[2:]
	}
	if i := strings.IndexAny(f, "=!<>"); i >= 0 {
		field := strings.TrimSpace(f[:i])
		op := string(f[i])
		if i+1 < len(f) && f[i+1] == '=' {
			op += "="
		}
		rhs := strings.Trim(strings.TrimSpace(f[i+len(op):]), "'\"")
		x, ok := get(v, field)
		if !ok {
			return false
		}
		return compare(x, op, rhs)
	}
	var x interface{}
	var ok bool
	for _, part := range strings.Split(f, ".") {
		x, ok = get(v, part)
		if !ok {
			return false
		}
		v = x
	}
	return ok && truth(x)
}
func compare(x interface{}, op, r string) bool {
	if n, e := strconv.ParseFloat(r, 64); e == nil {
		a, ok := reflect.ValueOf(x).Convert(reflect.TypeOf(float64(0))).Interface().(float64)
		if !ok {
			return false
		}
		switch op {
		case "==":
			return a == n
		case "!=":
			return a != n
		case "<":
			return a < n
		case ">":
			return a > n
		case "<=":
			return a <= n
		case ">=":
			return a >= n
		}
	}
	return (fmt.Sprint(x) == r) == (op == "==")
}
