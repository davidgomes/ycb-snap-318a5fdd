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
	"slices"
	"testing"
)

// orderedLabelValues is a strict ascending sequence. Spellings that compare
// equal (same typed value and the same natural order, such as "1" and "01")
// are omitted and checked separately.
var orderedLabelValues = []string{
	// Leading whitespace, natural order of the original strings.
	"\t",
	"\t1",
	"\n",
	" ",
	"  ",
	"  1",
	" +Inf",
	"\u00a0Inf",

	// Positive infinity, then natural spelling order.
	"+Inf",
	"+Infinity",
	"+inf",
	"Inf",
	"Infinity",
	"inf",

	// Finite numbers, most negative first. Equal magnitudes are in natural order.
	"-1e1001",
	"-1e1000",
	"-1e310",
	"-9.99e309",
	"-1e309",
	"-1e308",
	"-2",
	"-1.5",
	"-1",
	"-1.0",
	"-0.1",
	"-1e-1",
	"+0",
	"+0.0",
	"-0",
	"-0.0",
	".0",
	"0",
	"0.",
	"0.0",
	"+.5",
	".5",
	"0.5",
	"+1",
	"1",
	"1.",
	"1.0",
	"1e0",
	"1.2",
	"1.5",
	"2",
	"1e1",
	"10",
	"1.0e2",
	"1E+2",
	"1e2",
	"1e+2",
	"100",
	"100.0",
	"1e+06",
	"1000000",
	"99999999999999999999",
	"1e20",
	"100000000000000000000",
	"1e308",
	"1e309",
	"9.99e309",
	"1e310",
	"1e1000",
	"1000e997",
	"1e1001",

	// Negative infinity sorts after every finite number.
	"-Inf",
	"-Infinity",
	"-inf",

	// Durations. Equal magnitudes are in natural order.
	"-1e1000000s1ns",
	"-1e1000000s",
	"-1e1000s",
	"-1h30m",
	"-90m",
	"-5400s",
	"-1.5e1m",
	"-15m",
	"-900s",
	"-1s",
	"-1e-3s",
	"-1ms",
	"-1000000ns",
	"+0s",
	"-0s",
	"0ms",
	"0ns",
	"0s",
	"0us",
	"1ns",
	"1us",
	"1\u00b5s",
	"1\u03bcs",
	"1000ns",
	"1e-3s",
	"1ms",
	"0.1s",
	"100ms",
	"+1s",
	"1e3ms",
	"1s",
	"1.5e0s",
	"1.5s",
	"1m",
	"60s",
	"2e-1h",
	"12m",
	"720s",
	"1.5e1m",
	"15m",
	"900s",
	"1.5e1m30s",
	"930s",
	"1.5h",
	"1h30m",
	"90m",
	"5400s",
	"1w",
	"7d",
	"1y",
	"365d",
	"1e1000s",
	"1e1000000s",
	"1e1000000s1ns",

	// Byte sizes.
	"-1e1000B",
	"-2MiB",
	"-1.5KiB",
	"-1KiB",
	"-1KB",
	"-1e3B",
	"-1000B",
	"0B",
	"0KiB",
	"1B",
	"512B",
	"1KB",
	"1e3B",
	"1000B",
	"1.024e3B",
	"1KiB",
	"1024B",
	"1KiB1B",
	"+1.5e3B",
	"+1.5KiB",
	"+2e3B",
	"1MB",
	"1e6B",
	"1MiB",
	"1EB",
	"1e18B",
	"1EiB",
	"1e1000B",

	// Semantic versions. Invalid forms are untyped and appear later.
	"1.0.0-alpha",
	"v1.0.0-alpha",
	"1.0.0-alpha.1",
	"1.0.0-alpha.beta",
	"1.0.0-beta",
	"1.0.0-beta.2",
	"1.0.0-beta.11",
	"1.0.0-rc.1",
	"1.0.0",
	"1.2.3",
	"V1.2.3",
	"v1.2.3",
	"v1.2.3+build",
	"1.11.3",
	"1.111.3",
	"2.0.0",
	"v2.0.0",

	// IP addresses. IPv4 sorts before IPv6, including IPv4-mapped IPv6.
	"0.0.0.0",
	"1.2.3.4",
	"10.0.0.1",
	"192.168.0.1",
	"255.255.255.255",
	"::1",
	"::ffff:1.2.3.4",
	"2001:DB8::1",
	"2001:db8::1",
	"fe80::1",
	"fe80::1%eth0",

	// CIDR prefixes. Smaller prefix lengths sort first for the same network.
	"0.0.0.0/0",
	"0.0.0.0/1",
	"10.0.0.0/7",
	"10.1.0.0/7",
	"10.0.0.0/8",
	"10.0.0.0/16",
	"10.1.0.0/16",
	"192.168.1.0/24",
	"192.168.1.5/24",
	"255.255.255.255/32",
	"::/0",
	"::/1",
	"::ffff:192.0.2.0/96",
	"2001:db8::/32",
	"2001:db8::/48",

	// Timestamps. Equal instants break ties by natural order.
	"2020-01-01",
	"2020-01-01T00:00:00+00:00",
	"2020-01-01T00:00:00Z",
	"2020-01-02",
	"2020-01-02T00:00:00Z",
	"2020-01-02T00:00:00.5Z",
	"2020-01-02T01:00:00Z",
	"2020-01-02T03:00:00+02:00",

	// Untyped strings, including values that look typed but are not.
	"",
	"+NaN",
	"+info",
	"-NaN",
	"01.2.3",
	"1.2.3-01",
	"1.2.3.4.5",
	"1.e",
	"1b",
	"1e",
	"1e+",
	"1e-",
	"1kb",
	"4m5",
	"4m600",
	"4m1000",
	"NaN",
	"apple",
	"host2",
	"host10",
	"nan",
	"v1.2",
}

