package promql

import (
	"math/big"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/facette/natsort"
)

type labelSortValue struct {
	class   int
	number  *big.Float
	text    string
	ip      netip.Addr
	prefix  netip.Prefix
	version *semver.Version
	time    time.Time
}

var (
	numberRE   = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
	durationRE = regexp.MustCompile(`^([+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)(ns|us|µs|ms|s|m|h|d|w|y)$`)
	bytesRE    = regexp.MustCompile(`^([+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)([KMGTPE]i?B?)$`)
)

func parseLabelSortValue(s string) labelSortValue {
	v := labelSortValue{text: s, class: 9}
	if s == "" || strings.TrimLeft(s, " \t\r\n") != s {
		return v
	}
	if strings.EqualFold(s, "infinity") || strings.EqualFold(s, "+infinity") || s == "+Inf" || s == "Inf" {
		v.class, v.number = 0, new(big.Float).SetInf(false)
		return v
	}
	if strings.EqualFold(s, "-infinity") || s == "-Inf" {
		v.class, v.number = 2, new(big.Float).SetInf(true)
		return v
	}
	if numberRE.MatchString(s) && !strings.Contains(strings.ToLower(s), "nan") {
		n, _, err := big.ParseFloat(s, 10, 256, big.ToNearestEven)
		if err == nil {
			v.class, v.number = 1, n
			return v
		}
	}
	if m := durationRE.FindStringSubmatch(s); m != nil {
		n, ok := parseMagnitude(m[1], durationUnit(m[2]))
		if ok {
			v.class, v.number = 3, n
			return v
		}
	}
	if m := bytesRE.FindStringSubmatch(s); m != nil {
		n, ok := parseMagnitude(m[1], byteUnit(m[2]))
		if ok {
			v.class, v.number = 4, n
			return v
		}
	}
	if x, err := semver.NewVersion(strings.TrimPrefix(s, "v")); err == nil && strings.TrimPrefix(s, "v") != "" {
		v.class, v.version = 5, x
		return v
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		v.class, v.prefix = 7, p
		return v
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		v.class, v.ip = 6, ip
		return v
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			v.class, v.time = 8, t
			return v
		}
	}
	return v
}

func parseMagnitude(s string, multiplier *big.Float) (*big.Float, bool) {
	n, _, err := big.ParseFloat(s, 10, 256, big.ToNearestEven)
	if err != nil {
		return nil, false
	}
	return new(big.Float).Mul(n, multiplier), true
}

func durationUnit(s string) *big.Float {
	units := map[string]float64{"ns": 1, "us": 1e3, "µs": 1e3, "ms": 1e6, "s": 1e9, "m": 6e10, "h": 3.6e12, "d": 8.64e13, "w": 6.048e14, "y": 3.1536e16}
	return new(big.Float).SetFloat64(units[s])
}
func byteUnit(s string) *big.Float {
	s = strings.TrimSuffix(s, "B")
	base := 1000.0
	if strings.HasSuffix(s, "i") {
		base = 1024
		s = strings.TrimSuffix(s, "i")
	}
	p, _ := strconv.Atoi(map[string]string{"K": "1", "M": "2", "G": "3", "T": "4", "P": "5", "E": "6"}[s])
	return new(big.Float).SetFloat64(func() float64 {
		n := 1.0
		for i := 0; i < p; i++ {
			n *= base
		}
		return n
	}())
}

func compareLabelSort(a, b string) int {
	x, y := parseLabelSortValue(a), parseLabelSortValue(b)
	if x.class != y.class {
		if x.class < y.class {
			return -1
		}
		return 1
	}
	var c int
	switch x.class {
	case 1, 3, 4:
		c = x.number.Cmp(y.number)
	case 5:
		c = x.version.Compare(y.version)
	case 6:
		if x.ip.Is4() != y.ip.Is4() {
			if x.ip.Is4() {
				return -1
			}
			return 1
		}
		c = x.ip.Compare(y.ip)
	case 7:
		c = x.prefix.Masked().Addr().Compare(y.prefix.Masked().Addr())
		if c == 0 {
			c = x.prefix.Bits() - y.prefix.Bits()
		}
	case 8:
		if x.time.Before(y.time) {
			c = -1
		} else if x.time.After(y.time) {
			c = 1
		}
	default:
		return naturalCompare(a, b)
	}
	if c != 0 {
		return c
	}
	return naturalCompare(a, b)
}

func naturalCompare(a, b string) int {
	if a == b {
		return 0
	}
	if natsort.Compare(a, b) {
		return -1
	}
	return 1
}
