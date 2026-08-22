package promql

import (
	"math/big"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/facette/natsort"
)

type labelSortValue struct {
	class   int
	number  *big.Rat
	ip      netip.Addr
	prefix  int
	version string
	stamp   time.Time
	text    string
}

var numberRE = regexp.MustCompile(`^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$`)
var durationRE = regexp.MustCompile(`^[+-]?(?:(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?(?:ms|us|µs|ns|d|h|m|s|w))+$`)
var bytesRE = regexp.MustCompile(`^[+-]?(?:(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?(?:KiB|MiB|GiB|TiB|PiB|KB|MB|GB|TB|PB|B))+$`)

func parseLabelSortValue(s string) labelSortValue {
	v := labelSortValue{text: s, class: 9}
	if s == "" || strings.TrimSpace(s) != s || strings.HasPrefix(s, " ") || strings.HasPrefix(s, "\t") {
		return v
	}
	switch s {
	case "Inf", "+Inf", "+Infinity", "Infinity":
		v.class = 0
		return v
	case "-Inf", "-Infinity":
		v.class = 2
		return v
	}
	if numberRE.MatchString(s) {
		v.class, v.number = 1, decimalRat(s)
		return v
	}
	if durationRE.MatchString(s) {
		if n, ok := quantityRat(s, map[string]int64{"ns": 1, "us": 1000, "µs": 1000, "ms": 1000000, "s": 1000000000, "m": 60000000000, "h": 3600000000000, "d": 86400000000000, "w": 604800000000000}); ok {
			v.class, v.number = 3, n
			return v
		}
	}
	if bytesRE.MatchString(s) {
		if n, ok := quantityRat(s, map[string]int64{"B": 1, "KB": 1000, "MB": 1000000, "GB": 1000000000, "TB": 1000000000000, "PB": 1000000000000000, "KiB": 1024, "MiB": 1048576, "GiB": 1073741824, "TiB": 1099511627776, "PiB": 1125899906842624}); ok {
			v.class, v.number = 4, n
			return v
		}
	}
	if strings.HasPrefix(s, "v") {
		v.version = s[1:]
	} else {
		v.version = s
	}
	if validVersion(v.version) {
		v.class = 5
		return v
	}
	if a, err := netip.ParseAddr(s); err == nil {
		v.class, v.ip = 6, a
		return v
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		v.class, v.ip, v.prefix = 7, p.Masked().Addr(), p.Bits()
		return v
	}
	if stamp, err := time.Parse(time.RFC3339Nano, s); err == nil {
		v.class, v.stamp = 8, stamp
		return v
	}
	return v
}

func decimalRat(s string) *big.Rat {
	sign := 1
	if s[0] == '-' {
		sign = -1
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}
	parts := strings.SplitN(strings.ToLower(s), "e", 2)
	exp := 0
	if len(parts) == 2 {
		exp, _ = strconv.Atoi(parts[1])
	}
	digits := strings.ReplaceAll(parts[0], ".", "")
	scale := len(parts[0]) - strings.IndexByte(parts[0], '.') - 1
	if !strings.Contains(parts[0], ".") {
		scale = 0
	}
	exp -= scale
	n := new(big.Int)
	n.SetString(digits, 10)
	if sign < 0 {
		n.Neg(n)
	}
	if exp >= 0 {
		n.Mul(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil))
		return new(big.Rat).SetInt(n)
	}
	return new(big.Rat).SetFrac(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-exp)), nil))
}

func quantityRat(s string, units map[string]int64) (*big.Rat, bool) {
	sign := 1
	if s[0] == '-' {
		sign = -1
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}
	total := new(big.Rat)
	for len(s) > 0 {
		i := 0
		for i < len(s) && (s[i] == '.' || s[i] >= '0' && s[i] <= '9' || s[i] == 'e' || s[i] == 'E' || (i > 0 && (s[i] == '+' || s[i] == '-') && (s[i-1] == 'e' || s[i-1] == 'E'))) {
			i++
		}
		j := i
		for j < len(s) && (s[j] < '0' || s[j] > '9') && s[j] != 'µ' {
			j++
		}
		if j == i {
			return nil, false
		}
		u := s[i:j]
		mul, ok := units[u]
		if !ok {
			return nil, false
		}
		total.Add(total, new(big.Rat).Mul(decimalRat(s[:i]), new(big.Rat).SetInt64(mul)))
		s = s[j:]
	}
	if sign < 0 {
		total.Neg(total)
	}
	return total, true
}

func validVersion(s string) bool {
	parts := strings.SplitN(s, "+", 2)
	if len(parts) == 2 && !validIdentifiers(parts[1], false) {
		return false
	}
	parts = strings.SplitN(parts[0], "-", 2)
	p := parts[0]
	if len(parts) == 2 && !validIdentifiers(parts[1], true) {
		return false
	}
	x := strings.Split(p, ".")
	if len(x) != 3 {
		return false
	}
	for _, n := range x {
		if n == "" || (len(n) > 1 && n[0] == '0') {
			return false
		}
		if _, err := strconv.Atoi(n); err != nil {
			return false
		}
	}
	return true
}

func validIdentifiers(s string, prerelease bool) bool {
	for _, id := range strings.Split(s, ".") {
		if id == "" || (prerelease && len(id) > 1 && id[0] == '0' && allDigits(id)) {
			return false
		}
		for _, c := range id {
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '-') {
				return false
			}
		}
	}
	return true
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func compareLabelSort(a, b string) int {
	x, y := parseLabelSortValue(a), parseLabelSortValue(b)
	if x.class != y.class {
		if x.class < y.class {
			return -1
		}
		return 1
	}
	if x.number != nil {
		if c := x.number.Cmp(y.number); c != 0 {
			return c
		}
	}
	if x.class == 6 || x.class == 7 {
		if x.ip.Is4() != y.ip.Is4() {
			if x.ip.Is4() {
				return -1
			}
			return 1
		}
		if c := x.ip.Compare(y.ip); c != 0 {
			return c
		}
		if x.class == 7 && x.prefix != y.prefix {
			if x.prefix < y.prefix {
				return -1
			}
			return 1
		}
	}
	if x.class == 5 {
		xp, yp := strings.SplitN(strings.SplitN(x.version, "+", 2)[0], "-", 2)[0], strings.SplitN(strings.SplitN(y.version, "+", 2)[0], "-", 2)[0]
		for i, p := range strings.Split(xp, ".") {
			if c := new(big.Int).SetInt64(0); c != nil {
				a, _ := strconv.Atoi(p)
				b, _ := strconv.Atoi(strings.Split(yp, ".")[i])
				if a != b {
					if a < b {
						return -1
					}
					return 1
				}
			}
		}
	}
	if x.class == 8 {
		if c := x.stamp.Compare(y.stamp); c != 0 {
			return c
		}
	}
	if x.text != y.text {
		if natsort.Compare(x.text, y.text) {
			return -1
		}
		return 1
	}
	return 0
}
