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
	"math/rand"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/model/labels"
)

// orderedLabelValues is in ascending sort_by_label order.
var orderedLabelValues = []string{
	// Leading whitespace.
	"\t1",
	" 1",
	" 10",
	" a",
	" inf",
	"\u00a0x",

	// Positive infinity.
	"+Inf",
	"+infinity",
	"Inf",
	"inf",

	// Finite numbers.
	"-1e999999999999999999999",
	"-1e400",
	"-2",
	"-1.5",
	"-0",
	"0",
	".5",
	"0.5",
	"5e-1",
	"+1",
	"1",
	"1.0",
	"1e0",
	"2",
	"10",
	"1e3",
	"20000000000000001",
	"2.00000000000000015e16",
	"1e400",
	"2e400",
	"1e999999999999999999999",

	// Negative infinity.
	"-Inf",
	"-inf",
	"-infinity",

	// Durations.
	"-1h",
	"-1.5e3ms",
	"0s",
	"1ns",
	"1us",
	"1µs",
	"1μs",
	"1ms",
	"+1s",
	"1e3ms",
	"1s",
	"1.5m",
	"1m30s",
	"90s",
	"1h",
	"1d",
	"1w",
	"1y",
	"1e400y",

	// Bytes.
	"-1KiB",
	"0B",
	"1B",
	"1b",
	"1KB",
	"1kB",
	"1000B",
	"1KiB",
	"1.5GiB",
	"2e3MB",
	"1EB",
	"1YiB",
	"1e30YB",

	// Semantic versions.
	"0.9.0",
	"1.0.0-alpha",
	"1.0.0-alpha.1",
	"1.0.0-alpha.beta",
	"1.0.0-beta",
	"1.0.0-beta.2",
	"1.0.0-beta.11",
	"1.0.0-rc.1",
	"1.0.0",
	"1.0.0+build.5",
	"v1.0.0",
	"1.2.3",
	"1.10.0",
	"v2.0.0",
	"10.0.0",
	"18446744073709551616.0.0",

	// IP addresses.
	"1.2.3.4",
	"10.0.0.1",
	"255.255.255.255",
	"::",
	"::1",
	"::ffff:1.2.3.4",
	"2001:db8::1",
	"fe80::1",
	"fe80::1%eth0",

	// CIDR prefixes.
	"10.0.0.0/8",
	"10.1.2.3/8",
	"10.0.0.0/16",
	"192.168.0.0/16",
	"::/0",
	"::ffff:10.0.0.0/104",
	"2001:db8::/32",
	"2001:db8::/48",

	// Timestamps.
	"2024-01-01T00:00:00+01:00",
	"2023-12-31T23:30:00Z",
	"2024-01-01T00:00:00.000Z",
	"2024-01-01T00:00:00Z",
	"2024-01-01T00:00:00.5Z",

	// Untyped.
	"",
	"+NaN",
	"-",
	"1 GB",
	"01.2.3",
	"1.2.3-01",
	"1.02.3.4",
	"1.2.3.4.5",
	"1.5 ",
	"1e",
	"1e+",
	"4m5",
	"NaN",
	"a",
	"a01",
	"a1",
	"a2",
	"a10",
	"b",
	"v1.2",
}

func TestCompareLabelSortKeysTotalOrder(t *testing.T) {
	keys := make([]labelSortKey, len(orderedLabelValues))
	for i, v := range orderedLabelValues {
		keys[i] = newLabelSortKey(v)
	}
	for i := range keys {
		for j := range keys {
			require.Equal(t, cmp.Compare(i, j), compareLabelSortKeys(&keys[i], &keys[j]),
				"comparing %q with %q", orderedLabelValues[i], orderedLabelValues[j])
		}
	}
}

func TestSortByLabelTypedValues(t *testing.T) {
	vec := make(Vector, 0, len(orderedLabelValues))
	for i, v := range orderedLabelValues {
		// The second label must not influence the order of distinct values.
		vec = append(vec, Sample{Metric: labels.FromStrings("v", v, "w", string(rune('z'-i%26)))})
	}
	values := func(vec Vector) []string {
		var vs []string
		for _, s := range vec {
			vs = append(vs, s.Metric.Get("v"))
		}
		return vs
	}

	r := rand.New(rand.NewSource(1))
	r.Shuffle(len(vec), func(i, j int) { vec[i], vec[j] = vec[j], vec[i] })
	require.Equal(t, orderedLabelValues, values(sortByLabel(vec, []string{"v", "w"}, false)))

	r.Shuffle(len(vec), func(i, j int) { vec[i], vec[j] = vec[j], vec[i] })
	reversed := slices.Clone(orderedLabelValues)
	slices.Reverse(reversed)
	require.Equal(t, reversed, values(sortByLabel(vec, []string{"v", "w"}, true)))
}

