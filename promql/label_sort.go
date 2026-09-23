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

	"github.com/prometheus/prometheus/model/labels"
)

// labelValueClass orders the different typed interpretations of a label value.
type labelValueClass int

const (
	classLeadingSpace labelValueClass = iota
	classPosInf
	classNumber
	classNegInf
	classDuration
	classBytes
	classSemver
	classIP
	classCIDR
	classTimestamp
	classString
)

// labelSortKey is the parsed, comparable form of a label value.
type labelSortKey struct {
	raw   string
	class labelValueClass
	dec   bigDecimal // classNumber, classDuration (nanoseconds), classBytes.
	ver   semVersion
	addr  netip.Addr
	pfx   netip.Prefix
	ts    time.Time
}

// sortVectorByLabels sorts vec by the typed values of lbls, falling back to
// the full label set when all of them are equal.
func sortVectorByLabels(vec Vector, lbls []string, desc bool) {
	type keyedSample struct {
		s    Sample
		keys []labelSortKey
	}
	keys := make([]labelSortKey, len(vec)*len(lbls))
	items := make([]keyedSample, len(vec))
	for i, s := range vec {
		k := keys[i*len(lbls) : (i+1)*len(lbls)]
		for j, l := range lbls {
			k[j] = parseLabelSortKey(s.Metric.Get(l))
		}
		items[i] = keyedSample{s: s, keys: k}
	}

	slices.SortFunc(items, func(a, b keyedSample) int {
		c := 0
		for j := range a.keys {
			if c = compareLabelSortKeys(&a.keys[j], &b.keys[j]); c != 0 {
				break
			}
		}
		if c == 0 {
			c = labels.Compare(a.s.Metric, b.s.Metric)
		}
		if desc {
			return -c
		}
		return c
	})

	for i := range items {
		vec[i] = items[i].s
	}
}

// compareLabelValues compares two label values using typed, multi-domain
// ordering. It is a total order: it only returns 0 for identical strings.
func compareLabelValues(a, b string) int {
	ka, kb := parseLabelSortKey(a), parseLabelSortKey(b)
	return compareLabelSortKeys(&ka, &kb)
}

func compareLabelSortKeys(a, b *labelSortKey) int {
	if a.class != b.class {
		return cmp.Compare(a.class, b.class)
	}
	c := 0
	switch a.class {
	case classNumber, classDuration, classBytes:
		c = a.dec.cmp(b.dec)
	case classSemver:
		c = a.ver.cmp(b.ver)
	case classIP:
		c = a.addr.Compare(b.addr)
	case classCIDR:
		c = comparePrefixes(a.pfx, b.pfx)
	case classTimestamp:
		c = a.ts.Compare(b.ts)
	}
	if c != 0 {
		return c
	}
	return compareNatural(a.raw, b.raw)
}

func parseLabelSortKey(s string) labelSortKey {
	k := labelSortKey{raw: s, class: classString}
	if s == "" {
		return k
	}
	if r, _ := utf8.DecodeRuneInString(s); unicode.IsSpace(r) {
		k.class = classLeadingSpace
		return k
	}
	if inf, ok := parseInfinity(s); ok {
		k.class = classNegInf
		if inf > 0 {
			k.class = classPosInf
		}
		return k
	}
	if d, ok := parseNumber(s); ok {
		k.class, k.dec = classNumber, d
		return k
	}
	if d, ok := parseDurationValue(s); ok {
		k.class, k.dec = classDuration, d
		return k
	}
	if d, ok := parseBytesValue(s); ok {
		k.class, k.dec = classBytes, d
		return k
	}
	if v, ok := parseSemver(s); ok {
		k.class, k.ver = classSemver, v
		return k
	}
	if a, err := netip.ParseAddr(s); err == nil {
		k.class, k.addr = classIP, a
		return k
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		k.class, k.pfx = classCIDR, p
		return k
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		k.class, k.ts = classTimestamp, t
		return k
	}
	return k
}

