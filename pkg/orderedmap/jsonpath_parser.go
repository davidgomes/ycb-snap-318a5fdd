// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	unicodeEscapeDigits = 4
	descentLen          = 2
	logicOpLen          = 2
	floatBitSize        = 64
	intBase             = 10
	hexBase             = 16
	runeBitSize         = 32
	emptyLit            = ""
	unterminatedString  = "unterminated string"
)

type parser struct {
	s    string
	i    int
	expr bool
}

func parsePath(path string) ([]segment, error) {
	if path == emptyLit || path[0] != '$' {
		return nil, &SyntaxError{
			Position: 0,
			Message:  "path must start with '$'",
		}
	}
	p := &parser{s: path, i: 1}
	segs, err := p.parseTopSegments()
	if err != nil {
		return nil, err
	}
	if p.i != len(p.s) {
		return nil, p.failHere("unexpected trailing input")
	}
	return segs, nil
}

func (p *parser) parseTopSegments() ([]segment, error) {
	var segs []segment
	for p.i < len(p.s) {
		seg, err := p.parseSegment()
		if err != nil {
			return nil, err
		}
		segs = append(segs, seg)
	}
	return segs, nil
}

func (p *parser) parseRelSegments() ([]segment, error) {
	var segs []segment
	for !p.atRelEnd() {
		seg, err := p.parseSegment()
		if err != nil {
			return nil, err
		}
		segs = append(segs, seg)
	}
	return segs, nil
}

func (p *parser) atRelEnd() bool {
	p.skipSpace()
	if p.i >= len(p.s) || isExprBoundary(p.s[p.i]) {
		return true
	}
	c := p.s[p.i]
	return c != '.' && c != '['
}

func isExprBoundary(c byte) bool {
	switch c {
	case ')', ']', ',', '=', '!', '<', '>', '&', '|':
		return true
	default:
		return false
	}
}

func (p *parser) parseSegment() (segment, error) {
	if p.i >= len(p.s) {
		return nil, p.failHere("unexpected end of path")
	}
	switch {
	case strings.HasPrefix(p.s[p.i:], ".."):
		return p.parseRecursive()
	case p.s[p.i] == '.':
		return p.parseDot()
	case p.s[p.i] == '[':
		return p.parseBracket()
	default:
		return nil, p.failHere("expected '.' or '['")
	}
}

func (p *parser) parseDot() (segment, error) {
	p.i++
	if p.expr {
		p.skipSpace()
	}
	if p.i < len(p.s) && p.s[p.i] == '*' {
		p.i++
		return wildSegment{}, nil
	}
	id, ok := p.readIdent()
	if !ok {
		return nil, p.failHere("expected identifier")
	}
	if seg, ok, err := p.lengthCall(id); ok || err != nil {
		return seg, err
	}
	return childSegment{key: id}, nil
}

func (p *parser) parseRecursive() (segment, error) {
	p.i += descentLen
	if p.expr {
		p.skipSpace()
	}
	return p.recursiveSelector()
}

func (p *parser) recursiveSelector() (segment, error) {
	if p.i < len(p.s) && p.s[p.i] == '*' {
		p.i++
		return recursiveSegment{all: true}, nil
	}
	if p.i < len(p.s) && p.s[p.i] == '[' {
		return p.recursiveBracket()
	}
	return p.recursiveName()
}

func (p *parser) recursiveBracket() (segment, error) {
	inner, err := p.parseBracket()
	if err != nil {
		return nil, err
	}
	return recursiveSegment{inner: inner}, nil
}

func (p *parser) recursiveName() (segment, error) {
	id, ok := p.readIdent()
	if !ok {
		return nil, p.failHere("expected selector after '..'")
	}
	seg, called, err := p.lengthCall(id)
	if err != nil {
		return nil, err
	}
	if called {
		return recursiveSegment{inner: seg}, nil
	}
	return recursiveSegment{inner: childSegment{key: id}}, nil
}

func (p *parser) lengthCall(id string) (segment, bool, error) {
	if id != "length" {
		return nil, false, nil
	}
	saved := p.i
	p.skipSpace()
	if p.i >= len(p.s) || p.s[p.i] != '(' {
		p.i = saved
		return nil, false, nil
	}
	if err := p.consumeCallParens(); err != nil {
		return nil, true, err
	}
	return lengthSegment{}, true, nil
}

func (p *parser) consumeCallParens() error {
	if err := p.expect('('); err != nil {
		return err
	}
	p.skipSpace()
	return p.expect(')')
}

