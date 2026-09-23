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
	"bytes"
	"cmp"
	"math/big"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/facette/natsort"
	"github.com/grafana/regexp"
)

// Label values sort in this class order. Leading whitespace is never parsed.
// Positive infinity sorts before every finite number, and negative infinity
// sorts after every finite number.
const (
	classLeadingWS = iota
	classPosInf
	classNumeric
	classNegInf
	classDuration
	classBytes
	classSemver
	classIP
	classCIDR
	classTimestamp
	classUntyped
)

// term is one exact summand: coeff * 10^exp, with coeff signed.
// Exponents stay as big.Int so huge magnitudes compare without expanding 10^n.
type term struct {
	coeff *big.Int
	exp   *big.Int
}

type unitDef struct {
	name   string
	mul    *big.Int
	addExp int64
	pos    int
}

type semVer struct {
	major *big.Int
	minor *big.Int
	patch *big.Int
	pre   []string
}

type labelKey struct {
	class int
	terms []term
	sem   *semVer
	ip4   bool
	ip    []byte
	bits  int
	ts    time.Time
}

var (
	ten = big.NewInt(10)

	durationUnits []unitDef
	byteUnits     []unitDef

	semverRE = regexp.MustCompile(`^(v)?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
)

func init() {
	const (
		ns = int64(1)
		us = ns * 1000
		ms = us * 1000
		s  = ms * 1000
		m  = s * 60
		h  = m * 60
		d  = h * 24
		w  = d * 7
		y  = d * 365
	)
	durationUnits = []unitDef{
		uInt("y", y, 1),
		uInt("w", w, 2),
		uInt("d", d, 3),
		uInt("h", h, 4),
		uInt("m", m, 5),
		uInt("s", s, 6),
		uInt("ms", ms, 7),
		uInt("us", us, 8),
		uInt("µs", us, 8), // U+00B5 MICRO SIGN.
		uInt("μs", us, 8), // U+03BC GREEK SMALL LETTER MU.
		uInt("ns", ns, 9),
	}
	// Decimal SI uses powers of 1000. IEC prefixes use powers of 1024.
	// A bare "E" is intentionally absent so "1E" stays an invalid number.
	byteUnits = []unitDef{
		uPow2("YiB", 80),
		uPow2("ZiB", 70),
		uPow2("EiB", 60),
		uPow2("PiB", 50),
		uPow2("TiB", 40),
		uPow2("GiB", 30),
		uPow2("MiB", 20),
		uPow2("KiB", 10),
		uPow10("YB", 24),
		uPow10("ZB", 21),
		uPow10("EB", 18),
		uPow10("PB", 15),
		uPow10("TB", 12),
		uPow10("GB", 9),
		uPow10("MB", 6),
		uPow10("KB", 3),
		uPow10("kB", 3),
		uPow10("B", 0),
	}
	slices.SortStableFunc(durationUnits, func(a, b unitDef) int {
		return cmp.Compare(len(b.name), len(a.name))
	})
	slices.SortStableFunc(byteUnits, func(a, b unitDef) int {
		return cmp.Compare(len(b.name), len(a.name))
	})
}

func uInt(name string, mul int64, pos int) unitDef {
	return unitDef{name: name, mul: big.NewInt(mul), pos: pos}
}

func uPow2(name string, shift uint) unitDef {
	return unitDef{name: name, mul: new(big.Int).Lsh(big.NewInt(1), shift)}
}

func uPow10(name string, exp10 int64) unitDef {
	return unitDef{name: name, mul: big.NewInt(1), addExp: exp10}
}

// compareLabelValues orders label strings by typed class, then by typed value,
// then by natural order of the original text. The result is a total order.
func compareLabelValues(a, b string) int {
	if a == b {
		return 0
	}
	ka, kb := classify(a), classify(b)
	if ka.class != kb.class {
		return ka.class - kb.class
	}
	c := 0
	switch ka.class {
	case classNumeric, classDuration, classBytes:
		c = cmpSums(ka.terms, kb.terms)
	case classSemver:
		c = compareSemver(ka.sem, kb.sem)
	case classIP:
		c = compareIP(ka, kb)
	case classCIDR:
		c = compareCIDR(ka, kb)
	case classTimestamp:
		c = ka.ts.Compare(kb.ts)
	}
	if c != 0 {
		return c
	}
	return naturalCompare(a, b)
}

func classify(s string) labelKey {
	if hasLeadingWhitespace(s) {
		return labelKey{class: classLeadingWS}
	}
	if positive, ok := parseInfinity(s); ok {
		if positive {
			return labelKey{class: classPosInf}
		}
		return labelKey{class: classNegInf}
	}
	if isNaNLiteral(s) || isBareExponent(s) {
		return labelKey{class: classUntyped}
	}
	if terms, ok := parseNumberTerms(s); ok {
		return labelKey{class: classNumeric, terms: terms}
	}
	if terms, ok := parseMagnitudes(s, durationUnits, true); ok {
		return labelKey{class: classDuration, terms: terms}
	}
	if terms, ok := parseMagnitudes(s, byteUnits, false); ok {
		return labelKey{class: classBytes, terms: terms}
	}
	if sem, ok := parseSemver(s); ok {
		return labelKey{class: classSemver, sem: sem}
	}
	if ip4, addr, bits, ok := parseCIDRValue(s); ok {
		return labelKey{class: classCIDR, ip4: ip4, ip: addr, bits: bits}
	}
	if ip4, addr, ok := parseIPValue(s); ok {
		return labelKey{class: classIP, ip4: ip4, ip: addr}
	}
	if ts, ok := parseTimestamp(s); ok {
		return labelKey{class: classTimestamp, ts: ts}
	}
	return labelKey{class: classUntyped}
}

func hasLeadingWhitespace(s string) bool {
	for _, r := range s {
		return unicode.IsSpace(r)
	}
	return false
}

func parseInfinity(s string) (positive bool, ok bool) {
	if s == "" {
		return false, false
	}
	positive = true
	if s[0] == '+' || s[0] == '-' {
		positive = s[0] != '-'
		s = s[1:]
	}
	switch strings.ToLower(s) {
	case "inf", "infinity":
		return positive, true
	default:
		return false, false
	}
}

func isNaNLiteral(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	return strings.EqualFold(s, "nan")
}

// isBareExponent reports forms such as "1e", "1E+", and "+.5e-" which must
// stay untyped instead of being read as a byte unit or a number.
func isBareExponent(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	sawDigit := false
	sawDot := false
	for i < len(s) {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			sawDigit = true
			i++
		case c == '.' && !sawDot:
			sawDot = true
			i++
		default:
			goto exponent
		}
	}
	return false
exponent:
	if !sawDigit || (s[i] != 'e' && s[i] != 'E') {
		return false
	}
	i++
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if i < len(s) && s[i] >= '0' && s[i] <= '9' {
		return false
	}
	return i == len(s)
}

func parseNumberTerms(s string) ([]term, bool) {
	if s == "" {
		return nil, false
	}
	neg := false
	body := s
	if body[0] == '+' || body[0] == '-' {
		neg = body[0] == '-'
		body = body[1:]
		if body == "" {
			return nil, false
		}
	}
	coeff, exp, n, ok := parseUnsignedDecimalPrefix(body)
	if !ok || n != len(body) {
		return nil, false
	}
	if coeff.Sign() == 0 {
		return nil, true
	}
	if neg {
		coeff.Neg(coeff)
	}
	return []term{{coeff: coeff, exp: exp}}, true
}

// parseUnsignedDecimalPrefix parses a decimal coefficient with an optional
// scientific exponent. A bare exponent marker is left unconsumed.
func parseUnsignedDecimalPrefix(s string) (coeff *big.Int, exp *big.Int, n int, ok bool) {
	i := 0
	coeff = new(big.Int)
	sawDigit := false
	sawDot := false
	frac := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			coeff.Mul(coeff, ten)
			coeff.Add(coeff, big.NewInt(int64(c-'0')))
			sawDigit = true
			if sawDot {
				frac++
			}
			i++
		case c == '.' && !sawDot:
			sawDot = true
			i++
		default:
			goto doneMantissa
		}
	}
doneMantissa:
	if !sawDigit {
		return nil, nil, 0, false
	}
	exp = big.NewInt(int64(-frac))
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		sign := int64(1)
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			if s[j] == '-' {
				sign = -1
			}
			j++
		}
		if j < len(s) && s[j] >= '0' && s[j] <= '9' {
			start := j
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			expVal := new(big.Int)
			if _, ok := expVal.SetString(s[start:j], 10); !ok {
				return nil, nil, 0, false
			}
			if sign < 0 {
				expVal.Neg(expVal)
			}
			exp.Add(exp, expVal)
			i = j
		}
	}
	return coeff, exp, i, true
}

// parseMagnitudes parses a duration or byte string. An optional leading sign
// applies to the whole value. When ordered is set, units must run from larger
// to smaller, matching Prometheus duration syntax.
func parseMagnitudes(s string, table []unitDef, ordered bool) ([]term, bool) {
	if s == "" {
		return nil, false
	}
	negAll := false
	if s[0] == '+' || s[0] == '-' {
		negAll = s[0] == '-'
		s = s[1:]
		if s == "" {
			return nil, false
		}
	}
	var terms []term
	seen := false
	lastPos := 0
	for s != "" {
		coeff, exp, n, ok := parseUnsignedDecimalPrefix(s)
		if !ok {
			return nil, false
		}
		s = s[n:]
		unit, rest, ok := matchUnit(table, s)
		if !ok {
			return nil, false
		}
		if ordered {
			if seen && unit.pos <= lastPos {
				return nil, false
			}
			seen = true
			lastPos = unit.pos
		}
		c := new(big.Int).Mul(coeff, unit.mul)
		if negAll {
			c.Neg(c)
		}
		e := new(big.Int).Add(exp, big.NewInt(unit.addExp))
		if c.Sign() != 0 {
			terms = append(terms, term{coeff: c, exp: e})
		}
		s = rest
	}
	return terms, true
}

func matchUnit(table []unitDef, s string) (unitDef, string, bool) {
	for _, u := range table {
		if strings.HasPrefix(s, u.name) {
			return u, s[len(u.name):], true
		}
	}
	return unitDef{}, "", false
}

func parseSemver(s string) (*semVer, bool) {
	m := semverRE.FindStringSubmatch(s)
	if m == nil {
		return nil, false
	}
	if m[5] != "" && !validPrerelease(m[5]) {
		return nil, false
	}
	sv := &semVer{
		major: mustInt(m[2]),
		minor: mustInt(m[3]),
		patch: mustInt(m[4]),
	}
	if m[5] != "" {
		sv.pre = strings.Split(m[5], ".")
	}
	return sv, true
}

func mustInt(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return big.NewInt(0)
	}
	return n
}

func validPrerelease(pre string) bool {
	for _, id := range strings.Split(pre, ".") {
		if !validPreIdent(id) {
			return false
		}
	}
	return true
}

func validPreIdent(id string) bool {
	if id == "" {
		return false
	}
	numeric := true
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c < '0' || c > '9' {
			numeric = false
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-') {
				return false
			}
		}
	}
	if numeric && len(id) > 1 && id[0] == '0' {
		return false
	}
	return true
}

func compareSemver(a, b *semVer) int {
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
	n := len(a.pre)
	if len(b.pre) < n {
		n = len(b.pre)
	}
	for i := 0; i < n; i++ {
		if c := comparePreIdent(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	return len(a.pre) - len(b.pre)
}

func comparePreIdent(a, b string) int {
	aNum, aOK := new(big.Int).SetString(a, 10)
	bNum, bOK := new(big.Int).SetString(b, 10)
	// SetString accepts leading zeros; prerelease validation already rejected those.
	aDigits := aOK && isDigits(a)
	bDigits := bOK && isDigits(b)
	switch {
	case aDigits && bDigits:
		return aNum.Cmp(bNum)
	case aDigits:
		return -1
	case bDigits:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func isDigits(s string) bool {
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

func parseIPValue(s string) (ip4 bool, addr []byte, ok bool) {
	ip, err := netip.ParseAddr(s)
	if err != nil || !ip.IsValid() {
		return false, nil, false
	}
	// IPv4-mapped IPv6 literals stay IPv6. Do not call Unmap.
	if ip.Is4() {
		a := ip.As4()
		return true, append([]byte(nil), a[:]...), true
	}
	a := ip.As16()
	return false, append([]byte(nil), a[:]...), true
}

func parseCIDRValue(s string) (ip4 bool, addr []byte, bits int, ok bool) {
	if !strings.Contains(s, "/") {
		return false, nil, 0, false
	}
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.IsValid() {
		return false, nil, 0, false
	}
	p = p.Masked()
	ip := p.Addr()
	bits = p.Bits()
	if ip.Is4() {
		a := ip.As4()
		return true, append([]byte(nil), a[:]...), bits, true
	}
	a := ip.As16()
	return false, append([]byte(nil), a[:]...), bits, true
}

func compareIP(a, b labelKey) int {
	if a.ip4 != b.ip4 {
		if a.ip4 {
			return -1
		}
		return 1
	}
	return bytes.Compare(a.ip, b.ip)
}

func compareCIDR(a, b labelKey) int {
	if c := compareIP(a, b); c != 0 {
		return c
	}
	if c := bytes.Compare(a.ip, b.ip); c != 0 {
		return c
	}
	return a.bits - b.bits
}

func parseTimestamp(s string) (time.Time, bool) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
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

func naturalCompare(a, b string) int {
	if a == b {
		return 0
	}
	lessAB := natsort.Compare(a, b)
	lessBA := natsort.Compare(b, a)
	switch {
	case lessAB && !lessBA:
		return -1
	case lessBA && !lessAB:
		return 1
	}
	if a < b {
		return -1
	}
	return 1
}

func cmpSums(a, b []term) int {
	diff := make([]term, 0, len(a)+len(b))
	diff = append(diff, a...)
	for _, t := range b {
		diff = append(diff, term{coeff: new(big.Int).Neg(t.coeff), exp: t.exp})
	}
	return signOfSum(diff)
}

func signOfSum(terms []term) int {
	merged := mergeTerms(terms)
	i := 0
	for i < len(merged) {
		accC := new(big.Int).Set(merged[i].coeff)
		accE := merged[i].exp
		i++
		if accC.Sign() == 0 {
			continue
		}
		for i < len(merged) {
			if dominatesAll(accC, accE, merged[i:]) {
				return accC.Sign()
			}
			gap := new(big.Int).Sub(accE, merged[i].exp)
			accC = scaleAdd(accC, gap, merged[i].coeff)
			accE = merged[i].exp
			i++
			if accC.Sign() == 0 {
				break
			}
		}
		if accC.Sign() != 0 {
			return accC.Sign()
		}
	}
	return 0
}

func mergeTerms(terms []term) []term {
	if len(terms) == 0 {
		return nil
	}
	sorted := append([]term(nil), terms...)
	slices.SortFunc(sorted, func(a, b term) int {
		return b.exp.Cmp(a.exp)
	})
	out := make([]term, 0, len(sorted))
	for _, t := range sorted {
		if t.coeff == nil || t.coeff.Sign() == 0 || t.exp == nil {
			continue
		}
		if len(out) > 0 && out[len(out)-1].exp.Cmp(t.exp) == 0 {
			sum := new(big.Int).Add(out[len(out)-1].coeff, t.coeff)
			if sum.Sign() == 0 {
				out = out[:len(out)-1]
				continue
			}
			out[len(out)-1].coeff = sum
			continue
		}
		out = append(out, term{coeff: new(big.Int).Set(t.coeff), exp: t.exp})
	}
	return out
}

// dominatesAll reports whether |acc| is strictly larger than the sum of the
// absolute values of rest. Exponent gaps are decided from digit counts so
// huge powers of ten are not materialized.
func dominatesAll(accC *big.Int, accE *big.Int, rest []term) bool {
	maxExp := rest[0].exp
	sumAbs := new(big.Int)
	for _, t := range rest {
		sumAbs.Add(sumAbs, new(big.Int).Abs(t.coeff))
	}
	gap := new(big.Int).Sub(accE, maxExp)
	return cmpPow10(new(big.Int).Abs(accC), gap, sumAbs) > 0
}

// cmpPow10 compares c * 10^gap with other. c and other are non-negative.
func cmpPow10(c *big.Int, gap *big.Int, other *big.Int) int {
	if other.Sign() == 0 {
		if c.Sign() == 0 {
			return 0
		}
		return 1
	}
	if c.Sign() == 0 {
		return -1
	}
	if gap.Sign() < 0 {
		return -cmpPow10(other, new(big.Int).Neg(gap), c)
	}
	if gap.Sign() == 0 {
		return c.Cmp(other)
	}
	dc := decDigits(c)
	do := decDigits(other)
	// c * 10^gap has magnitude between 10^(dc-1+gap) and 10^(dc+gap).
	s := new(big.Int).Add(gap, big.NewInt(dc))
	switch s.Cmp(big.NewInt(do)) {
	case 1:
		return 1
	case -1:
		return -1
	}
	pow := new(big.Int).Exp(ten, gap, nil)
	scaled := new(big.Int).Mul(c, pow)
	return scaled.Cmp(other)
}

func decDigits(n *big.Int) int64 {
	if n.Sign() == 0 {
		return 1
	}
	s := n.String()
	if s[0] == '-' {
		return int64(len(s) - 1)
	}
	return int64(len(s))
}

func scaleAdd(acc *big.Int, gap *big.Int, next *big.Int) *big.Int {
	if gap.Sign() == 0 {
		return new(big.Int).Add(acc, next)
	}
	if gap.Sign() < 0 {
		ng := new(big.Int).Neg(gap)
		pow := new(big.Int).Exp(ten, ng, nil)
		return new(big.Int).Add(acc, new(big.Int).Mul(next, pow))
	}
	pow := new(big.Int).Exp(ten, gap, nil)
	scaled := new(big.Int).Mul(acc, pow)
	return scaled.Add(scaled, next)
}
