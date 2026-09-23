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

	"github.com/stretchr/testify/require"
)

func TestCompareLabelValuesOrder(t *testing.T) {
	// Already in ascending order.
	got := []string{
		"\t1",
		" 2",
		" 10",
		"\u00a0x",
		"+Inf",
		"Inf",
		"-1000",
		"-1",
		"+0",
		"-0",
		"0",
		"+1",
		"1",
		"1.0",
		"1e2",
		"100",
		"1e+06",
		"9007199254740992",
		"9007199254740993",
		"1e+999",
		"1e+1000",
		"-Inf",
		"-Infinity",
		"-1h",
		"1e-3s",
		"1ms",
		"1e3ms",
		"1s",
		"1m",
		"60s",
		"1h",
		"1KB",
		"1e3B",
		"1000B",
		"1KiB",
		"v1.2.3",
		"1.2.4",
		"1.10.0",
		"0.0.0.0",
		"192.0.2.1",
		"255.255.255.255",
		"::1",
		"::ffff:192.0.2.1",
		"2001:db8::1",
		"0.0.0.0/0",
		"10.1.2.3/8",
		"10.0.0.0/16",
		"192.168.1.0/24",
		"::/0",
		"2001:db8::/32",
		"2019-12-31T00:00:00Z",
		"2020-01-02T00:00:00Z",
		"2020-01-02T00:00:00.5Z",
		"",
		"1e",
		"1e+",
		"1e-",
		"NaN",
		"abc",
		"nan",
	}
	sorted := slices.Clone(got)
	slices.SortFunc(sorted, compareLabelValues)
	require.Equal(t, got, sorted)

	for i, a := range got {
		require.Zero(t, compareLabelValues(a, a), a)
		for j, b := range got {
			c := compareLabelValues(a, b)
			require.Equal(t, -c, compareLabelValues(b, a), "%q vs %q", a, b)
			switch {
			case i < j:
				require.Negative(t, c, "%q should sort before %q", a, b)
			case i > j:
				require.Positive(t, c, "%q should sort after %q", a, b)
			default:
				require.Zero(t, c)
			}
		}
	}
}

func TestCompareLabelValuesEqualTypedTieBreak(t *testing.T) {
	// Same numeric value; natural order of the original strings decides.
	require.Negative(t, compareLabelValues("+0", "-0"))
	require.Negative(t, compareLabelValues("-0", "0"))
	require.Negative(t, compareLabelValues("+1", "1"))
	require.Negative(t, compareLabelValues("1", "1.0"))
	require.Negative(t, compareLabelValues("1e2", "100"))
	require.Negative(t, compareLabelValues("1", "1e+0"))

	// 1s == 1000ms == 1e3ms. Natural order: "1e3ms" < "1s" < "1000ms".
	require.Negative(t, compareLabelValues("1e3ms", "1s"))
	require.Negative(t, compareLabelValues("1s", "1000ms"))
	require.Negative(t, compareLabelValues("1m", "60s"))
	require.Negative(t, compareLabelValues("1h", "3600s"))
	require.Negative(t, compareLabelValues("1e-3s", "1ms"))

	// 1KB == 1000B == 1e3B, and 1e-3KB == 1B.
	require.Negative(t, compareLabelValues("1KB", "1e3B"))
	require.Negative(t, compareLabelValues("1e3B", "1000B"))
	require.Negative(t, compareLabelValues("1B", "1e-3KB"))
	require.Zero(t, compareLabelValues("1024B", "1024B"))
	require.Negative(t, compareLabelValues("1KiB", "1024B")) // equal magnitude, natural tie-break
	// Magnitudes match, so neither string sorts as a larger byte count.
	require.Negative(t, compareLabelValues("0B", "1KiB"))
	require.Negative(t, compareLabelValues("1024B", "1MiB"))

	require.Negative(t, compareLabelValues("1.2.3", "v1.2.3"))
	require.Negative(t, compareLabelValues("1.2.3", "1.2.3+aaa"))
	require.Negative(t, compareLabelValues("1.2.3+aaa", "1.2.3+bbb"))
	require.Negative(t, compareLabelValues("192.168.1.0/24", "192.168.1.5/24"))
	require.Negative(t, compareLabelValues("2020-01-02", "2020-01-02T00:00:00Z"))
	require.Negative(t, compareLabelValues("2020-01-02T00:00:00-05:00", "2020-01-02T05:00:00Z"))
}

