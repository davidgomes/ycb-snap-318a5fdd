// Copyright The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package promql

import (
	"cmp"
	"math/big"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Label values sort by class, then by typed magnitude, then by natural order of
// the original string. Leading whitespace is never typed and sorts first.
// Positive infinity sorts before every finite number, and negative infinity
// sorts after every finite number.
const (
	classLeadingSpace = iota
	classPosInf
	classNumber
	classNegInf
	classDuration
	classBytes
	classSemver
	classIP
	classCIDR
	classTimestamp
	classUntyped
)

// maxAlign is the largest decimal span (in orders of magnitude) collapsed into
// one coefficient. Wider spans stay as exact residual terms so a huge exponent
// is never expanded into a digit string.
const maxAlign int64 = 100_000

const dateOnlyLayout = "2006-01-02"

type parsed struct {
	class int
	mag   magnitude
	sem   *semVer
	ip    netip.Addr
	cidr  netip.Prefix
	ts    time.Time
}

// dec is a signed decimal: (+/-) coeff * 10^exp, with coeff >= 0 and not
// divisible by 10 unless the value is zero.
type dec struct {
	neg   bool
	coeff *big.Int
	exp   *big.Int
}

// magnitude is a sum of positive decimals with an overall sign.
type magnitude struct {
	neg   bool
	terms []dec
}

type semVer struct {
	major string
	minor string
	patch string
	pre   []preID
}

type preID struct {
	num string
	raw string
}

type unit struct {
	name string
	pos  int
	mult *big.Int
}

var (
	multNS = big.NewInt(1)
	multUS = big.NewInt(1_000)
	multMS = big.NewInt(1_000_000)
	multS  = big.NewInt(1_000_000_000)
	multM  = new(big.Int).Mul(big.NewInt(1_000_000_000), big.NewInt(60))
	multH  = new(big.Int).Mul(big.NewInt(1_000_000_000), big.NewInt(3_600))
	multD  = new(big.Int).Mul(big.NewInt(1_000_000_000), big.NewInt(86_400))
)

var (
	multW = new(big.Int).Mul(new(big.Int).Set(multD), big.NewInt(7))
	multY = new(big.Int).Mul(new(big.Int).Set(multD), big.NewInt(365))
)

// Duration units are longest-first so "ms" wins over "m". Positions must
// increase from largest unit to smallest, matching Prometheus durations.
var durUnits = []unit{
	{name: "\u00b5s", pos: 8, mult: multUS},
	{name: "\u03bcs", pos: 8, mult: multUS},
	{name: "ms", pos: 7, mult: multMS},
	{name: "us", pos: 8, mult: multUS},
	{name: "ns", pos: 9, mult: multNS},
	{name: "y", pos: 1, mult: multY},
	{name: "w", pos: 2, mult: multW},
	{name: "d", pos: 3, mult: multD},
	{name: "h", pos: 4, mult: multH},
	{name: "m", pos: 5, mult: multM},
	{name: "s", pos: 6, mult: multS},
}

// Byte units follow ParseStrictBytes: KiB is 1024, kB and KB are 1000.
// Names are longest-first.
var byteUnits = []unit{
	{name: "YiB", mult: pow2(80)},
	{name: "ZiB", mult: pow2(70)},
	{name: "EiB", mult: pow2(60)},
	{name: "PiB", mult: pow2(50)},
	{name: "TiB", mult: pow2(40)},
	{name: "GiB", mult: pow2(30)},
	{name: "MiB", mult: pow2(20)},
	{name: "KiB", mult: pow2(10)},
	{name: "YB", mult: pow10Big(24)},
	{name: "ZB", mult: pow10Big(21)},
	{name: "EB", mult: pow10Big(18)},
	{name: "PB", mult: pow10Big(15)},
	{name: "TB", mult: pow10Big(12)},
	{name: "GB", mult: pow10Big(9)},
	{name: "MB", mult: pow10Big(6)},
	{name: "KB", mult: pow10Big(3)},
	{name: "kB", mult: pow10Big(3)},
	{name: "B", mult: big.NewInt(1)},
}

func pow2(n uint) *big.Int {
	return new(big.Int).Lsh(big.NewInt(1), n)
}

func pow10Big(n int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil)
}

