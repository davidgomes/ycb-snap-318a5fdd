// SPDX-License-Identifier: MIT

package processor

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func restoreBoundedGlobals(t *testing.T) {
	t.Helper()
	prevMemory := BoundedMemory
	prevDir := BoundedMemoryDir
	prevMax := BoundedMemoryMaxInMemoryFiles
	prevStats := BoundedMemoryStats
	prevAbs := boundedMemoryAbsDir
	prevEmitted := boundedMemoryStatsEmitted
	prevFormat := FormatMulti
	prevSort := SortBy
	prevFiles := Files
	t.Cleanup(func() {
		BoundedMemory = prevMemory
		BoundedMemoryDir = prevDir
		BoundedMemoryMaxInMemoryFiles = prevMax
		BoundedMemoryStats = prevStats
		boundedMemoryAbsDir = prevAbs
		boundedMemoryStatsEmitted = prevEmitted
		FormatMulti = prevFormat
		SortBy = prevSort
		Files = prevFiles
	})
}

func testFileJobs() []*FileJob {
	return []*FileJob{
		{
			Language: "Go",
			Filename: "b.go",
			Location: "src/b.go",
			Lines:    2,
			Code:     2,
			Bytes:    20,
			Comment:  1,
			Blank:    0,
		},
		{
			Language: "Go",
			Filename: "a.go",
			Location: "src/a.go",
			Lines:    5,
			Code:     4,
			Bytes:    40,
			Comment:  0,
			Blank:    1,
			Uloc:     3,
		},
		{
			Language:          "Python",
			Filename:          "c.py",
			Location:          "src/c.py",
			Lines:             3,
			Code:              3,
			Bytes:             30,
			PossibleLanguages: []string{"Python"},
		},
	}
}

func jobsChan(jobs []*FileJob) chan *FileJob {
	ch := make(chan *FileJob, len(jobs))
	for _, job := range jobs {
		copied := *job
		if job.PossibleLanguages != nil {
			copied.PossibleLanguages = append([]string(nil), job.PossibleLanguages...)
		}
		ch <- &copied
	}
	close(ch)
	return ch
}

