// SPDX-License-Identifier: MIT

package processor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// BoundedMemory enables bounded-memory mode for --format-multi.
var BoundedMemory = false

// BoundedMemoryDir is the directory used to spill file records to disk.
var BoundedMemoryDir = ""

// BoundedMemoryMaxInMemoryFiles is the maximum number of file records kept in memory at once.
var BoundedMemoryMaxInMemoryFiles = 0

// BoundedMemoryStats enables bounded-memory statistics on stderr.
var BoundedMemoryStats = false

type spillRecord struct {
	Language   string `json:"language"`
	Filename   string `json:"filename"`
	Location   string `json:"location"`
	Lines      int64  `json:"lines"`
	Code       int64  `json:"code"`
	Comment    int64  `json:"comment"`
	Blank      int64  `json:"blank"`
	Complexity int64  `json:"complexity"`
	Bytes      int64  `json:"bytes"`
	Uloc       int    `json:"uloc"`
	LineLength []int  `json:"lineLength,omitempty"`
}

type replaySegment struct {
	spillPath string
	jobs      []*FileJob
}

type boundedMemoryCollector struct {
	maxInMemory  int
	spillDir     string
	inMemory     []*FileJob
	segments     []replaySegment
	spills       int
	peak         int
	spillCounter int
}

func setupBoundedMemory() {
	if !BoundedMemory {
		return
	}

	if BoundedMemoryDir == "" {
		fmt.Fprintln(os.Stderr, "--bounded-memory-dir is required when --bounded-memory is enabled")
		os.Exit(1)
	}

	if BoundedMemoryMaxInMemoryFiles <= 0 {
		fmt.Fprintln(os.Stderr, "--bounded-memory-max-in-memory-files must be > 0 when --bounded-memory is enabled")
		os.Exit(1)
	}

	if FormatMulti == "" {
		fmt.Fprintln(os.Stderr, "--bounded-memory requires --format-multi")
		os.Exit(1)
	}

	if err := os.MkdirAll(BoundedMemoryDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "unable to create bounded-memory spill directory: %s\n", err)
		os.Exit(1)
	}

	absSpill, err := filepath.Abs(BoundedMemoryDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to resolve bounded-memory spill directory: %s\n", err)
		os.Exit(1)
	}
	absSpill = strings.TrimRight(filepath.ToSlash(absSpill), "/")
	BoundedMemoryDir = absSpill

	for _, scanPath := range DirFilePaths {
		absScan, err := filepath.Abs(scanPath)
		if err != nil {
			continue
		}

		absScan = strings.TrimRight(filepath.ToSlash(absScan), "/")
		if pathIsInside(absScan, absSpill) {
			PathDenyList = append(PathDenyList, absSpill)
			break
		}
	}
}

func pathIsInside(base, target string) bool {
	if base == target {
		return true
	}

	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}

	rel = filepath.ToSlash(rel)
	return rel != ".." && !strings.HasPrefix(rel, "../")
}

func slimFileJob(job *FileJob) *FileJob {
	return &FileJob{
		Language:   job.Language,
		Filename:   job.Filename,
		Location:   job.Location,
		Lines:      job.Lines,
		Code:       job.Code,
		Comment:    job.Comment,
		Blank:      job.Blank,
		Complexity: job.Complexity,
		Bytes:      job.Bytes,
		Uloc:       job.Uloc,
		LineLength: job.LineLength,
	}
}

func fileJobToSpillRecord(job *FileJob) spillRecord {
	return spillRecord{
		Language:   job.Language,
		Filename:   job.Filename,
		Location:   job.Location,
		Lines:      job.Lines,
		Code:       job.Code,
		Comment:    job.Comment,
		Blank:      job.Blank,
		Complexity: job.Complexity,
		Bytes:      job.Bytes,
		Uloc:       job.Uloc,
		LineLength: job.LineLength,
	}
}