// splitSign strips an optional leading '+' or '-' and reports whether the
// value is negative.
func splitSign(s string) (string, bool) {
	if s != "" && (s[0] == '+' || s[0] == '-') {
		return s[1:], s[0] == '-'
	}
	return s, false
}

func parseInfinity(s string) (int, bool) {
	rest, neg := splitSign(s)
	if !strings.EqualFold(rest, "inf") && !strings.EqualFold(rest, "infinity") {
		return 0, false
	}
	if neg {
		return -1, true
	}
	return 1, true
}

func parseNumber(s string) (bigDecimal, bool) {
	rest, neg := splitSign(s)
	d, n, ok := scanUnsignedDecimal(rest)
	if !ok || n != len(rest) {
		return bigDecimal{}, false
	}
	return d.withSign(neg), true
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func scanDigits(s string, i int) int {
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}

// scanUnsignedDecimal scans a prefix of s of the form
// digits[.digits][e[+-]digits] or .digits[e[+-]digits]. An exponent marker
// that is not followed by digits is not consumed.
func scanUnsignedDecimal(s string) (bigDecimal, int, bool) {
	intEnd := scanDigits(s, 0)
	intPart, fracPart := s[:intEnd], ""
	i := intEnd
	if i < len(s) && s[i] == '.' {
		fracEnd := scanDigits(s, i+1)
		fracPart = s[i+1 : fracEnd]
		i = fracEnd
	}
	if intPart == "" && fracPart == "" {
		return bigDecimal{}, 0, false
	}
	exp := new(big.Int)
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if expEnd := scanDigits(s, j); expEnd > j {
			exp.SetString(s[j:expEnd], 10)
			if s[j-1] == '-' {
				exp.Neg(exp)
			}
			i = expEnd
		}
	}
	exp.Sub(exp, big.NewInt(int64(len(fracPart))))
	return newBigDecimal(intPart+fracPart, exp), i, true
}

var durationUnits = []struct {
	unit string
	ns   int64
}{
	{"ns", 1},
	{"us", 1e3},
	{"µs", 1e3},
	{"μs", 1e3},
	{"ms", 1e6},
	{"s", 1e9},
	{"m", 60 * 1e9},
	{"h", 60 * 60 * 1e9},
	{"d", 24 * 60 * 60 * 1e9},
	{"w", 7 * 24 * 60 * 60 * 1e9},
	{"y", 365 * 24 * 60 * 60 * 1e9},
}

// maxDurationExponentSpread bounds the work needed to exactly add the
// components of a compound duration such as "1h30m".
const maxDurationExponentSpread = 1024

// parseDurationValue parses [+-](number unit)+ into nanoseconds.
func parseDurationValue(s string) (bigDecimal, bool) {
	rest, neg := splitSign(s)
	var parts []bigDecimal
	for rest != "" {
		d, n, ok := scanUnsignedDecimal(rest)
		if !ok {
			return bigDecimal{}, false
		}
		rest = rest[n:]
		matched := false
		for _, u := range durationUnits {
			if strings.HasPrefix(rest, u.unit) {
				parts = append(parts, d.mulInt(big.NewInt(u.ns)))
				rest = rest[len(u.unit):]
				matched = true
				break
			}
		}
		if !matched {
			return bigDecimal{}, false
		}
	}
	if len(parts) == 0 {
		return bigDecimal{}, false
	}
	sum, ok := sumNonNegative(parts, maxDurationExponentSpread)
	if !ok {
		return bigDecimal{}, false
	}
	return sum.withSign(neg), true
}

