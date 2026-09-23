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

func TestCompareLabelValuesClassOrder(t *testing.T) {
	// One representative of each class, strictly increasing.
	requireOrder(t,
		" inf",
		"+inf",
		"-1e400",
		"-2",
		"0",
		"1.5",
		"2",
		"1e1",
		"1e400",
		"-inf",
		"-1h",
		"1ns",
		"1ms",
		"1s",
		"1m",
		"1h",
		"1d",
		"1w",
		"1y",
		"-1KiB",
		"1B",
		"1KB",
		"1KiB",
		"1MB",
		"1MiB",
		"1.0.0-alpha",
		"1.0.0",
		"1.2.3",
		"1.10.0",
		"2.0.0",
		"0.0.0.0",
		"10.0.0.1",
		"255.255.255.255",
		"::",
		"::1",
		"::ffff:192.0.2.1",
		"2001:db8::1",
		"0.0.0.0/0",
		"0.0.0.0/1",
		"10.0.0.0/8",
		"10.0.0.0/16",
		"2001:db8::/32",
		"2001:db8::/48",
		"2019-12-31T23:00:00Z",
		"2020-06-15T12:00:00Z",
		"NaN",
	)

	// Positive infinity sorts before every finite number, and negative infinity after.
	require.Negative(t, compareLabelValues("+Infinity", "1e309"))
	require.Negative(t, compareLabelValues("-1", "-infinity"))
	require.Negative(t, compareLabelValues("1e309", "-inf"))
}

func TestCompareLabelValuesTiesAndPrecision(t *testing.T) {
	requireTypedTie(t, " inf", []string{"inf", "Inf", "+inf", "+Inf", "infinity", "Infinity", "+infinity"}, "-1")
	requireTypedTie(t, "-1", []string{"0", "+0", "-0", "0.0", "0e99", "0.00"}, "1")
	requireTypedTie(t, "0", []string{"1", "+1", "1.0", "1.00", "1.", "1e0", "+1e0"}, "2")
	requireTypedTie(t, "2", []string{"10", "1e1", "1E1", "+1e1", "100e-1", "1.0e1"}, "11")
	requireTypedTie(t, "99999999999999999999", []string{"100000000000000000000", "1e20", "10e19"}, "100000000000000000001")

	// Integers around 2^53 stay ordered. float64 cannot tell these apart.
	require.Negative(t, compareLabelValues("9007199254740992", "9007199254740993"))
	require.Negative(t, compareLabelValues("1e309", "1e310"))
	require.Negative(t, compareLabelValues("1e100000000000000000000", "2e100000000000000000000"))
	require.Negative(t, compareLabelValues("2e100000000000000000000", "1e100000000000000000001"))

	requireTypedTie(t, "1m", []string{"1h", "+1h", "60m", "3600s", "3600000ms"}, "2h")
	requireTypedTie(t, "1ms", []string{"1s", "1000ms", "1e3ms", "1e0s"}, "1m")
	requireTypedTie(t, "1ns", []string{"1us", "1\u00b5s", "1\u03bcs", "1000ns", "1e3ns"}, "1ms")
	requireTypedTie(t, "-2h", []string{"-1h", "-60m"}, "-1s")
	requireTypedTie(t, "1h", []string{"1h30m", "90m"}, "2h")
	require.Negative(t, compareLabelValues("1h", "1h30m"))
	requireTypedTie(t, "1e19ns", []string{"1e20ns", "1e11s"}, "1e21ns")
	require.Negative(t, compareLabelValues("1e1000000000000000000s", "1e1000000000000000001s"))

	requireTypedTie(t, "1B", []string{"1KB", "1kB", "1000B", "1e3B"}, "1KiB")
	requireTypedTie(t, "1KB", []string{"1KiB", "1024B", "1.024e3B"}, "2KiB")
	requireTypedTie(t, "1KiB", []string{"1MiB", "1024KiB", "1048576B"}, "2MiB")
	requireTypedTie(t, "999B", []string{"1MB", "1000000B", "1e6B"}, "1MiB")
	requireTypedTie(t, "-2KiB", []string{"-1KiB", "-1024B"}, "-1B")
	requireTypedTie(t, "1B", []string{"+1KiB", "1KiB"}, "2KiB")
	require.Negative(t, compareLabelValues("-2KB", "-1.5e3B"))
	require.Negative(t, compareLabelValues("-1.5e3B", "-1KB"))
	require.Negative(t, compareLabelValues("1e1000000000000000000B", "1e1000000000000000000KiB"))

	requireTypedTie(t, "1.2.2", []string{"1.2.3", "v1.2.3", "1.2.3+build.7", "1.2.3+aaa"}, "1.2.4")
	requireOrder(t,
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
	)
	require.Negative(t, compareLabelValues("1.0.0-1", "1.0.0-alpha"))
	require.Negative(t, compareLabelValues("1.0.999999999999999999999999", "1.1.0"))
	// Semver precedence puts the prerelease first. Natural order does the opposite.
	require.Negative(t, compareLabelValues("1.0.0-alpha", "1.0.0"))
	require.Negative(t, naturalCompare("1.0.0", "1.0.0-alpha"))

	requireTypedTie(t, "::", []string{"::1", "0:0:0:0:0:0:0:1", "::0001"}, "2001:db8::1")
	require.Negative(t, compareLabelValues("192.0.2.1", "::ffff:192.0.2.1"))
	require.Negative(t, compareLabelValues("255.255.255.255", "::"))

	requireTypedTie(t, "192.168.0.0/24", []string{"192.168.1.0/24", "192.168.1.9/24", "192.168.1.255/24"}, "192.168.1.0/25")
	requireTypedTie(t, "0.0.0.0/7", []string{"10.0.0.0/7", "11.0.0.0/7"}, "12.0.0.0/7")
	require.Negative(t, compareLabelValues("10.0.0.0/8", "2001:db8::/128"))
	require.Negative(t, compareLabelValues("255.255.255.255/32", "::/0"))

	requireTypedTie(t, "2019-12-31", []string{
		"2020-01-01",
		"2020-01-01T00:00:00Z",
		"2020-01-01T00:00:00+00:00",
		"2020-01-01T00:00:00.0Z",
		"2020-01-01 00:00:00Z",
	}, "2020-01-01T00:00:01Z")
	require.Negative(t, compareLabelValues("2020-01-01T00:00:00+01:00", "2020-01-01T00:00:00Z"))
}