func spillRecordToFileJob(record spillRecord) *FileJob {
	return &FileJob{
		Language:   record.Language,
		Filename:   record.Filename,
		Location:   record.Location,
		Lines:      record.Lines,
		Code:       record.Code,
		Comment:    record.Comment,
		Blank:      record.Blank,
		Complexity: record.Complexity,
		Bytes:      record.Bytes,
		Uloc:       record.Uloc,
		LineLength: record.LineLength,
	}
}

func newBoundedMemoryCollector() *boundedMemoryCollector {
	return &boundedMemoryCollector{
		maxInMemory: BoundedMemoryMaxInMemoryFiles,
		spillDir:    BoundedMemoryDir,
	}
}

func (c *boundedMemoryCollector) collect(input chan *FileJob) {
	for job := range input {
		slim := slimFileJob(job)

		if len(c.inMemory) >= c.maxInMemory {
			c.flushToSpill()
		}

		c.inMemory = append(c.inMemory, slim)
		if len(c.inMemory) > c.peak {
			c.peak = len(c.inMemory)
		}
	}

	if len(c.inMemory) > 0 {
		c.segments = append(c.segments, replaySegment{jobs: c.inMemory})
		c.inMemory = nil
	}
}

func (c *boundedMemoryCollector) flushToSpill() {
	if len(c.inMemory) == 0 {
		return
	}

	c.spillCounter++
	spillPath := filepath.Join(c.spillDir, fmt.Sprintf("scc-spill-%d.jsonl", c.spillCounter))

	file, err := os.OpenFile(spillPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to write bounded-memory spill file: %s\n", err)
		os.Exit(1)
	}

	writer := bufio.NewWriter(file)
	for _, job := range c.inMemory {
		record := fileJobToSpillRecord(job)
		data, err := json.Marshal(record)
		if err != nil {
			_ = file.Close()
			fmt.Fprintf(os.Stderr, "unable to encode bounded-memory spill record: %s\n", err)
			os.Exit(1)
		}
		if _, err := writer.Write(data); err != nil {
			_ = file.Close()
			fmt.Fprintf(os.Stderr, "unable to write bounded-memory spill record: %s\n", err)
			os.Exit(1)
		}
		if err := writer.WriteByte('\n'); err != nil {
			_ = file.Close()
			fmt.Fprintf(os.Stderr, "unable to write bounded-memory spill record: %s\n", err)
			os.Exit(1)
		}
	}

	if err := writer.Flush(); err != nil {
		_ = file.Close()
		fmt.Fprintf(os.Stderr, "unable to flush bounded-memory spill file: %s\n", err)
		os.Exit(1)
	}

	if err := file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "unable to close bounded-memory spill file: %s\n", err)
		os.Exit(1)
	}

	c.segments = append(c.segments, replaySegment{spillPath: spillPath})
	c.inMemory = nil
	c.spills++
}

func (c *boundedMemoryCollector) replay() chan *FileJob {
	ch := make(chan *FileJob, c.maxInMemory)

	go func() {
		defer close(ch)

		for _, segment := range c.segments {
			if segment.spillPath != "" {
				if err := readSpillFile(segment.spillPath, ch); err != nil {
					fmt.Fprintf(os.Stderr, "unable to read bounded-memory spill file: %s\n", err)
					os.Exit(1)
				}
				continue
			}

			for _, job := range segment.jobs {
				ch <- job
			}
		}
	}()

	return ch
}

func readSpillFile(path string, ch chan *FileJob) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var record spillRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}

		ch <- spillRecordToFileJob(record)
	}

	return scanner.Err()
}

func emitBoundedMemoryStats(spills, peak int) {
	if !BoundedMemoryStats {
		return
	}

	fmt.Fprintf(os.Stderr, "bounded-memory: spills=%d peak_in_memory_files=%d\n", spills, peak)
}

