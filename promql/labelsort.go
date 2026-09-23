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
	"math/big"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Label value classes, from earliest to latest in ascending order.
const (
	classLeadingSpace = iota
	classPosInf
	classFinite
	classNegInf
	classDuration
	classBytes
	classSemver
	classIP
	classCIDR
	classTimestamp
	classUntyped
)

// compareLabelValues orders label values by typed class, then by value within
// that class. Equal typed values (and untyped strings) break ties by natural
// order of the original strings. The result is a strict total order.
func compareLabelValues(a, b string) int {
	if a == b {
		return 0
	}
	pa := parseLabelValue(a)
	pb := parseLabelValue(b)
	if pa.class != pb.class {
		if pa.class < pb.class {
			return -1
		}
		return 1
	}
	c := 0
	switch pa.class {
	case classLeadingSpace, classUntyped:
		return naturalCmp(a, b)
	case classPosInf, classNegInf:
		c = 0
	case classFinite:
		c = cmpDec(pa.num, pb.num)
	case classDuration, classBytes:
		c = pa.mag.Cmp(pb.mag)
	case classSemver:
		c = cmpSemver(pa.ver, pb.ver)
	case classIP:
		c = pa.ip.Compare(pb.ip)
	case classCIDR:
		c = pa.pfx.Addr().Compare(pb.pfx.Addr())
		if c == 0 {
			// Smaller prefix lengths sort first when the network address matches.
			c = cmpInt(pa.pfx.Bits(), pb.pfx.Bits())
		}
	case classTimestamp:
		c = pa.ts.Compare(pb.ts)
	}
	if c != 0 {
		return c
	}
	return naturalCmp(a, b)
}

type decNum struct {
	neg  bool
	mant *big.Int // non-negative integer significand
	exp  *big.Int // value = ±mant × 10^exp
}

type preID struct {
	num *big.Int // non-nil when the identifier is numeric
	str string
}

type semVer struct {
	major, minor, patch *big.Int
	pre                 []preID
}

type parsedLabel struct {
	class int
	num   decNum
	mag   *big.Rat
	ver   semVer
	ip    netip.Addr
	pfx   netip.Prefix
	ts    time.Time
}

func parseLabelValue(s string) parsedLabel {
	if hasLeadingSpace(s) {
		return parsedLabel{class: classLeadingSpace}
	}
	if class, num, ok := parseNumeric(s); ok {
		return parsedLabel{class: class, num: num}
	}
	if mag, ok := parseMagnitude(s, durationUnits); ok {
		return parsedLabel{class: classDuration, mag: mag}
	}
	if mag, ok := parseMagnitude(s, byteUnits); ok {
		return parsedLabel{class: classBytes, mag: mag}
	}
	if ver, ok := parseSemver(s); ok {
		return parsedLabel{class: classSemver, ver: ver}
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		// Keep IPv4-mapped IPv6 literals in IPv6 form.
		return parsedLabel{class: classIP, ip: ip}
	}
	if pfx, err := netip.ParsePrefix(s); err == nil {
		masked := pfx.Masked()
		if masked.IsValid() {
			return parsedLabel{class: classCIDR, pfx: masked}
		}
	}
	if ts, ok := parseTimestamp(s); ok {
		return parsedLabel{class: classTimestamp, ts: ts}
	}
	return parsedLabel{class: classUntyped}
}

func hasLeadingSpace(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r)
}

func parseNumeric(s string) (int, decNum, bool) {
	if s == "" {
		return 0, decNum{}, false
	}
	neg := false
	body := s
	switch body[0] {
	case '+':
		body = body[1:]
	case '-':
		neg = true
		body = body[1:]
	}
	if body == "" {
		return 0, decNum{}, false
	}
	if strings.EqualFold(body, "inf") || strings.EqualFold(body, "infinity") {
		if neg {
			return classNegInf, decNum{}, true
		}
		return classPosInf, decNum{}, true
	}
	if strings.EqualFold(body, "nan") {
		return 0, decNum{}, false
	}
	mant, exp, rest, ok := parseUnsignedDecimal(body)
	if !ok || rest != "" {
		return 0, decNum{}, false
	}
	if mant.Sign() == 0 {
		neg = false
	}
	return classFinite, decNum{neg: neg, mant: mant, exp: exp}, true
}

