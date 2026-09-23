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

// labelValueClass is the domain a label value is interpreted in when sorting
// by label. Values of a lower class sort before values of a higher class.
type labelValueClass uint8

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
	classUntyped
)

// labelSortKey is a label value together with its typed interpretation.
type labelSortKey struct {
	raw   string
	class labelValueClass
	num   bigDecimal // classNumber, classDuration (in nanoseconds) and classBytes.
	ver   semver     // classSemver.
	addr  netip.Addr // classIP, and the masked network address for classCIDR.
	bits  int        // classCIDR.
	ts    time.Time  // classTimestamp.
}

func newLabelSortKey(s string) labelSortKey {
	k := labelSortKey{raw: s, class: classUntyped}
	if r, _ := utf8.DecodeRuneInString(s); unicode.IsSpace(r) {
		k.class = classLeadingSpace
		return k
	}
	if class, ok := parseInfinity(s); ok {
		k.class = class
		return k
	}

	var ok bool
	if k.num, ok = parseNumber(s); ok {
		k.class = classNumber
	} else if k.num, ok = parseDuration(s); ok {
		k.class = classDuration
	} else if k.num, ok = parseBytes(s); ok {
		k.class = classBytes
	} else if k.ver, ok = parseSemver(s); ok {
		k.class = classSemver
	} else if addr, err := netip.ParseAddr(s); err == nil {
		k.class, k.addr = classIP, addr
	} else if pfx, err := netip.ParsePrefix(s); err == nil {
		k.class, k.addr, k.bits = classCIDR, pfx.Masked().Addr(), pfx.Bits()
	} else if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		k.class, k.ts = classTimestamp, ts
	}
	return k
}

// compareLabelSortKeys orders label values by class, then by typed value
// within the class, and finally by natural order of the raw strings. It is a
// total order: the result is 0 only if both raw values are identical.
func compareLabelSortKeys(a, b *labelSortKey) int {
	if a.raw == b.raw {
		return 0
	}
	if c := cmp.Compare(a.class, b.class); c != 0 {
		return c
	}
	var c int
	switch a.class {
	case classNumber, classDuration, classBytes:
		c = a.num.cmp(b.num)
	case classSemver:
		c = a.ver.cmp(b.ver)
	case classIP:
		c = a.addr.Compare(b.addr)
	case classCIDR:
		c = cmp.Or(a.addr.Compare(b.addr), cmp.Compare(a.bits, b.bits))
	case classTimestamp:
		c = a.ts.Compare(b.ts)
	}
	if c != 0 {
		return c
	}
	return naturalCompare(a.raw, b.raw)
}

// sortByLabel sorts vec in place by the values of lbls. Samples whose values
// are equal for all of lbls are ordered by their full label sets.
func sortByLabel(vec Vector, lbls []string, desc bool) Vector {
	type keyedSample struct {
		keys   []labelSortKey
		sample Sample
	}
	keys := make([]labelSortKey, len(vec)*len(lbls))
	keyed := make([]keyedSample, len(vec))
	for i, s := range vec {
		ks := keys[i*len(lbls) : (i+1)*len(lbls)]
		for j, l := range lbls {
			ks[j] = newLabelSortKey(s.Metric.Get(l))
		}
		keyed[i] = keyedSample{keys: ks, sample: s}
	}

	slices.SortFunc(keyed, func(a, b keyedSample) int {
		c := 0
		for j := range a.keys {
			if c = compareLabelSortKeys(&a.keys[j], &b.keys[j]); c != 0 {
				break
			}
		}
		if c == 0 {
			// If all labels provided as arguments were equal, sort by the full label set. This ensures a consistent ordering.
			c = labels.Compare(a.sample.Metric, b.sample.Metric)
		}
		if desc {
			return -c
		}
		return c
	})

	for i := range keyed {
		vec[i] = keyed[i].sample
	}
	return vec
}

// naturalCompare compares a and b in natural order: runs of ASCII digits are
// compared by numeric value and all other runs byte-wise. Strings whose runs
// only differ in leading zeros are ordered byte-wise, so the result is 0 only
// if a == b.
func naturalCompare(a, b string) int {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ai, bj := runEnd(a, i), runEnd(b, j)
		ra, rb := a[i:ai], b[j:bj]
		var c int
		if isDigit(ra[0]) && isDigit(rb[0]) {
			c = cmpDigits(ra, rb)
		} else {
			c = strings.Compare(ra, rb)
		}
		if c != 0 {
			return c
		}
		i, j = ai, bj
	}
	switch {
	case i < len(a):
		return 1
	case j < len(b):
		return -1
	}
	return strings.Compare(a, b)
}

// runEnd returns the end of the run of digits or non-digits starting at s[i].
func runEnd(s string, i int) int {
	digit := isDigit(s[i])
	for i++; i < len(s) && isDigit(s[i]) == digit; i++ {
	}
	return i
}

