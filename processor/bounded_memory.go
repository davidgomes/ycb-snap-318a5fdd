// SPDX-License-Identifier: MIT

package processor

import (
	"container/heap"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// BoundedMemory limits how many per-file results --format-multi retains at once.
var BoundedMemory = false

// BoundedMemoryDir is the spill directory used by bounded-memory mode.
var BoundedMemoryDir = ""

// BoundedMemoryMaxInMemoryFiles is the maximum number of file records held in memory.
var BoundedMemoryMaxInMemoryFiles = 0

// BoundedMemoryStats emits a single bounded-memory summary line on stderr.
var BoundedMemoryStats = false

// boundedMemoryAbsDir is the cleaned absolute spill directory while the mode is on.
var boundedMemoryAbsDir = ""

func validateBoundedMemory() error {
	boundedMemoryAbsDir = ""
	if !BoundedMemory {
		return nil
	}
	if strings.TrimSpace(BoundedMemoryDir) == "" {
		return errors.New("--bounded-memory-dir is required when --bounded-memory is enabled")
	}
	if BoundedMemoryMaxInMemoryFiles <= 0 {
		return errors.New("--bounded-memory-max-in-memory-files must be > 0 when --bounded-memory is enabled")
	}
	if err := os.MkdirAll(BoundedMemoryDir, 0o755); err != nil {
		return err
	}
	abs, err := filepath.Abs(BoundedMemoryDir)
	if err != nil {
		return err
	}
	boundedMemoryAbsDir = filepath.Clean(abs)
	return nil
}

func addBoundedMemoryExclusions(dirPaths []string) {
	if boundedMemoryAbsDir == "" {
		return
	}
	PathDenyList = append(PathDenyList, boundedMemoryAbsDir)
	for _, dir := range dirPaths {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(absDir), boundedMemoryAbsDir)
		if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		PathDenyList = append(PathDenyList, rel)
		PathDenyList = append(PathDenyList, filepath.Clean(filepath.Join(dir, rel)))
	}
}