// parseUnsignedDecimal parses a decimal magnitude with an optional scientific
// exponent. A bare exponent marker ("1e", "1e+", "1e-") is not consumed, so
// the marker stays in rest and the value is not a finished number.
func parseUnsignedDecimal(s string) (mant *big.Int, exp *big.Int, rest string, ok bool) {
	if s == "" {
		return nil, nil, s, false
	}
	i := 0
	sawDigit := false
	for i < len(s) && isASCIIDigit(s[i]) {
		sawDigit = true
		i++
	}
	intPart := s[:i]
	fracPart := ""
	if i < len(s) && s[i] == '.' {
		i++
		fracStart := i
		for i < len(s) && isASCIIDigit(s[i]) {
			sawDigit = true
			i++
		}
		fracPart = s[fracStart:i]
	}
	if !sawDigit {
		return nil, nil, s, false
	}
	exp = big.NewInt(0)
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		expNeg := false
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			expNeg = s[j] == '-'
			j++
		}
		if j < len(s) && isASCIIDigit(s[j]) {
			expStart := j
			for j < len(s) && isASCIIDigit(s[j]) {
				j++
			}
			e, okE := new(big.Int).SetString(s[expStart:j], 10)
			if !okE {
				return nil, nil, s, false
			}
			if expNeg {
				e.Neg(e)
			}
			exp = e
			i = j
		}
	}
	digits := intPart + fracPart
	mant, ok = new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, nil, s, false
	}
	if len(fracPart) > 0 {
		exp = new(big.Int).Sub(exp, big.NewInt(int64(len(fracPart))))
	}
	return mant, exp, s[i:], true
}

func cmpDec(a, b decNum) int {
	aZero := a.mant == nil || a.mant.Sign() == 0
	bZero := b.mant == nil || b.mant.Sign() == 0
	if aZero && bZero {
		return 0
	}
	if a.neg != b.neg {
		if a.neg {
			return -1
		}
		return 1
	}
	c := cmpMag(a.mant, a.exp, b.mant, b.exp)
	if a.neg {
		return -c
	}
	return c
}

// cmpMag compares mant×10^exp without expanding huge powers of ten.
func cmpMag(m1 *big.Int, e1 *big.Int, m2 *big.Int, e2 *big.Int) int {
	if m1.Sign() == 0 && m2.Sign() == 0 {
		return 0
	}
	if m1.Sign() == 0 {
		return -1
	}
	if m2.Sign() == 0 {
		return 1
	}
	diff := new(big.Int).Sub(e1, e2)
	if diff.Sign() >= 0 {
		return cmpMantShift(m1, diff, m2)
	}
	return -cmpMantShift(m2, new(big.Int).Neg(diff), m1)
}

func cmpMantShift(m *big.Int, shift *big.Int, other *big.Int) int {
	if shift.Sign() == 0 {
		return m.Cmp(other)
	}
	dM := digitCount(m)
	dO := digitCount(other)
	sum := new(big.Int).Add(dM, shift)
	if c := sum.Cmp(dO); c != 0 {
		return c
	}
	// Same number of digits, so shift equals a difference of input lengths.
	pow := new(big.Int).Exp(big.NewInt(10), shift, nil)
	scaled := new(big.Int).Mul(m, pow)
	return scaled.Cmp(other)
}

func digitCount(n *big.Int) *big.Int {
	if n.Sign() == 0 {
		return big.NewInt(1)
	}
	return big.NewInt(int64(len(n.String())))
}

type namedMult struct {
	name string
	mult *big.Int
	pos  int // duration units must appear in increasing pos order; 0 for bytes
}