func (p *parser) parseBracket() (segment, error) {
	open := p.i
	p.i++
	p.skipSpace()
	if p.i >= len(p.s) {
		return nil, p.fail(open, "unclosed '['")
	}
	switch p.s[p.i] {
	case '?':
		return p.parseFilter()
	case '(':
		return p.parseScript()
	case '*':
		p.i++
		p.skipSpace()
		if err := p.expect(']'); err != nil {
			return nil, err
		}
		return wildSegment{}, nil
	default:
		return p.parseUnion()
	}
}

func (p *parser) parseUnion() (segment, error) {
	var parts []unionPart
	for {
		part, err := p.parseUnionPart()
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
		more, err := p.moreUnionParts()
		if err != nil {
			return nil, err
		}
		if !more {
			break
		}
	}
	return segmentFromParts(parts), nil
}

func (p *parser) moreUnionParts() (bool, error) {
	p.skipSpace()
	if p.i < len(p.s) && p.s[p.i] == ',' {
		p.i++
		return true, nil
	}
	if err := p.expect(']'); err != nil {
		return false, err
	}
	return false, nil
}

func segmentFromParts(parts []unionPart) segment {
	if len(parts) == 1 {
		if parts[0].isIndex {
			return indexSegment{index: parts[0].index}
		}
		return childSegment{key: parts[0].key}
	}
	return unionSegment{parts: parts}
}

func (p *parser) parseUnionPart() (unionPart, error) {
	p.skipSpace()
	if p.i >= len(p.s) {
		return unionPart{}, p.failHere("unclosed '['")
	}
	if isQuote(p.s[p.i]) {
		return p.unionString()
	}
	if isIndexStart(p.s[p.i]) {
		return p.unionIndex()
	}
	return unionPart{}, p.failHere("expected string or index")
}

func isQuote(c byte) bool {
	return c == '\'' || c == '"'
}

func (p *parser) unionString() (unionPart, error) {
	key, err := p.parseString()
	if err != nil {
		return unionPart{}, err
	}
	return unionPart{key: key}, nil
}

func (p *parser) unionIndex() (unionPart, error) {
	n, err := p.readInt()
	if err != nil {
		return unionPart{}, err
	}
	return unionPart{isIndex: true, index: n}, nil
}

func isIndexStart(c byte) bool {
	return c == '-' || isDigit(c)
}

func (p *parser) parseFilter() (segment, error) {
	if err := p.expect('?'); err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expect('('); err != nil {
		return nil, err
	}
	saved := p.expr
	p.expr = true
	ex, err := p.parseOr()
	p.expr = saved
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expect(')'); err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expect(']'); err != nil {
		return nil, err
	}
	return filterSegment{e: ex}, nil
}

func (p *parser) parseScript() (segment, error) {
	offset, err := p.scriptOffset()
	if err != nil {
		return nil, err
	}
	return scriptSegment{offset: offset}, nil
}

func (p *parser) scriptOffset() (int, error) {
	if err := p.expectScriptHead(); err != nil {
		return 0, err
	}
	if err := p.optionalLengthCall(); err != nil {
		return 0, err
	}
	return p.scriptTail()
}

func (p *parser) expectScriptHead() error {
	if err := p.expect('('); err != nil {
		return err
	}
	p.skipSpace()
	if err := p.expect('@'); err != nil {
		return err
	}
	p.skipSpace()
	if err := p.expect('.'); err != nil {
		return err
	}
	p.skipSpace()
	if !p.consumeKeyword("length") {
		return p.failHere("expected length")
	}
	return nil
}

func (p *parser) optionalLengthCall() error {
	p.skipSpace()
	if p.i >= len(p.s) || p.s[p.i] != '(' {
		return nil
	}
	if err := p.consumeCallParens(); err != nil {
		return err
	}
	p.skipSpace()
	return nil
}

func (p *parser) scriptTail() (int, error) {
	if err := p.expect('-'); err != nil {
		return 0, err
	}
	p.skipSpace()
	offset, err := p.readPlainInt()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if err := p.expect(')'); err != nil {
		return 0, err
	}
	p.skipSpace()
	if err := p.expect(']'); err != nil {
		return 0, err
	}
	return offset, nil
}

