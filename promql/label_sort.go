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

	"github.com/facette/natsort"
)

// Label value classes in ascending order. A value is assigned to the first
// class whose parser accepts the whole string.
const (
	labelClassSpace = iota
	labelClassPosInf
	labelClassNumber
	labelClassNegInf
	labelClassDuration
	labelClassBytes
	labelClassSemver
	labelClassIP
	labelClassCIDR
	labelClassTimestamp
	labelClassString
)

// compareLabelValues orders label values with a typed total order.
// Leading whitespace is its own class and is never parsed. Typed ties and
// untyped values use natural ordering of the original strings.
func compareLabelValues(a, b string) int {
	ka := classifyLabel(a)
	kb := classifyLabel(b)
	if ka.class != kb.class {
		return ka.class - kb.class
	}
	var cmp int
	switch ka.class {
	case labelClassNumber:
		cmp = compareDecimal(ka.num, kb.num)
	case labelClassDuration, labelClassBytes:
		cmp = ka.mag.Cmp(kb.mag)
	case labelClassSemver:
		cmp = compareSemver(ka.ver, kb.ver)
	case labelClassIP:
		cmp = compareIP(ka.ip, kb.ip)
	case labelClassCIDR:
		cmp = compareCIDR(ka.cidr, kb.cidr)
	case labelClassTimestamp:
		cmp = ka.ts.Compare(kb.ts)
	default:
		cmp = 0
	}
	if cmp != 0 {
		return cmp
	}
	return naturalCompare(a, b)
}

type labelKey struct {
	class int
	num   decimal
	mag   *big.Rat
	ver   semver
	ip    ipKey
	cidr  cidrKey
	ts    time.Time
}

func classifyLabel(s string) labelKey {
	if s != "" {
		r, _ := utf8.DecodeRuneInString(s)
		if unicode.IsSpace(r) {
			return labelKey{class: labelClassSpace}
		}
	}
	if sign, ok := parseInfinity(s); ok {
		if sign > 0 {
			return labelKey{class: labelClassPosInf}
		}
		return labelKey{class: labelClassNegInf}
	}
	if n, ok := parseDecimal(s); ok {
		return labelKey{class: labelClassNumber, num: n}
	}
	if mag, ok := parseDurationMag(s); ok {
		return labelKey{class: labelClassDuration, mag: mag}
	}
	if mag, ok := parseBytesMag(s); ok {
		return labelKey{class: labelClassBytes, mag: mag}
	}
	if v, ok := parseSemver(s); ok {
		return labelKey{class: labelClassSemver, ver: v}
	}
	if ip, ok := parseIPKey(s); ok {
		return labelKey{class: labelClassIP, ip: ip}
	}
	if c, ok := parseCIDRKey(s); ok {
		return labelKey{class: labelClassCIDR, cidr: c}
	}
	if ts, ok := parseTimestamp(s); ok {
		return labelKey{class: labelClassTimestamp, ts: ts}
	}
	return labelKey{class: labelClassString}
}

func naturalCompare(a, b string) int {
	if a == b {
		return 0
	}
	if natsort.Compare(a, b) {
		return -1
	}
	if natsort.Compare(b, a) {
		return 1
	}
	return strings.Compare(a, b)
}

func parseInfinity(s string) (sign int, ok bool) {
	switch strings.ToLower(s) {
	case "inf", "+inf", "infinity", "+infinity":
		return 1, true
	case "-inf", "-infinity":
		return -1, true
	default:
		return 0, false
	}
}

func isNaNLiteral(s string) bool {
	switch strings.ToLower(s) {
	case "nan", "+nan", "-nan":
		return true
	default:
		return false
	}
}

// decimal is mant * 10^exp. mant is non-negative and, unless zero, not
// divisible by 10. exp is the base-10 exponent as a big integer so magnitudes
// stay exact.
type decimal struct {
	neg  bool
	mant *big.Int
	exp  *big.Int
}

func compareDecimal(a, b decimal) int {
	aZero := a.mant.Sign() == 0
	bZero := b.mant.Sign() == 0
	if aZero || bZero {
		switch {
		case aZero && bZero:
			return 0
		case aZero:
			if b.neg {
				return 1
			}
			return -1
		default:
			if a.neg {
				return -1
			}
			return 1
		}
	}
	if a.neg != b.neg {
		if a.neg {
			return -1
		}
		return 1
	}
	cmp := comparePositiveDecimal(a, b)
	if a.neg {
		return -cmp
	}
	return cmp
}

