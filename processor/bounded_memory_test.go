// SPDX-License-Identifier: MIT

package processor

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestBoundedMemoryValidation(t *testing.T) {
	t.Cleanup(func() {
		BoundedMemory = false
		BoundedMemoryDir = ""
		BoundedMemoryMaxInMemoryFiles = 0
		boundedMemoryAbsDir = ""
	})

	BoundedMemory = true
	BoundedMemoryDir = ""
	BoundedMemoryMaxInMemoryFiles = 1
	if err := validateBoundedMemory(); err == nil {
		t.Fatal("expected missing dir to fail")
	}

	BoundedMemoryDir = filepath.Join(t.TempDir(), "does", "not", "exist")
	BoundedMemoryMaxInMemoryFiles = 0
	if err := validateBoundedMemory(); err == nil {
		t.Fatal("expected non-positive max to fail")
	}

	BoundedMemoryMaxInMemoryFiles = 2
	if err := validateBoundedMemory(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(BoundedMemoryDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("spill directory was not created: %v", err)
	}
}

func TestBoundedMemoryMatchesUnbounded(t *testing.T) {
	jobs := []*FileJob{
		{Language: "Go", Filename: "b.go", Location: "proj/b.go", Lines: 10, Code: 8, Comment: 1, Blank: 1, Complexity: 2, Bytes: 100, Uloc: 3, LineLength: []int{10, 20}},
		{Language: "Go", Filename: "a.go", Location: "proj/a.go", Lines: 4, Code: 4, Comment: 0, Blank: 0, Complexity: 1, Bytes: 40, Uloc: 1, LineLength: []int{4}},
		{Language: "Python", Filename: "c.py", Location: "proj/c.py", Lines: 7, Code: 5, Comment: 2, Blank: 0, Complexity: 3, Bytes: 70, Uloc: 2, LineLength: []int{7, 8}},
		{Language: "Python", Filename: "d.py", Location: "proj/d.py", Lines: 3, Code: 1, Comment: 1, Blank: 1, Complexity: 0, Bytes: 20, Uloc: 1},
	}

	for _, sort := range []string{"files", "lines", "name", "code"} {
		for _, format := range []string{
			"json:stdout",
			"json2:stdout",
			"csv:stdout",
			"csv-stream:stdout",
			"tabular:stdout",
			"wide:stdout",
		} {
			unboundedOut, unboundedRet := runFormatMulti(t, format, jobs, false, "", 1, sort)
			dir := t.TempDir()
			boundedOut, boundedRet := runFormatMulti(t, format, jobs, true, dir, 1, sort)
			if unboundedRet != boundedRet {
				t.Errorf("sort=%s format=%s returned output mismatch\nunbounded:\n%s\nbounded:\n%s", sort, format, unboundedRet, boundedRet)
			}
			if unboundedOut != boundedOut {
				t.Errorf("sort=%s format=%s stdout mismatch\nunbounded:\n%s\nbounded:\n%s", sort, format, unboundedOut, boundedOut)
			}
		}
	}
}

func TestBoundedMemoryCSVStreamFileAndStats(t *testing.T) {
	jobs := []*FileJob{
		{Language: "Go", Filename: "b.go", Location: "b.go", Lines: 9, Code: 8, Comment: 0, Blank: 1, Complexity: 2, Bytes: 80},
		{Language: "Go", Filename: "a.go", Location: "a.go", Lines: 3, Code: 3, Comment: 0, Blank: 0, Complexity: 1, Bytes: 30},
		{Language: "Rust", Filename: "z.rs", Location: "z.rs", Lines: 12, Code: 10, Comment: 1, Blank: 1, Complexity: 4, Bytes: 120},
	}

	unboundedOut, _ := runFormatMulti(t, "csv-stream:stdout", jobs, false, "", 1, "lines")

	dir := t.TempDir()
	outPath := filepath.Join(t.TempDir(), "out.csv")
	stdout, _, stderr := runFormatMultiFull(t, "csv-stream:"+outPath, jobs, true, dir, 1, "lines", true)
	if stdout != "" {
		t.Fatalf("csv-stream file destination wrote stdout: %q", stdout)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != unboundedOut {
		t.Fatalf("file bytes mismatch\nwant:\n%s\ngot:\n%s", unboundedOut, body)
	}
	if !csvStreamSortedByLines(string(body)) {
		t.Fatalf("csv-stream rows not sorted by lines:\n%s", body)
	}

	matches := regexp.MustCompile(`(?m)^bounded-memory:.*\bspills=(\d+)\b.*\bpeak_in_memory_files=(\d+)\b`).FindAllStringSubmatch(stderr, -1)
	if len(matches) != 1 {
		t.Fatalf("expected exactly one stats line, stderr=%q", stderr)
	}
	if matches[0][1] == "0" {
		t.Fatalf("expected spills>0 for max=1, stderr=%q", stderr)
	}
	if matches[0][2] != "1" {
		t.Fatalf("peak_in_memory_files=%s, want 1", matches[0][2])
	}

	found := false
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() && info.Size() > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected a non-empty spill file to remain in the spill directory")
	}
}

func TestBoundedMemoryExcludesSpillDirectory(t *testing.T) {
	root := t.TempDir()
	spill := filepath.Join(root, "spill")
	if err := os.MkdirAll(spill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("package keep\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spill, "skip.go"), []byte("package skip\nfunc Skip() {}\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	restore := snapshotProcessorState()
	t.Cleanup(restore)

	BoundedMemory = true
	BoundedMemoryDir = spill
	BoundedMemoryMaxInMemoryFiles = 1
	BoundedMemoryStats = false
	FormatMulti = "tabular:stdout"
	Files = true
	SortBy = "files"
	DirFilePaths = []string{root}
	GitIgnore = true
	Ignore = true
	GitModuleIgnore = true
	SccIgnore = true
	FileOutput = ""

	stdout, stderr := captureProcessOutput(t, Process)
	if strings.Contains(stdout, "skip.go") || strings.Contains(stderr, "skip.go") {
		t.Fatalf("spill directory was counted\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "keep.go") {
		t.Fatalf("expected keep.go in output\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

func csvStreamSortedByLines(out string) bool {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		return false
	}
	prev := int64(1<<62 - 1)
	for _, line := range lines[1:] {
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			return false
		}
		var n int64
		for _, c := range fields[3] {
			if c < '0' || c > '9' {
				return false
			}
			n = n*10 + int64(c-'0')
		}
		if n > prev {
			return false
		}
		prev = n
	}
	return true
}

func runFormatMulti(t *testing.T, format string, jobs []*FileJob, bounded bool, dir string, max int, sort string) (string, string) {
	t.Helper()
	stdout, returned, _ := runFormatMultiFull(t, format, jobs, bounded, dir, max, sort, false)
	return stdout, returned
}

func runFormatMultiFull(t *testing.T, format string, jobs []*FileJob, bounded bool, dir string, max int, sort string, stats bool) (string, string, string) {
	t.Helper()
	restore := snapshotProcessorState()
	t.Cleanup(restore)

	FormatMulti = format
	BoundedMemory = bounded
	BoundedMemoryDir = dir
	BoundedMemoryMaxInMemoryFiles = max
	BoundedMemoryStats = stats
	SortBy = sort
	Files = false
	More = false
	Complexity = false
	Percent = false
	Cocomo = false
	Locomo = false
	UlocMode = false
	Dryness = false
	MaxMean = false
	Ci = true
	HBorder = false

	oldOut := os.Stdout
	oldErr := os.Stderr
	or, ow, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	er, ew, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = ow
	os.Stderr = ew

	returned := fileSummarize(cloneJobs(jobs))

	_ = ow.Close()
	_ = ew.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr
	out, err := io.ReadAll(or)
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := io.ReadAll(er)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), returned, string(errOut)
}

func captureProcessOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()
	oldOut := os.Stdout
	oldErr := os.Stderr
	or, ow, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	er, ew, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = ow
	os.Stderr = ew
	fn()
	_ = ow.Close()
	_ = ew.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr
	out, _ := io.ReadAll(or)
	errOut, _ := io.ReadAll(er)
	return string(out), string(errOut)
}

func cloneJobs(jobs []*FileJob) chan *FileJob {
	ch := make(chan *FileJob, len(jobs))
	for _, job := range jobs {
		cp := *job
		if job.LineLength != nil {
			cp.LineLength = append([]int(nil), job.LineLength...)
		}
		ch <- &cp
	}
	close(ch)
	return ch
}

func snapshotProcessorState() func() {
	prev := struct {
		bounded     bool
		dir         string
		max         int
		stats       bool
		formatMulti string
		sort        string
		files       bool
		more        bool
		complexity  bool
		percent     bool
		cocomo      bool
		locomo      bool
		uloc        bool
		dryness     bool
		maxMean     bool
		ci          bool
		hborder     bool
		gitIgnore   bool
		ignore      bool
		gitModule   bool
		sccIgnore   bool
		paths       []string
		fileOutput  string
		abs         string
		deny        []string
	}{
		BoundedMemory, BoundedMemoryDir, BoundedMemoryMaxInMemoryFiles, BoundedMemoryStats,
		FormatMulti, SortBy, Files, More, Complexity, Percent, Cocomo, Locomo, UlocMode, Dryness, MaxMean, Ci, HBorder,
		GitIgnore, Ignore, GitModuleIgnore, SccIgnore, append([]string(nil), DirFilePaths...), FileOutput, boundedMemoryAbsDir,
		append([]string(nil), PathDenyList...),
	}
	return func() {
		BoundedMemory = prev.bounded
		BoundedMemoryDir = prev.dir
		BoundedMemoryMaxInMemoryFiles = prev.max
		BoundedMemoryStats = prev.stats
		FormatMulti = prev.formatMulti
		SortBy = prev.sort
		Files = prev.files
		More = prev.more
		Complexity = prev.complexity
		Percent = prev.percent
		Cocomo = prev.cocomo
		Locomo = prev.locomo
		UlocMode = prev.uloc
		Dryness = prev.dryness
		MaxMean = prev.maxMean
		Ci = prev.ci
		HBorder = prev.hborder
		GitIgnore = prev.gitIgnore
		Ignore = prev.ignore
		GitModuleIgnore = prev.gitModule
		SccIgnore = prev.sccIgnore
		DirFilePaths = prev.paths
		FileOutput = prev.fileOutput
		boundedMemoryAbsDir = prev.abs
		PathDenyList = prev.deny
	}
}

func TestBoundedMemoryStatsLine(t *testing.T) {
	jobs := []*FileJob{
		{Language: "Go", Filename: "a.go", Location: "a.go", Lines: 1, Code: 1, Bytes: 1},
		{Language: "Go", Filename: "b.go", Location: "b.go", Lines: 2, Code: 2, Bytes: 2},
		{Language: "Go", Filename: "c.go", Location: "c.go", Lines: 3, Code: 3, Bytes: 3},
	}
	dir := t.TempDir()
	restore := snapshotProcessorState()
	t.Cleanup(restore)
	FormatMulti = "json:stdout"
	BoundedMemory = true
	BoundedMemoryDir = dir
	BoundedMemoryMaxInMemoryFiles = 1
	BoundedMemoryStats = true
	SortBy = "files"
	Files = false

	oldErr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	_ = fileSummarize(cloneJobs(jobs))
	_ = w.Close()
	os.Stderr = oldErr
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	lines := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "bounded-memory:") {
			lines++
		}
	}
	if lines != 1 {
		t.Fatalf("stats lines=%d stderr=%q", lines, buf.String())
	}
}