func compareLabelValues(a, b string) int {
	if a == b {
		return 0
	}
	if c := compareParsed(classify(a), classify(b)); c != 0 {
		return c
	}
	return naturalCompare(a, b)
}

func compareParsed(a, b parsed) int {
	if a.class != b.class {
		return cmp.Compare(a.class, b.class)
	}
	switch a.class {
	case classNumber, classDuration, classBytes:
		return cmpMagnitude(a.mag, b.mag)
	case classSemver:
		return cmpSemver(*a.sem, *b.sem)
	case classIP:
		return a.ip.Compare(b.ip)
	case classCIDR:
		return cmpCIDR(a.cidr, b.cidr)
	case classTimestamp:
		return a.ts.Compare(b.ts)
	default:
		return 0
	}
}

func classify(s string) parsed {
	if hasLeadingSpace(s) {
		return parsed{class: classLeadingSpace}
	}
	if sign, ok := parseInfinity(s); ok {
		if sign < 0 {
			return parsed{class: classNegInf}
		}
		return parsed{class: classPosInf}
	}
	if isNaNLiteral(s) {
		return parsed{class: classUntyped}
	}
	if m, ok := parseDecimal(s); ok {
		return parsed{class: classNumber, mag: m}
	}
	if m, ok := parseDuration(s); ok {
		return parsed{class: classDuration, mag: m}
	}
	if m, ok := parseBytes(s); ok {
		return parsed{class: classBytes, mag: m}
	}
	if sv, ok := parseSemver(s); ok {
		return parsed{class: classSemver, sem: &sv}
	}
	if ip, ok := parseIP(s); ok {
		return parsed{class: classIP, ip: ip}
	}
	if p, ok := parseCIDR(s); ok {
		return parsed{class: classCIDR, cidr: p}
	}
	if ts, ok := parseTimestamp(s); ok {
		return parsed{class: classTimestamp, ts: ts}
	}
	return parsed{class: classUntyped}
}

