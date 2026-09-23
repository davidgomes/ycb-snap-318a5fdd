// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// SyntaxError describes a malformed JSONPath expression.
type SyntaxError struct {
	Message  string
	Position int
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("syntax error at position %d: %s", e.Position, e.Message)
}

// Query evaluates the JSONPath expression against doc and returns all matches
// (an empty, non-nil slice when nothing matches).
func Query(doc interface{}, path string) ([]interface{}, error) {
	segments, err := parseJSONPath(path)
	if err != nil {
		return nil, err
	}
	return evalSegments(segments, doc, doc), nil
}

// QueryOne evaluates the JSONPath expression against doc and returns the first match.
func QueryOne(doc interface{}, path string) (interface{}, bool, error) {
	results, err := Query(doc, path)
	if err != nil {
		return nil, false, err
	}
	if len(results) == 0 {
		return nil, false, nil
	}
	return results[0], true, nil
}

// --- evaluation ---

type jpSegment struct {
	recursive bool
	// nil selector on a recursive segment means `..*` (descendants including self)
	selector jpSelector
}

type jpSelector interface {
	selectFrom(node, root interface{}, out []interface{}) []interface{}
}

func evalSegments(segments []jpSegment, current, root interface{}) []interface{} {
	nodes := []interface{}{current}
	for _, seg := range segments {
		next := []interface{}{}
		for _, node := range nodes {
			if !seg.recursive {
				next = seg.selector.selectFrom(node, root, next)
				continue
			}
			for _, desc := range jpDescendantsOrSelf(node, nil) {
				if seg.selector == nil {
					next = append(next, desc)
				} else {
					next = seg.selector.selectFrom(desc, root, next)
				}
			}
		}
		nodes = next
	}
	return nodes
}

type jpKeySelector struct {
	key string
	// inside filter expressions `@.length` on a non-map yields its length
	lengthFallback bool
}

func (s jpKeySelector) selectFrom(node, _ interface{}, out []interface{}) []interface{} {
	if val, found := jpMapGet(node, s.key); found {
		return append(out, val)
	}
	if s.lengthFallback && s.key == "length" && !jpIsMap(node) {
		if l, ok := jpLength(node); ok {
			return append(out, l)
		}
	}
	return out
}

type jpWildcardSelector struct{}

func (jpWildcardSelector) selectFrom(node, _ interface{}, out []interface{}) []interface{} {
	children, _ := jpChildren(node)
	return append(out, children...)
}

type jpIndexSelector struct{ index int }

func (s jpIndexSelector) selectFrom(node, _ interface{}, out []interface{}) []interface{} {
	arr, ok := node.([]interface{})
	if !ok {
		return out
	}
	idx := s.index
	if idx < 0 {
		idx += len(arr)
	}
	if idx < 0 || idx >= len(arr) {
		return out
	}
	return append(out, arr[idx])
}

type jpScriptSelector struct{ offset int }

func (s jpScriptSelector) selectFrom(node, _ interface{}, out []interface{}) []interface{} {
	arr, ok := node.([]interface{})
	if !ok {
		return out
	}
	idx := len(arr) + s.offset
	if idx < 0 || idx >= len(arr) {
		return out
	}
	return append(out, arr[idx])
}

type jpUnionSelector struct{ selectors []jpSelector }

func (s jpUnionSelector) selectFrom(node, root interface{}, out []interface{}) []interface{} {
	for _, sel := range s.selectors {
		out = sel.selectFrom(node, root, out)
	}
	return out
}

type jpLengthSelector struct{}

func (jpLengthSelector) selectFrom(node, _ interface{}, out []interface{}) []interface{} {
	if l, ok := jpLength(node); ok {
		return append(out, l)
	}
	return out
}

type jpFilterSelector struct{ expr jpExpr }

func (s jpFilterSelector) selectFrom(node, root interface{}, out []interface{}) []interface{} {
	children, _ := jpChildren(node)
	for _, child := range children {
		if s.expr.eval(child, root) {
			out = append(out, child)
		}
	}
	return out
}

type jpExpr interface {
	eval(current, root interface{}) bool
}

type jpOrExpr struct{ left, right jpExpr }

func (e jpOrExpr) eval(current, root interface{}) bool {
	return e.left.eval(current, root) || e.right.eval(current, root)
}

type jpAndExpr struct{ left, right jpExpr }

