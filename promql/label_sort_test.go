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

func TestCompareLabelValuesOrder(t *testing.T) {
	// Ascending class order, with intra-class expectations.
	want := []string{
		"\t2",
		" 10",
		"  z",
		"+Inf",
		"Infinity",
		"-1e1000",
		"-2",
		"-0",
		"0",
		"+1",
		"1.0",
		"1e1",
		"1e+1000",
		"-infinity",
		"-1h",
		"0s",
		"1e3ms",
		"1s",
		"+1.5e1s",
		"1m",
		"-2KiB",
		"512B",
		"1KB",
		"1KiB",
		"1e3KB",
		"1.0.0-alpha",
		"1.2.3",
		"v1.2.3",
		"1.11.3",
		"10.0.0.1",
		"192.168.0.1",
		"::ffff:192.0.2.1",
		"2001:db8::1",
		"10.0.0.0/8",
		"10.0.0.1/8",
		"10.0.0.0/16",
		"2001:db8::/32",
		"2001:db8::/48",
		"2020-01-01",
		"2020-01-02T00:00:00Z",
		"2020-01-02T01:00:00+01:00",
		"2020-01-03T00:00:00Z",
		"",
		"1e",
		"1e+",
		"4m5",
		"4m600",
		"4m1000",
		"NaN",
		"nan",
		"zebra",
	}
	got := slices.Clone(want)
	slices.SortFunc(got, compareLabelValues)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q want %q\nfull: %#v", i, got[i], want[i], got)
		}
	}
}

func TestCompareLabelValuesTies(t *testing.T) {
	if compareLabelValues("1", "1.0") >= 0 {
		t.Fatal("natural tie-break should order 1 before 1.0")
	}
	if compareLabelValues("1.0", "1") <= 0 {
		t.Fatal("natural tie-break should order 1.0 after 1")
	}
	if compareLabelValues("+Inf", "Infinity") >= 0 {
		t.Fatal("+Inf and Infinity are the same value and should use natural order")
	}
	if compareLabelValues("2020-01-02T00:00:00Z", "2020-01-02T01:00:00+01:00") >= 0 {
		t.Fatal("equal instants should break ties by the original string")
	}
	if compareLabelValues("v1.2.3", "1.2.3") == 0 {
		t.Fatal("equal semver with different text must not compare equal")
	}
	if compareLabelValues("", "zebra") >= 0 {
		t.Fatal("empty label is an untyped string and sorts before zebra")
	}
	if compareLabelValues("1e", "1e+") >= 0 {
		t.Fatal("bare exponent markers are untyped natural strings")
	}
	if compareLabelValues("NaN", "nan") >= 0 {
		t.Fatal("NaN literals are untyped and use natural order")
	}
	// Huge magnitudes must not collapse.
	if compareLabelValues("1e100000", "1e100001") >= 0 {
		t.Fatal("1e100000 should sort before 1e100001")
	}
	if compareLabelValues("1e100000s", "1e100001s") >= 0 {
		t.Fatal("duration magnitudes must preserve huge exponents")
	}
	if compareLabelValues("1e100000KB", "1e100001KB") >= 0 {
		t.Fatal("byte magnitudes must preserve huge exponents")
	}
	if compareLabelValues("10.0.0.0/8", "10.0.0.0/16") >= 0 {
		t.Fatal("smaller prefix length sorts first")
	}
	if compareLabelValues("192.168.0.1", "::ffff:192.0.2.1") >= 0 {
		t.Fatal("IPv4 sorts before IPv4-mapped IPv6")
	}
}
