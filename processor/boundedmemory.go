// SPDX-License-Identifier: MIT

package processor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// BoundedMemory enables spilling per-file results to disk for --format-multi
var BoundedMemory = false

// SortBySet is true when --sort was explicitly supplied
var SortBySet = false

// BoundedMemoryDir is the directory spill files are written to
var BoundedMemoryDir = ""

// BoundedMemoryMaxInMemoryFiles is the maximum number of file records retained in memory at once
var BoundedMemoryMaxInMemoryFiles = 0

// BoundedMemoryStats enables the bounded-memory stats line on stderr
var BoundedMemoryStats = false

var boundedMemoryDirAbs = ""

type spillRecord struct {
	Seq                int64
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

func toSpillRecord(seq int64, f *FileJob) spillRecord {
	return spillRecord{
		Seq: seq, Language: f.Language, Filename: f.Filename, Extension: f.Extension, Location: f.Location,
		Symlocation: f.Symlocation, Bytes: f.Bytes, Lines: f.Lines, Code: f.Code, Comment: f.Comment,
		Blank: f.Blank, Complexity: f.Complexity, WeightedComplexity: f.WeightedComplexity, Binary: f.Binary,
		Minified: f.Minified, Generated: f.Generated, Uloc: f.Uloc, LineLength: f.LineLength,
	}
}

func (r *spillRecord) toFileJob() *FileJob {
	return &FileJob{
		Language: r.Language, Filename: r.Filename, Extension: r.Extension, Location: r.Location,
		Symlocation: r.Symlocation, Bytes: r.Bytes, Lines: r.Lines, Code: r.Code, Comment: r.Comment,
		Blank: r.Blank, Complexity: r.Complexity, WeightedComplexity: r.WeightedComplexity, Binary: r.Binary,
		Minified: r.Minified, Generated: r.Generated, Uloc: r.Uloc, LineLength: r.LineLength,
	}
}

// setupBoundedMemory validates the bounded-memory options and creates the spill directory
func setupBoundedMemory() error {
	if !BoundedMemory {
		return nil
	}
	if BoundedMemoryDir == "" {
		return fmt.Errorf("--bounded-memory-dir is required when --bounded-memory is enabled")
	}
	if BoundedMemoryMaxInMemoryFiles <= 0 {
		return fmt.Errorf("--bounded-memory-max-in-memory-files must be > 0 when --bounded-memory is enabled")
	}
	if err := os.MkdirAll(BoundedMemoryDir, 0755); err != nil {
		return fmt.Errorf("unable to create bounded memory dir %s: %w", BoundedMemoryDir, err)
	}
	abs, err := filepath.Abs(BoundedMemoryDir)
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	boundedMemoryDirAbs = abs
	return nil
}

func isInBoundedMemoryDir(location string) bool {
	if boundedMemoryDirAbs == "" {
		return false
	}
	abs, err := filepath.Abs(location)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(resolved, filepath.Base(abs))
	}
	return abs == boundedMemoryDirAbs || strings.HasPrefix(abs, boundedMemoryDirAbs+string(os.PathSeparator))
}

type boundedStore struct {
	max      int
	buf      []spillRecord
	runs     []string
	count    int64
	spills   int
	peak     int
	inMemory int
}

var boundedStats struct {
	spills int
	peak   int
}

func (s *boundedStore) hold(n int) {
	s.inMemory += n
	if s.inMemory > s.peak {
		s.peak = s.inMemory
	}
}

