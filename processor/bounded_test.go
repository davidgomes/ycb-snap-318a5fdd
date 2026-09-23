// SPDX-License-Identifier: MIT

package processor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func boundedTestJobs() []*FileJob {
	var jobs []*FileJob
	langs := []string{"Go", "Java", "Python", "C \"Header\""}
	for i := range 40 {
		lang := langs[i%len(langs)]
		jobs = append(jobs, &FileJob{
			Language:          lang,
			PossibleLanguages: []string{lang},
			Filename:          fmt.Sprintf("file%d.%s", i%9, strings.ToLower(lang[:1])),
			Extension:         strings.ToLower(lang[:1]),
			Location:          fmt.Sprintf("dir%d/sub \"%d\"/file%d", i%3, i, i%9),
			Bytes:             int64(100 + i*7%13),
			Lines:             int64(10 + i%5),
			Code:              int64(5 + i%4),
			Comment:           int64(i % 3),
			Blank:             int64(i % 2),
			Complexity:        int64(i % 6),
			Uloc:              i % 4,
			LineLength:        []int{i, i + 1},
		})
	}
	return jobs
}

func cloneJobs(jobs []*FileJob) chan *FileJob {
	ch := make(chan *FileJob, len(jobs))
	for _, j := range jobs {
		c := *j
		c.PossibleLanguages = slices.Clone(j.PossibleLanguages)
		c.LineLength = slices.Clone(j.LineLength)
		ch <- &c
	}
	close(ch)
	return ch
}

func captureStdout(t *testing.T, fn func() string) (string, string) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.Bytes()
	}()
	result := fn()
	_ = w.Close()
	os.Stdout = orig
	return string(<-done), result
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan []byte)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.Bytes()
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return string(<-done)
}

func resetBoundedGlobals() {
	BoundedMemory = false
	BoundedMemoryDir = ""
	BoundedMemoryMaxInMemoryFiles = 0
	BoundedMemoryStats = false
	FormatMulti = ""
	Files = false
	SortBy = ""
	SortBySet = false
}

func TestRecordEncodingRoundTrip(t *testing.T) {
	jobs := boundedTestJobs()
	jobs[0].PossibleLanguages = nil
	jobs[1].LineLength = nil
	jobs[2].Binary, jobs[2].Minified, jobs[2].Generated = true, true, true
	jobs[3].WeightedComplexity = 12.3456789
	jobs[4].Symlocation = "link\x00name"
	jobs[5].Bytes, jobs[5].EndPoint = -5, 77

	for _, j := range jobs {
		got, err := decodeRecord(encodeRecord(nil, j))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, j) {
			t.Errorf("round trip mismatch\nwant %+v\ngot  %+v", j, got)
		}
	}

	if _, err := decodeRecord(encodeRecord(nil, jobs[0])[:5]); err == nil {
		t.Error("expected error decoding truncated record")
	}
}

func TestCSVFileKeyMatchesComparator(t *testing.T) {
	defer resetBoundedGlobals()
	jobs := boundedTestJobs()
	for _, sortBy := range []string{"", "name", "language", "lines", "blanks", "code", "comments", "complexity", "bytes", "files"} {
		SortBy = sortBy
		records := make([][]string, 0, len(jobs))
		for _, j := range jobs {
			records = append(records, csvFileRecord(j))
		}
		slices.SortFunc(records, compareCSVFileRecords(sortBy))

		keyed := slices.Clone(jobs)
		slices.SortStableFunc(keyed, func(a, b *FileJob) int {
			return bytes.Compare(csvFileKey(nil, a, 0), csvFileKey(nil, b, 0))
		})
		for i, j := range keyed {
			if !slices.Equal(csvFileRecord(j), records[i]) {
				t.Fatalf("sort %q: position %d want %v got %v", sortBy, i, records[i], csvFileRecord(j))
			}
		}
	}
}

func TestAppendKeyOrdering(t *testing.T) {
	strs := []string{"", "a", "a\x00", "a\x00b", "ab", "b", "\xff"}
	for _, a := range strs {
		for _, b := range strs {
			want := strings.Compare(a, b)
			got := bytes.Compare(appendKeyString(nil, a), appendKeyString(nil, b))
			if want != got {
				t.Errorf("string %q vs %q want %d got %d", a, b, want, got)
			}
		}
	}

	ints := []int64{-1 << 63, -5, -1, 0, 1, 5, 1<<63 - 1}
	for _, a := range ints {
		for _, b := range ints {
			want := 0
			if a < b {
				want = 1
			} else if a > b {
				want = -1
			}
			if got := bytes.Compare(appendKeyDesc(nil, a), appendKeyDesc(nil, b)); got != want {
				t.Errorf("desc %d vs %d want %d got %d", a, b, want, got)
			}
		}
	}
}

var boundedStatsRegex = regexp.MustCompile(`^bounded-memory: spills=(\d+) peak_in_memory_files=(\d+) `)

func runBounded(t *testing.T, jobs []*FileJob, maxInMemory int) (stdout, result string, spills, peak int) {
	t.Helper()
	BoundedMemory = true
	BoundedMemoryDir = t.TempDir()
	BoundedMemoryMaxInMemoryFiles = maxInMemory
	BoundedMemoryStats = true
	defer func() { BoundedMemory = false }()

	stderr := captureStderr(t, func() {
		stdout, result = captureStdout(t, func() string { return fileSummarize(cloneJobs(jobs)) })
	})

	var lines []string
	for l := range strings.SplitSeq(strings.TrimSpace(stderr), "\n") {
		if strings.HasPrefix(l, "bounded-memory:") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("expected exactly one stats line got %q", stderr)
	}
	m := boundedStatsRegex.FindStringSubmatch(lines[0])
	if m == nil {
		t.Fatalf("unexpected stats line %q", lines[0])
	}
	spills, _ = strconv.Atoi(m[1])
	peak, _ = strconv.Atoi(m[2])
	if peak > maxInMemory {
		t.Errorf("peak %d exceeds max %d", peak, maxInMemory)
	}

	if spills > 0 {
		entries, _ := os.ReadDir(BoundedMemoryDir)
		found := false
		for _, e := range entries {
			if info, err := e.Info(); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
				found = true
			}
		}
		if !found {
			t.Error("expected a non-empty spill file in the spill directory")
		}
	}
	return stdout, result, spills, peak
}