func TestCompareLabelValuesClasses(t *testing.T) {
	require.Negative(t, compareLabelValues(" 1", "+Inf"))
	require.Negative(t, compareLabelValues(" +1", "1"))
	require.Negative(t, compareLabelValues(" 2", " 10"))

	// +Inf, then every finite number, then -Inf.
	require.Negative(t, compareLabelValues("+Inf", "-100"))
	require.Negative(t, compareLabelValues("-100", "0"))
	require.Negative(t, compareLabelValues("1e+1000", "-Inf"))
	require.Negative(t, compareLabelValues("-Inf", "1s"))

	// Bare exponent markers and NaN are not numeric.
	for _, s := range []string{"1e", "1e+", "1e-", "+1e", "NaN", "nan", "+NaN", "-NaN"} {
		require.Negative(t, compareLabelValues("2020-01-01T00:00:00Z", s), s)
		require.Negative(t, compareLabelValues("1", s), s)
		require.Positive(t, compareLabelValues(s, "1"), s)
	}

	// Signed scientific durations and bytes, including values past float64.
	require.Negative(t, compareLabelValues("-2s", "-1s"))
	require.Negative(t, compareLabelValues("-1e3s", "1ms"))
	require.Negative(t, compareLabelValues("+1.5e2ms", "1s"))
	require.Negative(t, compareLabelValues("1e+99s", "1e+100s"))
	require.Negative(t, compareLabelValues("-2KiB", "-1KiB"))
	require.Negative(t, compareLabelValues("-1KB", "0B"))
	require.Negative(t, compareLabelValues("1e+99B", "1e+100B"))
	require.Negative(t, compareLabelValues("1KB", "1KiB"))
	require.Negative(t, compareLabelValues("1MB", "1MiB"))

	require.Negative(t, compareLabelValues("1.0.0-alpha", "1.0.0-alpha.1"))
	require.Negative(t, compareLabelValues("1.0.0-alpha.1", "1.0.0-alpha.beta"))
	require.Negative(t, compareLabelValues("1.0.0-alpha.beta", "1.0.0-beta"))
	require.Negative(t, compareLabelValues("1.0.0-beta.2", "1.0.0-beta.11"))
	require.Negative(t, compareLabelValues("1.0.0-beta.11", "1.0.0-rc.1"))
	require.Negative(t, compareLabelValues("1.0.0-rc.1", "1.0.0"))
	require.Negative(t, compareLabelValues("1.0.99999999999999999999", "1.1.0"))
	// Invalid semver falls back to untyped, after timestamps.
	require.Negative(t, compareLabelValues("1.2.3", "01.2.3"))
	require.Negative(t, compareLabelValues("2020-01-02T00:00:00Z", "01.2.3"))
	require.Negative(t, compareLabelValues("2020-01-02T00:00:00Z", "1.0.0-01"))
	require.Negative(t, compareLabelValues("1.2", "1.2.0")) // 1.2 is numeric, not semver

	// IPv4 before IPv6, including IPv4-mapped literals.
	require.Negative(t, compareLabelValues("192.0.2.1", "::ffff:192.0.2.1"))
	require.Negative(t, compareLabelValues("255.255.255.255", "::"))
	require.Negative(t, compareLabelValues("::ffff:192.0.2.1", "2001:db8::1"))
	require.Equal(t, classIP, parseLabelValue("::ffff:192.0.2.1").class)
	require.True(t, parseLabelValue("::ffff:192.0.2.1").ip.Is6())
	require.False(t, parseLabelValue("::ffff:192.0.2.1").ip.Is4())

	// Equal masked network address: smaller prefix length first.
	require.Negative(t, compareLabelValues("10.1.2.3/8", "10.0.0.0/16"))
	require.Negative(t, compareLabelValues("10.0.0.0/8", "10.0.0.0/16"))
	require.Negative(t, compareLabelValues("255.255.255.255/32", "::/0"))
	require.Negative(t, compareLabelValues("192.0.2.0/24", "::ffff:192.0.2.0/112"))

	// Empty is untyped, and leading whitespace sorts before it.
	require.Negative(t, compareLabelValues(" ", ""))
	require.Negative(t, compareLabelValues("", "abc"))
	require.Negative(t, compareLabelValues("1", ""))
	require.Equal(t, classUntyped, parseLabelValue("").class)
	require.Equal(t, classLeadingSpace, parseLabelValue(" 1").class)

	// Untyped natural order still orders embedded integers.
	require.Negative(t, compareLabelValues("4m5", "4m600"))
	require.Negative(t, compareLabelValues("4m600", "4m1000"))
	require.Equal(t, classUntyped, parseLabelValue("4m5").class)
}

func TestCompareLabelValuesSameInstant(t *testing.T) {
	// These are the same moment; natural order of the text breaks the tie,
	// and both sit with other timestamps rather than untyped strings.
	a := "2020-01-02T05:00:00Z"
	b := "2020-01-02T00:00:00-05:00"
	require.Negative(t, compareLabelValues("2020-01-02T04:00:00Z", b))
	require.Negative(t, compareLabelValues(b, a))
	require.Negative(t, compareLabelValues(a, "2020-01-02T06:00:00Z"))
	require.Equal(t, classTimestamp, parseLabelValue(a).class)
	require.Equal(t, classTimestamp, parseLabelValue(b).class)
	require.True(t, parseLabelValue(a).ts.Equal(parseLabelValue(b).ts))
}
