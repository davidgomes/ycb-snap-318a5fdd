// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	lengthFunctionName  = "length"
	unicodeEscapeDigits = 4
	hexBase             = 16
)

var simpleEscapes = map[byte]byte{
	'\\': '\\', '\'': '\'', '"': '"', '/': '/',
	'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t',
}

type pathParser struct {
	path string
	pos  int
}

func parsePath(path string) (jsonPath, error) {
	p := &pathParser{path: path}
	if !p.consume('$') {
		return nil, p.errorAt(0, "path must start with '$'")
	}
	segments, err := p.parseSegments(false)
	if err != nil {
		return nil, err
	}
	if !p.atEnd() {
		return nil, p.expected("'.' or '['")
	}
	return segments, nil
}

// parseSegments parses segments for as long as the input continues with one.
// Queries within filters are compared as single values, so when singular is
// set, segments that could select several values are rejected.
func (p *pathParser) parseSegments(singular bool) (jsonPath, error) {
	segments := jsonPath{}
	for {
		start := p.pos
		var segment pathSegment
		var err error

		switch {
		case p.consumeString(".."):
			segment, err = p.parseDescendantSegment()
		case p.consume('.'):
			var sel selector
			sel, err = p.parseDotSelector()
			segment = pathSegment{selectors: []selector{sel}}
		case p.peek() == '[':
			segment.selectors, err = p.parseBracketSelectors()
		default:
			return segments, nil
		}

		if err != nil {
			return nil, err
		}
		if singular && !segment.isSingular() {
			return nil, p.errorAt(start, "queries within filters must select a single value")
		}
		segments = append(segments, segment)
	}
}

func (s pathSegment) isSingular() bool {
	if s.descendant || len(s.selectors) != 1 {
		return false
	}
	switch s.selectors[0].(type) {
	case nameSelector, indexSelector, fromEndSelector, lengthSelector:
		return true
	}
	return false
}

// parseDescendantSegment parses what follows `..`: a name, `*`, `length()`
// or a bracketed selection.
func (p *pathParser) parseDescendantSegment() (pathSegment, error) {
	var selectors []selector
	var err error
	if p.peek() == '[' {
		selectors, err = p.parseBracketSelectors()
	} else {
		var sel selector
		sel, err = p.parseDotSelector()
		selectors = []selector{sel}
	}
	if err != nil {
		return pathSegment{}, err
	}
	if len(selectors) == 1 {
		if _, isWildcard := selectors[0].(wildcardSelector); isWildcard {
			selectors = []selector{selfSelector{}}
		}
	}
	return pathSegment{descendant: true, selectors: selectors}, nil
}

func (p *pathParser) parseDotSelector() (selector, error) {
	if p.consume('*') {
		return wildcardSelector{}, nil
	}
	name := p.scanWhile(isIdentifierRune)
	if name == "" {
		return nil, p.expected("a name or '*'")
	}
	if name == lengthFunctionName && p.consume('(') {
		if !p.consume(')') {
			return nil, p.expected("')'")
		}
		return lengthSelector{}, nil
	}
	return nameSelector(name), nil
}

func (p *pathParser) parseBracketSelectors() ([]selector, error) {
	p.pos++ // '['
	p.skipSpace()

	var sel selector
	var err error
	switch {
	case p.consume('*'):
		sel = wildcardSelector{}
	case p.consume('?'):
		var expr filterExpr
		expr, err = p.parseOr()
		sel = filterSelector{expr}
	case p.consume('('):
		sel, err = p.parseScript()
	default:
		return p.parseUnion()
	}
	if err != nil {
		return nil, err
	}

	p.skipSpace()
	if !p.consume(']') {
		return nil, p.expected("']'")
	}
	return []selector{sel}, nil
}

// parseUnion parses a comma separated list of quoted names and indices,
// including the closing bracket.
func (p *pathParser) parseUnion() ([]selector, error) {
	var selectors []selector
	for {
		p.skipSpace()
		sel, err := p.parseUnionMember()
		if err != nil {
			return nil, err
		}
		selectors = append(selectors, sel)

		p.skipSpace()
		if p.consume(']') {
			return selectors, nil
		}
		if !p.consume(',') {
			return nil, p.expected("',' or ']'")
		}
	}
}

func (p *pathParser) parseUnionMember() (selector, error) {
	switch c := p.peek(); {
	case c == '\'' || c == '"':
		name, err := p.parseString()
		return nameSelector(name), err
	case c == '-' || isDigit(rune(c)):
		index, err := p.parseInt()
		return indexSelector(index), err
	}
	return nil, p.expected("a quoted name or an index")
}

