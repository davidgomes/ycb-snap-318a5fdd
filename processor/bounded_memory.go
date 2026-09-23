// SPDX-License-Identifier: MIT

package processor

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// boundedFileRecord is the per-file summary kept for --format-multi.
// File contents are omitted so a spill stays proportional to result rows.
type boundedFileRecord struct {
	Language           string
	PossibleLanguages  []string
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
	EndPoint           int
	Uloc               int
	LineLength         []int
	HasHash            bool
}

type spillStore struct {
	dir          string
	max          int
	mem          []boundedFileRecord
	paths        []string
	seq          int
	spills       int
	peak         int
	wideWeighted bool
}

func configureBoundedMemory() error {
	boundedMemoryDirAbs = ""
	if !BoundedMemory {
		return nil
	}
	if BoundedMemoryDir == "" {
		return fmt.Errorf("--bounded-memory requires --bounded-memory-dir")
	}
	if BoundedMemoryMaxInMemoryFiles <= 0 {
		return fmt.Errorf("--bounded-memory requires --bounded-memory-max-in-memory-files > 0")
	}
	if err := os.MkdirAll(BoundedMemoryDir, 0o755); err != nil {
		return fmt.Errorf("unable to create bounded-memory directory: %s", err)
	}
	abs, err := filepath.Abs(BoundedMemoryDir)
	if err != nil {
		return fmt.Errorf("unable to resolve bounded-memory directory: %s", err)
	}
	boundedMemoryDirAbs = filepath.Clean(abs)
	return nil
}