func formatMultiOutput(replay func() chan *FileJob) string {
	var str strings.Builder

	for s := range strings.SplitSeq(FormatMulti, ",") {
		t := strings.Split(s, ":")
		if len(t) != 2 {
			continue
		}

		formatName := strings.ToLower(t[0])
		destination := t[1]
		i := replay()

		switch formatName {
		case "csv-stream":
			if destination == "stdout" {
				_ = toCSVStream(i)
			} else {
				if err := toCSVStreamFile(i, destination); err != nil {
					fmt.Printf("%s unable to be written to for format %s: %s", destination, formatName, err)
				}
			}
			continue
		}

		val := formatMultiValue(formatName, i)

		if destination == "stdout" {
			str.WriteString(val)
			str.WriteString("\n")
		} else {
			err := os.WriteFile(destination, []byte(val), 0600)
			if err != nil {
				fmt.Printf("%s unable to be written to for format %s: %s", destination, formatName, err)
			}
		}
	}

	return str.String()
}

func formatMultiValue(formatName string, input chan *FileJob) string {
	switch formatName {
	case "tabular":
		return fileSummarizeShort(input)
	case "wide":
		return fileSummarizeLong(input)
	case "json":
		return toJSON(input)
	case "json2":
		return toJSON2(input)
	case "cloc-yaml", "cloc-yml":
		return toClocYAML(input)
	case "csv":
		return toCSV(input)
	case "html":
		return toHtml(input)
	case "html-table":
		return toHtmlTable(input)
	case "sql":
		return toSql(input)
	case "sql-insert":
		return toSqlInsert(input)
	case "openmetrics":
		return toOpenMetrics(input)
	default:
		return ""
	}
}

func toCSVStreamFile(input chan *FileJob, path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	err = toCSVStreamWriter(file, input)
	closeErr := file.Close()
	if err != nil {
		return err
	}

	return closeErr
}

func toCSVStreamWriter(w io.Writer, input chan *FileJob) error {
	if SortBy != "" {
		return toCSVStreamSortedWriter(w, input)
	}

	if _, err := fmt.Fprintln(w, "Language,Provider,Filename,Lines,Code,Comments,Blanks,Complexity,Bytes,Uloc"); err != nil {
		return err
	}

	quoteRegex := regexp.MustCompile("\"")

	for result := range input {
		location := "\"" + quoteRegex.ReplaceAllString(result.Location, "\"\"") + "\""
		filename := "\"" + quoteRegex.ReplaceAllString(result.Filename, "\"\"") + "\""

		if _, err := fmt.Fprintf(w, "%s,%s,%s,%d,%d,%d,%d,%d,%d,%d\n",
			result.Language,
			location,
			filename,
			result.Lines,
			result.Code,
			result.Comment,
			result.Blank,
			result.Complexity,
			result.Bytes,
			result.Uloc,
		); err != nil {
			return err
		}
	}

	return nil
}

func toCSVStreamSortedWriter(w io.Writer, input chan *FileJob) error {
	records := make([][]string, 0)

	for result := range input {
		records = append(records, []string{
			result.Language,
			result.Location,
			result.Filename,
			strconv.FormatInt(result.Lines, 10),
			strconv.FormatInt(result.Code, 10),
			strconv.FormatInt(result.Comment, 10),
			strconv.FormatInt(result.Blank, 10),
			strconv.FormatInt(result.Complexity, 10),
			strconv.FormatInt(result.Bytes, 10),
			strconv.Itoa(result.Uloc),
		})
	}

	slices.SortFunc(records, getCSVFilesSortFunc(SortBy))

	if _, err := fmt.Fprintln(w, "Language,Provider,Filename,Lines,Code,Comments,Blanks,Complexity,Bytes,Uloc"); err != nil {
		return err
	}

	quoteRegex := regexp.MustCompile("\"")

	for _, record := range records {
		location := "\"" + quoteRegex.ReplaceAllString(record[1], "\"\"") + "\""
		filename := "\"" + quoteRegex.ReplaceAllString(record[2], "\"\"") + "\""

		if _, err := fmt.Fprintf(w, "%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
			record[0],
			location,
			filename,
			record[3],
			record[4],
			record[5],
			record[6],
			record[7],
			record[8],
			record[9],
		); err != nil {
			return err
		}
	}

	return nil
}