func comparePositiveDecimal(a, b decimal) int {
	// Order-of-magnitude is exp + number of decimal digits.
	magA := new(big.Int).Add(a.exp, big.NewInt(int64(digitLen(a.mant))))
	magB := new(big.Int).Add(b.exp, big.NewInt(int64(digitLen(b.mant))))
	if c := magA.Cmp(magB); c != 0 {
		return c
	}
	as := a.mant.String()
	bs := b.mant.String()
	if len(as) < len(bs) {
		as += strings.Repeat("0", len(bs)-len(as))
	} else if len(bs) < len(as) {
		bs += strings.Repeat("0", len(as)-len(bs))
	}
	return strings.Compare(as, bs)
}

func digitLen(n *big.Int) int {
	if n.Sign() == 0 {
		return 1
	}
	return len(n.String())
}

// parseDecimal accepts an optional leading sign, a decimal significand, and a
// scientific exponent. A bare exponent marker is rejected. NaN is rejected.
func parseDecimal(s string) (decimal, bool) {
	if s == "" || isNaNLiteral(s) {
		return decimal{}, false
	}
	i := 0
	neg := false
	if s[0] == '+' || s[0] == '-' {
		neg = s[0] == '-'
		i++
		if i >= len(s) {
			return decimal{}, false
		}
	}
	mant, exp10, next, ok := parseSignificand(s, i)
	if !ok {
		return decimal{}, false
	}
	i = next
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i >= len(s) {
			return decimal{}, false
		}
		expNeg := false
		if s[i] == '+' || s[i] == '-' {
			expNeg = s[i] == '-'
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return decimal{}, false
		}
		exp := new(big.Int)
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			exp.Mul(exp, big.NewInt(10))
			exp.Add(exp, big.NewInt(int64(s[i]-'0')))
			i++
		}
		if expNeg {
			exp.Neg(exp)
		}
		exp10.Add(exp10, exp)
	}
	if i != len(s) {
		return decimal{}, false
	}
	normalizeDecimal(mant, exp10)
	return decimal{neg: neg && mant.Sign() != 0, mant: mant, exp: exp10}, true
}

func parseSignificand(s string, i int) (mant, exp10 *big.Int, next int, ok bool) {
	start := i
	digits := 0
	frac := -1
	for i < len(s) {
		c := s[i]
		if c >= '0' && c <= '9' {
			digits++
			i++
			continue
		}
		if c == '.' && frac < 0 {
			frac = digits
			i++
			continue
		}
		break
	}
	if digits == 0 || (i > start && s[i-1] == '.' && digits == 0) {
		return nil, nil, 0, false
	}
	raw := strings.ReplaceAll(s[start:i], ".", "")
	mant = new(big.Int)
	if _, ok := mant.SetString(raw, 10); !ok {
		return nil, nil, 0, false
	}
	exp10 = new(big.Int)
	if frac >= 0 {
		exp10.SetInt64(-int64(digits - frac))
	}
	return mant, exp10, i, true
}

func normalizeDecimal(mant, exp *big.Int) {
	if mant.Sign() == 0 {
		exp.SetInt64(0)
		return
	}
	ten := big.NewInt(10)
	zero := big.NewInt(0)
	mod := new(big.Int)
	for {
		mod.Mod(mant, ten)
		if mod.Cmp(zero) != 0 {
			return
		}
		mant.Quo(mant, ten)
		exp.Add(exp, big.NewInt(1))
	}
}

var durationUnits = []struct {
	name string
	pos  int
	ns   *big.Rat
}{
	{"y", 1, ratInt(365 * 24 * int64(time.Hour))},
	{"w", 2, ratInt(7 * 24 * int64(time.Hour))},
	{"d", 3, ratInt(24 * int64(time.Hour))},
	{"h", 4, ratInt(int64(time.Hour))},
	{"m", 5, ratInt(int64(time.Minute))},
	{"s", 6, ratInt(int64(time.Second))},
	{"ms", 7, ratInt(int64(time.Millisecond))},
}

func ratInt(n int64) *big.Rat {
	return new(big.Rat).SetInt64(n)
}

