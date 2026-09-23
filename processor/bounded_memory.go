// SPDX-License-Identifier: MIT

package processor

import (
	"bytes"
	"cmp"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Bounded-memory mode spills per-file results so --format-multi does not
// retain every file record at once. See fileSummarizeMulti.

// BoundedMemory enables spilling of per-file results for --format-multi.
var BoundedMemory = false

// BoundedMemoryDir is the directory that receives spill files.
var BoundedMemoryDir = ""

// BoundedMemoryMaxInMemoryFiles is the maximum number of file records kept
// in memory. Required and must be > 0 when BoundedMemory is set.
var BoundedMemoryMaxInMemoryFiles = 0

// BoundedMemoryStats prints one stderr line describing spill behaviour.
var BoundedMemoryStats = false

// boundedMemoryAbsDir is the cleaned absolute spill directory.
var boundedMemoryAbsDir = ""

// boundedMemoryStatsEmitted ensures stats are printed once per Process call.
var boundedMemoryStatsEmitted = false

const csvStreamHeader = "Language,Provider,Filename,Lines,Code,Comments,Blanks,Complexity,Bytes,Uloc"

type csvStreamSortKey struct {
	Language   string
	Filename   string
	Location   string
	Lines      int64
	Blank      int64
	Code       int64
	Comment    int64
	Complexity int64
	Bytes      int64
}

func sortKeyFromJob(job *FileJob) csvStreamSortKey {
	return csvStreamSortKey{
		Language:   job.Language,
		Filename:   job.Filename,
		Location:   job.Location,
		Lines:      job.Lines,
		Blank:      job.Blank,
		Code:       job.Code,
		Comment:    job.Comment,
		Complexity: job.Complexity,
		Bytes:      job.Bytes,
	}
}

func compareCSVStreamSortKeys(a, b csvStreamSortKey) int {
	var c int
	switch SortBy {
	case "name", "names":
		c = strings.Compare(a.Filename, b.Filename)
	case "language", "languages", "lang", "langs":
		c = strings.Compare(a.Language, b.Language)
	case "line", "lines":
		c = cmp.Compare(b.Lines, a.Lines)
	case "blank", "blanks":
		c = cmp.Compare(b.Blank, a.Blank)
	case "code", "codes":
		c = cmp.Compare(b.Code, a.Code)
	case "comment", "comments":
		c = cmp.Compare(b.Comment, a.Comment)
	case "complexity", "complexitys":
		c = cmp.Compare(b.Complexity, a.Complexity)
	case "byte", "bytes":
		c = cmp.Compare(b.Bytes, a.Bytes)
	default:
		c = strings.Compare(a.Filename, b.Filename)
	}
	if c != 0 {
		return c
	}
	if c = strings.Compare(a.Location, b.Location); c != 0 {
		return c
	}
	if c = strings.Compare(a.Filename, b.Filename); c != 0 {
		return c
	}
	return strings.Compare(a.Language, b.Language)
}

func compareCSVStreamJobs(a, b *FileJob) int {
	return compareCSVStreamSortKeys(sortKeyFromJob(a), sortKeyFromJob(b))
}

func configureBoundedMemory() error {
	if !BoundedMemory {
		boundedMemoryAbsDir = ""
		return nil
	}
	if strings.TrimSpace(BoundedMemoryDir) == "" {
		return errors.New("error: --bounded-memory-dir is required when --bounded-memory is enabled")
	}
	if BoundedMemoryMaxInMemoryFiles <= 0 {
		return errors.New("error: --bounded-memory-max-in-memory-files must be > 0 when --bounded-memory is enabled")
	}
	if err := os.MkdirAll(BoundedMemoryDir, 0o755); err != nil {
		return fmt.Errorf("error: unable to create bounded-memory dir: %w", err)
	}
	abs, err := filepath.Abs(BoundedMemoryDir)
	if err != nil {
		return fmt.Errorf("error: unable to resolve bounded-memory dir: %w", err)
	}
	boundedMemoryAbsDir = filepath.Clean(abs)
	return nil
}

func pathWithinDir(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func skipBoundedMemoryPath(path string) bool {
	if !BoundedMemory || boundedMemoryAbsDir == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	return pathWithinDir(boundedMemoryAbsDir, abs)
}

func boundedMemoryDirectoryExclusions(scanPaths []string) []string {
	if !BoundedMemory || boundedMemoryAbsDir == "" {
		return nil
	}
	seen := map[string]struct{}{}
	add := func(p string) {
		p = filepath.Clean(p)
		if p == "" || p == "." {
			return
		}
		seen[p] = struct{}{}
	}
	// Absolute path matches walkers started with an absolute root.
	// The path joined onto each scan root matches relative walks.
	// A bare directory name is not added: ExcludeDirectory is suffix-based
	// and would also skip unrelated directories with the same name.
	add(boundedMemoryAbsDir)
	for _, scan := range scanPaths {
		absScan, err := filepath.Abs(scan)
		if err != nil {
			continue
		}
		absScan = filepath.Clean(absScan)
		if absScan == boundedMemoryAbsDir || !pathWithinDir(absScan, boundedMemoryAbsDir) {
			continue
		}
		rel, err := filepath.Rel(absScan, boundedMemoryAbsDir)
		if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		add(filepath.Join(scan, rel))
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out
}

func emitBoundedMemoryStats(spills, peak int) {
	if !BoundedMemoryStats || boundedMemoryStatsEmitted {
		return
	}
	fmt.Fprintf(os.Stderr, "bounded-memory: spills=%d peak_in_memory_files=%d\n", spills, peak)
	boundedMemoryStatsEmitted = true
}

type boundedMemItem struct {
	seq int
	job *FileJob
}

type spilledFileRecord struct {
	Seq                int
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
}

func recordFromItem(item boundedMemItem) spilledFileRecord {
	job := item.job
	return spilledFileRecord{
		Seq:                item.seq,
		Language:           job.Language,
		PossibleLanguages:  job.PossibleLanguages,
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
		LineLength:         job.LineLength,
	}
}

func (rec spilledFileRecord) toJob() *FileJob {
	return &FileJob{
		Language:           rec.Language,
		PossibleLanguages:  rec.PossibleLanguages,
		Filename:           rec.Filename,
		Extension:          rec.Extension,
		Location:           rec.Location,
		Symlocation:        rec.Symlocation,
		Bytes:              rec.Bytes,
		Lines:              rec.Lines,
		Code:               rec.Code,
		Comment:            rec.Comment,
		Blank:              rec.Blank,
		Complexity:         rec.Complexity,
		WeightedComplexity: rec.WeightedComplexity,
		Binary:             rec.Binary,
		Minified:           rec.Minified,
		Generated:          rec.Generated,
		EndPoint:           rec.EndPoint,
		Uloc:               rec.Uloc,
		LineLength:         rec.LineLength,
	}
}

type boundedFileStore struct {
	dir        string
	max        int
	mem        []boundedMemItem
	spillPaths []string
	spills     int
	peak       int
	total      int
}

func newBoundedFileStore(dir string, max int) *boundedFileStore {
	return &boundedFileStore{dir: dir, max: max}
}

func releaseBoundedFilePayload(job *FileJob) {
	job.Content = nil
	job.ContentByteType = nil
	job.ComplexityLine = nil
	job.Callback = nil
}

func (s *boundedFileStore) noteCount(n int) {
	if n > s.peak {
		s.peak = n
	}
}

func (s *boundedFileStore) add(job *FileJob) error {
	releaseBoundedFilePayload(job)
	if len(s.mem) >= s.max {
		if err := s.spillMem(); err != nil {
			return err
		}
	}
	s.mem = append(s.mem, boundedMemItem{seq: s.total, job: job})
	s.total++
	s.noteCount(len(s.mem))
	return nil
}

func (s *boundedFileStore) finish() error {
	// Flush the tail once earlier batches were spilled so a later replay
	// never holds the tail together with a reloaded batch.
	if len(s.spillPaths) > 0 && len(s.mem) > 0 {
		return s.spillMem()
	}
	return nil
}

func (s *boundedFileStore) spillMem() error {
	if len(s.mem) == 0 {
		return nil
	}
	path := filepath.Join(s.dir, fmt.Sprintf("spill-%06d", len(s.spillPaths)))
	if err := writeSpillFile(path, s.mem); err != nil {
		return err
	}
	s.spillPaths = append(s.spillPaths, path)
	s.spills++
	s.mem = nil
	return nil
}

func writeSpillFile(path string, items []boundedMemItem) error {
	recs := make([]spilledFileRecord, len(items))
	for i, item := range items {
		recs[i] = recordFromItem(item)
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(recs); err != nil {
		return err
	}
	if buf.Len() == 0 {
		return errors.New("error: bounded-memory spill encoded empty")
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

func readSpillFile(path string) ([]boundedMemItem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var recs []spilledFileRecord
	if err := gob.NewDecoder(f).Decode(&recs); err != nil {
		return nil, err
	}
	items := make([]boundedMemItem, len(recs))
	for i, rec := range recs {
		items[i] = boundedMemItem{seq: rec.Seq, job: rec.toJob()}
	}
	return items, nil
}

func (s *boundedFileStore) forEach(fn func(seq int, job *FileJob)) {
	if len(s.spillPaths) == 0 {
		for _, item := range s.mem {
			fn(item.seq, item.job)
		}
		return
	}
	for _, path := range s.spillPaths {
		items, err := readSpillFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: reading bounded-memory spill: %s\n", err)
			os.Exit(1)
		}
		s.noteCount(len(items))
		for _, item := range items {
			fn(item.seq, item.job)
		}
	}
}

func (s *boundedFileStore) replay() chan *FileJob {
	ch := make(chan *FileJob)
	go func() {
		defer close(ch)
		s.forEach(func(_ int, job *FileJob) {
			ch <- job
		})
	}()
	return ch
}

type csvStreamKey struct {
	seq  int
	sort csvStreamSortKey
	line string
}

func csvStreamKeyFrom(seq int, job *FileJob) csvStreamKey {
	return csvStreamKey{
		seq:  seq,
		sort: sortKeyFromJob(job),
		line: formatCSVStreamJob(job),
	}
}

func (s *boundedFileStore) writeCSVStream(w io.Writer) {
	fmt.Fprintln(w, csvStreamHeader)
	if s.total == 0 {
		return
	}
	// The in-memory tail is at most max records. Sort it in place when requested.
	if len(s.spillPaths) == 0 {
		jobs := make([]*FileJob, len(s.mem))
		for i, item := range s.mem {
			jobs[i] = item.job
		}
		if SortBy != "" {
			slices.SortFunc(jobs, compareCSVStreamJobs)
		}
		for _, job := range jobs {
			fmt.Fprintln(w, formatCSVStreamJob(job))
		}
		return
	}
	if SortBy == "" {
		s.forEach(func(_ int, job *FileJob) {
			fmt.Fprintln(w, formatCSVStreamJob(job))
		})
		return
	}

	// Multiple spill batches: keep at most one batch of file records resident
	// and choose the next sorted row from copied keys.
	emitted := make([]bool, s.total)
	remaining := s.total
	for remaining > 0 {
		found := false
		var best csvStreamKey
		s.forEach(func(seq int, job *FileJob) {
			if seq < 0 || seq >= len(emitted) || emitted[seq] {
				return
			}
			key := csvStreamKeyFrom(seq, job)
			if !found || compareCSVStreamSortKeys(key.sort, best.sort) < 0 {
				best = key
				found = true
			}
		})
		if !found {
			break
		}
		fmt.Fprintln(w, best.line)
		emitted[best.seq] = true
		remaining--
	}
}

func multiFormatReadsJobs(format string) bool {
	switch format {
	case "tabular", "wide", "json", "json2", "cloc-yaml", "cloc-yml", "csv", "csv-stream", "html", "html-table", "sql", "sql-insert", "openmetrics":
		return true
	default:
		return false
	}
}