func (e jpAndExpr) eval(current, root interface{}) bool {
	return e.left.eval(current, root) && e.right.eval(current, root)
}

type jpExistenceExpr struct{ operand jpOperand }

func (e jpExistenceExpr) eval(current, root interface{}) bool {
	val, found := e.operand.value(current, root)
	return found && jpTruthy(val)
}

type jpComparisonExpr struct {
	left  jpOperand
	op    string
	right jpOperand
}

func (e jpComparisonExpr) eval(current, root interface{}) bool {
	left, found := e.left.value(current, root)
	if !found {
		return false
	}
	right, found := e.right.value(current, root)
	if !found {
		return false
	}
	return jpCompare(left, e.op, right)
}

type jpOperand interface {
	value(current, root interface{}) (interface{}, bool)
}

type jpLiteralOperand struct{ val interface{} }

func (o jpLiteralOperand) value(_, _ interface{}) (interface{}, bool) { return o.val, true }

type jpPathOperand struct {
	fromRoot bool
	segments []jpSegment
}

func (o jpPathOperand) value(current, root interface{}) (interface{}, bool) {
	base := current
	if o.fromRoot {
		base = root
	}
	results := evalSegments(o.segments, base, root)
	if len(results) == 0 {
		return nil, false
	}
	return results[0], true
}

// --- value helpers ---

func jpIsMap(node interface{}) bool {
	switch node.(type) {
	case *Map, map[string]interface{}, map[interface{}]interface{}:
		return true
	}
	return false
}

func jpMapGet(node interface{}, key string) (interface{}, bool) {
	switch typed := node.(type) {
	case *Map:
		if typed == nil {
			return nil, false
		}
		return typed.Get(key)
	case map[string]interface{}:
		val, found := typed[key]
		return val, found
	case map[interface{}]interface{}:
		val, found := typed[key]
		return val, found
	}
	return nil, false
}

func jpChildren(node interface{}) ([]interface{}, bool) {
	switch typed := node.(type) {
	case []interface{}:
		return typed, true
	case *Map:
		if typed == nil {
			return nil, true
		}
		var vals []interface{}
		typed.Iterate(func(_, v interface{}) { vals = append(vals, v) })
		return vals, true
	case map[string]interface{}:
		var vals []interface{}
		for _, k := range (Conversion{}).sortedMapKeys((Conversion{}).mapKeysFromStringMap(typed)) {
			vals = append(vals, typed[k.(string)])
		}
		return vals, true
	case map[interface{}]interface{}:
		var vals []interface{}
		for _, k := range (Conversion{}).sortedMapKeys((Conversion{}).mapKeysFromInterfaceMap(typed)) {
			vals = append(vals, typed[k])
		}
		return vals, true
	}
	return nil, false
}

func jpDescendantsOrSelf(node interface{}, out []interface{}) []interface{} {
	out = append(out, node)
	children, _ := jpChildren(node)
	for _, child := range children {
		out = jpDescendantsOrSelf(child, out)
	}
	return out
}

func jpLength(node interface{}) (int, bool) {
	switch typed := node.(type) {
	case string:
		return utf8.RuneCountInString(typed), true
	case []interface{}:
		return len(typed), true
	case *Map:
		if typed == nil {
			return 0, true
		}
		return typed.Len(), true
	case map[string]interface{}:
		return len(typed), true
	case map[interface{}]interface{}:
		return len(typed), true
	}
	return 0, false
}

func jpTruthy(val interface{}) bool {
	if val == nil {
		return false
	}
	switch typed := val.(type) {
	case bool:
		return typed
	case string:
		return typed != ""
	}
	if f, ok := jpFloat(val); ok {
		return f != 0
	}
	if l, ok := jpLength(val); ok {
		return l != 0
	}
	return true
}

func jpInt(val interface{}) (int64, bool) {
	switch typed := val.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		if uint64(typed) <= math.MaxInt64 {
			return int64(typed), true
		}
	case uint8:
		return int64(typed), true
	case uint16:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		if typed <= math.MaxInt64 {
			return int64(typed), true
		}
	}
	return 0, false
}

func jpFloat(val interface{}) (float64, bool) {
	switch typed := val.(type) {
	case float32:
		return float64(typed), true
	case float64:
		return typed, true
	case uint:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	}
	if i, ok := jpInt(val); ok {
		return float64(i), true
	}
	return 0, false
}

