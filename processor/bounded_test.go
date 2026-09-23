// SPDX-License-Identifier: MIT

package processor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

func withGlobal[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

func captureOutput(t *testing.T, f **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	orig := *f
	*f = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()

	*f = orig
	_ = w.Close()
	return <-done
}

// boundedTestJobs returns fresh jobs covering ties, duplicate names, characters needing
// escaping in every format and the fields only some formatters output
func boundedTestJobs(n int) []*FileJob {
	languages := []string{"Go", "Python", "Java", "C Header", "JavaScript (min)", "Go (gen)", "Rust", "C#"}
	dirs := []string{"a", "b/c", `we"ird`, "comma,dir", "uni文中", `back\slash`, "<html>&"}

	jobs := make([]*FileJob, 0, n)
	for i := range n {
		language := languages[(i*7)%len(languages)]
		job := &FileJob{
			Language:    language,
			Filename:    fmt.Sprintf("file%d.ext", i%11),
			Extension:   "ext",
			Location:    fmt.Sprintf("%s/file%d.ext", dirs[i%len(dirs)], i),
			Symlocation: "",
			Bytes:       int64(100 + (i*37)%250),
			Lines:       int64(10 + (i*13)%9),
			Code:        int64((i * 5) % 7),
			Comment:     int64(i % 4),
			Blank:       int64((i * 3) % 5),
			Complexity:  int64((i * 11) % 6),
			Uloc:        i % 9,
			Minified:    strings.HasSuffix(language, "(min)"),
			Generated:   strings.HasSuffix(language, "(gen)"),
			Content:     []byte("content is dropped"),
			LineLength:  []int{i % 17, (i * 3) % 29, 4},
		}
		if i%3 != 0 {
			job.PossibleLanguages = []string{language}
		}
		if i%5 == 0 {
			job.PossibleLanguages = []string{}
		}
		if i%4 == 0 {
			job.Hash, _ = blake2b.New256(nil)
		}
		if i%10 == 0 {
			job.Symlocation = "/real/" + job.Location
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func jobChan(jobs []*FileJob) chan *FileJob {
	input := make(chan *FileJob, len(jobs))
	for _, job := range jobs {
		input <- job
	}
	close(input)
	return input
}

var timeDependent = regexp.MustCompile(`(?m)(elapsed_seconds|files_per_second|lines_per_second): .*$|insert into (locomo_)?metadata values\('[^']*', '[^']*', [0-9.]+`)

func maskTimes(s string) string {
	return timeDependent.ReplaceAllString(s, "TIME")
}

type boundedRun struct {
	result string
	stdout string
	stderr string
}

// summarize runs fileSummarize over fresh jobs, bounded when maxFiles is above zero
func summarize(t *testing.T, maxFiles int, n int) boundedRun {
	t.Helper()
	withGlobal(t, &BoundedMemory, maxFiles > 0)
	withGlobal(t, &BoundedMemoryDir, t.TempDir())
	withGlobal(t, &BoundedMemoryMaxInMemoryFiles, maxFiles)
	withGlobal(t, &BoundedMemoryStats, true)

	var run boundedRun
	run.stderr = captureOutput(t, &os.Stderr, func() {
		run.stdout = captureOutput(t, &os.Stdout, func() {
			run.result = fileSummarize(jobChan(boundedTestJobs(n)))
		})
	})
	return run
}

// sameLines reports whether two outputs have the same lines ignoring their order
func sameLines(a, b string) bool {
	la := strings.Split(a, "\n")
	lb := strings.Split(b, "\n")
	slices.Sort(la)
	slices.Sort(lb)
	return slices.Equal(la, lb)
}

// Files with equal sort values are listed in no particular order by the in memory
// summary formatters, so their by-file output only matches exactly for unique sorts
const (
	exactTestFormats   = "json:stdout,json2:stdout,csv:stdout,cloc-yaml:stdout,sql:stdout,sql-insert:stdout,openmetrics:stdout,unknown:stdout"
	summaryTestFormats = "tabular:stdout,wide:stdout,html:stdout,html-table:stdout"
)

func TestBoundedMatchesUnboundedFormatMulti(t *testing.T) {
	sorts := []string{"files", "name", "lang", "lines", "blanks", "code", "comments", "complexity", "comp", "bytes"}
	options := []struct {
		name string
		set  func(t *testing.T)
	}{
		{"default", func(t *testing.T) {}},
		{"by-file", func(t *testing.T) { withGlobal(t, &Files, true) }},
		{"by-file max-mean percent", func(t *testing.T) {
			withGlobal(t, &Files, true)
			withGlobal(t, &MaxMean, true)
			withGlobal(t, &Percent, true)
		}},
		{"percent uloc no-complexity", func(t *testing.T) {
			withGlobal(t, &Percent, true)
			withGlobal(t, &UlocMode, true)
			withGlobal(t, &Complexity, true)
		}},
		{"locomo no-cocomo", func(t *testing.T) {
			withGlobal(t, &Locomo, true)
			withGlobal(t, &Cocomo, true)
		}},
	}

	for _, option := range options {
		for _, sortBy := range sorts {
			t.Run(option.name+" sort "+sortBy, func(t *testing.T) {
				option.set(t)
				withGlobal(t, &SortBy, sortBy)

				for _, formats := range []string{exactTestFormats, summaryTestFormats} {
					withGlobal(t, &FormatMulti, formats)
					exact := formats == exactTestFormats || !Files || sortBy == "name" || sortBy == "lang"

					want := maskTimes(summarize(t, 0, 45).result)
					for _, maxFiles := range []int{1, 2, 3, 16, 17, 45, 1000} {
						got := maskTimes(summarize(t, maxFiles, 45).result)
						if exact && got != want || !exact && !sameLines(got, want) {
							t.Fatalf("max %d: bounded %s output differs\nwant:\n%s\ngot:\n%s", maxFiles, formats, want, got)
						}
					}
				}
			})
		}
	}
}

func TestBoundedMatchesUnboundedSingleFormat(t *testing.T) {
	formats := []string{"tabular", "wide", "json", "json2", "csv", "cloc-yaml", "cloc-yml", "html", "html-table", "sql", "sql-insert", "openmetrics", "not-a-format", "JSON"}
	for _, files := range []bool{false, true} {
		for _, format := range formats {
			t.Run(fmt.Sprintf("%s by-file %t", format, files), func(t *testing.T) {
				withGlobal(t, &Files, files)
				withGlobal(t, &Format, format)
				withGlobal(t, &SortBy, "name")

				want := summarize(t, 0, 30)
				got := summarize(t, 1, 30)
				if maskTimes(got.result) != maskTimes(want.result) {
					t.Fatalf("bounded output differs\nwant:\n%s\ngot:\n%s", want.result, got.result)
				}
			})
		}
	}

	t.Run("wide flag", func(t *testing.T) {
		withGlobal(t, &More, true)
		withGlobal(t, &Format, "json")
		want := summarize(t, 0, 30)
		got := summarize(t, 2, 30)
		if got.result != want.result || !strings.Contains(got.result, "Complexity/Lines") {
			t.Fatalf("bounded output differs\nwant:\n%s\ngot:\n%s", want.result, got.result)
		}
	})
}

// wide stores the weighted complexity on every job, which json outputs formatted after it include
func TestBoundedJSONAfterWideByFile(t *testing.T) {
	withGlobal(t, &Files, true)
	withGlobal(t, &SortBy, "name")
	withGlobal(t, &FormatMulti, "json:stdout,wide:stdout,json2:stdout,json:stdout")

	want := summarize(t, 0, 25)
	if !strings.Contains(want.result, `"WeightedComplexity":0,`) || !regexp.MustCompile(`"WeightedComplexity":[1-9]`).MatchString(want.result) {
		t.Fatal("expected json both without and with weighted complexity")
	}

	for _, maxFiles := range []int{1, 4, 100} {
		got := summarize(t, maxFiles, 25)
		if got.result != want.result {
			t.Fatalf("max %d: bounded output differs\nwant:\n%s\ngot:\n%s", maxFiles, want.result, got.result)
		}
	}
}

func TestBoundedCSVStreamMatchesUnbounded(t *testing.T) {
	for _, sortBy := range []string{"", "name", "lines", "lang", "code", "bytes", "complexity"} {
		t.Run("sort "+sortBy, func(t *testing.T) {
			withGlobal(t, &SortBy, sortBy)
			withGlobal(t, &SortBySet, sortBy != "")
			withGlobal(t, &FormatMulti, "csv-stream:stdout,json:stdout,csv-stream:stdout")

			want := summarize(t, 0, 40)
			for _, maxFiles := range []int{1, 3, 40} {
				got := summarize(t, maxFiles, 40)
				if got.stdout != want.stdout {
					t.Fatalf("max %d: csv-stream differs\nwant:\n%s\ngot:\n%s", maxFiles, want.stdout, got.stdout)
				}
				if got.result != want.result {
					t.Fatalf("max %d: combined output differs", maxFiles)
				}
			}

			if sortBy == "" {
				return
			}
			rows := strings.Split(strings.TrimSpace(want.stdout), "\n")
			rows = rows[1:41]
			sorted := slices.Clone(rows)
			slices.SortStableFunc(sorted, func(a, b string) int {
				ra := parseCSVStreamRow(t, a)
				rb := parseCSVStreamRow(t, b)
				return compareCSVFileJobs(ra, rb)
			})
			if !slices.Equal(rows, sorted) {
				t.Fatalf("csv-stream rows are not sorted by %s", sortBy)
			}
		})
	}
}

func parseCSVStreamRow(t *testing.T, row string) *FileJob {
	t.Helper()
	// Location and filename are quoted and may contain commas, the numbers never do
	fields := strings.Split(row, ",")
	numbers := fields[len(fields)-7:]
	values := make([]int64, 0, 7)
	for _, n := range numbers {
		v, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			t.Fatalf("bad row %q", row)
		}
		values = append(values, v)
	}
	quoted := strings.Join(fields[1:len(fields)-7], ",")
	split := strings.LastIndex(quoted, `","`)
	return &FileJob{
		Language:   fields[0],
		Location:   strings.ReplaceAll(quoted[1:split], `""`, `"`),
		Filename:   strings.ReplaceAll(quoted[split+3:len(quoted)-1], `""`, `"`),
		Lines:      values[0],
		Code:       values[1],
		Comment:    values[2],
		Blank:      values[3],
		Complexity: values[4],
		Bytes:      values[5],
	}
}

func TestBoundedCSVStreamFileDestination(t *testing.T) {
	withGlobal(t, &SortBy, "lines")
	withGlobal(t, &SortBySet, true)
	withGlobal(t, &FormatMulti, "csv-stream:stdout")
	want := summarize(t, 0, 30)

	out := filepath.Join(t.TempDir(), "out.csv")
	withGlobal(t, &FormatMulti, "json:stdout,csv-stream:"+out)
	got := summarize(t, 1, 30)

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want.stdout {
		t.Fatalf("csv-stream file differs\nwant:\n%s\ngot:\n%s", want.stdout, b)
	}
	if got.stdout != "" {
		t.Fatalf("csv-stream with a file destination wrote to stdout: %s", got.stdout)
	}
}

var statsLine = regexp.MustCompile(`^bounded-memory: spills=(\d+) peak_in_memory_files=(\d+) `)

func parseStats(t *testing.T, stderr string) (int, int) {
	t.Helper()
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSpace(stderr), "\n") {
		if strings.HasPrefix(line, "bounded-memory:") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("expected exactly one bounded-memory line, got %q", stderr)
	}

	m := statsLine.FindStringSubmatch(lines[0])
	if m == nil {
		t.Fatalf("unexpected stats line %q", lines[0])
	}
	spills, _ := strconv.Atoi(m[1])
	peak, _ := strconv.Atoi(m[2])
	return spills, peak
}

func TestBoundedSpillStats(t *testing.T) {
	withGlobal(t, &Files, true)
	withGlobal(t, &FormatMulti, "tabular:stdout,csv:stdout,json:stdout,csv-stream:stdout")
	withGlobal(t, &SortBySet, true)

	for _, tc := range []struct {
		maxFiles  int
		n         int
		wantSpill bool
		wantPeak  int
	}{
		{1, 50, true, 1},
		{7, 50, true, 7},
		{50, 50, false, 50},
		{100, 50, false, 50},
		{5, 0, false, 0},
	} {
		t.Run(fmt.Sprintf("max %d files %d", tc.maxFiles, tc.n), func(t *testing.T) {
			withGlobal(t, &BoundedMemory, true)
			dir := filepath.Join(t.TempDir(), "spill")
			withGlobal(t, &BoundedMemoryDir, dir)
			withGlobal(t, &BoundedMemoryMaxInMemoryFiles, tc.maxFiles)
			withGlobal(t, &BoundedMemoryStats, true)
			if err := validateBoundedMemory(); err != nil {
				t.Fatal(err)
			}

			stderr := captureOutput(t, &os.Stderr, func() {
				captureOutput(t, &os.Stdout, func() {
					fileSummarize(jobChan(boundedTestJobs(tc.n)))
				})
			})

			spills, peak := parseStats(t, stderr)
			if peak != tc.wantPeak {
				t.Errorf("peak_in_memory_files=%d, want %d", peak, tc.wantPeak)
			}
			if tc.wantSpill != (spills > 0) {
				t.Errorf("spills=%d, want spilling %t", spills, tc.wantSpill)
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			nonEmpty := 0
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().IsRegular() && info.Size() > 0 {
					nonEmpty++
				}
			}
			if tc.wantSpill && nonEmpty != 1 {
				t.Errorf("expected the spill file to remain, found %d non-empty files", nonEmpty)
			}
			if !tc.wantSpill && len(entries) != 0 {
				t.Errorf("expected no spill files, found %d", len(entries))
			}
		})
	}
}

// Enough runs that merging takes several passes
func TestBoundedExternalSortMultiplePasses(t *testing.T) {
	withGlobal(t, &Files, true)

	for _, tc := range []struct{ sortBy, formats string }{
		{"lines", "csv:stdout,json:stdout,json2:stdout"},
		{"name", "csv:stdout,tabular:stdout,html-table:stdout"},
	} {
		withGlobal(t, &SortBy, tc.sortBy)
		withGlobal(t, &FormatMulti, tc.formats)

		want := summarize(t, 0, 600)
		for _, maxFiles := range []int{1, 2, 37} {
			got := summarize(t, maxFiles, 600)
			if got.result != want.result {
				t.Fatalf("sort %s max %d: bounded output differs", tc.sortBy, maxFiles)
			}
			if _, peak := parseStats(t, got.stderr); peak > maxFiles {
				t.Fatalf("sort %s max %d: peak_in_memory_files=%d", tc.sortBy, maxFiles, peak)
			}
		}
	}
}

func TestBoundedRecordRoundTrip(t *testing.T) {
	for i, job := range boundedTestJobs(20) {
		rec := boundedRecord{seq: uint64(i * 1000), job: job}
		got, err := decodeBoundedRecord(appendBoundedRecord(nil, rec))
		if err != nil {
			t.Fatal(err)
		}

		want := *job
		want.Content, want.LineLength = nil, nil
		if want.Hash != nil {
			want.Hash = boundedHash()
		}
		if got.seq != rec.seq || fmt.Sprintf("%#v", *got.job) != fmt.Sprintf("%#v", want) {
			t.Fatalf("round trip differs\nwant: %#v\ngot:  %#v", want, *got.job)
		}
	}

	encoded := appendBoundedRecord(nil, boundedRecord{job: &FileJob{Language: "Go"}})
	if _, err := decodeBoundedRecord(encoded[:len(encoded)-1]); err == nil {
		t.Fatal("expected truncated record to fail")
	}
	if _, err := decodeBoundedRecord(append(encoded, 0)); err == nil {
		t.Fatal("expected trailing bytes to fail")
	}
}

func TestCSVSortKeyMatchesGetCSVFilesSortFunc(t *testing.T) {
	jobs := boundedTestJobs(60)
	for _, sortBy := range []string{"name", "names", "language", "langs", "line", "lines", "blank", "blanks", "code", "codes", "comment", "comments", "complexity", "complexitys", "comp", "byte", "bytes", "files", ""} {
		withGlobal(t, &SortBy, sortBy)

		byKey := slices.Clone(jobs)
		slices.SortFunc(byKey, compareCSVFileJobs)

		sortFunc := getCSVFilesSortFunc(sortBy)
		byRow := slices.Clone(jobs)
		slices.SortFunc(byRow, func(a, b *FileJob) int {
			if order := sortFunc(csvFileRecord(a), csvFileRecord(b)); order != 0 {
				return order
			}
			return strings.Compare(a.Location, b.Location)
		})

		if !slices.Equal(byKey, byRow) {
			t.Errorf("sort %q: csvSortKey order differs from getCSVFilesSortFunc", sortBy)
		}
	}
}

func TestSummarySortKeyMatchesSortSummaryFiles(t *testing.T) {
	jobs := boundedTestJobs(60)
	for _, sortBy := range []string{"name", "language", "lang", "lines", "blanks", "code", "comments", "complexity", "comp", "files", ""} {
		withGlobal(t, &SortBy, sortBy)

		byKey := slices.Clone(jobs)
		slices.SortFunc(byKey, func(a, b *FileJob) int {
			ka := newFileSortKey(0, summarySortKey, a, 0)
			kb := newFileSortKey(0, summarySortKey, b, 0)
			return compareFileSortKeys(&ka, &kb)
		})

		summary := LanguageSummary{Files: slices.Clone(byKey)}
		sortSummaryFiles(&summary)
		for i := range byKey {
			ka, sa := summarySortKey(sortBy, byKey[i])
			kb, sb := summarySortKey(sortBy, summary.Files[i])
			if ka != kb || sa != sb {
				t.Fatalf("sort %q: summarySortKey order differs from sortSummaryFiles at %d", sortBy, i)
			}
		}
	}
}

func TestValidateBoundedMemory(t *testing.T) {
	withGlobal(t, &BoundedMemory, false)
	withGlobal(t, &BoundedMemoryDir, "")
	withGlobal(t, &BoundedMemoryMaxInMemoryFiles, 0)
	if err := validateBoundedMemory(); err != nil {
		t.Fatalf("disabled mode should not validate: %v", err)
	}

	BoundedMemory = true
	if err := validateBoundedMemory(); err == nil {
		t.Fatal("expected missing dir to fail")
	}

	dir := filepath.Join(t.TempDir(), "nested", "spill")
	BoundedMemoryDir = dir
	for _, maxFiles := range []int{0, -1} {
		BoundedMemoryMaxInMemoryFiles = maxFiles
		if err := validateBoundedMemory(); err == nil {
			t.Fatalf("expected max %d to fail", maxFiles)
		}
	}

	BoundedMemoryMaxInMemoryFiles = 1
	if err := validateBoundedMemory(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("expected spill dir to be created: %v", err)
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	BoundedMemoryDir = file
	if err := validateBoundedMemory(); err == nil {
		t.Fatal("expected a file as the spill dir to fail")
	}
}

func TestBoundedMemorySpillPrefixes(t *testing.T) {
	root := t.TempDir()
	spill := filepath.Join(root, "sub", "spill")
	if err := os.MkdirAll(spill, 0755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	withGlobal(t, &BoundedMemory, true)
	withGlobal(t, &BoundedMemoryDir, spill)

	got := boundedMemorySpillPrefixes([]string{root, outside, spill, filepath.Join(root, "sub")})
	want := []string{
		filepath.Join(root, "sub", "spill") + string(filepath.Separator),
		filepath.Join(root, "sub", "spill") + string(filepath.Separator),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	t.Chdir(root)
	BoundedMemoryDir = filepath.Join("sub", "spill")
	got = boundedMemorySpillPrefixes([]string{"."})
	want = []string{filepath.Join("sub", "spill") + string(filepath.Separator)}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	BoundedMemory = false
	if got := boundedMemorySpillPrefixes([]string{"."}); got != nil {
		t.Fatalf("expected no prefixes when disabled, got %v", got)
	}
}