func addBoundedMemoryExcludes(dirPaths []string) {
	if boundedMemoryDirAbs == "" {
		return
	}
	PathDenyList = appendUniquePath(PathDenyList, boundedMemoryDirAbs)
	for _, root := range dirPaths {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		absRoot = filepath.Clean(absRoot)
		rel, err := filepath.Rel(absRoot, boundedMemoryDirAbs)
		if err != nil || rel == "." || rel == "" {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		// Use the path the walker will actually join. A bare directory name
		// would also skip unrelated directories with the same final element.
		PathDenyList = appendUniquePath(PathDenyList, filepath.Join(root, rel))
	}
}

func appendUniquePath(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

func insideBoundedMemoryDir(path string) bool {
	if boundedMemoryDirAbs == "" || path == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	abs = filepath.Clean(abs)
	if abs == boundedMemoryDirAbs {
		return true
	}
	rel, err := filepath.Rel(boundedMemoryDirAbs, abs)
	if err != nil || rel == "." {
		return err == nil && rel == "."
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func emitBoundedMemoryStats(spills, peak int) {
	if !BoundedMemoryStats || boundedStatsEmitted {
		return
	}
	boundedStatsEmitted = true
	fmt.Fprintf(os.Stderr, "bounded-memory: spills=%d peak_in_memory_files=%d\n", spills, peak)
}

func fileSummarizeMultiBounded(input chan *FileJob) string {
	st, err := newSpillStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	for job := range input {
		if err := st.add(job); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}

	out := renderFormatMulti(st.source, func() { st.wideWeighted = true })
	emitBoundedMemoryStats(st.spills, st.peak)
	return out
}

func newSpillStore() (*spillStore, error) {
	if BoundedMemoryDir == "" {
		return nil, fmt.Errorf("--bounded-memory requires --bounded-memory-dir")
	}
	if BoundedMemoryMaxInMemoryFiles <= 0 {
		return nil, fmt.Errorf("--bounded-memory requires --bounded-memory-max-in-memory-files > 0")
	}
	if err := os.MkdirAll(BoundedMemoryDir, 0o755); err != nil {
		return nil, fmt.Errorf("unable to create bounded-memory directory: %s", err)
	}
	return &spillStore{
		dir: BoundedMemoryDir,
		max: BoundedMemoryMaxInMemoryFiles,
	}, nil
}

func (st *spillStore) add(job *FileJob) error {
	if len(st.mem) >= st.max {
		if err := st.flushMem(); err != nil {
			return err
		}
	}
	st.mem = append(st.mem, recordFromJob(job))
	if len(st.mem) > st.peak {
		st.peak = len(st.mem)
	}
	if len(st.mem) > st.max {
		return fmt.Errorf("bounded-memory retained %d file records (max %d)", len(st.mem), st.max)
	}
	job.Content = nil
	job.ContentByteType = nil
	job.ComplexityLine = nil
	return nil
}

func (st *spillStore) flushMem() error {
	if len(st.mem) == 0 {
		return nil
	}
	path, err := st.writeRecords(st.mem)
	if err != nil {
		return err
	}
	st.paths = append(st.paths, path)
	st.spills++
	st.mem = nil
	return nil
}

// persistTail moves the in-memory tail onto disk without counting a spill.
// Replay and external sort then stream one record at a time.
func (st *spillStore) persistTail() error {
	if len(st.mem) == 0 {
		return nil
	}
	path, err := st.writeRecords(st.mem)
	if err != nil {
		return err
	}
	st.paths = append(st.paths, path)
	st.mem = nil
	return nil
}

func (st *spillStore) writeRecords(recs []boundedFileRecord) (string, error) {
	if len(recs) == 0 {
		return "", fmt.Errorf("refusing to write an empty bounded-memory file")
	}
	st.seq++
	path := filepath.Join(st.dir, fmt.Sprintf("bm-%06d.json", st.seq))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	enc := json.NewEncoder(f)
	for i := range recs {
		if err := enc.Encode(&recs[i]); err != nil {
			_ = f.Close()
			return "", err
		}
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func (st *spillStore) source(sorted bool) chan *FileJob {
	ch := make(chan *FileJob, 1)
	go func() {
		defer close(ch)
		var err error
		emit := func(rec *boundedFileRecord) {
			ch <- rec.toJob(st.wideWeighted)
		}
		if sorted {
			err = st.forEachSorted(emit)
		} else {
			err = st.forEachOriginal(emit)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "bounded memory replay failed: %s\n", err)
		}
	}()
	return ch
}

func (st *spillStore) forEachOriginal(fn func(*boundedFileRecord)) error {
	// When earlier batches were spilled, move the tail to disk too so the
	// in-memory set is not held alongside the record being replayed.
	if len(st.paths) > 0 {
		if err := st.persistTail(); err != nil {
			return err
		}
	}
	for _, path := range st.paths {
		if err := streamRecords(path, fn); err != nil {
			return err
		}
	}
	for i := range st.mem {
		fn(&st.mem[i])
	}
	return nil
}

func (st *spillStore) forEachSorted(fn func(*boundedFileRecord)) error {
	if len(st.paths) == 0 && len(st.mem) <= 1 {
		for i := range st.mem {
			fn(&st.mem[i])
		}
		return nil
	}
	if err := st.persistTail(); err != nil {
		return err
	}
	runs := make([]string, 0, len(st.paths))
	for _, path := range st.paths {
		sorted, err := st.sortedCopy(path)
		if err != nil {
			return err
		}
		runs = append(runs, sorted)
	}
	merged, err := st.mergeRuns(runs)
	if err != nil {
		return err
	}
	if merged == "" {
		return nil
	}
	return streamRecords(merged, fn)
}

func (st *spillStore) sortedCopy(path string) (string, error) {
	recs, err := readAllRecords(path)
	if err != nil {
		return "", err
	}
	sortBoundedRecords(recs)
	return st.writeRecords(recs)
}

func (st *spillStore) mergeRuns(paths []string) (string, error) {
	fan := st.max
	if fan < 2 {
		fan = 2
	}
	current := paths
	for len(current) > 1 {
		next := make([]string, 0, (len(current)+fan-1)/fan)
		for i := 0; i < len(current); i += fan {
			end := min(i+fan, len(current))
			merged, err := st.mergeGroup(current[i:end])
			if err != nil {
				return "", err
			}
			next = append(next, merged)
		}
		current = next
	}
	if len(current) == 0 {
		return "", nil
	}
	return current[0], nil
}

func (st *spillStore) mergeGroup(paths []string) (string, error) {
	if len(paths) == 1 {
		return paths[0], nil
	}
	readers := make([]*recReader, 0, len(paths))
	for _, path := range paths {
		reader, err := openRecReader(path)
		if err != nil {
			for _, open := range readers {
				_ = open.Close()
			}
			return "", err
		}
		readers = append(readers, reader)
	}
	defer func() {
		for _, reader := range readers {
			_ = reader.Close()
		}
	}()

	st.seq++
	outPath := filepath.Join(st.dir, fmt.Sprintf("bm-%06d.json", st.seq))
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	enc := json.NewEncoder(out)
	wrote := false
	for {
		best := -1
		for i, reader := range readers {
			if !reader.ok {
				continue
			}
			if best == -1 || compareCSVStreamRecords(&reader.cur, &readers[best].cur) < 0 {
				best = i
			}
		}
		if best == -1 {
			break
		}
		if err := enc.Encode(&readers[best].cur); err != nil {
			_ = out.Close()
			return "", err
		}
		wrote = true
		if err := readers[best].advance(); err != nil {
			_ = out.Close()
			return "", err
		}
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if !wrote {
		return "", fmt.Errorf("bounded-memory merge produced an empty file")
	}
	return outPath, nil
}

type recReader struct {
	f   *os.File
	dec *json.Decoder
	cur boundedFileRecord
	ok  bool
}

func openRecReader(path string) (*recReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	reader := &recReader{f: f, dec: json.NewDecoder(f)}
	if err := reader.advance(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return reader, nil
}

func (r *recReader) advance() error {
	err := r.dec.Decode(&r.cur)
	if err == io.EOF {
		r.ok = false
		return nil
	}
	if err != nil {
		r.ok = false
		return err
	}
	r.ok = true
	return nil
}

func (r *recReader) Close() error {
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

func streamRecords(path string, fn func(*boundedFileRecord)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var rec boundedFileRecord
		err := dec.Decode(&rec)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		fn(&rec)
	}
}

func readAllRecords(path string) ([]boundedFileRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var recs []boundedFileRecord
	dec := json.NewDecoder(f)
	for {
		var rec boundedFileRecord
		err := dec.Decode(&rec)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

func sortBoundedRecords(recs []boundedFileRecord) {
	slices.SortStableFunc(recs, func(a, b boundedFileRecord) int {
		return compareCSVStreamRecords(&a, &b)
	})
}

func recordFromJob(job *FileJob) boundedFileRecord {
	return boundedFileRecord{
		Language:           job.Language,
		PossibleLanguages:  copyStrings(job.PossibleLanguages),
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
		EndPoint:           job.EndPoint,
		Uloc:               job.Uloc,
		LineLength:         copyInts(job.LineLength),
		HasHash:            job.Hash != nil,
	}
}

func (r *boundedFileRecord) toJob(applyWide bool) *FileJob {
	job := &FileJob{
		Language:           r.Language,
		PossibleLanguages:  copyStrings(r.PossibleLanguages),
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
		EndPoint:           r.EndPoint,
		Uloc:               r.Uloc,
		LineLength:         copyInts(r.LineLength),
	}
	// Duplicate detection stores an unexported digest that JSON renders as {}.
	if r.HasHash {
		job.Hash, _ = blake2b.New256(nil)
	}
	if applyWide {
		if job.Code != 0 {
			job.WeightedComplexity = (float64(job.Complexity) / float64(job.Code)) * 100
		} else {
			job.WeightedComplexity = 0
		}
	}
	return job
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func copyInts(in []int) []int {
	if in == nil {
		return nil
	}
	out := make([]int, len(in))
	copy(out, in)
	return out
}

func compareCSVStreamJobs(a, b *FileJob) int {
	return compareCSVStream(
		a.Language, a.Location, a.Filename, a.Lines, a.Code, a.Comment, a.Blank, a.Complexity, a.Bytes,
		b.Language, b.Location, b.Filename, b.Lines, b.Code, b.Comment, b.Blank, b.Complexity, b.Bytes,
	)
}

func compareCSVStreamRecords(a, b *boundedFileRecord) int {
	return compareCSVStream(
		a.Language, a.Location, a.Filename, a.Lines, a.Code, a.Comment, a.Blank, a.Complexity, a.Bytes,
		b.Language, b.Location, b.Filename, b.Lines, b.Code, b.Comment, b.Blank, b.Complexity, b.Bytes,
	)
}

func compareCSVStream(aLang, aLoc, aName string, aLines, aCode, aComment, aBlank, aComplexity, aBytes int64, bLang, bLoc, bName string, bLines, bCode, bComment, bBlank, bComplexity, bBytes int64) int {
	var order int
	switch SortBy {
	case "name", "names":
		order = strings.Compare(aName, bName)
	case "language", "languages", "lang", "langs":
		order = strings.Compare(aLang, bLang)
	case "line", "lines":
		order = cmp.Compare(bLines, aLines)
	case "blank", "blanks":
		order = cmp.Compare(bBlank, aBlank)
	case "code", "codes":
		order = cmp.Compare(bCode, aCode)
	case "comment", "comments":
		order = cmp.Compare(bComment, aComment)
	case "complexity", "complexitys":
		order = cmp.Compare(bComplexity, aComplexity)
	case "byte", "bytes":
		order = cmp.Compare(bBytes, aBytes)
	default:
		order = strings.Compare(aName, bName)
	}
	if order != 0 {
		return order
	}
	if order = strings.Compare(aLoc, bLoc); order != 0 {
		return order
	}
	if order = strings.Compare(aName, bName); order != 0 {
		return order
	}
	return strings.Compare(aLang, bLang)
}