// parseScript parses the remainder of a `(@.length-N)` script expression,
// which selects an array item counted from the end.
func (p *pathParser) parseScript() (selector, error) {
	p.skipSpace()
	if !p.consume('@') {
		return nil, p.expected("'@'")
	}
	p.skipSpace()
	if !p.consumeString("." + lengthFunctionName) {
		return nil, p.expected("'.length'")
	}
	p.consumeString("()")
	p.skipSpace()

	offset := 0
	if sign := p.peek(); sign == '-' || sign == '+' {
		p.pos++
		p.skipSpace()
		n, err := p.parseDigits()
		if err != nil {
			return nil, err
		}
		offset = n
		if sign == '-' {
			offset = -n
		}
		p.skipSpace()
	}

	if !p.consume(')') {
		return nil, p.expected("')'")
	}
	return fromEndSelector(offset), nil
}

func (p *pathParser) parseOr() (filterExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if !p.consumeString("||") {
			return left, nil
		}
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = logicalExpr{isAnd: false, left: left, right: right}
	}
}

func (p *pathParser) parseAnd() (filterExpr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if !p.consumeString("&&") {
			return left, nil
		}
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = logicalExpr{isAnd: true, left: left, right: right}
	}
}

func (p *pathParser) parseComparison() (filterExpr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	op, found := p.consumeComparisonOp()
	if !found {
		if p.peek() == '=' {
			return nil, p.errorAt(p.pos, "unexpected '=', use '==' to test equality")
		}
		return left, nil
	}
	right, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	return comparisonExpr{op: op, left: left, right: right}, nil
}

func (p *pathParser) consumeComparisonOp() (comparisonOp, bool) {
	for _, op := range comparisonOps {
		if p.consumeString(string(op)) {
			return op, true
		}
	}
	return "", false
}

func (p *pathParser) parseUnary() (filterExpr, error) {
	p.skipSpace()
	if p.consume('!') {
		operand, err := p.parseUnary()
		return notExpr{operand}, err
	}
	return p.parsePrimary()
}

func (p *pathParser) parsePrimary() (filterExpr, error) {
	switch c := p.peek(); {
	case c == '(':
		p.pos++
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if !p.consume(')') {
			return nil, p.expected("')'")
		}
		return expr, nil
	case c == '@' || c == '$':
		p.pos++
		path, err := p.parseSegments(true)
		return queryExpr{relative: c == '@', path: path}, err
	case c == '\'' || c == '"':
		value, err := p.parseString()
		return literalExpr{value}, err
	case c == '-' || isDigit(rune(c)):
		value, err := p.parseNumber()
		return literalExpr{value}, err
	}
	return p.parseKeyword()
}

func (p *pathParser) parseKeyword() (filterExpr, error) {
	start := p.pos
	switch p.scanWhile(isKeywordRune) {
	case "true":
		return literalExpr{true}, nil
	case "false":
		return literalExpr{false}, nil
	case "null":
		return literalExpr{nil}, nil
	case lengthFunctionName:
		if p.consume('(') {
			arg, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			p.skipSpace()
			if !p.consume(')') {
				return nil, p.expected("')'")
			}
			return lengthCallExpr{arg}, nil
		}
	}
	p.pos = start
	return nil, p.expected("'@', '$', a literal or '('")
}

func (p *pathParser) parseNumber() (interface{}, error) {
	start := p.pos
	p.consume('-')
	if _, err := p.parseDigits(); err != nil {
		return nil, err
	}
	isFloat := false
	if p.consume('.') {
		isFloat = true
		if _, err := p.parseDigits(); err != nil {
			return nil, err
		}
	}
	if p.consume('e') || p.consume('E') {
		isFloat = true
		if !p.consume('+') {
			p.consume('-')
		}
		if _, err := p.parseDigits(); err != nil {
			return nil, err
		}
	}

	text := p.path[start:p.pos]
	if !isFloat {
		if value, err := strconv.ParseInt(text, 10, 64); err == nil {
			return value, nil
		}
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, p.errorAt(start, "number %s is out of range", text)
	}
	return value, nil
}

func (p *pathParser) parseInt() (int, error) {
	negative := p.consume('-')
	value, err := p.parseDigits()
	if negative {
		value = -value
	}
	return value, err
}

