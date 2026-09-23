// SPDX-License-Identifier: MIT

package processor

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

func TestSpillStoreRespectsMaxAndKeepsFiles(t *testing.T) {
	dir := t.TempDir()
	restore := snapshotBoundedGlobals(t)
	defer restore()

	BoundedMemoryDir = dir
	BoundedMemoryMaxInMemoryFiles = 1
	st, err := newSpillStore()
	if err != nil {
		t.Fatal(err)
	}

	const n = 5
	for i := 0; i < n; i++ {
		job := &FileJob{
			Language: "Go",
			Filename: fmt.Sprintf("f%d.go", i),
			Location: fmt.Sprintf("f%d.go", i),
			Lines:    int64(n - i),
			Code:     int64(i + 1),
		}
		if err := st.add(job); err != nil {
			t.Fatal(err)
		}
		if len(st.mem) > 1 {
			t.Fatalf("retained %d file records", len(st.mem))
		}
	}
	if st.spills != n-1 {
		t.Fatalf("spills=%d, want %d", st.spills, n-1)
	}
	if st.peak != 1 {
		t.Fatalf("peak_in_memory_files=%d, want 1", st.peak)
	}

	nonEmpty := countRegularNonEmpty(t, dir)
	if nonEmpty < 1 {
		t.Fatal("expected a non-empty spill file to remain on disk")
	}
}

func TestBoundedFormatMultiMatchesUnbounded(t *testing.T) {
	dir := t.TempDir()
	restore := snapshotBoundedGlobals(t)
	defer restore()

	jobs := []*FileJob{
		{Language: "Go", Filename: "b.go", Location: "src/b.go", Lines: 10, Code: 8, Comment: 1, Blank: 1, Complexity: 2, Bytes: 100, Uloc: 3, PossibleLanguages: []string{"Go"}},
		{Language: "Python", Filename: "a.py", Location: "src/a.py", Lines: 4, Code: 3, Comment: 1, Blank: 0, Complexity: 1, Bytes: 40, Uloc: 2},
		{Language: "Go", Filename: "a.go", Location: "src/a.go", Lines: 10, Code: 6, Comment: 2, Blank: 2, Complexity: 1, Bytes: 80, Uloc: 4, LineLength: []int{10, 20}},
	}
	digest, err := blake2b.New256(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		job.Hash = digest
	}

	cases := []struct {
		name   string
		format string
		files  bool
		sort   string
	}{
		{name: "json", format: "json:stdout", sort: "lines"},
		{name: "json2", format: "json2:stdout", sort: "name"},
		{name: "csv", format: "csv:stdout", sort: "code"},
		{name: "csv-stream", format: "csv-stream:stdout", sort: "lines"},
		{name: "tabular-wide", format: "tabular:stdout,wide:stdout", sort: "files"},
		{name: "json-with-files", format: "json:stdout", files: true, sort: "bytes"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			Files = tc.files
			SortBy = tc.sort
			FormatMulti = tc.format
			BoundedMemory = false
			boundedStatsEmitted = false

			unboundedOut, unboundedErr := captureStd(t, func() {
				_ = fileSummarize(feedJobs(jobs))
			})

			BoundedMemory = true
			BoundedMemoryDir = dir
			BoundedMemoryMaxInMemoryFiles = 1
			BoundedMemoryStats = true
			boundedStatsEmitted = false
			boundedOut, boundedErr := captureStd(t, func() {
				_ = fileSummarize(feedJobs(jobs))
			})

			if unboundedOut != boundedOut {
				t.Fatalf("stdout mismatch\nunbounded:\n%s\nbounded:\n%s", unboundedOut, boundedOut)
			}
			assertOneStatsLine(t, boundedErr, true)
			if strings.Contains(unboundedErr, "bounded-memory:") {
				t.Fatalf("unbounded stderr should not contain stats: %s", unboundedErr)
			}
		})
	}

	if countRegularNonEmpty(t, dir) < 1 {
		t.Fatal("spill files were removed before the process finished the calls")
	}
}

