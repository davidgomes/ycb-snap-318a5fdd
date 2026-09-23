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
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// labelValueClass orders the kinds of typed label values relative to each other.
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

const decimalPattern = `(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?`

var (
	numberRe            = regexp.MustCompile(`^[+-]?` + decimalPattern + `$`)
	decimalPartsRe      = regexp.MustCompile(`^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$`)
	durationRe          = regexp.MustCompile(`^([+-]?)((?:` + decimalPattern + `(?:ns|us|µs|μs|ms|s|m|h|d|w|y))+)$`)
	durationComponentRe = regexp.MustCompile(`(` + decimalPattern + `)(ns|us|µs|μs|ms|s|m|h|d|w|y)`)
	bytesRe             = regexp.MustCompile(`^([+-]?` + decimalPattern + `) ?(?i:([kmgtpezy])(i?))?[bB]$`)
	semverRe            = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
		`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
		`(?:\+[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*)?$`)
)

var durationUnitNanos = map[string]int64{
	"ns": 1,
	"us": int64(time.Microsecond),
	"µs": int64(time.Microsecond),
	"μs": int64(time.Microsecond),
	"ms": int64(time.Millisecond),
	"s":  int64(time.Second),
	"m":  int64(time.Minute),
	"h":  int64(time.Hour),
	"d":  24 * int64(time.Hour),
	"w":  7 * 24 * int64(time.Hour),
	"y":  365 * 24 * int64(time.Hour),
}

// maxDecimalAlignShift bounds the power of ten used to align exponents when
// adding compound duration components, so absurd exponents cannot exhaust memory.
const maxDecimalAlignShift = 4096

// decimal is an exact value mant × 10^exp.
type decimal struct {
	mant *big.Int
	exp  *big.Int
}

func parseDecimal(s string) (decimal, bool) {
	m := decimalPartsRe.FindStringSubmatch(s)
	if m == nil || m[2]+m[3] == "" {
		return decimal{}, false
	}
	mant, ok := new(big.Int).SetString(m[2]+m[3], 10)
	if !ok {
		return decimal{}, false
	}
	if m[1] == "-" {
		mant.Neg(mant)
	}
	exp := new(big.Int)
	if m[4] != "" {
		if _, ok := exp.SetString(m[4], 10); !ok {
			return decimal{}, false
		}
	}
	exp.Sub(exp, big.NewInt(int64(len(m[3]))))
	return decimal{mant: mant, exp: exp}, true
}

func (d decimal) mulInt(n int64) decimal {
	return decimal{mant: new(big.Int).Mul(d.mant, big.NewInt(n)), exp: d.exp}
}

func (d decimal) add(o decimal) (decimal, bool) {
	if d.exp.Cmp(o.exp) < 0 {
		d, o = o, d
	}
	shift := new(big.Int).Sub(d.exp, o.exp)
	if !shift.IsInt64() || shift.Int64() > maxDecimalAlignShift {
		return decimal{}, false
	}
	scale := new(big.Int).Exp(big.NewInt(10), shift, nil)
	mant := new(big.Int).Mul(d.mant, scale)
	mant.Add(mant, o.mant)
	return decimal{mant: mant, exp: o.exp}, true
}

// normDecimal is the canonical form sign × 0.digits × 10^order, where digits
// has no leading or trailing zeros, allowing comparison without arithmetic.
type normDecimal struct {
	sign   int
	digits string
	order  *big.Int
}

func (d decimal) normalize() normDecimal {
	sign := d.mant.Sign()
	if sign == 0 {
		return normDecimal{}
	}
	digits := new(big.Int).Abs(d.mant).String()
	order := new(big.Int).Add(d.exp, big.NewInt(int64(len(digits))))
	return normDecimal{sign: sign, digits: strings.TrimRight(digits, "0"), order: order}
}

func (a normDecimal) compare(b normDecimal) int {
	if a.sign != b.sign {
		return cmp.Compare(a.sign, b.sign)
	}
	if a.sign == 0 {
		return 0
	}
	c := a.order.Cmp(b.order)
	if c == 0 {
		c = strings.Compare(a.digits, b.digits)
	}
	return a.sign * c
}

type semver struct {
	core       [3]string
	prerelease []string
}

func compareNumericIdent(a, b string) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func isNumericIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (a semver) compare(b semver) int {
	for i := range a.core {
		if c := compareNumericIdent(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.prerelease) == 0 && len(b.prerelease) == 0:
		return 0
	case len(a.prerelease) == 0:
		return 1
	case len(b.prerelease) == 0:
		return -1
	}
	for i := 0; i < len(a.prerelease) && i < len(b.prerelease); i++ {
		x, y := a.prerelease[i], b.prerelease[i]
		xn, yn := isNumericIdent(x), isNumericIdent(y)
		var c int
		switch {
		case xn && yn:
			c = compareNumericIdent(x, y)
		case xn:
			c = -1
		case yn:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.prerelease), len(b.prerelease))
}

// typedLabelValue is a label value together with its parsed typed form.
type typedLabelValue struct {
	raw    string
	class  labelValueClass
	num    normDecimal
	ver    semver
	addr   netip.Addr
	prefix netip.Prefix
	ts     time.Time
}

func parseTypedLabelValue(s string) typedLabelValue {
	v := typedLabelValue{raw: s, class: classString}
	if s == "" {
		return v
	}
	if r, _ := utf8.DecodeRuneInString(s); unicode.IsSpace(r) {
		v.class = classLeadingSpace
		return v
	}
	if numberRe.MatchString(s) {
		if d, ok := parseDecimal(s); ok {
			v.class, v.num = classNumber, d.normalize()
			return v
		}
	}
	unsigned := strings.TrimLeft(s, "+-")
	if len(s)-len(unsigned) <= 1 && (strings.EqualFold(unsigned, "inf") || strings.EqualFold(unsigned, "infinity")) {
		v.class = classPosInf
		if s[0] == '-' {
			v.class = classNegInf
		}
		return v
	}
	if d, ok := parseDurationValue(s); ok {
		v.class, v.num = classDuration, d.normalize()
		return v
	}
	if d, ok := parseBytesValue(s); ok {
		v.class, v.num = classBytes, d.normalize()
		return v
	}
	if m := semverRe.FindStringSubmatch(s); m != nil {
		v.class = classSemver
		v.ver.core = [3]string{m[1], m[2], m[3]}
		if m[4] != "" {
			v.ver.prerelease = strings.Split(m[4], ".")
		}
		return v
	}
	if a, err := netip.ParseAddr(s); err == nil {
		v.class, v.addr = classIP, a
		return v
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		v.class, v.prefix = classCIDR, p.Masked()
		return v
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		v.class, v.ts = classTimestamp, t
		return v
	}
	return v
}

// parseDurationValue returns the duration in nanoseconds.
func parseDurationValue(s string) (decimal, bool) {
	m := durationRe.FindStringSubmatch(s)
	if m == nil {
		return decimal{}, false
	}
	var total decimal
	for i, comp := range durationComponentRe.FindAllStringSubmatch(m[2], -1) {
		coef, ok := parseDecimal(comp[1])
		if !ok {
			return decimal{}, false
		}
		part := coef.mulInt(durationUnitNanos[comp[2]])
		if i == 0 {
			total = part
			continue
		}
		if total, ok = total.add(part); !ok {
			return decimal{}, false
		}
	}
	if m[1] == "-" {
		total.mant.Neg(total.mant)
	}
	return total, true
}

// parseBytesValue returns the size in bytes.
func parseBytesValue(s string) (decimal, bool) {
	m := bytesRe.FindStringSubmatch(s)
	if m == nil {
		return decimal{}, false
	}
	d, ok := parseDecimal(m[1])
	if !ok {
		return decimal{}, false
	}
	if m[2] == "" {
		return d, true
	}
	power := int64(strings.IndexByte("kmgtpezy", strings.ToLower(m[2])[0]) + 1)
	base := int64(1000)
	if m[3] != "" {
		base = 1024
	}
	scale := new(big.Int).Exp(big.NewInt(base), big.NewInt(power), nil)
	return decimal{mant: new(big.Int).Mul(d.mant, scale), exp: d.exp}, true
}

func compareTypedLabelValues(a, b *typedLabelValue) int {
	if a.raw == b.raw {
		return 0
	}
	if a.class != b.class {
		return cmp.Compare(a.class, b.class)
	}
	var c int
	switch a.class {
	case classNumber, classDuration, classBytes:
		c = a.num.compare(b.num)
	case classSemver:
		c = a.ver.compare(b.ver)
	case classIP:
		c = a.addr.Compare(b.addr)
	case classCIDR:
		c = cmp.Compare(a.prefix.Addr().BitLen(), b.prefix.Addr().BitLen())
		if c == 0 {
			c = a.prefix.Addr().Compare(b.prefix.Addr())
		}
		if c == 0 {
			c = cmp.Compare(a.prefix.Bits(), b.prefix.Bits())
		}
	case classTimestamp:
		c = a.ts.Compare(b.ts)
	}
	if c != 0 {
		return c
	}
	return naturalCompare(a.raw, b.raw)
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// naturalCompare orders strings by comparing runs of ASCII digits numerically
// and all other runs bytewise. Strings that are naturally equal (e.g. "01" and
// "1") fall back to bytewise comparison so the result is a total order.
func naturalCompare(a, b string) int {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ad, bd := isASCIIDigit(a[i]), isASCIIDigit(b[j])
		si, sj := i, j
		for i < len(a) && isASCIIDigit(a[i]) == ad {
			i++
		}
		for j < len(b) && isASCIIDigit(b[j]) == bd {
			j++
		}
		ca, cb := a[si:i], b[sj:j]
		var c int
		if ad && bd {
			c = compareNumericIdent(strings.TrimLeft(ca, "0"), strings.TrimLeft(cb, "0"))
		} else {
			c = strings.Compare(ca, cb)
		}
		if c != 0 {
			return c
		}
	}
	if c := cmp.Compare(len(a)-i, len(b)-j); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}