// parseDurationMag parses a Prometheus-style duration with an optional leading
// sign and scientific-notation coefficients. Units must run from largest to
// smallest. The result is an exact nanosecond magnitude.
func parseDurationMag(s string) (*big.Rat, bool) {
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
	saw := false
	for s != "" {
		// The optional sign applies to the whole duration. A component
		// that starts with a sign is not a valid unit sequence.
		coeff, rest, ok := parseUnsignedCoeffPrefix(s)
		if !ok || rest == "" {
			return nil, false
		}
		unit, rest, pos, ok := matchDurationUnit(rest)
		if !ok || pos <= lastPos {
			return nil, false
		}
		lastPos = pos
		total.Add(total, new(big.Rat).Mul(coeff, unit))
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

func matchDurationUnit(s string) (scale *big.Rat, rest string, pos int, ok bool) {
	for _, u := range durationUnits {
		if u.name == "m" {
			continue
		}
		if strings.HasPrefix(s, u.name) {
			return u.ns, s[len(u.name):], u.pos, true
		}
	}
	if strings.HasPrefix(s, "m") {
		for _, u := range durationUnits {
			if u.name == "m" {
				return u.ns, s[1:], u.pos, true
			}
		}
	}
	return nil, "", 0, false
}

func parseUnsignedCoeffPrefix(s string) (*big.Rat, string, bool) {
	return parseCoeffPrefix(s, false)
}

func parseCoeffPrefix(s string, allowSign bool) (coeff *big.Rat, rest string, ok bool) {
	if s == "" {
		return nil, "", false
	}
	i := 0
	neg := false
	if allowSign && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		i++
	}
	mant, exp10, next, ok := parseSignificand(s, i)
	if !ok {
		return nil, "", false
	}
	i = next
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j >= len(s) {
			return nil, "", false
		}
		expNeg := false
		if s[j] == '+' || s[j] == '-' {
			expNeg = s[j] == '-'
			j++
		}
		if j >= len(s) || s[j] < '0' || s[j] > '9' {
			return nil, "", false
		}
		exp := new(big.Int)
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			exp.Mul(exp, big.NewInt(10))
			exp.Add(exp, big.NewInt(int64(s[j]-'0')))
			j++
		}
		if expNeg {
			exp.Neg(exp)
		}
		exp10.Add(exp10, exp)
		i = j
	}
	normalizeDecimal(mant, exp10)
	coeff = new(big.Rat).SetInt(mant)
	if exp10.Sign() != 0 {
		pow := new(big.Int).Exp(big.NewInt(10), new(big.Int).Abs(exp10), nil)
		scale := new(big.Rat).SetInt(pow)
		if exp10.Sign() > 0 {
			coeff.Mul(coeff, scale)
		} else {
			coeff.Quo(coeff, scale)
		}
	}
	if neg {
		coeff.Neg(coeff)
	}
	return coeff, s[i:], true
}

func parseBytesMag(s string) (*big.Rat, bool) {
	if s == "" {
		return nil, false
	}
	// Longest suffix wins so KiB is not read as B.
	suffixes := []struct {
		name  string
		scale *big.Rat
	}{
		{"yib", ratPow1024(8)},
		{"zib", ratPow1024(7)},
		{"eib", ratPow1024(6)},
		{"pib", ratPow1024(5)},
		{"tib", ratPow1024(4)},
		{"gib", ratPow1024(3)},
		{"mib", ratPow1024(2)},
		{"kib", ratPow1024(1)},
		{"yb", ratPow1000(8)},
		{"zb", ratPow1000(7)},
		{"eb", ratPow1000(6)},
		{"pb", ratPow1000(5)},
		{"tb", ratPow1000(4)},
		{"gb", ratPow1000(3)},
		{"mb", ratPow1000(2)},
		{"kb", ratPow1000(1)},
		{"b", ratInt(1)},
	}
	lower := strings.ToLower(s)
	var unit *big.Rat
	body := ""
	for _, suf := range suffixes {
		if strings.HasSuffix(lower, suf.name) && len(lower) > len(suf.name) {
			body = s[:len(s)-len(suf.name)]
			unit = suf.scale
			break
		}
	}
	if unit == nil {
		return nil, false
	}
	coeff, rest, ok := parseCoeffPrefix(body, true)
	if !ok || rest != "" {
		return nil, false
	}
	return new(big.Rat).Mul(coeff, unit), true
}

func ratPow1000(n int) *big.Rat {
	base := big.NewInt(1000)
	return new(big.Rat).SetInt(new(big.Int).Exp(base, big.NewInt(int64(n)), nil))
}

func ratPow1024(n int) *big.Rat {
	base := big.NewInt(1024)
	return new(big.Rat).SetInt(new(big.Int).Exp(base, big.NewInt(int64(n)), nil))
}