func TestBoundedCSVStreamFileMatchesStdout(t *testing.T) {
	dir := t.TempDir()
	restore := snapshotBoundedGlobals(t)
	defer restore()

	dest := filepath.Join(t.TempDir(), "out.csv")
	jobs := []*FileJob{
		{Language: "Go", Filename: "b.go", Location: "b.go", Lines: 3, Code: 3, Bytes: 10},
		{Language: "Go", Filename: "a.go", Location: "a.go", Lines: 9, Code: 7, Bytes: 20},
	}
	SortBy = "lines"
	FormatMulti = "csv-stream:" + dest

	BoundedMemory = false
	stdout, _ := captureStd(t, func() {
		_ = fileSummarize(feedJobs(jobs))
	})

	BoundedMemory = true
	BoundedMemoryDir = dir
	BoundedMemoryMaxInMemoryFiles = 1
	BoundedMemoryStats = false
	boundedStatsEmitted = false
	boundedStdout, _ := captureStd(t, func() {
		_ = fileSummarize(feedJobs(jobs))
	})
	if boundedStdout != "" {
		t.Fatalf("file destination should not also write stdout, got %q", boundedStdout)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != stdout {
		t.Fatalf("file bytes\n%s\nstdout bytes\n%s", got, stdout)
	}
	if !csvStreamSortedByLines(string(got)) {
		t.Fatalf("csv-stream not sorted by lines:\n%s", got)
	}
}

func TestBoundedMemoryDirExcluded(t *testing.T) {
	root := t.TempDir()
	spill := filepath.Join(root, "spill")
	if err := os.MkdirAll(spill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spill, "secret.go"), []byte("package secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	restore := snapshotBoundedGlobals(t)
	defer restore()
	BoundedMemory = true
	BoundedMemoryDir = spill
	BoundedMemoryMaxInMemoryFiles = 1
	if err := configureBoundedMemory(); err != nil {
		t.Fatal(err)
	}
	if !insideBoundedMemoryDir(filepath.Join(spill, "secret.go")) {
		t.Fatal("spill file was not excluded")
	}
	if insideBoundedMemoryDir(filepath.Join(root, "keep.go")) {
		t.Fatal("file outside the spill directory was excluded")
	}
}

func snapshotBoundedGlobals(t *testing.T) func() {
	t.Helper()
	prev := struct {
		boundedMemory bool
		dir           string
		abs           string
		max           int
		stats         bool
		emitted       bool
		formatMulti   string
		sortBy        string
		files         bool
		summaryQueue  int
	}{
		BoundedMemory,
		BoundedMemoryDir,
		boundedMemoryDirAbs,
		BoundedMemoryMaxInMemoryFiles,
		BoundedMemoryStats,
		boundedStatsEmitted,
		FormatMulti,
		SortBy,
		Files,
		FileSummaryJobQueueSize,
	}
	return func() {
		BoundedMemory = prev.boundedMemory
		BoundedMemoryDir = prev.dir
		boundedMemoryDirAbs = prev.abs
		BoundedMemoryMaxInMemoryFiles = prev.max
		BoundedMemoryStats = prev.stats
		boundedStatsEmitted = prev.emitted
		FormatMulti = prev.formatMulti
		SortBy = prev.sortBy
		Files = prev.files
		FileSummaryJobQueueSize = prev.summaryQueue
	}
}

func feedJobs(jobs []*FileJob) chan *FileJob {
	ch := make(chan *FileJob, len(jobs))
	for _, job := range jobs {
		cp := *job
		if job.PossibleLanguages != nil {
			cp.PossibleLanguages = append([]string(nil), job.PossibleLanguages...)
		}
		if job.LineLength != nil {
			cp.LineLength = append([]int(nil), job.LineLength...)
		}
		ch <- &cp
	}
	close(ch)
	return ch
}

func captureStd(t *testing.T, fn func()) (string, string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut := os.Stdout
	oldErr := os.Stderr
	os.Stdout = outW
	os.Stderr = errW
	fn()
	_ = outW.Close()
	_ = errW.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr
	var outBuf, errBuf bytes.Buffer
	if _, err := io.Copy(&outBuf, outR); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(&errBuf, errR); err != nil {
		t.Fatal(err)
	}
	return outBuf.String(), errBuf.String()
}

func assertOneStatsLine(t *testing.T, stderr string, expectSpill bool) {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "bounded-memory:") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("expected exactly one stats line, got %d: %q", len(lines), stderr)
	}
	if !strings.Contains(lines[0], "spills=") || !strings.Contains(lines[0], "peak_in_memory_files=") {
		t.Fatalf("stats line missing fields: %s", lines[0])
	}
	var spills, peak int
	if _, err := fmt.Sscanf(lines[0], "bounded-memory: spills=%d peak_in_memory_files=%d", &spills, &peak); err != nil {
		t.Fatal(err)
	}
	if peak > 1 {
		t.Fatalf("peak %d exceeds max 1", peak)
	}
	if expectSpill && spills <= 0 {
		t.Fatalf("expected spills>0, line %s", lines[0])
	}
}

func countRegularNonEmpty(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() && info.Size() > 0 && !entry.IsDir() {
			n++
		}
	}
	return n
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
		if _, err := fmt.Sscanf(fields[3], "%d", &n); err != nil {
			return false
		}
		if n > prev {
			return false
		}
		prev = n
	}
	return true
}