func (p *parser) parseOr() (expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.sees("||") {
		p.i += logicOpLen
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orExpr{left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (expr, error) {
	left, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for p.sees("&&") {
		p.i += logicOpLen
		right, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		left = andExpr{left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseCmp() (expr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	op, ok := p.parseOp()
	if !ok {
		return left, nil
	}
	right, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	return cmpExpr{op: op, left: left, right: right}, nil
}

func (p *parser) parseOp() (string, bool) {
	p.skipSpace()
	ops := []string{"==", "!=", "<=", ">=", "<", ">"}
	for _, op := range ops {
		if strings.HasPrefix(p.s[p.i:], op) {
			p.i += len(op)
			return op, true
		}
	}
	return emptyLit, false
}

func (p *parser) parsePrimary() (expr, error) {
	p.skipSpace()
	if p.i >= len(p.s) {
		return nil, p.failHere("expected value")
	}
	switch p.s[p.i] {
	case '(':
		return p.parseGroup()
	case '@':
		return p.parseAt()
	case '\'', '"':
		return p.parseStringLit()
	default:
		return p.parseScalar()
	}
}

func (p *parser) parseGroup() (expr, error) {
	if err := p.expect('('); err != nil {
		return nil, err
	}
	ex, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if err := p.expect(')'); err != nil {
		return nil, err
	}
	return ex, nil
}

func (p *parser) parseAt() (expr, error) {
	if err := p.expect('@'); err != nil {
		return nil, err
	}
	segs, err := p.parseRelSegments()
	if err != nil {
		return nil, err
	}
	return pathExpr{segs: segs}, nil
}

func (p *parser) parseStringLit() (expr, error) {
	val, err := p.parseString()
	if err != nil {
		return nil, err
	}
	return litExpr{val: val}, nil
}

func (p *parser) parseScalar() (expr, error) {
	if lit, ok, err := p.parseKeywordLit(); ok || err != nil {
		return lit, err
	}
	if p.i < len(p.s) && (p.s[p.i] == '-' || isDigit(p.s[p.i])) {
		return p.parseNumberLit()
	}
	return nil, p.failHere("expected value")
}

func (p *parser) parseKeywordLit() (expr, bool, error) {
	switch {
	case p.consumeKeyword("true"):
		return litExpr{val: true}, true, nil
	case p.consumeKeyword("false"):
		return litExpr{val: false}, true, nil
	case p.consumeKeyword("null"):
		return litExpr{val: nil}, true, nil
	default:
		return nil, false, nil
	}
}

func (p *parser) parseNumberLit() (expr, error) {
	start := p.i
	tok, isFloat, err := p.scanNumber()
	if err != nil {
		return nil, err
	}
	if !isFloat {
		return integerLiteral(tok, start)
	}
	f, err := strconv.ParseFloat(tok, floatBitSize)
	if err != nil {
		return nil, p.fail(start, "invalid number")
	}
	return litExpr{val: f}, nil
}

func integerLiteral(tok string, start int) (expr, error) {
	n, err := strconv.ParseInt(tok, intBase, floatBitSize)
	if err != nil {
		f, ferr := strconv.ParseFloat(tok, floatBitSize)
		if ferr != nil {
			return nil, &SyntaxError{
				Position: start,
				Message:  "invalid number",
			}
		}
		return litExpr{val: f}, nil
	}
	if n >= math.MinInt && n <= math.MaxInt {
		return litExpr{val: int(n)}, nil
	}
	return litExpr{val: n}, nil
}

func (p *parser) scanNumber() (string, bool, error) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	if p.i >= len(p.s) || !isDigit(p.s[p.i]) {
		return emptyLit, false, p.fail(start, "expected number")
	}
	p.consumeDigits()
	isFloat := p.consumeFraction()
	exp, err := p.consumeExponent(start)
	if err != nil {
		return emptyLit, false, err
	}
	return p.s[start:p.i], isFloat || exp, nil
}

func (p *parser) consumeDigits() {
	for p.i < len(p.s) && isDigit(p.s[p.i]) {
		p.i++
	}
}

func (p *parser) consumeFraction() bool {
	if p.i+1 >= len(p.s) || p.s[p.i] != '.' || !isDigit(p.s[p.i+1]) {
		return false
	}
	p.i++
	p.consumeDigits()
	return true
}

func (p *parser) consumeExponent(start int) (bool, error) {
	if !p.atExponent() {
		return false, nil
	}
	p.i++
	p.consumeExpSign()
	if p.i >= len(p.s) || !isDigit(p.s[p.i]) {
		return false, p.fail(start, "invalid exponent")
	}
	p.consumeDigits()
	return true, nil
}

func (p *parser) atExponent() bool {
	if p.i >= len(p.s) {
		return false
	}
	c := p.s[p.i]
	return c == 'e' || c == 'E'
}

func (p *parser) consumeExpSign() {
	if p.i >= len(p.s) {
		return
	}
	if p.s[p.i] == '+' || p.s[p.i] == '-' {
		p.i++
	}
}

func (p *parser) parseString() (string, error) {
	quote, start, err := p.openString()
	if err != nil {
		return emptyLit, err
	}
	var b strings.Builder
	for p.i < len(p.s) {
		done, err := p.appendStringChar(&b, quote, start)
		if err != nil {
			return emptyLit, err
		}
		if done {
			return b.String(), nil
		}
	}
	return emptyLit, p.fail(start, unterminatedString)
}

func (p *parser) openString() (byte, int, error) {
	if p.i >= len(p.s) {
		return 0, p.i, p.failHere("expected string")
	}
	quote := p.s[p.i]
	if !isQuote(quote) {
		return 0, p.i, p.failHere("expected string")
	}
	start := p.i
	p.i++
	return quote, start, nil
}

func (p *parser) appendStringChar(
	b *strings.Builder,
	quote byte,
	start int,
) (bool, error) {
	c := p.s[p.i]
	if c == quote {
		p.i++
		return true, nil
	}
	if c == '\n' || c == '\r' {
		return false, p.fail(p.i, unterminatedString)
	}
	if c != '\\' {
		b.WriteByte(c)
		p.i++
		return false, nil
	}
	return false, p.writeEscape(b, start)
}

func (p *parser) writeEscape(b *strings.Builder, start int) error {
	p.i++
	r, err := p.decodeEscape(start)
	if err != nil {
		return err
	}
	b.WriteRune(r)
	return nil
}

func (p *parser) decodeEscape(start int) (rune, error) {
	if p.i >= len(p.s) {
		return 0, p.fail(start, unterminatedString)
	}
	c := p.s[p.i]
	p.i++
	switch c {
	case '\\', '\'', '"', '/':
		return rune(c), nil
	case 'n':
		return '\n', nil
	case 't':
		return '\t', nil
	case 'r':
		return '\r', nil
	case 'b':
		return '\b', nil
	case 'f':
		return '\f', nil
	case 'u':
		return p.decodeUnicode()
	default:
		return 0, p.fail(p.i-1, "invalid escape")
	}
}

func (p *parser) decodeUnicode() (rune, error) {
	if p.i+unicodeEscapeDigits > len(p.s) {
		return 0, p.fail(p.i, "invalid unicode escape")
	}
	raw := p.s[p.i : p.i+unicodeEscapeDigits]
	n, err := strconv.ParseUint(raw, hexBase, runeBitSize)
	if err != nil {
		return 0, p.fail(p.i, "invalid unicode escape")
	}
	p.i += unicodeEscapeDigits
	return rune(n), nil
}

func (p *parser) readInt() (int, error) {
	start := p.i
	if p.i < len(p.s) && p.s[p.i] == '-' {
		p.i++
	}
	n, err := p.readPlainIntAt(start)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (p *parser) readPlainInt() (int, error) {
	return p.readPlainIntAt(p.i)
}

func (p *parser) readPlainIntAt(start int) (int, error) {
	if p.i >= len(p.s) || !isDigit(p.s[p.i]) {
		return 0, p.fail(start, "expected integer")
	}
	p.consumeDigits()
	if p.hasFractionOrExponent() {
		return 0, p.fail(start, "expected integer")
	}
	n, err := strconv.Atoi(p.s[start:p.i])
	if err != nil {
		return 0, p.fail(start, "invalid integer")
	}
	return n, nil
}

func (p *parser) hasFractionOrExponent() bool {
	if p.i >= len(p.s) {
		return false
	}
	c := p.s[p.i]
	return c == '.' || c == 'e' || c == 'E'
}

func (p *parser) readIdent() (string, bool) {
	if p.i >= len(p.s) {
		return emptyLit, false
	}
	start := p.i
	r, size := utf8.DecodeRuneInString(p.s[p.i:])
	if !isIdentRune(r) {
		return emptyLit, false
	}
	p.i += size
	for p.i < len(p.s) {
		r, size = utf8.DecodeRuneInString(p.s[p.i:])
		if !isIdentRune(r) {
			break
		}
		p.i += size
	}
	return p.s[start:p.i], true
}

func isIdentRune(r rune) bool {
	return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (p *parser) consumeKeyword(kw string) bool {
	if !strings.HasPrefix(p.s[p.i:], kw) {
		return false
	}
	end := p.i + len(kw)
	if end < len(p.s) && isKeywordContinue(p.s[end:]) {
		return false
	}
	p.i = end
	return true
}

func isKeywordContinue(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func (p *parser) sees(tok string) bool {
	p.skipSpace()
	if !strings.HasPrefix(p.s[p.i:], tok) {
		return false
	}
	return true
}

func (p *parser) expect(c byte) error {
	if p.i >= len(p.s) || p.s[p.i] != c {
		return p.failHere(fmt.Sprintf("expected '%c'", c))
	}
	p.i++
	return nil
}

func (p *parser) skipSpace() {
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return
		}
		p.i++
	}
}

func (p *parser) failHere(msg string) error {
	return p.fail(p.i, msg)
}

func (*parser) fail(pos int, msg string) error {
	return &SyntaxError{Position: pos, Message: msg}
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