// parseBytesValue parses [+-]number unit where unit is B, or a decimal (KB,
// MB, ...) or binary (KiB, MiB, ...) multiple of it, case-insensitively.
func parseBytesValue(s string) (bigDecimal, bool) {
	rest, neg := splitSign(s)
	d, n, ok := scanUnsignedDecimal(rest)
	if !ok {
		return bigDecimal{}, false
	}
	unit := strings.ToLower(rest[n:])
	if !strings.HasSuffix(unit, "b") {
		return bigDecimal{}, false
	}
	unit = unit[:len(unit)-1]
	base := int64(1000)
	if strings.HasSuffix(unit, "i") && len(unit) == 2 {
		unit, base = unit[:1], 1024
	}
	power := 0
	if unit != "" {
		if len(unit) != 1 {
			return bigDecimal{}, false
		}
		idx := strings.IndexByte("kmgtpezy", unit[0])
		if idx < 0 {
			return bigDecimal{}, false
		}
		power = idx + 1
	}
	factor := new(big.Int).Exp(big.NewInt(base), big.NewInt(int64(power)), nil)
	return d.mulInt(factor).withSign(neg), true
}

// bigDecimal is an exact decimal: sign * digits * 10^exp, where digits has no
// leading or trailing zeros. Zero has sign 0 and empty digits.
type bigDecimal struct {
	sign   int
	digits string
	exp    *big.Int
}

func newBigDecimal(digits string, exp *big.Int) bigDecimal {
	digits = strings.TrimLeft(digits, "0")
	trimmed := strings.TrimRight(digits, "0")
	if trimmed == "" {
		return bigDecimal{}
	}
	e := new(big.Int).Add(exp, big.NewInt(int64(len(digits)-len(trimmed))))
	return bigDecimal{sign: 1, digits: trimmed, exp: e}
}

func (d bigDecimal) withSign(neg bool) bigDecimal {
	if neg && d.sign != 0 {
		d.sign = -1
	}
	return d
}

func (d bigDecimal) mulInt(f *big.Int) bigDecimal {
	if d.sign == 0 {
		return d
	}
	m, _ := new(big.Int).SetString(d.digits, 10)
	r := newBigDecimal(m.Mul(m, f).String(), d.exp)
	return r.withSign(d.sign < 0)
}

func (d bigDecimal) cmp(o bigDecimal) int {
	if d.sign != o.sign {
		return cmp.Compare(d.sign, o.sign)
	}
	if d.sign == 0 {
		return 0
	}
	// Compare the position of the most significant digit first.
	da := new(big.Int).Add(d.exp, big.NewInt(int64(len(d.digits))))
	oa := new(big.Int).Add(o.exp, big.NewInt(int64(len(o.digits))))
	c := da.Cmp(oa)
	if c == 0 {
		c = strings.Compare(d.digits, o.digits)
	}
	return c * d.sign
}

// sumNonNegative exactly adds non-negative decimals, failing if their
// exponents differ by more than maxSpread.
func sumNonNegative(parts []bigDecimal, maxSpread int64) (bigDecimal, bool) {
	var minExp, maxExp *big.Int
	for _, p := range parts {
		if p.sign == 0 {
			continue
		}
		if minExp == nil || p.exp.Cmp(minExp) < 0 {
			minExp = p.exp
		}
		if maxExp == nil || p.exp.Cmp(maxExp) > 0 {
			maxExp = p.exp
		}
	}
	if minExp == nil {
		return bigDecimal{}, true
	}
	if new(big.Int).Sub(maxExp, minExp).Cmp(big.NewInt(maxSpread)) > 0 {
		return bigDecimal{}, false
	}
	sum := new(big.Int)
	ten := big.NewInt(10)
	for _, p := range parts {
		if p.sign == 0 {
			continue
		}
		m, _ := new(big.Int).SetString(p.digits, 10)
		shift := new(big.Int).Exp(ten, new(big.Int).Sub(p.exp, minExp), nil)
		sum.Add(sum, m.Mul(m, shift))
	}
	return newBigDecimal(sum.String(), minExp), true
}