func TestBoundedMemoryMatchesUnbounded(t *testing.T) {
	defer resetBoundedGlobals()
	ProcessConstants()
	jobs := boundedTestJobs()

	for _, byFile := range []bool{false, true} {
		for _, sortBy := range []string{"files", "name", "lines", "code", "language"} {
			Files = byFile
			SortBy = sortBy
			SortBySet = true
			FormatMulti = "json:stdout,csv-stream:stdout,json2:stdout,csv:stdout,wide:" + filepath.Join(t.TempDir(), "wide.txt") + ",json:stdout"

			wantStdout, wantResult := captureStdout(t, func() string { return fileSummarize(cloneJobs(jobs)) })

			for _, maxInMemory := range []int{1, 3, 1000} {
				gotStdout, gotResult, spills, _ := runBounded(t, jobs, maxInMemory)
				if gotStdout != wantStdout {
					t.Errorf("byFile=%v sort=%s max=%d csv-stream differs\nwant %q\ngot  %q", byFile, sortBy, maxInMemory, wantStdout, gotStdout)
				}
				if gotResult != wantResult {
					i := 0
					for i < min(len(wantResult), len(gotResult)) && wantResult[i] == gotResult[i] {
						i++
					}
					t.Errorf("byFile=%v sort=%s max=%d output differs at byte %d\nwant %q\ngot  %q", byFile, sortBy, maxInMemory, i, wantResult[max(0, i-80):min(len(wantResult), i+80)], gotResult[max(0, i-80):min(len(gotResult), i+80)])
				}
				if maxInMemory < len(jobs) && spills == 0 {
					t.Errorf("max=%d expected spills", maxInMemory)
				}
				if maxInMemory >= len(jobs) && spills != 0 {
					t.Errorf("max=%d expected no spills got %d", maxInMemory, spills)
				}
			}
		}
	}
}

func TestBoundedMemoryTabularTotals(t *testing.T) {
	defer resetBoundedGlobals()
	ProcessConstants()
	jobs := boundedTestJobs()
	Files = true
	SortBy = "lines"
	FormatMulti = "tabular:stdout,wide:stdout,html-table:stdout"

	_, want := captureStdout(t, func() string { return fileSummarize(cloneJobs(jobs)) })
	_, got, _, _ := runBounded(t, jobs, 2)

	totals := func(s string) []string {
		var out []string
		for l := range strings.SplitSeq(s, "\n") {
			if strings.HasPrefix(l, "Total") || strings.HasPrefix(l, "Estimated") || strings.HasPrefix(l, "Processed") {
				out = append(out, l)
			}
		}
		return out
	}
	if !slices.Equal(totals(want), totals(got)) {
		t.Errorf("totals differ\nwant %v\ngot  %v", totals(want), totals(got))
	}
	if strings.Count(want, "\n") != strings.Count(got, "\n") {
		t.Errorf("expected the same number of output lines")
	}
}

func TestBoundedMemoryCSVStreamFileDestination(t *testing.T) {
	defer resetBoundedGlobals()
	ProcessConstants()
	jobs := boundedTestJobs()
	SortBy = "name"
	SortBySet = true

	FormatMulti = "csv-stream:stdout"
	want, _ := captureStdout(t, func() string { return fileSummarize(cloneJobs(jobs)) })

	dest := filepath.Join(t.TempDir(), "out.csv")
	FormatMulti = "csv-stream:" + dest
	stdout, result, _, _ := runBounded(t, jobs, 1)
	if stdout != "" || result != "" {
		t.Errorf("expected nothing on stdout got %q %q", stdout, result)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("file destination differs\nwant %q\ngot  %q", want, got)
	}
}

func TestValidateBoundedMemory(t *testing.T) {
	defer resetBoundedGlobals()
	cases := []struct {
		enabled bool
		dir     string
		max     int
		ok      bool
	}{
		{false, "", 0, true},
		{true, "", 5, false},
		{true, "dir", 0, false},
		{true, "dir", -1, false},
		{true, "dir", 1, true},
	}
	for _, c := range cases {
		BoundedMemory, BoundedMemoryDir, BoundedMemoryMaxInMemoryFiles = c.enabled, c.dir, c.max
		if err := validateBoundedMemory(); (err == nil) != c.ok {
			t.Errorf("%+v got %v", c, err)
		}
	}
}

func TestIsInBoundedMemoryDir(t *testing.T) {
	defer func() { boundedDirAbs = nil }()
	BoundedMemoryDir = filepath.Join(t.TempDir(), "spill", "nested")
	defer resetBoundedGlobals()
	if err := setupBoundedMemoryDir(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(BoundedMemoryDir); err != nil || !info.IsDir() {
		t.Fatal("expected spill directory to be created")
	}
	if !isInBoundedMemoryDir(filepath.Join(BoundedMemoryDir, "x.bin")) {
		t.Error("expected file inside spill dir to be excluded")
	}
	if isInBoundedMemoryDir(BoundedMemoryDir + "-other/x.go") {
		t.Error("expected sibling directory not to be excluded")
	}
}