func TestCompareLabelValuesOrder(t *testing.T) {
	for i, a := range orderedLabelValues {
		if c := compareLabelValues(a, a); c != 0 {
			t.Fatalf("compare(%q, %q) = %d, want 0", a, a, c)
		}
		for j := i + 1; j < len(orderedLabelValues); j++ {
			b := orderedLabelValues[j]
			c := compareLabelValues(a, b)
			if c >= 0 {
				t.Fatalf("compare(%q, %q) = %d, want < 0 (classes %d, %d)", a, b, c, classify(a).class, classify(b).class)
			}
			if back := compareLabelValues(b, a); back != -c {
				t.Fatalf("compare(%q, %q) = %d, reverse = %d", a, b, c, back)
			}
		}
	}

	sorted := slices.Clone(orderedLabelValues)
	slices.Reverse(sorted)
	slices.SortFunc(sorted, compareLabelValues)
	for i := range orderedLabelValues {
		if sorted[i] != orderedLabelValues[i] {
			t.Fatalf("sorted[%d] = %q, want %q", i, sorted[i], orderedLabelValues[i])
		}
	}
}

func TestCompareLabelValuesEqualMagnitudes(t *testing.T) {
	pairs := [][2]string{
		{"1", "01"},
		{"1", "1.0"},
		{"1.0", "1.00"},
		{"1E+2", "100"},
		{"1.", "1"},
		{"+0", "-0"},
		{"0", "0.0"},
		{"+.5", "0.5"},
		{"1e1000", "1000e997"},
		{"1h30m", "90m"},
		{"1.5h", "5400s"},
		{"1e3ms", "1s"},
		{"0.1s", "100ms"},
		{"-1.5e1m", "-15m"},
		{"2e-1h", "12m"},
		{"1us", "1000ns"},
		{"1\u00b5s", "1us"},
		{"1\u03bcs", "1us"},
		{"60s", "1m"},
		{"7d", "1w"},
		{"365d", "1y"},
		{"0s", "-0s"},
		{"1KiB", "1024B"},
		{"1KB", "1000B"},
		{"1e3B", "1KB"},
		{"1.024e3B", "1KiB"},
		{"1EB", "1e18B"},
		{"-1KB", "-1e3B"},
		{"192.168.1.5/24", "192.168.1.0/24"},
		{"10.1.0.0/7", "10.0.0.0/7"},
		{"2001:DB8::1", "2001:db8::1"},
		{"2020-01-02", "2020-01-02T00:00:00Z"},
		{"2020-01-02T01:00:00Z", "2020-01-02T03:00:00+02:00"},
		{"v1.2.3", "1.2.3"},
		{"v1.2.3+build", "1.2.3"},
	}
	for _, pair := range pairs {
		a, b := classify(pair[0]), classify(pair[1])
		if a.class != b.class || compareParsed(a, b) != 0 {
			t.Errorf("magnitude compare(%q class %d, %q class %d) = %d, want 0", pair[0], a.class, pair[1], b.class, compareParsed(a, b))
		}
	}

	if c := compareLabelValues("1", "01"); c != 0 {
		t.Errorf("compare(1, 01) = %d, want 0", c)
	}
	if c := compareLabelValues("1.0", "1.00"); c != 0 {
		t.Errorf("compare(1.0, 1.00) = %d, want 0", c)
	}
	if c := compareLabelValues("1", "1.0"); c >= 0 {
		t.Errorf("compare(1, 1.0) = %d, want < 0", c)
	}
}