func jpCompare(left interface{}, op string, right interface{}) bool {
	if li, ok := jpInt(left); ok {
		if ri, ok := jpInt(right); ok {
			return jpOrdered(jpCmp(li, ri), op)
		}
	}
	if lf, ok := jpFloat(left); ok {
		if rf, ok := jpFloat(right); ok {
			if math.IsNaN(lf) || math.IsNaN(rf) {
				return op == "!="
			}
			return jpOrdered(jpCmp(lf, rf), op)
		}
	}
	if ls, ok := left.(string); ok {
		if rs, ok := right.(string); ok {
			return jpOrdered(strings.Compare(ls, rs), op)
		}
	}
	switch op {
	case "==":
		return reflect.DeepEqual(left, right)
	case "!=":
		return !reflect.DeepEqual(left, right)
	}
	return false
}

func jpCmp[T int64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func jpOrdered(cmp int, op string) bool {
	switch op {
	case "==":
		return cmp == 0
	case "!=":
		return cmp != 0
	case "<":
		return cmp < 0
	case ">":
		return cmp > 0
	case "<=":
		return cmp <= 0
	case ">=":
		return cmp >= 0
	}
	return false
}

// --- parsing ---

type jpParser struct {
	src string
	pos int
}

func parseJSONPath(path string) ([]jpSegment, error) {
	p := &jpParser{src: path}
	if p.peek() != '$' {
		return nil, p.errorf("path must start with '$'")
	}
	p.pos++
	segments, err := p.parseSegments(false)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.src) {
		return nil, p.errorf("unexpected character %q", p.src[p.pos])
	}
	return segments, nil
}

func (p *jpParser) errorf(format string, args ...interface{}) *SyntaxError {
	return &SyntaxError{Message: fmt.Sprintf(format, args...), Position: p.pos}
}

func (p *jpParser) peek() byte {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0
}

func (p *jpParser) skipWS() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jpParser) expect(c byte) error {
	if p.peek() != c {
		if p.pos >= len(p.src) {
			return p.errorf("expected %q but reached end of path", c)
		}
		return p.errorf("expected %q but found %q", c, p.src[p.pos])
	}
	p.pos++
	return nil
}

