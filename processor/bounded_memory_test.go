// SPDX-License-Identifier: MIT

package processor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedMemoryMatchesUnboundedSummary(t *testing.T) {
	jobs := []*FileJob{
		{Language: "Go", Filename: "b.go", Location: "b.go", Lines: 3, Code: 2, Comment: 1, Blank: 0, Bytes: 20, Complexity: 1},
		{Language: "Go", Filename: "a.go", Location: "a.go", Lines: 10, Code: 8, Comment: 1, Blank: 1, Bytes: 40, Complexity: 2},
		{Language: "Markdown", Filename: "README.md", Location: "README.md", Lines: 2, Code: 2, Bytes: 8},
	}

	dir := t.TempDir()
	FormatMulti = "json:stdout,csv:stdout,csv-stream:stdout"
	SortBy = "lines"
	BoundedMemory = false
	unbounded := runSummarize(jobs)

	BoundedMemory = true
	BoundedMemoryDir = dir
	BoundedMemoryMaxInMemoryFiles = 1
	bounded := runSummarize(jobs)
	BoundedMemory = false

	if unbounded != bounded {
		t.Fatalf("bounded output differs\nunbounded:\n%s\nbounded:\n%s", unbounded, bounded)
	}
	if boundedMemorySpills == 0 {
		t.Fatal("expected spills when max in-memory files is 1")
	}
	if boundedMemoryPeak > 1 {
		t.Fatalf("peak_in_memory_files=%d", boundedMemoryPeak)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 0 && strings.HasPrefix(e.Name(), "spill-") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a non-empty spill file to remain")
	}
}

func TestBoundedCSVStreamFileDestination(t *testing.T) {
	jobs := []*FileJob{
		{Language: "Go", Filename: "b.go", Location: "b.go", Lines: 3, Code: 2, Bytes: 10},
		{Language: "Go", Filename: "a.go", Location: "a.go", Lines: 1, Code: 1, Bytes: 4},
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out.csv")
	FormatMulti = "csv-stream:" + out
	SortBy = "name"
	BoundedMemory = true
	BoundedMemoryDir = filepath.Join(dir, "spill")
	if err := os.MkdirAll(BoundedMemoryDir, 0755); err != nil {
		t.Fatal(err)
	}
	BoundedMemoryMaxInMemoryFiles = 1
	_ = runSummarize(jobs)
	BoundedMemory = false

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	a := strings.Index(text, "a.go")
	b := strings.Index(text, "b.go")
	if a < 0 || b < 0 || a > b {
		t.Fatalf("expected filename sort, got:\n%s", text)
	}
	if !strings.HasPrefix(text, "Language,Provider,Filename,Lines,Code,Comments,Blanks,Complexity,Bytes,Uloc\n") {
		t.Fatalf("header mismatch:\n%s", text)
	}
}

func runSummarize(jobs []*FileJob) string {
	ch := make(chan *FileJob, len(jobs))
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	return fileSummarize(ch)
}
