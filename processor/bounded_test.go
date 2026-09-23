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

func boundedTestJobs() []*FileJob {
	languages := []string{"Go", "Java", "C", "Python", "YAML"}
	jobs := make([]*FileJob, 0, 60)
	for i := range 60 {
		lang := languages[(i*7)%len(languages)]
		name := fmt.Sprintf("file%d.%s", i%9, strings.ToLower(lang))
		if i%11 == 0 {
			name = `we"ird,` + name
		}
		code := int64((i * 13) % 17)
		job := &FileJob{
			Language:          lang,
			PossibleLanguages: []string{lang},
			Filename:          name,
			Extension:         strings.ToLower(lang),
			Location:          filepath.Join("dir"+strconv.Itoa(i%4), name),
			Bytes:             int64(100 + i*3),
			Lines:             int64(i % 6),
			Code:              code,
			Comment:           int64(i % 3),
			Blank:             int64(i % 5),
			Complexity:        int64(i % 4),
			Uloc:              i % 7,
			LineLength:        []int{i % 10, (i * 3) % 80},
		}
		if i%5 == 0 {
			job.Hash, _ = blake2b.New256(nil)
		}
		if i%8 == 0 {
			job.PossibleLanguages = nil
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func captureOutput(t *testing.T, fn func() string) (string, string) {
	t.Helper()

	stdout, stderr := os.Stdout, os.Stderr
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout, os.Stderr = outW, errW

	outC := make(chan string)
	errC := make(chan string)
	go func() { b, _ := io.ReadAll(outR); outC <- string(b) }()
	go func() { b, _ := io.ReadAll(errR); errC <- string(b) }()

	result := fn()

	_ = outW.Close()
	_ = errW.Close()
	os.Stdout, os.Stderr = stdout, stderr

	return <-outC + result, <-errC
}

func summarizeJobs(jobs []*FileJob) string {
	input := make(chan *FileJob, len(jobs))
	for _, job := range jobs {
		clone := *job
		input <- &clone
	}
	close(input)
	return fileSummarize(input)
}

func resetBoundedGlobals() func() {
	files, sortBy, sortBySet, formatMulti, format := Files, SortBy, SortBySet, FormatMulti, Format
	percent, maxMean := Percent, MaxMean
	bounded, dir, maxFiles, stats := BoundedMemory, BoundedMemoryDir, BoundedMemoryMaxInMemoryFiles, BoundedMemoryStats
	return func() {
		Files, SortBy, SortBySet, FormatMulti, Format = files, sortBy, sortBySet, formatMulti, format
		Percent, MaxMean = percent, maxMean
		BoundedMemory, BoundedMemoryDir, BoundedMemoryMaxInMemoryFiles, BoundedMemoryStats = bounded, dir, maxFiles, stats
	}
}

var boundedStatsRegex = regexp.MustCompile(`(?m)^bounded-memory: .*spills=(\d+) peak_in_memory_files=(\d+)`)

func TestBoundedMemoryMatchesUnbounded(t *testing.T) {
	defer resetBoundedGlobals()()
	ProcessConstants()

	jobs := boundedTestJobs()

	for _, files := range []bool{false, true} {
		for _, sortBy := range []string{"", "lines", "name", "code", "language"} {
			for _, maxFiles := range []int{1, 2, 3, 7, 1000} {
				name := fmt.Sprintf("files=%v/sort=%s/max=%d", files, sortBy, maxFiles)
				t.Run(name, func(t *testing.T) {
					outDir := t.TempDir()
					Files = files
					SortBy = sortBy
					SortBySet = sortBy != ""
					if sortBy == "" {
						SortBy = "files"
					}
					Percent = true
					MaxMean = true

					// json after wide ensures the WeightedComplexity side effect of wide is preserved
					FormatMulti = "json:stdout,csv-stream:stdout,json2:stdout,wide:stdout,json:stdout,csv:stdout,tabular:stdout," +
						"json:" + filepath.Join(outDir, "u.json") + ",csv:" + filepath.Join(outDir, "u.csv")
					BoundedMemory = false
					unbounded, _ := captureOutput(t, func() string { return summarizeJobs(jobs) })
					unboundedJSON, _ := os.ReadFile(filepath.Join(outDir, "u.json"))
					unboundedCSV, _ := os.ReadFile(filepath.Join(outDir, "u.csv"))

					FormatMulti = "csv-stream:stdout"
					unboundedStream, _ := captureOutput(t, func() string { return summarizeJobs(jobs) })

					spillDir := filepath.Join(outDir, "spill", "nested")
					FormatMulti = "json:stdout,csv-stream:stdout,json2:stdout,wide:stdout,json:stdout,csv:stdout,tabular:stdout," +
						"json:" + filepath.Join(outDir, "b.json") + ",csv:" + filepath.Join(outDir, "b.csv") +
						",csv-stream:" + filepath.Join(outDir, "b-stream.csv")
					BoundedMemory = true
					BoundedMemoryDir = spillDir
					BoundedMemoryMaxInMemoryFiles = maxFiles
					BoundedMemoryStats = true
					bounded, stderr := captureOutput(t, func() string { return summarizeJobs(jobs) })
					boundedJSON, _ := os.ReadFile(filepath.Join(outDir, "b.json"))
					boundedCSV, _ := os.ReadFile(filepath.Join(outDir, "b.csv"))
					boundedStream, _ := os.ReadFile(filepath.Join(outDir, "b-stream.csv"))

					if files {
						// per file rows in tabular output may tie on the sort column so only compare the lines present
						u := strings.Split(unbounded, "\n")
						b := strings.Split(bounded, "\n")
						slices.Sort(u)
						slices.Sort(b)
						if !slices.Equal(u, b) {
							t.Errorf("bounded output lines differ from unbounded\nunbounded:\n%s\nbounded:\n%s", unbounded, bounded)
						}
						if !strings.Contains(bounded, string(unboundedJSON)) {
							t.Errorf("bounded stdout missing unbounded json")
						}
					} else if unbounded != bounded {
						t.Errorf("bounded output differs from unbounded\nunbounded:\n%s\nbounded:\n%s", unbounded, bounded)
					}

					if string(unboundedJSON) != string(boundedJSON) {
						t.Errorf("json file differs\nunbounded:\n%s\nbounded:\n%s", unboundedJSON, boundedJSON)
					}
					if string(unboundedCSV) != string(boundedCSV) {
						t.Errorf("csv file differs\nunbounded:\n%s\nbounded:\n%s", unboundedCSV, boundedCSV)
					}
					if unboundedStream != string(boundedStream) {
						t.Errorf("csv-stream file differs\nunbounded:\n%s\nbounded:\n%s", unboundedStream, boundedStream)
					}

					matches := boundedStatsRegex.FindAllStringSubmatch(stderr, -1)
					if len(matches) != 1 {
						t.Fatalf("expected exactly one stats line, got %q", stderr)
					}
					spills, _ := strconv.Atoi(matches[0][1])
					peak, _ := strconv.Atoi(matches[0][2])
					if peak > maxFiles {
						t.Errorf("peak %d exceeds max %d", peak, maxFiles)
					}
					if maxFiles < len(jobs) {
						if spills == 0 {
							t.Errorf("expected spills with max %d", maxFiles)
						}
						entries, err := os.ReadDir(spillDir)
						if err != nil || len(entries) == 0 {
							t.Fatalf("expected spill files in %s: %v", spillDir, err)
						}
						info, _ := entries[0].Info()
						if !info.Mode().IsRegular() || info.Size() == 0 {
							t.Errorf("expected non-empty regular spill file")
						}
					} else if spills != 0 {
						t.Errorf("expected no spills with max %d, got %d", maxFiles, spills)
					}
				})
			}
		}
	}
}

func TestBoundedMemoryCSVStreamSorted(t *testing.T) {
	defer resetBoundedGlobals()()
	ProcessConstants()

	Files = false
	SortBy = "lines"
	SortBySet = true
	FormatMulti = "csv-stream:stdout"
	BoundedMemory = true
	BoundedMemoryDir = t.TempDir()
	BoundedMemoryMaxInMemoryFiles = 2
	BoundedMemoryStats = false

	out, _ := captureOutput(t, func() string { return summarizeJobs(boundedTestJobs()) })

	lines := strings.Split(strings.TrimSpace(out), "\n")[1:]
	previous := int64(-1)
	for i, line := range lines {
		fields := strings.Split(line, ",")
		value, err := strconv.ParseInt(fields[len(fields)-7], 10, 64)
		if err != nil {
			t.Fatalf("unable to parse lines from %q", line)
		}
		if i > 0 && value > previous {
			t.Fatalf("csv-stream not sorted by lines descending: %d after %d", value, previous)
		}
		previous = value
	}
}

func TestBoundedMemorySingleFormat(t *testing.T) {
	defer resetBoundedGlobals()()
	ProcessConstants()

	jobs := boundedTestJobs()
	for _, format := range []string{"json", "json2", "csv", "tabular", "wide", "html", "openmetrics"} {
		for _, files := range []bool{false, true} {
			Files = files
			SortBy = "files"
			SortBySet = false
			FormatMulti = ""
			Format = format
			BoundedMemory = false
			unbounded, _ := captureOutput(t, func() string { return summarizeJobs(jobs) })

			BoundedMemory = true
			BoundedMemoryDir = t.TempDir()
			BoundedMemoryMaxInMemoryFiles = 4
			bounded, _ := captureOutput(t, func() string { return summarizeJobs(jobs) })

			u := strings.Split(unbounded, "\n")
			b := strings.Split(bounded, "\n")
			slices.Sort(u)
			slices.Sort(b)
			if !slices.Equal(u, b) {
				t.Errorf("format %s files=%v differs\nunbounded:\n%s\nbounded:\n%s", format, files, unbounded, bounded)
			}
		}
	}
}

func TestInBoundedMemoryDir(t *testing.T) {
	defer func() { boundedMemoryDirs = nil }()
	defer resetBoundedGlobals()()

	dir := filepath.Join(t.TempDir(), "spill")
	BoundedMemoryDir = dir
	BoundedMemoryMaxInMemoryFiles = 1
	if err := setupBoundedMemory(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected spill dir to be created: %v", err)
	}

	if !inBoundedMemoryDir(filepath.Join(dir, "scc-bounded-memory-1.jsonl")) {
		t.Error("expected file in spill dir to be excluded")
	}
	if inBoundedMemoryDir(dir + "-other/file.go") {
		t.Error("expected sibling dir to not be excluded")
	}
}

func TestValidateBoundedMemory(t *testing.T) {
	defer resetBoundedGlobals()()

	BoundedMemoryDir = ""
	BoundedMemoryMaxInMemoryFiles = 1
	if validateBoundedMemory() == nil {
		t.Error("expected error for missing dir")
	}

	BoundedMemoryDir = t.TempDir()
	BoundedMemoryMaxInMemoryFiles = 0
	if validateBoundedMemory() == nil {
		t.Error("expected error for non positive max")
	}

	BoundedMemoryMaxInMemoryFiles = 1
	if validateBoundedMemory() != nil {
		t.Error("expected valid configuration")
	}
}
