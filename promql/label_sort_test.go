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
	"math/rand"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/model/labels"
)

// orderedLabelValues is sorted in strictly ascending order.
var orderedLabelValues = []string{
	// Leading whitespace, natural order.
	"\tz", " 1e3", " 9", " 10", " a",

	// Positive infinity.
	"+Inf", "Inf", "Infinity", "inf",

	// Finite numbers.
	"-1e400", "-5", "-0.5", "0", "0.0", "+0.5", "0.5", "5e-1", "1",
	"9007199254740992", "9007199254740993", "1e308", "1.5e308", "1e400", "1E401",

	// Negative infinity.
	"-Inf", "-inf",

	// Durations.
	"-1h", "-1ms", "0s", "1ns", "1us", "1.5e3ns", "1ms", "1s", "+5s", "1m",
	"1h", "1h30m", "90m", "1d", "1w", "1y", "1e30y", "1e31y",

	// Bytes.
	"-1KB", "0B", "1B", "1KB", "1000B", "1KiB", "1kib", "1MB", "1e3YB", "1e30YB",

	// Semantic versions.
	"0.9.0", "v0.9.0", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta",
	"1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
	"1.0.0+build", "1.2.3", "1.11.3", "1.111.3", "18446744073709551616.0.0",

	// IP addresses.
	"1.2.3.4", "10.0.0.1", "255.255.255.255", "::1", "::ffff:1.2.3.4", "2001:db8::1",

	// CIDR prefixes.
	"10.0.0.0/8", "10.0.0.0/16", "10.0.0.5/16", "192.168.0.0/16", "::/0",
	"::ffff:10.0.0.0/104", "2001:db8::/32",

	// Timestamps.
	"2020-01-01T01:00:00+02:00", "2020-01-01T00:00:00Z", "2020-01-01T00:00:00.5Z",
	"2021-06-01T12:00:00Z",

	// Untyped, natural order.
	"", "-", "01.2.3", "1.2.3-", "1.2.3-01", "1e", "1e+", "1h 30m", "4m5", "4m600",
	"4m1000", "5 ", "NaN", "a", "a2", "a10", "api-server", "b", "nan",
}

func TestCompareLabelValuesOrder(t *testing.T) {
	for i, a := range orderedLabelValues {
		require.Equal(t, 0, compareLabelValues(a, a), "%q", a)
		for _, b := range orderedLabelValues[i+1:] {
			require.Negative(t, compareLabelValues(a, b), "%q < %q", a, b)
			require.Positive(t, compareLabelValues(b, a), "%q > %q", b, a)
		}
	}
}

func TestParseLabelSortKeyClasses(t *testing.T) {
	for s, class := range map[string]labelValueClass{
		" 1":              classLeadingSpace,
		"\n1.2.3.4":       classLeadingSpace,
		"+inf":            classPosInf,
		"-Infinity":       classNegInf,
		"+1e-3":           classNumber,
		".5":              classNumber,
		"1.":              classNumber,
		"1e":              classString,
		"1e-":             classString,
		"NaN":             classString,
		"-1.5e2h":         classDuration,
		"5eb":             classBytes,
		"-2.5e3MiB":       classBytes,
		"v1.2.3":          classSemver,
		"V1.2.3":          classString,
		"1.2":             classNumber,
		"01.2.3":          classString,
		"::ffff:1.2.3.4":  classIP,
		"fe80::1/64":      classCIDR,
		"2020-01-01T00Z":  classString,
		"":                classString,
		"1h1e2000s":       classString,
		"4m5":             classString,
		"1.0.0-alpha..1":  classString,
		"1.0.0+build.001": classSemver,
	} {
		require.Equal(t, class, parseLabelSortKey(s).class, "%q", s)
	}
}

func TestSortVectorByLabels(t *testing.T) {
	vec := make(Vector, 0, len(orderedLabelValues))
	for _, v := range orderedLabelValues {
		vec = append(vec, Sample{Metric: labels.FromStrings("l", v)})
	}
	want := slices.Clone(vec)

	r := rand.New(rand.NewSource(1))
	for range 10 {
		r.Shuffle(len(vec), func(i, j int) { vec[i], vec[j] = vec[j], vec[i] })
		sortVectorByLabels(vec, []string{"l"}, false)
		require.Equal(t, want, vec)

		sortVectorByLabels(vec, []string{"l"}, true)
		slices.Reverse(vec)
		require.Equal(t, want, vec)
	}
}