func TestClassifyLabelValues(t *testing.T) {
	untyped := []string{"", "1e", "1e+", "1e-", "1E", "1E+", "1E-", "1.e", "NaN", "+NaN", "-NaN", "nan", "nAn", "NAN", "01.2.3", "1.2.3-01", "4m5", "v1.2", "1kb", "1b", "+info"}
	for _, s := range untyped {
		if got := classify(s).class; got != classUntyped {
			t.Errorf("classify(%q) = %d, want untyped", s, got)
		}
	}

	semvers := []string{"1.2.3", "v1.2.3", "V1.2.3", "1.0.0-alpha", "1.2.3+build.1"}
	for _, s := range semvers {
		if got := classify(s).class; got != classSemver {
			t.Errorf("classify(%q) = %d, want semver", s, got)
		}
	}

	if got := classify("1.2").class; got != classNumber {
		t.Errorf("classify(1.2) = %d, want number", got)
	}
	if got := classify("1.2.3.4").class; got != classIP {
		t.Errorf("classify(1.2.3.4) = %d, want IP", got)
	}

	ip, ok := parseIP("::ffff:1.2.3.4")
	if !ok || !ip.Is6() || !ip.Is4In6() || ip.Is4() {
		t.Fatalf("::ffff:1.2.3.4 parsed as %+v ok=%v", ip, ok)
	}
	if c := compareLabelValues("1.2.3.4", "::ffff:1.2.3.4"); c >= 0 {
		t.Errorf("IPv4 should sort before IPv4-mapped IPv6, compare = %d", c)
	}

	if got := classify(" +Inf").class; got != classLeadingSpace {
		t.Errorf("classify(leading space +Inf) = %d, want leading space", got)
	}
	if c := compareLabelValues(" +Inf", "+Inf"); c >= 0 {
		t.Errorf("leading whitespace should sort before +Inf, compare = %d", c)
	}
	if got := classify("+Inf").class; got != classPosInf {
		t.Errorf("classify(+Inf) = %d, want +Inf", got)
	}
	if got := classify("-infinity").class; got != classNegInf {
		t.Errorf("classify(-infinity) = %d, want -Inf", got)
	}
	if got := classify("inf").class; got != classPosInf {
		t.Errorf("classify(inf) = %d, want +Inf", got)
	}

	if c := compareLabelValues("10.0.0.0/8", "10.0.0.0/16"); c >= 0 {
		t.Errorf("shorter CIDR prefix should sort first, compare = %d", c)
	}
	if c := compareLabelValues("10.0.0.0/8", "10.1.0.0/16"); c >= 0 {
		t.Errorf("10.0.0.0/8 should sort before 10.1.0.0/16, compare = %d", c)
	}
	if c := compareLabelValues("255.255.255.255/32", "::/0"); c >= 0 {
		t.Errorf("IPv4 CIDR should sort before IPv6 CIDR, compare = %d", c)
	}
	if c := compareLabelValues("1e309", "1e310"); c >= 0 {
		t.Errorf("1e309 should sort before 1e310, compare = %d", c)
	}
	if c := compareLabelValues("-1e310", "-1e309"); c >= 0 {
		t.Errorf("-1e310 should sort before -1e309, compare = %d", c)
	}
	if c := compareLabelValues("+Inf", "-1e1000"); c >= 0 {
		t.Errorf("+Inf should sort before finite numbers, compare = %d", c)
	}
	if c := compareLabelValues("1e1000", "-Inf"); c >= 0 {
		t.Errorf("finite numbers should sort before -Inf, compare = %d", c)
	}
	if c := compareLabelValues("1e1000000s", "1e1000000s1ns"); c >= 0 {
		t.Errorf("1e1000000s should sort before 1e1000000s1ns, compare = %d", c)
	}
}