func excludedByBoundedMemory(path string) bool {
	if boundedMemoryAbsDir == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	if abs == boundedMemoryAbsDir {
		return true
	}
	rel, err := filepath.Rel(boundedMemoryAbsDir, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// keepPerFileRecords reports whether formatters should retain every *FileJob.
// Bounded-memory mode drops them after aggregation unless per-file output is requested.
func keepPerFileRecords() bool {
	return Files || !BoundedMemory
}

type persistedFileJob struct {
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

func persistedFrom(job *FileJob) persistedFileJob {
	return persistedFileJob{
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

func (p persistedFileJob) toJob() *FileJob {
	return &FileJob{
		Language:   p.Language,
		Filename:   p.Filename,
		Location:   p.Location,
		Lines:      p.Lines,
		Code:       p.Code,
		Comment:    p.Comment,
		Blank:      p.Blank,
		Complexity: p.Complexity,
		Bytes:      p.Bytes,
		Uloc:       p.Uloc,
		LineLength: p.LineLength,
	}
}

func releaseFileJobPayload(job *FileJob) {
	job.Content = nil
	job.ContentByteType = nil
	job.ComplexityLine = nil
	job.Hash = nil
	job.Callback = nil
}

type boundedFileStore struct {
	dir    string
	max    int
	mem    []*FileJob
	paths  []string
	spills int
	peak   int
	seq    int
	runs   []string
}

func newBoundedFileStore(dir string, max int) *boundedFileStore {
	if strings.TrimSpace(dir) == "" {
		fmt.Fprintln(os.Stderr, "--bounded-memory-dir is required when --bounded-memory is enabled")
		os.Exit(1)
	}
	if max <= 0 {
		fmt.Fprintln(os.Stderr, "--bounded-memory-max-in-memory-files must be > 0 when --bounded-memory is enabled")
		os.Exit(1)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	return &boundedFileStore{dir: dir, max: max}
}

func (s *boundedFileStore) Add(job *FileJob) {
	releaseFileJobPayload(job)
	if len(s.mem) >= s.max {
		s.flush()
	}
	s.mem = append(s.mem, job)
	if len(s.mem) > s.peak {
		s.peak = len(s.mem)
	}
}

func (s *boundedFileStore) Finish() {
	if len(s.mem) > 0 {
		s.flush()
	}
}

func (s *boundedFileStore) flush() {
	if len(s.mem) == 0 {
		return
	}
	s.seq++
	path := filepath.Join(s.dir, fmt.Sprintf("scc-spill-%06d.json", s.seq))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	enc := json.NewEncoder(f)
	for _, job := range s.mem {
		if err := enc.Encode(persistedFrom(job)); err != nil {
			_ = f.Close()
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	s.paths = append(s.paths, path)
	s.spills++
	s.mem = nil
}

func (s *boundedFileStore) Replay() chan *FileJob {
	ch := make(chan *FileJob)
	go func() {
		defer close(ch)
		for _, path := range s.paths {
			f, err := os.Open(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			dec := json.NewDecoder(f)
			for {
				var rec persistedFileJob
				err := dec.Decode(&rec)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					_ = f.Close()
					fmt.Fprintln(os.Stderr, err.Error())
					os.Exit(1)
				}
				ch <- rec.toJob()
			}
			_ = f.Close()
		}
	}()
	return ch
}

func readPersistedJobs(path string) ([]*FileJob, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var jobs []*FileJob
	for {
		var rec persistedFileJob
		err := dec.Decode(&rec)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, rec.toJob())
	}
	return jobs, nil
}

func (s *boundedFileStore) sortedRuns() ([]string, error) {
	if s.runs != nil {
		return s.runs, nil
	}
	order := csvStreamOrder()
	runs := make([]string, 0, len(s.paths))
	for i, path := range s.paths {
		jobs, err := readPersistedJobs(path)
		if err != nil {
			return nil, err
		}
		rows := make([][]string, len(jobs))
		for j, job := range jobs {
			rows[j] = fileJobToCSVRow(job)
		}
		slices.SortFunc(rows, order)
		runPath := filepath.Join(s.dir, fmt.Sprintf("scc-csv-run-%06d.json", i+1))
		if err := writeCSVRows(runPath, rows); err != nil {
			return nil, err
		}
		runs = append(runs, runPath)
	}
	s.runs = runs
	return runs, nil
}

func writeCSVRows(path string, rows [][]string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			_ = f.Close()
			return err
		}
	}
	return f.Close()
}

type runReader struct {
	f   *os.File
	dec *json.Decoder
	row []string
}

func openRun(path string) (*runReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &runReader{f: f, dec: json.NewDecoder(f)}, nil
}

func (r *runReader) close() {
	if r != nil && r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
}

func (r *runReader) next() (bool, error) {
	var row []string
	err := r.dec.Decode(&row)
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	r.row = row
	return true, nil
}

type csvMergeItem struct {
	row []string
	idx int
}

type csvMergeHeap struct {
	items []csvMergeItem
	less  func(a, b []string) int
}

func (h csvMergeHeap) Len() int { return len(h.items) }
func (h csvMergeHeap) Less(i, j int) bool {
	return h.less(h.items[i].row, h.items[j].row) < 0
}
func (h csvMergeHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *csvMergeHeap) Push(x any) {
	h.items = append(h.items, x.(csvMergeItem))
}
func (h *csvMergeHeap) Pop() any {
	old := h.items
	n := len(old)
	item := old[n-1]
	h.items = old[:n-1]
	return item
}

func (s *boundedFileStore) writeSortedCSVStream(w io.Writer) error {
	runs, err := s.sortedRuns()
	if err != nil {
		return err
	}
	fmt.Fprintln(w, csvStreamHeader)
	if len(runs) == 0 {
		return nil
	}

	readers := make([]*runReader, len(runs))
	defer func() {
		for _, rr := range readers {
			if rr != nil {
				rr.close()
			}
		}
	}()

	h := &csvMergeHeap{less: csvStreamOrder()}
	for i, path := range runs {
		rr, err := openRun(path)
		if err != nil {
			return err
		}
		readers[i] = rr
		ok, err := rr.next()
		if err != nil {
			return err
		}
		if ok {
			heap.Push(h, csvMergeItem{row: rr.row, idx: i})
		}
	}

	for h.Len() > 0 {
		item := heap.Pop(h).(csvMergeItem)
		fmt.Fprintln(w, formatCSVStreamRow(item.row))
		ok, err := readers[item.idx].next()
		if err != nil {
			return err
		}
		if ok {
			heap.Push(h, csvMergeItem{row: readers[item.idx].row, idx: item.idx})
		}
	}
	return nil
}

func fileSummarizeMultiBounded(input chan *FileJob) string {
	store := newBoundedFileStore(BoundedMemoryDir, BoundedMemoryMaxInMemoryFiles)
	for res := range input {
		store.Add(res)
	}
	store.Finish()

	var str strings.Builder
	for part := range strings.SplitSeq(FormatMulti, ",") {
		t := strings.Split(part, ":")
		if len(t) != 2 {
			continue
		}
		formatName := strings.ToLower(t[0])
		dest := t[1]

		if formatName == "csv-stream" {
			w, file, err := csvStreamDestination(dest, t[0])
			if err != nil {
				continue
			}
			if SortBy != "" {
				if err := store.writeSortedCSVStream(w); err != nil {
					fmt.Fprintln(os.Stderr, err.Error())
					os.Exit(1)
				}
			} else {
				writeCSVStream(w, store.Replay())
			}
			if file != nil {
				_ = file.Close()
			}
			continue
		}

		val := renderBoundedFormat(formatName, store.Replay())
		if dest == "stdout" {
			str.WriteString(val)
			str.WriteString("\n")
		} else {
			err := os.WriteFile(dest, []byte(val), 0o600)
			if err != nil {
				fmt.Printf("%s unable to be written to for format %s: %s", dest, t[0], err)
			}
		}
	}

	if BoundedMemoryStats {
		fmt.Fprintf(os.Stderr, "bounded-memory: spills=%d peak_in_memory_files=%d\n", store.spills, store.peak)
	}
	return str.String()
}

func csvStreamDestination(dest, formatName string) (io.Writer, *os.File, error) {
	if dest == "stdout" {
		return os.Stdout, nil, nil
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Printf("%s unable to be written to for format %s: %s", dest, formatName, err)
		return nil, nil, err
	}
	return f, f, nil
}

func renderBoundedFormat(formatName string, input chan *FileJob) string {
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