func digitsEnd(s string, i int) int {
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}

func isDigit(c byte) bool {
	return '0' <= c && c <= '9'
}

func isDigits(s string) bool {
	return s != "" && digitsEnd(s, 0) == len(s)
}

// cmpDigits compares two non-empty strings of ASCII digits by numeric value.
func cmpDigits(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// bigDecimal is an exact decimal number with the value 0.digits × 10^exp.
// digits has neither leading nor trailing zeros and is empty for zero, so two
// values can be compared without materializing them, however large exp is.
type bigDecimal struct {
	neg    bool
	digits string
	exp    *big.Int
}

// newBigDecimal returns the value mantissa × 10^exp, where mantissa is a string
// of ASCII digits.
func newBigDecimal(neg bool, mantissa string, exp *big.Int) bigDecimal {
	mantissa = strings.TrimLeft(mantissa, "0")
	digits := strings.TrimRight(mantissa, "0")
	if digits == "" {
		return bigDecimal{}
	}
	e := new(big.Int).Add(exp, big.NewInt(int64(len(mantissa))))
	return bigDecimal{neg: neg, digits: digits, exp: e}
}

func (d bigDecimal) sign() int {
	switch {
	case d.digits == "":
		return 0
	case d.neg:
		return -1
	}
	return 1
}

func (d bigDecimal) cmp(o bigDecimal) int {
	sign := d.sign()
	if c := cmp.Compare(sign, o.sign()); c != 0 || sign == 0 {
		return c
	}
	c := d.exp.Cmp(o.exp)
	if c == 0 {
		// Without trailing zeros, digits compare like the fractions they denote.
		c = strings.Compare(d.digits, o.digits)
	}
	return sign * c
}

// mulInt returns d × m for a positive m.
func (d bigDecimal) mulInt(m *big.Int) bigDecimal {
	if d.digits == "" {
		return d
	}
	n, _ := new(big.Int).SetString(d.digits, 10)
	e := new(big.Int).Sub(d.exp, big.NewInt(int64(len(d.digits))))
	return newBigDecimal(d.neg, n.Mul(n, m).String(), e)
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// parseInfinity parses case-insensitive "inf" or "infinity" with an optional
// sign.
func parseInfinity(s string) (labelValueClass, bool) {
	class := classPosInf
	if s != "" && (s[0] == '+' || s[0] == '-') {
		if s[0] == '-' {
			class = classNegInf
		}
		s = s[1:]
	}
	if strings.EqualFold(s, "inf") || strings.EqualFold(s, "infinity") {
		return class, true
	}
	return 0, false
}

func parseNumber(s string) (bigDecimal, bool) {
	d, rest, ok := scanDecimal(s)
	if !ok || rest != "" {
		return bigDecimal{}, false
	}
	return d, true
}

// scanDecimal scans an optionally signed decimal number with an optional
// exponent from the start of s and returns it along with the rest of s. An
// exponent marker that isn't followed by digits is left in the rest.
func scanDecimal(s string) (bigDecimal, string, bool) {
	i, neg := 0, false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	start := i
	i = digitsEnd(s, i)
	intPart, fracPart := s[start:i], ""
	if i < len(s) && s[i] == '.' {
		j := digitsEnd(s, i+1)
		fracPart, i = s[i+1:j], j
	}
	if intPart == "" && fracPart == "" {
		return bigDecimal{}, s, false
	}

	exp := big.NewInt(-int64(len(fracPart)))
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if k := digitsEnd(s, j); k > j {
			e, _ := new(big.Int).SetString(s[i+1:k], 10)
			exp.Add(exp, e)
			i = k
		}
	}
	return newBigDecimal(neg, intPart+fracPart, exp), s[i:], true
}

var durationUnits = map[string]*big.Int{
	"ns": big.NewInt(int64(time.Nanosecond)),
	"us": big.NewInt(int64(time.Microsecond)),
	"µs": big.NewInt(int64(time.Microsecond)), // U+00B5 MICRO SIGN.
	"μs": big.NewInt(int64(time.Microsecond)), // U+03BC GREEK SMALL LETTER MU.
	"ms": big.NewInt(int64(time.Millisecond)),
	"s":  big.NewInt(int64(time.Second)),
	"m":  big.NewInt(int64(time.Minute)),
	"h":  big.NewInt(int64(time.Hour)),
	"d":  big.NewInt(int64(24 * time.Hour)),
	"w":  big.NewInt(int64(7 * 24 * time.Hour)),
	"y":  big.NewInt(int64(365 * 24 * time.Hour)),
}

// parseDuration parses either a single term with a signed, possibly scientific
// coefficient like "-1.5e3ms", or a sum of unsigned terms like "1h30.5m" with
// an optional leading sign. The result is in nanoseconds.
func parseDuration(s string) (bigDecimal, bool) {
	if d, rest, ok := scanDecimal(s); ok {
		if unit, ok := durationUnits[rest]; ok {
			return d.mulInt(unit), true
		}
	}

	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if s == "" {
		return bigDecimal{}, false
	}
	// The sum is total × 10^-scale nanoseconds.
	total, scale := new(big.Int), 0
	for s != "" {
		i := digitsEnd(s, 0)
		intPart, fracPart := s[:i], ""
		if i < len(s) && s[i] == '.' {
			j := digitsEnd(s, i+1)
			fracPart, i = s[i+1:j], j
		}
		if intPart == "" && fracPart == "" {
			return bigDecimal{}, false
		}
		j := i
		for j < len(s) && !isDigit(s[j]) && s[j] != '.' {
			j++
		}
		unit, ok := durationUnits[s[i:j]]
		if !ok {
			return bigDecimal{}, false
		}

		v, _ := new(big.Int).SetString(intPart+fracPart, 10)
		v.Mul(v, unit)
		if len(fracPart) > scale {
			total.Mul(total, pow10(len(fracPart)-scale))
			scale = len(fracPart)
		} else {
			v.Mul(v, pow10(scale-len(fracPart)))
		}
		total.Add(total, v)
		s = s[j:]
	}
	return newBigDecimal(neg, total.String(), big.NewInt(-int64(scale))), true
}

// byteUnits maps lower-cased SI (powers of 1000) and IEC (powers of 1024) byte
// units to their size in bytes.
var byteUnits = func() map[string]*big.Int {
	units := map[string]*big.Int{"b": big.NewInt(1)}
	si, iec := big.NewInt(1), big.NewInt(1)
	for _, prefix := range []string{"k", "m", "g", "t", "p", "e", "z", "y"} {
		si = new(big.Int).Mul(si, big.NewInt(1000))
		iec = new(big.Int).Lsh(iec, 10)
		units[prefix+"b"], units[prefix+"ib"] = si, iec
	}
	return units
}()

// parseBytes parses a signed, possibly scientific coefficient followed by a
// case-insensitive byte unit, like "1.5GiB" or "-2e3kB". The result is in
// bytes.
func parseBytes(s string) (bigDecimal, bool) {
	d, rest, ok := scanDecimal(s)
	if !ok || len(rest) > len("kib") {
		return bigDecimal{}, false
	}
	unit, ok := byteUnits[strings.ToLower(rest)]
	if !ok {
		return bigDecimal{}, false
	}
	return d.mulInt(unit), true
}

// semver is a semantic version as specified by https://semver.org, without
// build metadata since it doesn't affect precedence.
type semver struct {
	major, minor, patch string
	pre                 []string
}

// parseSemver parses a semantic version with an optional "v" prefix.
func parseSemver(s string) (semver, bool) {
	s, build, hasBuild := strings.Cut(strings.TrimPrefix(s, "v"), "+")
	core, pre, hasPre := strings.Cut(s, "-")

	var v semver
	var rest string
	var ok1, ok2 bool
	v.major, rest, ok1 = strings.Cut(core, ".")
	v.minor, v.patch, ok2 = strings.Cut(rest, ".")
	if !ok1 || !ok2 || !isSemverNumber(v.major) || !isSemverNumber(v.minor) || !isSemverNumber(v.patch) {
		return semver{}, false
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if !isSemverIdentifier(id) || (isDigits(id) && !isSemverNumber(id)) {
				return semver{}, false
			}
		}
	}
	if hasBuild {
		for id := range strings.SplitSeq(build, ".") {
			if !isSemverIdentifier(id) {
				return semver{}, false
			}
		}
	}
	return v, true
}

// isSemverNumber reports whether s is a numeric identifier without leading
// zeros.
func isSemverNumber(s string) bool {
	return isDigits(s) && (len(s) == 1 || s[0] != '0')
}

func isSemverIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isDigit(c) && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '-' {
			return false
		}
	}
	return true
}

// cmp compares semantic versions by precedence.
func (v semver) cmp(o semver) int {
	if c := cmp.Or(cmpDigits(v.major, o.major), cmpDigits(v.minor, o.minor), cmpDigits(v.patch, o.patch)); c != 0 {
		return c
	}
	// A version without pre-release identifiers has higher precedence.
	switch {
	case len(v.pre) == 0 && len(o.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(o.pre) == 0:
		return -1
	}
	for i := range min(len(v.pre), len(o.pre)) {
		a, b := v.pre[i], o.pre[i]
		var c int
		switch an, bn := isDigits(a), isDigits(b); {
		case an && bn:
			c = cmpDigits(a, b)
		case an:
			c = -1
		case bn:
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