func TestNewLabelSortKeyClass(t *testing.T) {
	for value, class := range map[string]labelValueClass{
		" 1":                                  classLeadingSpace,
		"\t2024-01-01T00:00:00Z":              classLeadingSpace,
		"\n+Inf":                              classLeadingSpace,
		"+Inf":                                classPosInf,
		"INFINITY":                            classPosInf,
		"-inf":                                classNegInf,
		"1":                                   classNumber,
		"+1":                                  classNumber,
		"-1.5E+3":                             classNumber,
		"1.":                                  classNumber,
		".5e-3":                               classNumber,
		"1e400":                               classNumber,
		"1e":                                  classUntyped,
		"1e+":                                 classUntyped,
		"1e-":                                 classUntyped,
		"e5":                                  classUntyped,
		".":                                   classUntyped,
		"+":                                   classUntyped,
		"0x10":                                classUntyped,
		"1_000":                               classUntyped,
		"NaN":                                 classUntyped,
		"-nan":                                classUntyped,
		"1h30m":                               classDuration,
		"-1h30.5m":                            classDuration,
		"+1.5e3ms":                            classDuration,
		"-2E-3s":                              classDuration,
		"1e400y":                              classDuration,
		"1es":                                 classUntyped,
		"1e3h30m":                             classUntyped,
		"1h-30m":                              classUntyped,
		"1h30":                                classUntyped,
		"1M":                                  classUntyped,
		"-":                                   classUntyped,
		"1B":                                  classBytes,
		"1eb":                                 classBytes,
		"1e3b":                                classBytes,
		"-1.5e3KiB":                           classBytes,
		"+2gib":                               classBytes,
		"1 GB":                                classUntyped,
		"1KiBs":                               classUntyped,
		"1.2.3":                               classSemver,
		"v1.2.3-rc.1+build.7":                 classSemver,
		"1.2.3-alpha-1":                       classSemver,
		"V1.2.3":                              classUntyped,
		"vv1.2.3":                             classUntyped,
		"v1.2":                                classUntyped,
		"01.2.3":                              classUntyped,
		"1.2.3-":                              classUntyped,
		"1.2.3-01":                            classUntyped,
		"1.2.3-a..b":                          classUntyped,
		"1.2.3+":                              classUntyped,
		"1.2.3+b_1":                           classUntyped,
		"1.2.3.4":                             classIP,
		"::ffff:1.2.3.4":                      classIP,
		"fe80::1%eth0":                        classIP,
		"1.02.3.4":                            classUntyped,
		"10.0.0.0/8":                          classCIDR,
		"::ffff:10.0.0.0/104":                 classCIDR,
		"10.0.0.0/33":                         classUntyped,
		"2024-01-01T00:00:00Z":                classTimestamp,
		"2024-01-01T00:00:00.123456789+05:30": classTimestamp,
		"2024-01-01":                          classUntyped,
		"":                                    classUntyped,
		"api-server":                          classUntyped,
	} {
		require.Equal(t, class, newLabelSortKey(value).class, "class of %q", value)
	}
}

func TestCompareLabelSortKeysTypedEquality(t *testing.T) {
	for _, tc := range [][2]string{
		{"1", "1.000e0"},
		{"-0", "+0"},
		{"1e999999999999999999999", "10e999999999999999999998"},
		{"0.1h", "6m"},
		{"1h30m", "5400s"},
		{"1e400y", "31536000e400s"},
		{"1.5us", "1500ns"},
		{"1kB", "1000b"},
		{"1KiB", "1024B"},
		{"1e-3kB", "1B"},
		{"1.2.3", "v1.2.3+build"},
		{"10.0.0.0/8", "10.255.255.255/8"},
		{"2024-01-01T01:00:00+01:00", "2024-01-01T00:00:00Z"},
	} {
		a, b := newLabelSortKey(tc[0]), newLabelSortKey(tc[1])
		require.Equal(t, a.class, b.class, "classes of %q and %q", tc[0], tc[1])
		switch a.class {
		case classNumber, classDuration, classBytes:
			require.Zero(t, a.num.cmp(b.num), "comparing %q with %q", tc[0], tc[1])
		case classSemver:
			require.Zero(t, a.ver.cmp(b.ver), "comparing %q with %q", tc[0], tc[1])
		case classCIDR:
			require.Equal(t, a.addr, b.addr, "comparing %q with %q", tc[0], tc[1])
			require.Equal(t, a.bits, b.bits, "comparing %q with %q", tc[0], tc[1])
		case classTimestamp:
			require.True(t, a.ts.Equal(b.ts), "comparing %q with %q", tc[0], tc[1])
		default:
			require.Failf(t, "unexpected class", "%q has class %d", tc[0], a.class)
		}
		// Equal typed values are ordered by natural order of the raw strings.
		require.Equal(t, naturalCompare(tc[0], tc[1]), compareLabelSortKeys(&a, &b), "comparing %q with %q", tc[0], tc[1])
	}
}

func TestNaturalCompare(t *testing.T) {
	ordered := []string{"", "-1", "01", "1", "001a", "01a", "1a", "1a1", "2", "10", "99999999999999999999", "100000000000000000000", "A", "a", "a0", "a1", "a01b", "a1b", "a 2", "a 10", "b"}
	for i := range ordered {
		for j := range ordered {
			require.Equal(t, cmp.Compare(i, j), naturalCompare(ordered[i], ordered[j]), "comparing %q with %q", ordered[i], ordered[j])
		}
	}
}
