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
)

func compareLabelValueStrings(a, b string) int {
	ta, tb := parseTypedLabelValue(a), parseTypedLabelValue(b)
	return compareTypedLabelValues(&ta, &tb)
}

func TestTypedLabelValueOrder(t *testing.T) {
	expected := []string{
		"\tb", " 2", " 10",
		"+Inf", "inf",
		"-1e400", "-2", "-1.5", "0", "+0.5", "01", "1", "1.0", "1.2", "1e1", "10", "2e400", "1e401",
		"-Inf",
		"-1h", "1ns", "1ms", "1s", "1.5e3ms", "1m", "1h", "1h30m", "2h", "1d", "1e30y",
		"-1KB", "1 B", "1B", "1KB", "1000B", "1KiB", "1MB", "1GB", "1e3MB", "1YiB",
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11",
		"1.0.0-rc.1", "1.0.0", "1.0.0+build", "v1.0.0", "1.2.3", "1.11.3", "99999999999999999999999.0.0",
		"1.2.3.4", "10.0.0.1", "::1", "::ffff:1.2.3.4", "fe80::1",
		"10.0.0.0/8", "10.0.0.0/16", "10.0.0.1/16", "192.168.0.0/24", "::/0", "2001:db8::/32",
		"2020-01-01T00:00:00Z", "2020-01-01T01:00:00+00:00", "2020-01-01T00:30:00-01:00",
		"", "01.2.3", "1e", "1e+", "4m5", "4m600", "4m1000", "NaN", "a", "a01", "a1", "a2", "a10", "b",
	}

	shuffled := slices.Clone(expected)
	rand.New(rand.NewSource(1)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	slices.SortFunc(shuffled, compareLabelValueStrings)
	require.Equal(t, expected, shuffled)

	for _, a := range expected {
		for _, b := range expected {
			require.Equal(t, -compareLabelValueStrings(b, a), compareLabelValueStrings(a, b), "antisymmetry %q %q", a, b)
			for _, c := range expected {
				if compareLabelValueStrings(a, b) < 0 && compareLabelValueStrings(b, c) < 0 {
					require.Negative(t, compareLabelValueStrings(a, c), "transitivity %q %q %q", a, b, c)
				}
			}
		}
	}
}

func TestTypedLabelValueClasses(t *testing.T) {
	for s, class := range map[string]labelValueClass{
		" 1":                   classLeadingSpace,
		"+1e5":                 classNumber,
		"1e":                   classString,
		"nan":                  classString,
		"-infinity":            classNegInf,
		"+-inf":                classString,
		"-1.5e2h":              classDuration,
		"+2.5e3KiB":            classBytes,
		"v1.2.3-rc.1+b":        classSemver,
		"1.2.03":               classString,
		"::ffff:10.0.0.1":      classIP,
		"10.1.2.3/8":           classCIDR,
		"2024-05-06T07:08:09Z": classTimestamp,
		"":                     classString,
		"1e99999h1s":           classString,
	} {
		require.Equal(t, class, parseTypedLabelValue(s).class, s)
	}
	require.True(t, parseTypedLabelValue("::ffff:10.0.0.1").addr.Is6())
}