func TestBoundedMemorySpillsAndMatchesSummary(t *testing.T) {
	restoreBoundedGlobals(t)
	dir := t.TempDir()
	SortBy = "lines"
	Files = false

	formats := []string{"json:stdout", "json2:stdout", "csv:stdout", "tabular:stdout", "wide:stdout"}
	for _, format := range formats {
		FormatMulti = format
		BoundedMemory = false
		unbounded := fileSummarizeMulti(jobsChan(testFileJobs()))

		BoundedMemory = true
		BoundedMemoryDir = dir
		BoundedMemoryMaxInMemoryFiles = 1
		BoundedMemoryStats = false
		boundedMemoryStatsEmitted = false
		bounded := fileSummarizeMulti(jobsChan(testFileJobs()))
		if unbounded != bounded {
			t.Fatalf("format %s mismatched\nunbounded:\n%s\nbounded:\n%s", format, unbounded, bounded)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if !entry.IsDir() && info.Mode().IsRegular() && info.Size() > 0 && strings.HasPrefix(entry.Name(), "spill-") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a non-empty spill file to remain in the spill directory")
	}
}

func TestBoundedMemoryCSVStreamSorted(t *testing.T) {
	restoreBoundedGlobals(t)
	dir := t.TempDir()
	SortBy = "lines"
	Files = false
	FormatMulti = "csv-stream:stdout"

	unbounded := captureStdout(t, func() {
		BoundedMemory = false
		fileSummarizeMulti(jobsChan(testFileJobs()))
	})

	var boundedBuf bytes.Buffer
	BoundedMemory = true
	BoundedMemoryDir = filepath.Join(dir, "spill")
	BoundedMemoryMaxInMemoryFiles = 1
	store := collectTestStore(t, testFileJobs())
	store.writeCSVStream(&boundedBuf)
	if unbounded != boundedBuf.String() {
		t.Fatalf("csv-stream mismatch\nunbounded:\n%s\nbounded:\n%s", unbounded, boundedBuf.String())
	}
	if !strings.Contains(boundedBuf.String(), "a.go") || !strings.Contains(boundedBuf.String(), "b.go") {
		t.Fatalf("missing rows: %s", boundedBuf.String())
	}
	aIdx := strings.Index(boundedBuf.String(), "a.go")
	bIdx := strings.Index(boundedBuf.String(), "b.go")
	if aIdx < 0 || bIdx < 0 || aIdx > bIdx {
		t.Fatalf("expected higher line count first, output:\n%s", boundedBuf.String())
	}
	if store.spills == 0 {
		t.Fatal("expected spills for max=1")
	}
	if store.peak > 1 {
		t.Fatalf("peak_in_memory_files=%d exceeds max", store.peak)
	}
}

func TestBoundedMemoryStatsLine(t *testing.T) {
	restoreBoundedGlobals(t)
	dir := t.TempDir()
	FormatMulti = "json:stdout"
	SortBy = "name"
	BoundedMemory = true
	BoundedMemoryDir = dir
	BoundedMemoryMaxInMemoryFiles = 1
	BoundedMemoryStats = true
	boundedMemoryStatsEmitted = false

	stderr := captureStderr(t, func() {
		fileSummarizeMulti(jobsChan(testFileJobs()))
	})
	lines := 0
	var stats string
	for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
		if strings.HasPrefix(line, "bounded-memory:") {
			lines++
			stats = line
		}
	}
	if lines != 1 {
		t.Fatalf("expected exactly one stats line, stderr:\n%s", stderr)
	}
	if !strings.Contains(stats, "spills=") || !strings.Contains(stats, "peak_in_memory_files=") {
		t.Fatalf("stats line missing fields: %s", stats)
	}
	if !strings.Contains(stats, "spills=0") && !strings.Contains(stats, "spills=") {
		t.Fatalf("stats line: %s", stats)
	}
	if strings.Contains(stats, "spills=0") {
		t.Fatal("expected spills > 0")
	}
	if strings.Contains(stats, "peak_in_memory_files=0") {
		t.Fatal("expected peak > 0")
	}
}

func TestConfigureBoundedMemoryValidation(t *testing.T) {
	restoreBoundedGlobals(t)
	BoundedMemory = true
	BoundedMemoryDir = ""
	BoundedMemoryMaxInMemoryFiles = 1
	if err := configureBoundedMemory(); err == nil {
		t.Fatal("expected missing dir error")
	}
	BoundedMemoryDir = t.TempDir()
	BoundedMemoryMaxInMemoryFiles = 0
	if err := configureBoundedMemory(); err == nil {
		t.Fatal("expected invalid max error")
	}
	nested := filepath.Join(t.TempDir(), "nested", "spill")
	BoundedMemoryMaxInMemoryFiles = 2
	BoundedMemoryDir = nested
	if err := configureBoundedMemory(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(nested)
	if err != nil || !info.IsDir() {
		t.Fatalf("spill dir was not created: %v", err)
	}
}

func collectTestStore(t *testing.T, jobs []*FileJob) *boundedFileStore {
	t.Helper()
	if err := configureBoundedMemory(); err != nil {
		t.Fatal(err)
	}
	store := newBoundedFileStore(boundedMemoryAbsDir, BoundedMemoryMaxInMemoryFiles)
	for job := range jobsChan(jobs) {
		if err := store.add(job); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.finish(); err != nil {
		t.Fatal(err)
	}
	return store
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureFD(t, &os.Stdout, fn)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureFD(t, &os.Stderr, fn)
}

func captureFD(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := *target
	*target = w
	t.Cleanup(func() {
		*target = orig
	})
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	_ = w.Close()
	*target = orig
	return <-done
}