func (p *pathParser) parseDigits() (int, error) {
	digits := p.scanWhile(isDigit)
	if digits == "" {
		return 0, p.expected("a digit")
	}
	// Atoi clamps overflowing values, which are then out of range of any array.
	value, _ := strconv.Atoi(digits)
	return value, nil
}

func (p *pathParser) parseString() (string, error) {
	start := p.pos
	quote := p.path[p.pos]
	p.pos++

	var result strings.Builder
	for !p.atEnd() {
		switch c := p.path[p.pos]; c {
		case quote:
			p.pos++
			return result.String(), nil
		case '\\':
			if err := p.parseEscape(&result); err != nil {
				return "", err
			}
		default:
			result.WriteByte(c)
			p.pos++
		}
	}
	return "", p.errorAt(start, "unterminated string")
}

func (p *pathParser) parseEscape(result *strings.Builder) error {
	start := p.pos
	p.pos++ // '\'
	if p.atEnd() {
		return p.errorAt(start, "unterminated escape sequence")
	}

	c := p.path[p.pos]
	if unescaped, isSimple := simpleEscapes[c]; isSimple {
		p.pos++
		result.WriteByte(unescaped)
		return nil
	}
	if c != 'u' {
		r, _ := utf8.DecodeRuneInString(p.path[p.pos:])
		return p.errorAt(start, "invalid escape sequence '\\%c'", r)
	}

	p.pos++
	r, err := p.parseUnicodeEscape(start)
	if err != nil {
		return err
	}
	result.WriteRune(r)
	return nil
}

// parseUnicodeEscape parses the hex digits of a `\uXXXX` escape, combining a
// UTF-16 surrogate pair written as two consecutive escapes.
func (p *pathParser) parseUnicodeEscape(start int) (rune, error) {
	r, err := p.parseHex(start)
	if err != nil || !utf16.IsSurrogate(r) || !p.hasPrefix(`\u`) {
		return r, err
	}
	lowStart := p.pos
	p.pos += len(`\u`)
	low, err := p.parseHex(lowStart)
	if err != nil {
		return 0, err
	}
	if combined := utf16.DecodeRune(r, low); combined != unicode.ReplacementChar {
		return combined, nil
	}
	p.pos = lowStart
	return r, nil
}

func (p *pathParser) parseHex(escapeStart int) (rune, error) {
	end := p.pos + unicodeEscapeDigits
	if end > len(p.path) {
		return 0, p.errorAt(escapeStart, "invalid unicode escape sequence")
	}
	value, err := strconv.ParseUint(p.path[p.pos:end], hexBase, 32)
	if err != nil {
		return 0, p.errorAt(escapeStart, "invalid unicode escape sequence")
	}
	p.pos = end
	return rune(value), nil
}

func (p *pathParser) scanWhile(accept func(rune) bool) string {
	start := p.pos
	for !p.atEnd() {
		r, size := utf8.DecodeRuneInString(p.path[p.pos:])
		if !accept(r) {
			break
		}
		p.pos += size
	}
	return p.path[start:p.pos]
}

func (p *pathParser) skipSpace() {
	for !p.atEnd() && strings.IndexByte(" \t\n\r", p.path[p.pos]) >= 0 {
		p.pos++
	}
}

func (p *pathParser) atEnd() bool { return p.pos >= len(p.path) }

func (p *pathParser) peek() byte {
	if p.atEnd() {
		return 0
	}
	return p.path[p.pos]
}

func (p *pathParser) hasPrefix(prefix string) bool {
	return strings.HasPrefix(p.path[p.pos:], prefix)
}

func (p *pathParser) consume(c byte) bool {
	if p.atEnd() || p.path[p.pos] != c {
		return false
	}
	p.pos++
	return true
}

func (p *pathParser) consumeString(s string) bool {
	if !p.hasPrefix(s) {
		return false
	}
	p.pos += len(s)
	return true
}

func (p *pathParser) errorAt(pos int, format string, args ...interface{}) error {
	return &SyntaxError{Message: fmt.Sprintf(format, args...), Position: pos}
}

func (p *pathParser) expected(what string) error {
	if p.atEnd() {
		return p.errorAt(p.pos, "unexpected end of path, expected %s", what)
	}
	r, _ := utf8.DecodeRuneInString(p.path[p.pos:])
	return p.errorAt(p.pos, "unexpected character %q, expected %s", r, what)
}

func isIdentifierRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

func isKeywordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }
