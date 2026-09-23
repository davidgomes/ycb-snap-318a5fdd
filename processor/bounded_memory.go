// SPDX-License-Identifier: MIT

package processor

import (
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

// bounded-memory spill bookkeeping for a single Process run.
var (
	boundedMemorySpills       int
	boundedMemoryPeak         int
	boundedMemoryStatsEmitted bool
	// boundedSummaryNoRetain is set while bounded format-multi is rendering
	// summaries so those formatters do not keep every *FileJob.
	boundedSummaryNoRetain bool
)

// spillRecord is the on-disk form of a per-file result. Content and other
// scratch buffers are omitted so spill files stay small.
type spillRecord struct {
	Language           string
	Filename           string
	Extension          string
	Location           string
	Symlocation        string
	Bytes              int64
	Lines              int64
	Code               int64
	Comment            int64
	Blank              int64
	Complexity         int64
	WeightedComplexity float64
	Binary             bool
	Minified           bool
	Generated          bool
	Uloc               int
	LineLength         []int
}

func (r spillRecord) job() *FileJob {
	return &FileJob{
		Language:           r.Language,
		Filename:           r.Filename,
		Extension:          r.Extension,
		Location:           r.Location,
		Symlocation:        r.Symlocation,
		Bytes:              r.Bytes,
		Lines:              r.Lines,
		Code:               r.Code,
		Comment:            r.Comment,
		Blank:              r.Blank,
		Complexity:         r.Complexity,
		WeightedComplexity: r.WeightedComplexity,
		Binary:             r.Binary,
		Minified:           r.Minified,
		Generated:          r.Generated,
		Uloc:               r.Uloc,
		LineLength:         r.LineLength,
	}
}

func spillRecordFromJob(job *FileJob) spillRecord {
	lineLength := append([]int(nil), job.LineLength...)
	return spillRecord{
		Language:           job.Language,
		Filename:           job.Filename,
		Extension:          job.Extension,
		Location:           job.Location,
		Symlocation:        job.Symlocation,
		Bytes:              job.Bytes,
		Lines:              job.Lines,
		Code:               job.Code,
		Comment:            job.Comment,
		Blank:              job.Blank,
		Complexity:         job.Complexity,
		WeightedComplexity: job.WeightedComplexity,
		Binary:             job.Binary,
		Minified:           job.Minified,
		Generated:          job.Generated,
		Uloc:               job.Uloc,
		LineLength:         lineLength,
	}
}

type spillStore struct {
	dir    string
	max    int
	mem    []spillRecord
	paths  []string
	spills int
	peak   int
	seq    int
}

func (s *spillStore) add(job *FileJob) {
	if len(s.mem) >= s.max {
		s.flush()
	}
	s.mem = append(s.mem, spillRecordFromJob(job))
	if len(s.mem) > s.peak {
		s.peak = len(s.mem)
	}
}

// flush writes the current in-memory batch to a new file in the spill
// directory and drops those records. The file is left in place.
func (s *spillStore) flush() {
	if len(s.mem) == 0 {
		return
	}
	s.seq++
	name := filepath.Join(s.dir, fmt.Sprintf("spill-%06d.json", s.seq))
	payload, err := json.Marshal(s.mem)
	if err != nil {
		fmt.Println("unable to encode bounded-memory spill: " + err.Error())
		os.Exit(1)
	}
	if err := os.WriteFile(name, payload, 0600); err != nil {
		fmt.Println("unable to write bounded-memory spill: " + err.Error())
		os.Exit(1)
	}
	s.paths = append(s.paths, name)
	s.spills++
	s.mem = nil
}

// prepareReplay spills a trailing partial batch when earlier batches were
// already spilled, so a replay never holds the tail together with a loaded batch.
func (s *spillStore) prepareReplay() {
	if len(s.paths) > 0 {
		s.flush()
	}
}

func (s *spillStore) replay(fn func(chan *FileJob)) {
	s.prepareReplay()
	ch := make(chan *FileJob)
	go func() {
		defer close(ch)
		if len(s.paths) == 0 {
			for i := range s.mem {
				ch <- s.mem[i].job()
			}
			return
		}
		for _, p := range s.paths {
			data, err := os.ReadFile(p)
			if err != nil {
				fmt.Println("unable to read bounded-memory spill: " + err.Error())
				os.Exit(1)
			}
			var recs []spillRecord
			if err := json.Unmarshal(data, &recs); err != nil {
				fmt.Println("unable to decode bounded-memory spill: " + err.Error())
				os.Exit(1)
			}
			for i := range recs {
				ch <- recs[i].job()
			}
		}
	}()
	fn(ch)
}

func configureBoundedMemory(dirPaths []string) error {
	boundedMemorySpills = 0
	boundedMemoryPeak = 0
	boundedMemoryStatsEmitted = false
	if !BoundedMemory {
		return nil
	}
	if BoundedMemoryDir == "" {
		return fmt.Errorf("--bounded-memory-dir is required when --bounded-memory is set")
	}
	if BoundedMemoryMaxInMemoryFiles <= 0 {
		return fmt.Errorf("--bounded-memory-max-in-memory-files must be > 0 when --bounded-memory is set")
	}
	if err := os.MkdirAll(BoundedMemoryDir, 0755); err != nil {
		return fmt.Errorf("unable to create bounded-memory dir %s: %s", BoundedMemoryDir, err.Error())
	}
	abs, err := filepath.Abs(BoundedMemoryDir)
	if err != nil {
		abs = BoundedMemoryDir
	}
	abs = filepath.Clean(abs)
	// Exclude the spill directory itself so files written there are not counted
	// when it sits inside a scanned tree.
	PathDenyList = append(PathDenyList, strings.TrimRight(abs, string(os.PathSeparator)))
	for _, d := range dirPaths {
		rel, relErr := filepath.Rel(d, abs)
		if relErr != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		PathDenyList = append(PathDenyList, filepath.ToSlash(rel))
	}
	return nil
}

func emitBoundedMemoryStats() {
	if !BoundedMemory || !BoundedMemoryStats || boundedMemoryStatsEmitted {
		return
	}
	boundedMemoryStatsEmitted = true
	fmt.Fprintf(os.Stderr, "bounded-memory: spills=%d peak_in_memory_files=%d\n", boundedMemorySpills, boundedMemoryPeak)
}

func fileSummarizeMultiBounded(input chan *FileJob) string {
	boundedSummaryNoRetain = true
	defer func() { boundedSummaryNoRetain = false }()

	store := &spillStore{
		dir: BoundedMemoryDir,
		max: BoundedMemoryMaxInMemoryFiles,
	}
	for res := range input {
		store.add(res)
	}
	boundedMemorySpills = store.spills
	boundedMemoryPeak = store.peak

	var str strings.Builder
	for s := range strings.SplitSeq(FormatMulti, ",") {
		t := strings.Split(s, ":")
		if len(t) != 2 {
			continue
		}
		formatName := strings.ToLower(t[0])
		if formatName == "csv-stream" {
			writeBoundedCSVStream(store, t[1])
			continue
		}

		var val string
		store.replay(func(ch chan *FileJob) {
			switch formatName {
			case "tabular":
				val = fileSummarizeShort(ch)
			case "wide":
				val = fileSummarizeLong(ch)
			case "json":
				val = toJSON(ch)
			case "json2":
				val = toJSON2(ch)
			case "cloc-yaml", "cloc-yml":
				val = toClocYAML(ch)
			case "csv":
				val = toCSV(ch)
			case "html":
				val = toHtml(ch)
			case "html-table":
				val = toHtmlTable(ch)
			case "sql":
				val = toSql(ch)
			case "sql-insert":
				val = toSqlInsert(ch)
			case "openmetrics":
				val = toOpenMetrics(ch)
			}
		})

		if t[1] == "stdout" {
			str.WriteString(val)
			str.WriteString("\n")
		} else {
			err := os.WriteFile(t[1], []byte(val), 0600)
			if err != nil {
				fmt.Printf("%s unable to be written to for format %s: %s", t[1], t[0], err)
			}
		}
	}

	// Spills may have been added during replay; publish the final counts.
	boundedMemorySpills = store.spills
	boundedMemoryPeak = store.peak
	return str.String()
}

var csvStreamQuoteRegex = regexp.MustCompile("\"")

func csvStreamRow(result *FileJob) []string {
	return []string{
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
	}
}

func sortCSVStreamRecords(records [][]string) {
	if SortBy == "" {
		return
	}
	base := getCSVFilesSortFunc(SortBy)
	slices.SortStableFunc(records, func(a, b []string) int {
		if cmp := base(a, b); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(a[2], b[2]); cmp != 0 {
			return cmp
		}
		return strings.Compare(a[1], b[1])
	})
}

func writeCSVStreamRecords(w io.Writer, records [][]string) {
	_, _ = fmt.Fprintln(w, "Language,Provider,Filename,Lines,Code,Comments,Blanks,Complexity,Bytes,Uloc")
	for _, rec := range records {
		location := "\"" + csvStreamQuoteRegex.ReplaceAllString(rec[1], "\"\"") + "\""
		filename := "\"" + csvStreamQuoteRegex.ReplaceAllString(rec[2], "\"\"") + "\""
		_, _ = fmt.Fprintf(w, "%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
			rec[0], location, filename, rec[3], rec[4], rec[5], rec[6], rec[7], rec[8], rec[9])
	}
}

// writeBoundedCSVStream emits csv-stream bytes. Rows follow SortBy when a sort
// column is set, which matches sorted csv file output and is stable across runs.
// Destinations other than stdout are written as files.
func writeBoundedCSVStream(store *spillStore, dest string) {
	var records [][]string
	store.replay(func(ch chan *FileJob) {
		for result := range ch {
			records = append(records, csvStreamRow(result))
		}
	})
	sortCSVStreamRecords(records)

	var buf strings.Builder
	writeCSVStreamRecords(&buf, records)
	if dest == "stdout" {
		fmt.Print(buf.String())
		return
	}
	if err := os.WriteFile(dest, []byte(buf.String()), 0600); err != nil {
		fmt.Printf("%s unable to be written to for format csv-stream: %s", dest, err)
	}
}