func (s *boundedStore) writeRun(records []spillRecord) (string, error) {
	f, err := os.CreateTemp(BoundedMemoryDir, "scc-spill-*.jsonl")
	if err != nil {
		return "", err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for i := range records {
		if err := enc.Encode(&records[i]); err != nil {
			_ = f.Close()
			return "", err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return "", err
	}
	s.spills++
	return f.Name(), f.Close()
}

func (s *boundedStore) flush() error {
	if len(s.buf) == 0 {
		return nil
	}
	name, err := s.writeRun(s.buf)
	if err != nil {
		return err
	}
	s.runs = append(s.runs, name)
	s.inMemory -= len(s.buf)
	s.buf = s.buf[:0]
	return nil
}

func (s *boundedStore) add(f *FileJob) error {
	if len(s.buf) >= s.max {
		if err := s.flush(); err != nil {
			return err
		}
	}
	s.buf = append(s.buf, toSpillRecord(s.count, f))
	s.count++
	s.hold(1)
	return nil
}

// finish moves any remaining records to disk once anything has spilled so replays stream from disk only
func (s *boundedStore) finish() error {
	if len(s.runs) > 0 {
		return s.flush()
	}
	return nil
}

func readRun(name string, fn func(r *spillRecord) error) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(bufio.NewReader(f))
	for {
		var r spillRecord
		if err := dec.Decode(&r); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if err := fn(&r); err != nil {
			return err
		}
	}
}

// replay streams all records in their original order into a new channel
func (s *boundedStore) replay() chan *FileJob {
	ch := make(chan *FileJob)
	go func() {
		defer close(ch)
		if len(s.runs) == 0 {
			for i := range s.buf {
				ch <- s.buf[i].toFileJob()
			}
			return
		}
		for _, name := range s.runs {
			err := readRun(name, func(r *spillRecord) error {
				s.hold(1)
				ch <- r.toFileJob()
				s.hold(-1)
				return nil
			})
			if err != nil {
				printError(err.Error())
			}
		}
	}()
	return ch
}

func csvStreamSortRow(r *spillRecord) []string {
	return []string{
		r.Language, r.Location, r.Filename,
		strconv.FormatInt(r.Lines, 10), strconv.FormatInt(r.Code, 10), strconv.FormatInt(r.Comment, 10),
		strconv.FormatInt(r.Blank, 10), strconv.FormatInt(r.Complexity, 10), strconv.FormatInt(r.Bytes, 10),
		strconv.Itoa(r.Uloc),
	}
}

func csvStreamCompare(sortFunc func(a, b []string) int, a, b *spillRecord) int {
	if c := sortFunc(csvStreamSortRow(a), csvStreamSortRow(b)); c != 0 {
		return c
	}
	if a.Seq < b.Seq {
		return -1
	}
	if a.Seq > b.Seq {
		return 1
	}
	return 0
}

type runReader struct {
	f   *os.File
	dec *json.Decoder
}

func openRunReader(name string) (*runReader, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	return &runReader{f: f, dec: json.NewDecoder(bufio.NewReader(f))}, nil
}

func (r *runReader) next() (*spillRecord, error) {
	var rec spillRecord
	if err := r.dec.Decode(&rec); err == io.EOF {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &rec, nil
}

// sortedRuns builds a single sorted run file using chunked sorting and pairwise merges
func (s *boundedStore) sortedRun(sortFunc func(a, b []string) int) (string, error) {
	var runs []string
	var chunk []spillRecord
	writeChunk := func() error {
		if len(chunk) == 0 {
			return nil
		}
		slices.SortFunc(chunk, func(a, b spillRecord) int { return csvStreamCompare(sortFunc, &a, &b) })
		name, err := s.writeRun(chunk)
		if err != nil {
			return err
		}
		runs = append(runs, name)
		s.hold(-len(chunk))
		chunk = chunk[:0]
		return nil
	}
	for _, name := range s.runs {
		err := readRun(name, func(r *spillRecord) error {
			if len(chunk) >= s.max {
				if err := writeChunk(); err != nil {
					return err
				}
			}
			chunk = append(chunk, *r)
			s.hold(1)
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	if err := writeChunk(); err != nil {
		return "", err
	}

	for len(runs) > 1 {
		var next []string
		for i := 0; i < len(runs); i += 2 {
			if i+1 == len(runs) {
				next = append(next, runs[i])
				continue
			}
			var merged string
			var err error
			if s.max >= 2 {
				merged, err = s.mergeTwo(runs[i], runs[i+1], sortFunc)
			} else {
				merged, err = s.mergeTwoSingle(runs[i], runs[i+1], sortFunc)
			}
			if err != nil {
				return "", err
			}
			next = append(next, merged)
		}
		runs = next
	}
	if len(runs) == 0 {
		return "", nil
	}
	return runs[0], nil
}

func (s *boundedStore) newRunWriter() (*os.File, *bufio.Writer, *json.Encoder, error) {
	f, err := os.CreateTemp(BoundedMemoryDir, "scc-spill-*.jsonl")
	if err != nil {
		return nil, nil, nil, err
	}
	w := bufio.NewWriter(f)
	s.spills++
	return f, w, json.NewEncoder(w), nil
}

func closeRunWriter(f *os.File, w *bufio.Writer) error {
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// mergeTwo merges two sorted runs holding one head record from each
func (s *boundedStore) mergeTwo(a, b string, sortFunc func(a, b []string) int) (string, error) {
	ra, err := openRunReader(a)
	if err != nil {
		return "", err
	}
	defer func() { _ = ra.f.Close() }()
	rb, err := openRunReader(b)
	if err != nil {
		return "", err
	}
	defer func() { _ = rb.f.Close() }()
	f, w, enc, err := s.newRunWriter()
	if err != nil {
		return "", err
	}

	ha, err := ra.next()
	if err != nil {
		return "", err
	}
	hb, err := rb.next()
	if err != nil {
		return "", err
	}
	s.hold(2)
	for ha != nil || hb != nil {
		var take *spillRecord
		if hb == nil || (ha != nil && csvStreamCompare(sortFunc, ha, hb) <= 0) {
			take = ha
			ha, err = ra.next()
		} else {
			take = hb
			hb, err = rb.next()
		}
		if err != nil {
			return "", err
		}
		if err := enc.Encode(take); err != nil {
			return "", err
		}
	}
	s.hold(-2)
	return f.Name(), closeRunWriter(f, w)
}

// mergeTwoSingle merges two sorted runs while holding at most one record, keeping only the other
// run's head sort key and sequence number for comparison
func (s *boundedStore) mergeTwoSingle(a, b string, sortFunc func(a, b []string) int) (string, error) {
	ra, err := openRunReader(a)
	if err != nil {
		return "", err
	}
	defer func() { _ = ra.f.Close() }()
	rb, err := openRunReader(b)
	if err != nil {
		return "", err
	}
	defer func() { _ = rb.f.Close() }()
	f, w, enc, err := s.newRunWriter()
	if err != nil {
		return "", err
	}

	type key struct {
		row []string
		seq int64
		raw json.RawMessage
	}
	readKey := func(r *runReader) (*key, error) {
		var raw json.RawMessage
		if err := r.dec.Decode(&raw); err == io.EOF {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		var rec spillRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, err
		}
		return &key{row: csvStreamSortRow(&rec), seq: rec.Seq, raw: raw}, nil
	}
	less := func(x, y *key) bool {
		if c := sortFunc(x.row, y.row); c != 0 {
			return c < 0
		}
		return x.seq < y.seq
	}

	ka, err := readKey(ra)
	if err != nil {
		return "", err
	}
	kb, err := readKey(rb)
	if err != nil {
		return "", err
	}
	s.hold(1)
	for ka != nil || kb != nil {
		var take *key
		if kb == nil || (ka != nil && less(ka, kb)) {
			take = ka
			ka, err = readKey(ra)
		} else {
			take = kb
			kb, err = readKey(rb)
		}
		if err != nil {
			return "", err
		}
		if _, err := w.Write(append(take.raw, '\n')); err != nil {
			return "", err
		}
	}
	_ = enc
	s.hold(-1)
	return f.Name(), closeRunWriter(f, w)
}

func (s *boundedStore) writeCSVStream(out io.Writer) error {
	w := bufio.NewWriter(out)
	_, _ = w.WriteString(csvStreamHeader + "\n")
	emit := func(r *spillRecord) error {
		_, err := w.WriteString(csvStreamLine(r.toFileJob()))
		return err
	}

	switch {
	case len(s.runs) == 0:
		order := make([]int, len(s.buf))
		for i := range order {
			order[i] = i
		}
		if SortBySet {
			sortFunc := getCSVFilesSortFunc(SortBy)
			slices.SortFunc(order, func(a, b int) int { return csvStreamCompare(sortFunc, &s.buf[a], &s.buf[b]) })
		}
		for _, i := range order {
			if err := emit(&s.buf[i]); err != nil {
				return err
			}
		}
	case SortBySet:
		name, err := s.sortedRun(getCSVFilesSortFunc(SortBy))
		if err != nil {
			return err
		}
		if err := readRun(name, emit); err != nil {
			return err
		}
	default:
		for _, name := range s.runs {
			if err := readRun(name, emit); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}

// fileSummarizeMultiBounded mirrors fileSummarizeMulti while retaining at most
// BoundedMemoryMaxInMemoryFiles file records in memory, spilling the rest to disk
func fileSummarizeMultiBounded(input chan *FileJob) string {
	s := &boundedStore{max: BoundedMemoryMaxInMemoryFiles}
	var storeErr error
	for res := range input {
		if storeErr != nil {
			continue
		}
		storeErr = s.add(res)
	}
	if storeErr == nil {
		storeErr = s.finish()
	}
	if storeErr != nil {
		printError(storeErr.Error())
	}

	var str strings.Builder

	for spec := range strings.SplitSeq(FormatMulti, ",") {
		t := strings.Split(spec, ":")
		if len(t) != 2 {
			continue
		}

		var val string
		i := s.replay()

		switch strings.ToLower(t[0]) {
		case "tabular":
			val = fileSummarizeShort(i)
		case "wide":
			val = fileSummarizeLong(i)
		case "json":
			val = toJSON(i)
		case "json2":
			val = toJSON2(i)
		case "cloc-yaml", "cloc-yml":
			val = toClocYAML(i)
		case "csv":
			val = toCSV(i)
		case "csv-stream":
			for range i {
			}
			if err := writeCSVStreamTo(s, t[1]); err != nil {
				fmt.Printf("%s unable to be written to for format %s: %s", t[1], t[0], err)
			}
			continue
		case "html":
			val = toHtml(i)
		case "html-table":
			val = toHtmlTable(i)
		case "sql":
			val = toSql(i)
		case "sql-insert":
			val = toSqlInsert(i)
		case "openmetrics":
			val = toOpenMetrics(i)
		default:
			for range i {
			}
		}

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

	boundedStats.spills = s.spills
	boundedStats.peak = s.peak
	return str.String()
}

func writeCSVStreamTo(s *boundedStore, dest string) error {
	if dest == "stdout" {
		return s.writeCSVStream(os.Stdout)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err := s.writeCSVStream(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func printBoundedMemoryStats() {
	if BoundedMemory && BoundedMemoryStats {
		_, _ = fmt.Fprintf(os.Stderr, "bounded-memory: spills=%d peak_in_memory_files=%d\n", boundedStats.spills, boundedStats.peak)
	}
}

// sortedCSVStreamInput returns results in csv-stream sort order, preserving input order for ties
func sortedCSVStreamInput(results []*FileJob) chan *FileJob {
	sortFunc := getCSVFilesSortFunc(SortBy)
	type row struct {
		job *FileJob
		key []string
	}
	rows := make([]row, len(results))
	for idx, r := range results {
		rec := toSpillRecord(int64(idx), r)
		rows[idx] = row{job: r, key: csvStreamSortRow(&rec)}
	}
	slices.SortStableFunc(rows, func(a, b row) int { return sortFunc(a.key, b.key) })
	ch := make(chan *FileJob, len(rows))
	for _, r := range rows {
		ch <- r.job
	}
	close(ch)
	return ch
}
