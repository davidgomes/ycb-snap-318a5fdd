package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedMemoryFormatMultiMatchesUnbounded(t *testing.T) {
	tmpDir := t.TempDir()
	spillDir := filepath.Join(tmpDir, "spill")
	scanDir := filepath.Join(tmpDir, "src")

	if err := os.MkdirAll(scanDir, 0755); err != nil {
		t.Fatal(err)
	}

	files := []string{"a.go", "b.go", "c.go", "d.go"}
	for i, name := range files {
		content := fmt.Sprintf("package main\n\nfunc f%d() int { return %d }\n", i, i)
		if err := os.WriteFile(filepath.Join(scanDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	workerFlags := []string{
		"--file-process-job-workers", "1",
		"--directory-walker-job-workers", "1",
	}

	unboundedJSON := filepath.Join(tmpDir, "unbounded.json")
	unboundedCSV := filepath.Join(tmpDir, "unbounded.csv")
	unboundedStream := filepath.Join(tmpDir, "unbounded-stream.csv")
	boundedJSON := filepath.Join(tmpDir, "bounded.json")
	boundedCSV := filepath.Join(tmpDir, "bounded.csv")
	boundedStream := filepath.Join(tmpDir, "bounded-stream.csv")

	unboundedArgs := append([]string{
		"--format-multi",
		"json:" + unboundedJSON + ",csv:" + unboundedCSV + ",csv-stream:" + unboundedStream,
	}, append(workerFlags, scanDir)...)
	if _, err := runSCC(unboundedArgs...); err != nil {
		t.Fatal(err)
	}

	boundedArgs := append([]string{
		"--format-multi",
		"json:" + boundedJSON + ",csv:" + boundedCSV + ",csv-stream:" + boundedStream,
		"--bounded-memory",
		"--bounded-memory-dir", spillDir,
		"--bounded-memory-max-in-memory-files", "1",
		"--bounded-memory-stats",
	}, append(workerFlags, scanDir)...)
	boundedOutput, err := runSCC(boundedArgs...)
	if err != nil {
		t.Fatal(err)
	}

	assertSameFileContents(t, unboundedJSON, boundedJSON, "json")
	assertSameFileContents(t, unboundedCSV, boundedCSV, "csv")
	assertSameFileContents(t, unboundedStream, boundedStream, "csv-stream")

	if !strings.Contains(boundedOutput, "bounded-memory: spills=") {
		t.Fatalf("expected bounded-memory stats on stderr, got: %q", boundedOutput)
	}

	entries, err := os.ReadDir(spillDir)
	if err != nil {
		t.Fatal(err)
	}

	foundSpill := false
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if !entry.IsDir() && info.Size() > 0 {
			foundSpill = true
			break
		}
	}

	if !foundSpill {
		t.Fatal("expected non-empty spill file")
	}
}

func assertSameFileContents(t *testing.T, leftPath, rightPath, label string) {
	t.Helper()

	left, err := os.ReadFile(leftPath)
	if err != nil {
		t.Fatal(err)
	}

	right, err := os.ReadFile(rightPath)
	if err != nil {
		t.Fatal(err)
	}

	if string(left) != string(right) {
		t.Fatalf("%s output mismatch", label)
	}
}

func TestBoundedMemoryTabularTotalsMatch(t *testing.T) {
	tmpDir := t.TempDir()
	spillDir := filepath.Join(tmpDir, "spill")

	unboundedOut, err := runSCC("--format-multi", "tabular:stdout", "processor")
	if err != nil {
		t.Fatal(err)
	}

	boundedOut, err := runSCC(
		"--format-multi", "tabular:stdout",
		"--bounded-memory",
		"--bounded-memory-dir", spillDir,
		"--bounded-memory-max-in-memory-files", "1",
		"processor",
	)
	if err != nil {
		t.Fatal(err)
	}

	unboundedTotal := extractTabularTotalLine(unboundedOut)
	boundedTotal := extractTabularTotalLine(boundedOut)

	if unboundedTotal != boundedTotal {
		t.Fatalf("tabular totals mismatch:\nunbounded: %q\nbounded: %q", unboundedTotal, boundedTotal)
	}
}

func extractTabularTotalLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Total") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