var (
	ns     = big.NewInt(1)
	us     = big.NewInt(1_000)
	ms     = big.NewInt(1_000_000)
	sec    = new(big.Int).Mul(big.NewInt(1_000_000_000), big.NewInt(1))
	minute = new(big.Int).Mul(sec, big.NewInt(60))
	hour   = new(big.Int).Mul(minute, big.NewInt(60))
	day    = new(big.Int).Mul(hour, big.NewInt(24))
	week   = new(big.Int).Mul(day, big.NewInt(7))
	year   = new(big.Int).Mul(day, big.NewInt(365))

	// Longest name first so "ms" wins over "m" and "MiB" wins over "M"/"B".
	durationUnits = []namedMult{
		{"ms", ms, 7},
		{"µs", us, 8},
		{"μs", us, 8},
		{"us", us, 8},
		{"ns", ns, 9},
		{"s", sec, 6},
		{"m", minute, 5},
		{"h", hour, 4},
		{"d", day, 3},
		{"w", week, 2},
		{"y", year, 1},
	}
	byteUnits = []namedMult{
		{"EiB", pow1024(6), 0},
		{"PiB", pow1024(5), 0},
		{"TiB", pow1024(4), 0},
		{"GiB", pow1024(3), 0},
		{"MiB", pow1024(2), 0},
		{"KiB", pow1024(1), 0},
		{"EB", pow10(18), 0},
		{"PB", pow10(15), 0},
		{"TB", pow10(12), 0},
		{"GB", pow10(9), 0},
		{"MB", pow10(6), 0},
		{"kB", pow10(3), 0},
		{"KB", pow10(3), 0},
		{"Ei", pow1024(6), 0},
		{"Pi", pow1024(5), 0},
		{"Ti", pow1024(4), 0},
		{"Gi", pow1024(3), 0},
		{"Mi", pow1024(2), 0},
		{"Ki", pow1024(1), 0},
		{"E", pow10(18), 0},
		{"P", pow10(15), 0},
		{"T", pow10(12), 0},
		{"G", pow10(9), 0},
		{"M", pow10(6), 0},
		{"K", pow10(3), 0},
		{"k", pow10(3), 0},
		{"B", big.NewInt(1), 0},
	}
)

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

func pow1024(n int) *big.Int {
	return new(big.Int).Lsh(big.NewInt(1), uint(10*n))
}

// parseMagnitude parses a signed sum of scientific coefficients times units.
// A leading sign applies to the whole value. Duration units must run from
// larger to smaller.
func parseMagnitude(s string, units []namedMult) (*big.Rat, bool) {
	if s == "" {
		return nil, false
	}
	neg := false
	if s[0] == '+' || s[0] == '-' {
		neg = s[0] == '-'
		s = s[1:]
		if s == "" {
			return nil, false
		}
	}
	total := new(big.Rat)
	lastPos := 0
	ordered := units[0].pos != 0
	saw := false
	for s != "" {
		mant, exp, rest, ok := parseUnsignedDecimal(s)
		if !ok {
			return nil, false
		}
		unit, rest, ok := matchUnit(rest, units)
		if !ok {
			return nil, false
		}
		if ordered {
			if unit.pos <= lastPos {
				return nil, false
			}
			lastPos = unit.pos
		}
		total.Add(total, scalePow10(new(big.Int).Mul(mant, unit.mult), exp))
		s = rest
		saw = true
	}
	if !saw {
		return nil, false
	}
	if neg {
		total.Neg(total)
	}
	return total, true
}

func matchUnit(s string, units []namedMult) (namedMult, string, bool) {
	for _, u := range units {
		if strings.HasPrefix(s, u.name) {
			return u, s[len(u.name):], true
		}
	}
	return namedMult{}, s, false
}

func scalePow10(mant *big.Int, exp *big.Int) *big.Rat {
	r := new(big.Rat).SetInt(mant)
	if exp.Sign() == 0 {
		return r
	}
	if exp.Sign() > 0 {
		pow := new(big.Int).Exp(big.NewInt(10), exp, nil)
		return r.Mul(r, new(big.Rat).SetInt(pow))
	}
	pow := new(big.Int).Exp(big.NewInt(10), new(big.Int).Neg(exp), nil)
	return r.Quo(r, new(big.Rat).SetInt(pow))
}

func parseSemver(s string) (semVer, bool) {
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	major, rest, ok := parseSemverNum(s)
	if !ok || !strings.HasPrefix(rest, ".") {
		return semVer{}, false
	}
	minor, rest, ok := parseSemverNum(rest[1:])
	if !ok || !strings.HasPrefix(rest, ".") {
		return semVer{}, false
	}
	patch, rest, ok := parseSemverNum(rest[1:])
	if !ok {
		return semVer{}, false
	}
	var pre []preID
	if strings.HasPrefix(rest, "-") {
		pre, rest, ok = parsePre(rest[1:])
		if !ok {
			return semVer{}, false
		}
	}
	if strings.HasPrefix(rest, "+") {
		if !validBuild(rest[1:]) {
			return semVer{}, false
		}
		rest = ""
	}
	if rest != "" {
		return semVer{}, false
	}
	return semVer{major: major, minor: minor, patch: patch, pre: pre}, true
}

func parseSemverNum(s string) (*big.Int, string, bool) {
	if s == "" || !isASCIIDigit(s[0]) {
		return nil, s, false
	}
	i := 1
	for i < len(s) && isASCIIDigit(s[i]) {
		i++
	}
	tok := s[:i]
	if len(tok) > 1 && tok[0] == '0' {
		return nil, s, false
	}
	n, ok := new(big.Int).SetString(tok, 10)
	if !ok {
		return nil, s, false
	}
	return n, s[i:], true
}

func parsePre(s string) ([]preID, string, bool) {
	if s == "" {
		return nil, s, false
	}
	var ids []preID
	for {
		i := 0
		for i < len(s) && s[i] != '.' && s[i] != '+' {
			if !isSemverChar(s[i]) {
				return nil, s, false
			}
			i++
		}
		if i == 0 {
			return nil, s, false
		}
		id, ok := classifyPre(s[:i])
		if !ok {
			return nil, s, false
		}
		ids = append(ids, id)
		s = s[i:]
		if strings.HasPrefix(s, ".") {
			s = s[1:]
			continue
		}
		return ids, s, true
	}
}

func classifyPre(tok string) (preID, bool) {
	numeric := true
	for i := 0; i < len(tok); i++ {
		if !isASCIIDigit(tok[i]) {
			numeric = false
			break
		}
	}
	if numeric {
		if len(tok) > 1 && tok[0] == '0' {
			return preID{}, false
		}
		n, ok := new(big.Int).SetString(tok, 10)
		if !ok {
			return preID{}, false
		}
		return preID{num: n, str: tok}, true
	}
	return preID{str: tok}, true
}

func validBuild(s string) bool {
	if s == "" {
		return false
	}
	for {
		i := 0
		for i < len(s) && s[i] != '.' {
			if !isSemverChar(s[i]) {
				return false
			}
			i++
		}
		if i == 0 {
			return false
		}
		s = s[i:]
		if strings.HasPrefix(s, ".") {
			s = s[1:]
			continue
		}
		return true
	}
}

func isSemverChar(c byte) bool {
	return isASCIIDigit(c) || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-'
}

func cmpSemver(a, b semVer) int {
	if c := a.major.Cmp(b.major); c != 0 {
		return c
	}
	if c := a.minor.Cmp(b.minor); c != 0 {
		return c
	}
	if c := a.patch.Cmp(b.patch); c != 0 {
		return c
	}
	if len(a.pre) == 0 && len(b.pre) > 0 {
		return 1
	}
	if len(a.pre) > 0 && len(b.pre) == 0 {
		return -1
	}
	n := len(a.pre)
	if len(b.pre) < n {
		n = len(b.pre)
	}
	for i := 0; i < n; i++ {
		if c := cmpPre(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a.pre), len(b.pre))
}

func cmpPre(a, b preID) int {
	switch {
	case a.num != nil && b.num != nil:
		return a.num.Cmp(b.num)
	case a.num != nil && b.num == nil:
		return -1
	case a.num == nil && b.num != nil:
		return 1
	}
	if a.str < b.str {
		return -1
	}
	if a.str > b.str {
		return 1
	}
	return 0
}

var timestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999Z0700",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseTimestamp(s string) (time.Time, bool) {
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// naturalCmp is a total order. Numeric chunks compare as integers of unbounded
// size. When those integers are equal, remaining differences (leading zeros)
// fall back to byte order of the original strings.
func naturalCmp(a, b string) int {
	if a == b {
		return 0
	}
	ia, ib := 0, 0
	for ia < len(a) && ib < len(b) {
		aDig := isASCIIDigit(a[ia])
		bDig := isASCIIDigit(b[ib])
		ja := ia + 1
		for ja < len(a) && isASCIIDigit(a[ja]) == aDig {
			ja++
		}
		jb := ib + 1
		for jb < len(b) && isASCIIDigit(b[jb]) == bDig {
			jb++
		}
		as, bs := a[ia:ja], b[ib:jb]
		if aDig && bDig {
			if c := cmpNumericStrings(as, bs); c != 0 {
				return c
			}
		} else if as != bs {
			if as < bs {
				return -1
			}
			return 1
		}
		ia, ib = ja, jb
	}
	if ia == len(a) && ib == len(b) {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
	if ia == len(a) {
		return -1
	}
	return 1
}

func cmpNumericStrings(a, b string) int {
	ai, okA := new(big.Int).SetString(a, 10)
	bi, okB := new(big.Int).SetString(b, 10)
	if !okA || !okB {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
	return ai.Cmp(bi)
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