func hasLeadingSpace(s string) bool {
	if s == "" {
		return false
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size == 1 {
		return false
	}
	return unicode.IsSpace(r)
}

func parseInfinity(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	sign := 1
	if s[0] == '+' || s[0] == '-' {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	if equalFoldASCII(s, "inf") || equalFoldASCII(s, "infinity") {
		return sign, true
	}
	return 0, false
}

func isNaNLiteral(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	return equalFoldASCII(s, "nan")
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func parseDecimal(s string) (magnitude, bool) {
	if s == "" {
		return magnitude{}, false
	}
	sign := 1
	i := 0
	if s[0] == '+' || s[0] == '-' {
		if s[0] == '-' {
			sign = -1
		}
		i++
	}
	d, next, ok := parseMagnitude(s, i)
	if !ok || next != len(s) {
		return magnitude{}, false
	}
	return magnitudeFrom(sign, []dec{d}), true
}

func parseDuration(s string) (magnitude, bool) {
	return parseScaled(s, durUnits, true)
}

func parseBytes(s string) (magnitude, bool) {
	return parseScaled(s, byteUnits, false)
}

// parseScaled parses a signed sum of magnitude+unit pairs.
// When ordered is set, units must appear from largest to smallest.
func parseScaled(s string, units []unit, ordered bool) (magnitude, bool) {
	if s == "" {
		return magnitude{}, false
	}
	sign := 1
	i := 0
	if s[0] == '+' || s[0] == '-' {
		if s[0] == '-' {
			sign = -1
		}
		i++
		if i >= len(s) {
			return magnitude{}, false
		}
	}
	var terms []dec
	lastPos := 0
	for i < len(s) {
		mag, next, ok := parseMagnitude(s, i)
		if !ok {
			return magnitude{}, false
		}
		i = next
		u, nlen, ok := matchUnit(s[i:], units)
		if !ok {
			return magnitude{}, false
		}
		if ordered {
			if u.pos <= lastPos {
				return magnitude{}, false
			}
			lastPos = u.pos
		}
		i += nlen
		terms = append(terms, mulDec(mag, u.mult))
	}
	if len(terms) == 0 {
		return magnitude{}, false
	}
	return magnitudeFrom(sign, terms), true
}

func matchUnit(s string, units []unit) (unit, int, bool) {
	for _, u := range units {
		if strings.HasPrefix(s, u.name) {
			return u, len(u.name), true
		}
	}
	return unit{}, 0, false
}

func parseMagnitude(s string, i int) (dec, int, bool) {
	if i >= len(s) {
		return dec{}, i, false
	}
	intStart := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	intEnd := i
	fracStart, fracEnd := i, i
	if i < len(s) && s[i] == '.' {
		i++
		fracStart = i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		fracEnd = i
	}
	if intStart == intEnd && fracStart == fracEnd {
		return dec{}, i, false
	}
	exp := new(big.Int)
	// An exponent marker counts only when digits follow it. "1e" and "1e+"
	// stay unparsed so a unit such as "EB" can still match.
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		expNeg := false
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			expNeg = s[j] == '-'
			j++
		}
		expStart := j
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		if expStart < j {
			if _, ok := exp.SetString(s[expStart:j], 10); !ok {
				return dec{}, i, false
			}
			if expNeg {
				exp.Neg(exp)
			}
			i = j
		}
	}
	digits := make([]byte, 0, (intEnd-intStart)+(fracEnd-fracStart))
	digits = append(digits, s[intStart:intEnd]...)
	digits = append(digits, s[fracStart:fracEnd]...)
	coeff := new(big.Int)
	if _, ok := coeff.SetString(string(digits), 10); !ok {
		return dec{}, i, false
	}
	if fracEnd > fracStart {
		exp.Sub(exp, big.NewInt(int64(fracEnd-fracStart)))
	}
	d := dec{coeff: coeff, exp: exp}
	d.normalize()
	return d, i, true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func magnitudeFrom(sign int, terms []dec) magnitude {
	m := magnitude{neg: sign < 0, terms: canonicalTerms(terms)}
	if len(m.terms) == 0 {
		m.neg = false
	}
	return m
}

func parseIP(s string) (netip.Addr, bool) {
	// Leave IPv4-mapped IPv6 addresses mapped so they sort as IPv6.
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return ip, true
}

func parseCIDR(s string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.IsValid() {
		return netip.Prefix{}, false
	}
	return p.Masked(), true
}

func cmpCIDR(a, b netip.Prefix) int {
	if c := a.Addr().Compare(b.Addr()); c != 0 {
		return c
	}
	// Smaller prefix lengths sort first when the network address matches.
	return cmp.Compare(a.Bits(), b.Bits())
}

func parseTimestamp(s string) (time.Time, bool) {
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts, true
	}
	if ts, err := time.Parse(dateOnlyLayout, s); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func parseSemver(s string) (semVer, bool) {
	if len(s) > 1 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	major, rest, ok := cutSemNum(s)
	if !ok {
		return semVer{}, false
	}
	minor, rest, ok := cutDotNum(rest)
	if !ok {
		return semVer{}, false
	}
	patch, rest, ok := cutDotNum(rest)
	if !ok {
		return semVer{}, false
	}
	sv := semVer{major: major, minor: minor, patch: patch}
	if rest == "" {
		return sv, true
	}
	if rest[0] == '+' {
		if validIDList(rest[1:]) {
			return sv, true
		}
		return semVer{}, false
	}
	if rest[0] != '-' {
		return semVer{}, false
	}
	prePart := rest[1:]
	build := ""
	if i := strings.IndexByte(prePart, '+'); i >= 0 {
		build = prePart[i+1:]
		prePart = prePart[:i]
	}
	pre, ok := parsePre(prePart)
	if !ok {
		return semVer{}, false
	}
	if build != "" && !validIDList(build) {
		return semVer{}, false
	}
	sv.pre = pre
	return sv, true
}

func cutDotNum(s string) (string, string, bool) {
	if s == "" || s[0] != '.' {
		return "", s, false
	}
	return cutSemNum(s[1:])
}

func cutSemNum(s string) (string, string, bool) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	if i == 0 || (i > 1 && s[0] == '0') {
		return "", s, false
	}
	return s[:i], s[i:], true
}

func parsePre(s string) ([]preID, bool) {
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	out := make([]preID, 0, len(parts))
	for _, p := range parts {
		if p == "" || !validID(p) {
			return nil, false
		}
		id := preID{raw: p}
		if isAllDigits(p) {
			if len(p) > 1 && p[0] == '0' {
				return nil, false
			}
			id.num = p
		}
		out = append(out, id)
	}
	return out, true
}

func validIDList(s string) bool {
	if s == "" {
		return false
	}
	for _, p := range strings.Split(s, ".") {
		if p == "" || !validID(p) {
			return false
		}
	}
	return true
}

func validID(s string) bool {
	for i := range len(s) {
		c := s[i]
		if !isDigit(c) && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && c != '-' {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func cmpSemver(a, b semVer) int {
	if c := cmpIntStr(a.major, b.major); c != 0 {
		return c
	}
	if c := cmpIntStr(a.minor, b.minor); c != 0 {
		return c
	}
	if c := cmpIntStr(a.patch, b.patch); c != 0 {
		return c
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	n := min(len(a.pre), len(b.pre))
	for i := range n {
		if c := cmpPre(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.pre), len(b.pre))
}

func cmpPre(a, b preID) int {
	switch {
	case a.num != "" && b.num != "":
		return cmpIntStr(a.num, b.num)
	case a.num != "" && b.num == "":
		return -1
	case a.num == "" && b.num != "":
		return 1
	default:
		return strings.Compare(a.raw, b.raw)
	}
}

func cmpMagnitude(a, b magnitude) int {
	aZero := len(a.terms) == 0
	bZero := len(b.terms) == 0
	if aZero && bZero {
		return 0
	}
	if a.neg != b.neg {
		if a.neg {
			return -1
		}
		return 1
	}
	c := cmpAbsMag(a, b)
	if a.neg {
		return -c
	}
	return c
}

func cmpAbsMag(a, b magnitude) int {
	ca, aok := collapseAll(a.terms)
	cb, bok := collapseAll(b.terms)
	if aok && bok {
		return cmpAbs(ca, cb)
	}
	return cmpResidual(a.terms, b.terms)
}

func cmpResidual(a, b []dec) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return -1
	case len(b) == 0:
		return 1
	}
	ah, ar := splitHead(a)
	bh, br := splitHead(b)
	if c := cmpAbs(ah, bh); c != 0 {
		return c
	}
	return cmpResidual(ar, br)
}

func splitHead(terms []dec) (dec, []dec) {
	if len(terms) == 0 {
		return zeroDec(), nil
	}
	group := []dec{terms[0]}
	i := 1
	for ; i < len(terms); i++ {
		trial := make([]dec, len(group)+1)
		copy(trial, group)
		trial[len(group)] = terms[i]
		if !spanFits(trial) {
			break
		}
		group = trial
	}
	top, ok := collapseAll(group)
	if !ok {
		return terms[0].clone(), terms[1:]
	}
	return top, terms[i:]
}

func canonicalTerms(terms []dec) []dec {
	cleaned := terms
	for range 8 {
		next, stable := mergeTerms(cleaned)
		cleaned = next
		if stable {
			break
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	if collapsed, ok := collapseAll(cleaned); ok {
		if collapsed.isZero() {
			return nil
		}
		return []dec{collapsed}
	}
	return cleaned
}

func mergeTerms(terms []dec) ([]dec, bool) {
	nz := make([]dec, 0, len(terms))
	for _, t := range terms {
		t = t.clone()
		t.normalize()
		if !t.isZero() {
			nz = append(nz, t)
		}
	}
	slices.SortFunc(nz, func(a, b dec) int { return b.exp.Cmp(a.exp) })
	if len(nz) == 0 {
		return nil, true
	}
	out := []dec{nz[0]}
	stable := true
	for _, t := range nz[1:] {
		last := out[len(out)-1]
		if last.exp.Cmp(t.exp) != 0 {
			if last.exp.Cmp(t.exp) < 0 {
				stable = false
			}
			out = append(out, t)
			continue
		}
		sum := signedCoeff(last)
		sum.Add(sum, signedCoeff(t))
		merged := newDecFromSigned(sum, t.exp)
		if merged.isZero() {
			out = out[:len(out)-1]
			stable = false
			continue
		}
		out[len(out)-1] = merged
		if merged.exp.Cmp(t.exp) != 0 {
			stable = false
		}
	}
	return out, stable
}

func collapseAll(terms []dec) (dec, bool) {
	if len(terms) == 0 {
		return zeroDec(), true
	}
	if len(terms) == 1 {
		return terms[0].clone(), true
	}
	if !spanFits(terms) {
		return dec{}, false
	}
	total := zeroDec()
	for _, t := range terms {
		total = addDec(total, t)
	}
	return total, true
}

func spanFits(terms []dec) bool {
	if len(terms) <= 1 {
		return true
	}
	maxE := terms[0].exp
	minE := terms[0].exp
	digits := 0
	for _, t := range terms {
		if t.exp.Cmp(maxE) > 0 {
			maxE = t.exp
		}
		if t.exp.Cmp(minE) < 0 {
			minE = t.exp
		}
		if n := digitLen(t.coeff); n > digits {
			digits = n
		}
	}
	span := new(big.Int).Sub(maxE, minE)
	if !span.IsInt64() {
		return false
	}
	return span.Int64()+int64(digits) <= maxAlign
}

func addDec(a, b dec) dec {
	if a.isZero() {
		return b.clone()
	}
	if b.isZero() {
		return a.clone()
	}
	if a.exp.Cmp(b.exp) < 0 {
		a, b = b, a
	}
	diff := new(big.Int).Sub(a.exp, b.exp)
	if !diff.IsInt64() || diff.Int64() > maxAlign {
		if cmpAbs(a, b) >= 0 {
			return a.clone()
		}
		return b.clone()
	}
	sum := signedCoeff(a)
	if shift := diff.Int64(); shift > 0 {
		sum.Mul(sum, pow10Big(shift))
	}
	sum.Add(sum, signedCoeff(b))
	return newDecFromSigned(sum, b.exp)
}

func mulDec(d dec, n *big.Int) dec {
	if d.isZero() || n.Sign() == 0 {
		return zeroDec()
	}
	out := d.clone()
	out.coeff.Mul(out.coeff, new(big.Int).Abs(n))
	if n.Sign() < 0 {
		out.neg = !out.neg
	}
	out.normalize()
	return out
}

func cmpAbs(a, b dec) int {
	switch {
	case a.isZero() && b.isZero():
		return 0
	case a.isZero():
		return -1
	case b.isZero():
		return 1
	}
	da := big.NewInt(int64(digitLen(a.coeff)))
	db := big.NewInt(int64(digitLen(b.coeff)))
	ma := new(big.Int).Add(a.exp, da)
	ma.Sub(ma, big.NewInt(1))
	mb := new(big.Int).Add(b.exp, db)
	mb.Sub(mb, big.NewInt(1))
	if c := ma.Cmp(mb); c != 0 {
		return c
	}
	as := a.coeff.String()
	bs := b.coeff.String()
	if len(as) < len(bs) {
		as += strings.Repeat("0", len(bs)-len(as))
	} else if len(bs) < len(as) {
		bs += strings.Repeat("0", len(as)-len(bs))
	}
	return strings.Compare(as, bs)
}

func (d *dec) normalize() {
	if d.coeff == nil || d.coeff.Sign() == 0 {
		d.neg = false
		d.coeff = big.NewInt(0)
		d.exp = big.NewInt(0)
		return
	}
	if d.exp == nil {
		d.exp = big.NewInt(0)
	}
	if d.coeff.Sign() < 0 {
		d.neg = !d.neg
		d.coeff.Abs(d.coeff)
	}
	ten := big.NewInt(10)
	for d.coeff.Sign() != 0 && new(big.Int).Mod(d.coeff, ten).Sign() == 0 {
		d.coeff.Quo(d.coeff, ten)
		d.exp.Add(d.exp, big.NewInt(1))
	}
	if d.coeff.Sign() == 0 {
		d.neg = false
		d.exp = big.NewInt(0)
	}
}

func (d dec) isZero() bool {
	return d.coeff == nil || d.coeff.Sign() == 0
}

func (d dec) clone() dec {
	coeff := big.NewInt(0)
	exp := big.NewInt(0)
	if d.coeff != nil {
		coeff.Set(d.coeff)
	}
	if d.exp != nil {
		exp.Set(d.exp)
	}
	return dec{neg: d.neg, coeff: coeff, exp: exp}
}

func zeroDec() dec {
	return dec{coeff: big.NewInt(0), exp: big.NewInt(0)}
}

func signedCoeff(d dec) *big.Int {
	c := new(big.Int)
	if d.coeff != nil {
		c.Set(d.coeff)
	}
	if d.neg {
		c.Neg(c)
	}
	return c
}

func newDecFromSigned(coeff, exp *big.Int) dec {
	d := dec{coeff: new(big.Int).Set(coeff), exp: new(big.Int).Set(exp)}
	d.normalize()
	return d
}

func digitLen(n *big.Int) int {
	if n == nil || n.Sign() == 0 {
		return 1
	}
	return len(n.String())
}

// naturalCompare is a total order for mixed digit and non-digit runs.
// Numeric runs compare as integers, so leading zeros do not change the result.
// A zero result means the strings are naturally equal, including distinct
// spellings such as "1" and "01".
func naturalCompare(a, b string) int {
	ia, ib := 0, 0
	for ia < len(a) && ib < len(b) {
		if isDigit(a[ia]) && isDigit(b[ib]) {
			ja := ia + 1
			for ja < len(a) && isDigit(a[ja]) {
				ja++
			}
			jb := ib + 1
			for jb < len(b) && isDigit(b[jb]) {
				jb++
			}
			if c := cmpIntStr(a[ia:ja], b[ib:jb]); c != 0 {
				return c
			}
			ia, ib = ja, jb
			continue
		}
		ja := endChunk(a, ia)
		jb := endChunk(b, ib)
		if c := strings.Compare(a[ia:ja], b[ib:jb]); c != 0 {
			return c
		}
		ia, ib = ja, jb
	}
	switch {
	case ia < len(a):
		return 1
	case ib < len(b):
		return -1
	default:
		return 0
	}
}

func endChunk(s string, i int) int {
	digit := isDigit(s[i])
	j := i + 1
	for j < len(s) && isDigit(s[j]) == digit {
		j++
	}
	return j
}

func cmpIntStr(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "0"
	}
	if len(a) != len(b) {
		return cmp.Compare(len(a), len(b))
	}
	return strings.Compare(a, b)
}