type semver struct {
	major, minor, patch *big.Int
	pre                 []preIdent
}

type preIdent struct {
	num    *big.Int // nil when the identifier is non-numeric
	numSet bool
	text   string
}

func parseSemver(s string) (semver, bool) {
	if len(s) > 1 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	major, rest, ok := parseSemverNum(s)
	if !ok || rest == "" || rest[0] != '.' {
		return semver{}, false
	}
	minor, rest, ok := parseSemverNum(rest[1:])
	if !ok || rest == "" || rest[0] != '.' {
		return semver{}, false
	}
	patch, rest, ok := parseSemverNum(rest[1:])
	if !ok {
		return semver{}, false
	}
	var pre []preIdent
	if rest != "" {
		if rest[0] == '+' {
			if !validBuild(rest[1:]) {
				return semver{}, false
			}
		} else if rest[0] == '-' {
			var build string
			prePart := rest[1:]
			if i := strings.IndexByte(prePart, '+'); i >= 0 {
				build = prePart[i+1:]
				prePart = prePart[:i]
			}
			pre, ok = parsePre(prePart)
			if !ok || (build != "" && !validBuild(build)) {
				return semver{}, false
			}
		} else {
			return semver{}, false
		}
	}
	return semver{major: major, minor: minor, patch: patch, pre: pre}, true
}

func parseSemverNum(s string) (*big.Int, string, bool) {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return nil, "", false
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	raw := s[:i]
	if len(raw) > 1 && raw[0] == '0' {
		return nil, "", false
	}
	n := new(big.Int)
	if _, ok := n.SetString(raw, 10); !ok {
		return nil, "", false
	}
	return n, s[i:], true
}

func parsePre(s string) ([]preIdent, bool) {
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	out := make([]preIdent, 0, len(parts))
	for _, p := range parts {
		if p == "" || !validIdent(p) {
			return nil, false
		}
		id := preIdent{text: p}
		if allDigits(p) {
			if len(p) > 1 && p[0] == '0' {
				return nil, false
			}
			id.num = new(big.Int)
			id.num.SetString(p, 10)
			id.numSet = true
		}
		out = append(out, id)
	}
	return out, true
}

func validBuild(s string) bool {
	if s == "" {
		return false
	}
	for _, p := range strings.Split(s, ".") {
		if p == "" || !validIdent(p) {
			return false
		}
	}
	return true
}

func validIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-' {
			continue
		}
		return false
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func compareSemver(a, b semver) int {
	if c := a.major.Cmp(b.major); c != 0 {
		return c
	}
	if c := a.minor.Cmp(b.minor); c != 0 {
		return c
	}
	if c := a.patch.Cmp(b.patch); c != 0 {
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
	for i := 0; i < n; i++ {
		if c := comparePre(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return len(a.pre) - len(b.pre)
}

func comparePre(a, b preIdent) int {
	if a.numSet && b.numSet {
		return a.num.Cmp(b.num)
	}
	if a.numSet != b.numSet {
		if a.numSet {
			return -1
		}
		return 1
	}
	return strings.Compare(a.text, b.text)
}

type ipKey struct {
	addr netip.Addr
	v4   bool
}

func parseIPKey(s string) (ipKey, bool) {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return ipKey{}, false
	}
	// IPv4-mapped IPv6 literals contain ':' and stay in the IPv6 class.
	v4 := !strings.Contains(s, ":") && addr.Is4()
	return ipKey{addr: addr, v4: v4}, true
}

func compareIP(a, b ipKey) int {
	if a.v4 != b.v4 {
		if a.v4 {
			return -1
		}
		return 1
	}
	return a.addr.Compare(b.addr)
}

type cidrKey struct {
	addr netip.Addr
	bits int
	v4   bool
}

func parseCIDRKey(s string) (cidrKey, bool) {
	pfx, err := netip.ParsePrefix(s)
	if err != nil {
		return cidrKey{}, false
	}
	masked := pfx.Masked()
	addr := masked.Addr()
	v4 := !strings.Contains(s, ":") && addr.Is4()
	return cidrKey{addr: addr, bits: masked.Bits(), v4: v4}, true
}

func compareCIDR(a, b cidrKey) int {
	if a.v4 != b.v4 {
		if a.v4 {
			return -1
		}
		return 1
	}
	if c := a.addr.Compare(b.addr); c != 0 {
		return c
	}
	return a.bits - b.bits
}

func parseTimestamp(s string) (time.Time, bool) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}