func isJPIdentChar(c byte) bool {
	return c == '_' || c == '-' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// parseSegments consumes consecutive dot and bracket segments, stopping at
// the first character that cannot start a segment.
func (p *jpParser) parseSegments(inFilter bool) ([]jpSegment, error) {
	var segments []jpSegment
	for {
		switch p.peek() {
		case '.':
			seg, err := p.parseDotSegment(inFilter)
			if err != nil {
				return nil, err
			}
			segments = append(segments, seg)
		case '[':
			sel, err := p.parseBracket(inFilter)
			if err != nil {
				return nil, err
			}
			segments = append(segments, jpSegment{selector: sel})
		default:
			return segments, nil
		}
	}
}

func (p *jpParser) parseDotSegment(inFilter bool) (jpSegment, error) {
	p.pos++
	recursive := false
	if p.peek() == '.' {
		recursive = true
		p.pos++
	}
	c := p.peek()
	switch {
	case c == '*':
		p.pos++
		if recursive {
			return jpSegment{recursive: true}, nil
		}
		return jpSegment{selector: jpWildcardSelector{}}, nil
	case c == '[' && recursive:
		sel, err := p.parseBracket(inFilter)
		return jpSegment{recursive: true, selector: sel}, err
	case isJPIdentChar(c):
		return jpSegment{recursive: recursive, selector: p.parseNameSelector(inFilter)}, nil
	}
	if recursive {
		return jpSegment{}, p.errorf("expected key, '*' or '[' after '..'")
	}
	return jpSegment{}, p.errorf("expected key or '*' after '.'")
}

func (p *jpParser) parseNameSelector(inFilter bool) jpSelector {
	start := p.pos
	for p.pos < len(p.src) && isJPIdentChar(p.src[p.pos]) {
		p.pos++
	}
	name := p.src[start:p.pos]
	if name == "length" && strings.HasPrefix(p.src[p.pos:], "()") {
		p.pos += 2
		return jpLengthSelector{}
	}
	return jpKeySelector{key: name, lengthFallback: inFilter}
}

func (p *jpParser) parseBracket(inFilter bool) (jpSelector, error) {
	p.pos++
	p.skipWS()

	var sel jpSelector
	switch p.peek() {
	case '?':
		p.pos++
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		sel = jpFilterSelector{expr}
	case '(':
		script, err := p.parseScript()
		if err != nil {
			return nil, err
		}
		sel = script
	default:
		var items []jpSelector
		for {
			p.skipWS()
			item, err := p.parseUnionItem()
			if err != nil {
				return nil, err
			}
			items = append(items, item)
			p.skipWS()
			if p.peek() != ',' {
				break
			}
			p.pos++
		}
		if len(items) == 1 {
			sel = items[0]
		} else {
			sel = jpUnionSelector{items}
		}
	}

	p.skipWS()
	if err := p.expect(']'); err != nil {
		return nil, err
	}
	return sel, nil
}

func (p *jpParser) parseUnionItem() (jpSelector, error) {
	c := p.peek()
	switch {
	case c == '*':
		p.pos++
		return jpWildcardSelector{}, nil
	case c == '\'' || c == '"':
		key, err := p.parseString()
		if err != nil {
			return nil, err
		}
		return jpKeySelector{key: key}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		idx, err := p.parseInt()
		if err != nil {
			return nil, err
		}
		return jpIndexSelector{idx}, nil
	case c == 0:
		return nil, p.errorf("unexpected end of path in brackets")
	}
	return nil, p.errorf("expected quoted key, index or '*' but found %q", c)
}

// parseScript parses `(@.length)`, `(@.length-N)` or `(@.length+N)`.
func (p *jpParser) parseScript() (jpSelector, error) {
	p.pos++
	p.skipWS()
	if err := p.expect('@'); err != nil {
		return nil, err
	}
	p.skipWS()
	if err := p.expect('.'); err != nil {
		return nil, err
	}
	p.skipWS()
	if !strings.HasPrefix(p.src[p.pos:], "length") ||
		(p.pos+6 < len(p.src) && p.src[p.pos+6] != '-' && isJPIdentChar(p.src[p.pos+6])) {
		return nil, p.errorf("expected 'length' in script expression")
	}
	p.pos += len("length")
	p.skipWS()

	offset := 0
	if c := p.peek(); c == '-' || c == '+' {
		p.pos++
		p.skipWS()
		if c := p.peek(); c < '0' || c > '9' {
			return nil, p.errorf("expected integer in script expression")
		}
		n, err := p.parseInt()
		if err != nil {
			return nil, err
		}
		offset = n
		if c == '-' {
			offset = -n
		}
	}
	p.skipWS()
	if err := p.expect(')'); err != nil {
		return nil, err
	}
	return jpScriptSelector{offset}, nil
}

func (p *jpParser) parseInt() (int, error) {
	start := p.pos
	if p.peek() == '-' {
		p.pos++
	}
	digitsStart := p.pos
	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	if p.pos == digitsStart {
		return 0, p.errorf("expected digits")
	}
	n, err := strconv.Atoi(p.src[start:p.pos])
	if err != nil {
		return 0, &SyntaxError{Message: fmt.Sprintf("invalid integer %q", p.src[start:p.pos]), Position: start}
	}
	return n, nil
}

func (p *jpParser) parseString() (string, error) {
	start := p.pos
	quote := p.src[p.pos]
	p.pos++
	var sb strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == quote:
			p.pos++
			return sb.String(), nil
		case c == '\\':
			if err := p.parseEscape(&sb); err != nil {
				return "", err
			}
		default:
			sb.WriteByte(c)
			p.pos++
		}
	}
	return "", &SyntaxError{Message: "unterminated string", Position: start}
}

func (p *jpParser) parseEscape(sb *strings.Builder) error {
	escPos := p.pos
	p.pos++
	if p.pos >= len(p.src) {
		return &SyntaxError{Message: "unterminated escape sequence", Position: escPos}
	}
	c := p.src[p.pos]
	p.pos++
	switch c {
	case '\\', '\'', '"', '/':
		sb.WriteByte(c)
	case 'b':
		sb.WriteByte('\b')
	case 'f':
		sb.WriteByte('\f')
	case 'n':
		sb.WriteByte('\n')
	case 'r':
		sb.WriteByte('\r')
	case 't':
		sb.WriteByte('\t')
	case 'u':
		r, err := p.parseHex4(escPos)
		if err != nil {
			return err
		}
		if utf16.IsSurrogate(r) && strings.HasPrefix(p.src[p.pos:], `\u`) {
			save := p.pos
			p.pos += 2
			r2, err := p.parseHex4(save)
			if err != nil {
				return err
			}
			if combined := utf16.DecodeRune(r, r2); combined != utf8.RuneError {
				r = combined
			} else {
				p.pos = save
			}
		}
		sb.WriteRune(r)
	default:
		return &SyntaxError{Message: fmt.Sprintf("invalid escape sequence '\\%c'", c), Position: escPos}
	}
	return nil
}

