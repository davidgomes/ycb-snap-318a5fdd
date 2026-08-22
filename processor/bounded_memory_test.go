// SPDX-License-Identifier: MIT

package processor

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedMemoryCollectorSpills(t *testing.T) {
	t.Parallel()

	spillDir := t.TempDir()
	BoundedMemoryDir = spillDir
	BoundedMemoryMaxInMemoryFiles = 1

	collector := newBoundedMemoryCollector()
	input := make(chan *FileJob, 3)
	input <- &FileJob{Language: "Go", Filename: "a.go", Location: "a.go", Lines: 1, Code: 1}
	input <- &FileJob{Language: "Go", Filename: "b.go", Location: "b.go", Lines: 2, Code: 2}
	input <- &FileJob{Language: "Go", Filename: "c.go", Location: "c.go", Lines: 3, Code: 3}
	close(input)

	collector.collect(input)

	if collector.spills != 2 {
		t.Fatalf("expected 2 spills, got %d", collector.spills)
	}

	if collector.peak != 1 {
		t.Fatalf("expected peak 1, got %d", collector.peak)
	}

	foundSpill := false
	entries, err := os.ReadDir(spillDir)
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

		if info.Size() > 0 && strings.HasPrefix(entry.Name(), "scc-spill-") {
			foundSpill = true
		}
	}

	if !foundSpill {
		t.Fatal("expected at least one non-empty spill file")
	}

	replayed := 0
	for range collector.replay() {
		replayed++
	}

	if replayed != 3 {
		t.Fatalf("expected 3 replayed jobs, got %d", replayed)
	}
}

func TestPathIsInside(t *testing.T) {
	t.Parallel()

	base := filepath.Clean("/tmp/project")
	if !pathIsInside(base, base) {
		t.Fatal("expected base path to be inside itself")
	}

	if !pathIsInside(base, filepath.Join(base, "spill")) {
		t.Fatal("expected spill subdirectory to be inside base")
	}

	if pathIsInside(base, "/tmp/other") {
		t.Fatal("did not expect unrelated path to be inside base")
	}
}

func TestSetupBoundedMemoryExcludesSpillDir(t *testing.T) {
	root := t.TempDir()
	spillDir := filepath.Join(root, "spill")
	DirFilePaths = []string{root}
	BoundedMemory = true
	BoundedMemoryDir = spillDir
	BoundedMemoryMaxInMemoryFiles = 1
	FormatMulti = "json:stdout"
	PathDenyList = []string{".git"}

	setupBoundedMemory()

	found := false
	for _, deny := range PathDenyList {
		if strings.Contains(deny, "spill") {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected spill directory in PathDenyList, got %v", PathDenyList)
	}

	BoundedMemory = false
	BoundedMemoryDir = ""
	BoundedMemoryMaxInMemoryFiles = 0
	FormatMulti = ""
	PathDenyList = []string{".git"}
	DirFilePaths = []string{}
}

func TestEmitBoundedMemoryStats(t *testing.T) {
	BoundedMemoryStats = true

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	original := os.Stderr
	os.Stderr = writer

	emitBoundedMemoryStats(3, 1)

	_ = writer.Close()
	os.Stderr = original

	stderrBytes, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}

	expected := "bounded-memory: spills=3 peak_in_memory_files=1"
	if strings.TrimSpace(string(stderrBytes)) != expected {
		t.Fatalf("unexpected stats line: %q", string(stderrBytes))
	}

	BoundedMemoryStats = false
}

func captureStderr(fn func()) (string, func(), error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", func() {}, err
	}

	original := os.Stderr
	os.Stderr = writer

	fn()

	_ = writer.Close()
	os.Stderr = original

	stderrBytes, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return "", func() {}, err
	}

	return string(stderrBytes), func() {}, nil
}