// semVersion is a parsed semantic version (https://semver.org). Build
// metadata does not affect precedence and is not retained.
type semVersion struct {
	major, minor, patch string
	pre                 []string
}

func parseSemver(s string) (semVersion, bool) {
	s = strings.TrimPrefix(s, "v")
	s, build, hasBuild := strings.Cut(s, "+")
	if hasBuild && !validSemverIdents(build, false) {
		return semVersion{}, false
	}
	core, pre, hasPre := strings.Cut(s, "-")
	if hasPre && !validSemverIdents(pre, true) {
		return semVersion{}, false
	}
	nums := strings.Split(core, ".")
	if len(nums) != 3 {
		return semVersion{}, false
	}
	for _, n := range nums {
		if !isSemverNumber(n) {
			return semVersion{}, false
		}
	}
	v := semVersion{major: nums[0], minor: nums[1], patch: nums[2]}
	if hasPre {
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

func isAllDigits(s string) bool {
	return s != "" && scanDigits(s, 0) == len(s)
}

func isSemverNumber(s string) bool {
	return isAllDigits(s) && (s == "0" || s[0] != '0')
}

func validSemverIdents(s string, numericNoLeadingZero bool) bool {
	for id := range strings.SplitSeq(s, ".") {
		if id == "" {
			return false
		}
		for i := 0; i < len(id); i++ {
			c := id[i]
			if !isDigit(c) && c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
				return false
			}
		}
		if numericNoLeadingZero && isAllDigits(id) && !isSemverNumber(id) {
			return false
		}
	}
	return true
}

// compareDigitStrings numerically compares digit strings without leading zeros.
func compareDigitStrings(a, b string) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func (v semVersion) cmp(o semVersion) int {
	for _, c := range [...]int{
		compareDigitStrings(v.major, o.major),
		compareDigitStrings(v.minor, o.minor),
		compareDigitStrings(v.patch, o.patch),
	} {
		if c != 0 {
			return c
		}
	}
	switch {
	case len(v.pre) == 0 && len(o.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(o.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(o.pre); i++ {
		a, b := v.pre[i], o.pre[i]
		aNum, bNum := isAllDigits(a), isAllDigits(b)
		var c int
		switch {
		case aNum && bNum:
			c = compareDigitStrings(a, b)
		case aNum:
			c = -1
		case bNum:
			c = 1
		default:
			c = strings.Compare(a, b)
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(v.pre), len(o.pre))
}

// comparePrefixes orders IPv4 before IPv6, then by network address, then by
// prefix length.
func comparePrefixes(a, b netip.Prefix) int {
	if c := cmp.Compare(a.Addr().BitLen(), b.Addr().BitLen()); c != 0 {
		return c
	}
	if c := a.Masked().Addr().Compare(b.Masked().Addr()); c != 0 {
		return c
	}
	return cmp.Compare(a.Bits(), b.Bits())
}

// compareNatural compares strings in natural order, treating runs of ASCII
// digits as arbitrarily large integers. Strings that are naturally equal (e.g.
// "01" and "1") are ordered bytewise, so this is a total order.
func compareNatural(a, b string) int {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		aDigit, bDigit := isDigit(a[i]), isDigit(b[j])
		ie, je := i, j
		if aDigit {
			ie = scanDigits(a, i)
		} else {
			for ie < len(a) && !isDigit(a[ie]) {
				ie++
			}
		}
		if bDigit {
			je = scanDigits(b, j)
		} else {
			for je < len(b) && !isDigit(b[je]) {
				je++
			}
		}
		ca, cb := a[i:ie], b[j:je]
		var c int
		if aDigit && bDigit {
			c = compareDigitStrings(strings.TrimLeft(ca, "0"), strings.TrimLeft(cb, "0"))
		} else {
			c = strings.Compare(ca, cb)
		}
		if c != 0 {
			return c
		}
		i, j = ie, je
	}
	if c := cmp.Compare(len(a)-i, len(b)-j); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}