func (p *jpParser) parseHex4(escPos int) (rune, error) {
	if p.pos+4 > len(p.src) {
		return 0, &SyntaxError{Message: "invalid unicode escape sequence", Position: escPos}
	}
	n, err := strconv.ParseUint(p.src[p.pos:p.pos+4], 16, 32)
	if err != nil {
		return 0, &SyntaxError{Message: "invalid unicode escape sequence", Position: escPos}
	}
	p.pos += 4
	return rune(n), nil
}

// Filter grammar:
//   or      := and ('||' and)*
//   and     := primary ('&&' primary)*
//   primary := '(' or ')' | operand [op operand]

func (p *jpParser) parseOr() (jpExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		p.skipWS()
		if !strings.HasPrefix(p.src[p.pos:], "||") {
			return left, nil
		}
		p.pos += 2
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = jpOrExpr{left, right}
	}
}

func (p *jpParser) parseAnd() (jpExpr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		p.skipWS()
		if !strings.HasPrefix(p.src[p.pos:], "&&") {
			return left, nil
		}
		p.pos += 2
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = jpAndExpr{left, right}
	}
}

func (p *jpParser) parsePrimary() (jpExpr, error) {
	p.skipWS()
	if p.peek() == '(' {
		p.pos++
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipWS()
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		return expr, nil
	}

	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	op, err := p.parseOperator()
	if err != nil {
		return nil, err
	}
	if op == "" {
		return jpExistenceExpr{left}, nil
	}
	p.skipWS()
	right, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	return jpComparisonExpr{left: left, op: op, right: right}, nil
}

func (p *jpParser) parseOperator() (string, error) {
	rest := p.src[p.pos:]
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if strings.HasPrefix(rest, op) {
			p.pos += len(op)
			return op, nil
		}
	}
	if c := p.peek(); c == '=' || c == '!' {
		return "", p.errorf("invalid operator %q", c)
	}
	return "", nil
}

func (p *jpParser) parseOperand() (jpOperand, error) {
	c := p.peek()
	switch {
	case c == '@' || c == '$':
		p.pos++
		segments, err := p.parseSegments(true)
		if err != nil {
			return nil, err
		}
		return jpPathOperand{fromRoot: c == '$', segments: segments}, nil
	case c == '\'' || c == '"':
		s, err := p.parseString()
		if err != nil {
			return nil, err
		}
		return jpLiteralOperand{s}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		return p.parseNumber()
	case c >= 'a' && c <= 'z':
		start := p.pos
		for p.pos < len(p.src) && isJPIdentChar(p.src[p.pos]) {
			p.pos++
		}
		word := p.src[start:p.pos]
		switch word {
		case "true":
			return jpLiteralOperand{true}, nil
		case "false":
			return jpLiteralOperand{false}, nil
		case "null":
			return jpLiteralOperand{nil}, nil
		}
		p.pos = start
		return nil, p.errorf("unknown literal %q", word)
	case c == 0:
		return nil, p.errorf("unexpected end of path in filter expression")
	}
	return nil, p.errorf("expected value but found %q", c)
}

func (p *jpParser) parseNumber() (jpOperand, error) {
	start := p.pos
	digits := func() int {
		n := 0
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
			n++
		}
		return n
	}
	if p.peek() == '-' {
		p.pos++
	}
	if digits() == 0 {
		return nil, p.errorf("expected digits")
	}
	isFloat := false
	if p.peek() == '.' {
		isFloat = true
		p.pos++
		if digits() == 0 {
			return nil, p.errorf("expected digits after decimal point")
		}
	}
	if c := p.peek(); c == 'e' || c == 'E' {
		isFloat = true
		p.pos++
		if c := p.peek(); c == '+' || c == '-' {
			p.pos++
		}
		if digits() == 0 {
			return nil, p.errorf("expected digits in exponent")
		}
	}
	text := p.src[start:p.pos]
	if !isFloat {
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return jpLiteralOperand{n}, nil
		}
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, &SyntaxError{Message: fmt.Sprintf("invalid number %q", text), Position: start}
	}
	return jpLiteralOperand{f}, nil
}
