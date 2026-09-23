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
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/facette/natsort"
)

type labelClass int

const (
	classLeadingSpace labelClass = iota
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

// decimal is an exact value neg * mant * 10^exp with mant >= 0.
type decimal struct {
	neg  bool
	mant *big.Int
	exp  *big.Int
}

type typedLabel struct {
	class  labelClass
	num    decimal
	semver semver
	ip     netip.Addr
	prefix netip.Prefix
	ts     time.Time
}

type semver struct {
	major, minor, patch string
	pre                 []string
}

var (
	numberRe   = regexp.MustCompile(`^([+-]?)(\d+(?:\.\d*)?|\.\d+)(?:[eE]([+-]?\d+))?$`)
	infRe      = regexp.MustCompile(`^(?i)([+-]?)(inf|infinity)$`)
	unitRe     = regexp.MustCompile(`^([+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)([a-zA-Zµ]+)$`)
	semverRe   = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)
	durationNs = map[string]int64{
		"ns": 1, "us": 1e3, "µs": 1e3, "ms": 1e6, "s": 1e9, "m": 60e9, "h": 3600e9,
		"d": 86400e9, "w": 7 * 86400e9, "y": 365 * 86400e9,
	}
	byteUnits = map[string]int64{
		"b": 1,
		"kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "pb": 1e15, "eb": 1e18,
		"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40, "pib": 1 << 50, "eib": 1 << 60,
	}
	timestampLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"}
)

func parseDecimal(s string) (decimal, bool) {
	m := numberRe.FindStringSubmatch(s)
	if m == nil {
		return decimal{}, false
	}
	intPart, fracPart, _ := strings.Cut(m[2], ".")
	mant, ok := new(big.Int).SetString(strings.TrimLeft(intPart+fracPart, "0")+"0", 10)
	if !ok {
		return decimal{}, false
	}
	// The appended "0" above avoids parsing an empty string; compensate here.
	exp := big.NewInt(int64(-len(fracPart) - 1))
	if m[3] != "" {
		e, ok := new(big.Int).SetString(strings.TrimPrefix(m[3], "+"), 10)
		if !ok {
			return decimal{}, false
		}
		exp.Add(exp, e)
	}
	return decimal{neg: m[1] == "-", mant: mant, exp: exp}.normalize(), true
}

func (d decimal) normalize() decimal {
	if d.mant.Sign() == 0 {
		return decimal{mant: new(big.Int), exp: new(big.Int)}
	}
	ten := big.NewInt(10)
	q, r := new(big.Int), new(big.Int)
	for {
		q.QuoRem(d.mant, ten, r)
		if r.Sign() != 0 {
			break
		}
		d.mant = new(big.Int).Set(q)
		d.exp = new(big.Int).Add(d.exp, big.NewInt(1))
	}
	return d
}

func (d decimal) scale(f int64) decimal {
	return decimal{neg: d.neg, mant: new(big.Int).Mul(d.mant, big.NewInt(f)), exp: d.exp}.normalize()
}

func (d decimal) sign() int {
	switch {
	case d.mant.Sign() == 0:
		return 0
	case d.neg:
		return -1
	}
	return 1
}

func compareDecimal(a, b decimal) int {
	sa, sb := a.sign(), b.sign()
	if sa != sb {
		if sa < sb {
			return -1
		}
		return 1
	}
	if sa == 0 {
		return 0
	}
	c := compareAbs(a, b)
	if sa < 0 {
		return -c
	}
	return c
}

func compareAbs(a, b decimal) int {
	da, db := a.mant.String(), b.mant.String()
	// Position of the most significant digit.
	pa := new(big.Int).Add(a.exp, big.NewInt(int64(len(da))))
	pb := new(big.Int).Add(b.exp, big.NewInt(int64(len(db))))
	if c := pa.Cmp(pb); c != 0 {
		return c
	}
	return strings.Compare(da, db)
}

func parseUnit(s string, units map[string]int64, fold bool) (decimal, bool) {
	m := unitRe.FindStringSubmatch(s)
	if m == nil {
		return decimal{}, false
	}
	u := m[2]
	if fold {
		u = strings.ToLower(u)
	}
	f, ok := units[u]
	if !ok {
		return decimal{}, false
	}
	d, ok := parseDecimal(m[1])
	if !ok {
		return decimal{}, false
	}
	return d.scale(f), true
}

func parseTypedLabel(s string) typedLabel {
	if r, _ := utf8.DecodeRuneInString(s); s != "" && unicode.IsSpace(r) {
		return typedLabel{class: classLeadingSpace}
	}
	if s == "" {
		return typedLabel{class: classString}
	}
	if m := infRe.FindStringSubmatch(s); m != nil {
		if m[1] == "-" {
			return typedLabel{class: classNegInf}
		}
		return typedLabel{class: classPosInf}
	}
	if d, ok := parseDecimal(s); ok {
		return typedLabel{class: classNumber, num: d}
	}
	if d, ok := parseUnit(s, durationNs, false); ok {
		return typedLabel{class: classDuration, num: d}
	}
	if d, ok := parseUnit(s, byteUnits, true); ok {
		return typedLabel{class: classBytes, num: d}
	}
	if m := semverRe.FindStringSubmatch(s); m != nil {
		v := semver{major: m[1], minor: m[2], patch: m[3]}
		if m[4] != "" {
			v.pre = strings.Split(m[4], ".")
		}
		return typedLabel{class: classSemver, semver: v}
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		return typedLabel{class: classIP, ip: ip}
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return typedLabel{class: classCIDR, prefix: p}
	}
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return typedLabel{class: classTimestamp, ts: t}
		}
	}
	return typedLabel{class: classString}
}

func isNumericIdent(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// compareDigits compares non-negative integers without leading zeros.
func compareDigits(a, b string) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func compareSemver(a, b semver) int {
	for _, c := range []int{
		compareDigits(a.major, b.major),
		compareDigits(a.minor, b.minor),
		compareDigits(a.patch, b.patch),
	} {
		if c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, yn := isNumericIdent(x), isNumericIdent(y)
		var c int
		switch {
		case xn && yn:
			c = compareDigits(x, y)
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
	return len(a.pre) - len(b.pre)
}

func comparePrefix(a, b netip.Prefix) int {
	if c := a.Masked().Addr().Compare(b.Masked().Addr()); c != 0 {
		return c
	}
	return a.Bits() - b.Bits()
}

func compareTyped(a, b typedLabel) int {
	if a.class != b.class {
		return int(a.class) - int(b.class)
	}
	switch a.class {
	case classNumber, classDuration, classBytes:
		return compareDecimal(a.num, b.num)
	case classSemver:
		return compareSemver(a.semver, b.semver)
	case classIP:
		return a.ip.Compare(b.ip)
	case classCIDR:
		return comparePrefix(a.prefix, b.prefix)
	case classTimestamp:
		return a.ts.Compare(b.ts)
	}
	return 0
}

func compareNatural(a, b string) int {
	switch {
	case a == b:
		return 0
	case natsort.Compare(a, b):
		return -1
	case natsort.Compare(b, a):
		return 1
	}
	return strings.Compare(a, b)
}

// compareLabelValues orders label values by type class, then by typed value,
// then by natural ordering of the original strings.
func compareLabelValues(a, b string) int {
	if a == b {
		return 0
	}
	if c := compareTyped(parseTypedLabel(a), parseTypedLabel(b)); c != 0 {
		return c
	}
	return compareNatural(a, b)
}