func TestCompareLabelValuesUntypedAndWhitespace(t *testing.T) {
	ws := []string{" 10", " 2", "\tinf", "\n1", " 1.2.3", " +inf", "\t-1h", "  "}
	got := slices.Clone(ws)
	slices.SortFunc(got, compareLabelValues)
	want := slices.Clone(ws)
	slices.SortFunc(want, naturalCompare)
	require.Equal(t, want, got)
	for _, s := range ws {
		require.Negative(t, compareLabelValues(s, "+inf"), "%q < +inf", s)
		require.Negative(t, compareLabelValues(s, "abc"), "%q < abc", s)
	}

	untyped := []string{
		"", "NaN", "nan", "+NaN", "-nan", "1e", "1E", "1e+", "1E-", "+.5e-",
		"abc", "4m5", "4m600", "4m1000", "01.2.3", "1.2.3-", "1.0.0-01", "info",
		"infinity ", "1m1h", "1x", "+", ".", "e10", "1e3e4",
	}
	got = slices.Clone(untyped)
	slices.SortFunc(got, compareLabelValues)
	want = slices.Clone(untyped)
	slices.SortFunc(want, naturalCompare)
	require.Equal(t, want, got)
	for _, s := range untyped {
		require.Positive(t, compareLabelValues(s, "2020-06-15T12:00:00Z"), "%q is untyped", s)
	}
	require.Negative(t, compareLabelValues("4m5", "4m600"))
	require.Negative(t, compareLabelValues("4m600", "4m1000"))
	require.Negative(t, compareLabelValues("1.2.3", "1.11.3"))
	require.Negative(t, compareLabelValues("1.11.3", "1.111.3"))
	// A rejected duration stays untyped, so a real duration sorts first.
	require.Negative(t, compareLabelValues("61m", "1m1h"))
	require.Negative(t, compareLabelValues("0", ""))
}

func TestCompareLabelValuesTotalOrder(t *testing.T) {
	chain := []string{
		" inf", "+inf", "-1", "0", "10", "1e2", "-inf", "30s", "1m", "1KiB",
		"v1.2.3", "1.10.0", "10.1.0.1", "::1", "10.0.0.0/8", "10.0.0.0/16",
		"2020-01-02T03:04:05Z", "NaN",
	}
	shuffled := slices.Clone(chain)
	slices.Reverse(shuffled)
	slices.SortFunc(shuffled, compareLabelValues)
	require.Equal(t, chain, shuffled)

	for i := range chain {
		for j := range chain {
			cmp := compareLabelValues(chain[i], chain[j])
			switch {
			case i == j:
				require.Zero(t, cmp)
			case i < j:
				require.Negative(t, cmp)
			default:
				require.Positive(t, cmp)
			}
		}
	}
}

func requireOrder(t *testing.T, vals ...string) {
	t.Helper()
	for i := 0; i < len(vals); i++ {
		require.Zero(t, compareLabelValues(vals[i], vals[i]))
		for j := i + 1; j < len(vals); j++ {
			require.Negative(t, compareLabelValues(vals[i], vals[j]), "%q < %q", vals[i], vals[j])
			require.Positive(t, compareLabelValues(vals[j], vals[i]), "%q > %q", vals[j], vals[i])
		}
	}
}

func requireTypedTie(t *testing.T, before string, values []string, after string) {
	t.Helper()
	for i := range values {
		require.Negative(t, compareLabelValues(before, values[i]), "%q < %q", before, values[i])
		require.Negative(t, compareLabelValues(values[i], after), "%q < %q", values[i], after)
		for j := range values {
			require.Equal(t, naturalCompare(values[i], values[j]), compareLabelValues(values[i], values[j]),
				"tie-break %q vs %q", values[i], values[j])
		}
	}
}
