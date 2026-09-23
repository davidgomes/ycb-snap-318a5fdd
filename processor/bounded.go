// SPDX-License-Identifier: MIT

package processor

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"
	"golang.org/x/crypto/blake2b"
)

// BoundedMemory limits how many per-file records are held in memory before formatting
var BoundedMemory = false

// BoundedMemoryDir is the directory bounded memory mode spills per-file records into
var BoundedMemoryDir = ""

// BoundedMemoryMaxInMemoryFiles is the maximum number of per-file records held in memory in bounded memory mode
var BoundedMemoryMaxInMemoryFiles = 0

// BoundedMemoryStats prints spill statistics to stderr in bounded memory mode
var BoundedMemoryStats = false

// SortBySet indicates the sort column was explicitly requested rather than left as the default
var SortBySet = false

// Number of sorted runs merged at once during an external sort
const boundedMergeFanIn = 64

// Set while bounded memory formatting is running so formatters avoid retaining per-file records
var boundedActive *boundedStore

var boundedDirAbs []string

func validateBoundedMemory() error {
	if !BoundedMemory {
		return nil
	}
	if BoundedMemoryDir == "" {
		return errors.New("--bounded-memory-dir is required when --bounded-memory is enabled")
	}
	if BoundedMemoryMaxInMemoryFiles <= 0 {
		return errors.New("--bounded-memory-max-in-memory-files must be greater than 0 when --bounded-memory is enabled")
	}
	return nil
}

func setupBoundedMemoryDir() error {
	if err := os.MkdirAll(BoundedMemoryDir, 0755); err != nil {
		return err
	}
	abs, err := filepath.Abs(BoundedMemoryDir)
	if err != nil {
		return err
	}
	boundedDirAbs = []string{abs}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && resolved != abs {
		boundedDirAbs = append(boundedDirAbs, resolved)
	}
	return nil
}

// isInBoundedMemoryDir reports whether the path lives inside the spill directory, which must never be counted
func isInBoundedMemoryDir(location string) bool {
	if len(boundedDirAbs) == 0 {
		return false
	}
	abs, err := filepath.Abs(location)
	if err != nil {
		return false
	}
	for _, dir := range boundedDirAbs {
		rel, err := filepath.Rel(dir, abs)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func boundedFatal(err error) {
	printError(fmt.Sprintf("bounded-memory: %s", err))
	os.Exit(1)
}

type boundedStore struct {
	dir    string
	max    int
	prefix string

	mu     sync.Mutex
	live   int
	peak   int
	spills int

	buf      []*FileJob
	count    int
	spilled  bool
	records  *spillFile
	fileSeq  int
	weighted bool
}

func newBoundedStore(dir string, maxInMemory int) *boundedStore {
	return &boundedStore{
		dir:    dir,
		max:    maxInMemory,
		prefix: fmt.Sprintf("scc-bounded-%d-%d", os.Getpid(), time.Now().UnixNano()),
	}
}

func (s *boundedStore) acquire(n int) {
	s.mu.Lock()
	s.live += n
	s.peak = max(s.peak, s.live)
	s.mu.Unlock()
}

func (s *boundedStore) release(n int) {
	s.mu.Lock()
	s.live -= n
	s.mu.Unlock()
}

func (s *boundedStore) addSpill() {
	s.mu.Lock()
	s.spills++
	s.mu.Unlock()
}

func (s *boundedStore) stats() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("bounded-memory: spills=%d peak_in_memory_files=%d max_in_memory_files=%d files=%d dir=%s", s.spills, s.peak, s.max, s.count, s.dir)
}

// spillFile is an append-only file of length prefixed frames
type spillFile struct {
	path string
	f    *os.File
	w    *bufio.Writer
	off  int64
}

func (s *boundedStore) newSpillFile(kind string) *spillFile {
	s.fileSeq++
	path := filepath.Join(s.dir, fmt.Sprintf("%s-%s-%d.bin", s.prefix, kind, s.fileSeq))
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		boundedFatal(err)
	}
	return &spillFile{path: path, f: f, w: bufio.NewWriter(f)}
}

func (sf *spillFile) writeFrame(b []byte) {
	var hdr [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(hdr[:], uint64(len(b)))
	if _, err := sf.w.Write(hdr[:n]); err != nil {
		boundedFatal(err)
	}
	if _, err := sf.w.Write(b); err != nil {
		boundedFatal(err)
	}
	sf.off += int64(n + len(b))
}

func (sf *spillFile) flush() {
	if err := sf.w.Flush(); err != nil {
		boundedFatal(err)
	}
}

func (sf *spillFile) section(start, end int64) *bufio.Reader {
	return bufio.NewReader(io.NewSectionReader(sf.f, start, end-start))
}

func (sf *spillFile) remove() {
	_ = sf.f.Close()
	_ = os.Remove(sf.path)
}

func readFrame(r *bufio.Reader, buf []byte) ([]byte, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return buf, err
	}
	if uint64(cap(buf)) < n {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	if _, err := io.ReadFull(r, buf); err != nil {
		return buf, err
	}
	return buf, nil
}

// ingest drains the processed files, keeping at most max records in memory and spilling to disk otherwise
func (s *boundedStore) ingest(input chan *FileJob) {
	for job := range input {
		job.Content = nil
		job.ContentByteType = nil
		job.ComplexityLine = nil

		if len(s.buf) == s.max {
			s.flushBuffer()
		}
		s.buf = append(s.buf, job)
		s.count++
		s.acquire(1)
	}

	if s.spilled {
		if len(s.buf) != 0 {
			s.flushBuffer()
		}
		s.buf = nil
	}
}

func (s *boundedStore) flushBuffer() {
	if s.records == nil {
		s.records = s.newSpillFile("records")
	}
	var b []byte
	for i, job := range s.buf {
		b = encodeRecord(b[:0], job)
		s.records.writeFrame(b)
		s.buf[i] = nil
	}
	s.records.flush()
	s.release(len(s.buf))
	s.buf = s.buf[:0]
	s.spilled = true
	s.addSpill()
}

// eachRaw walks the encoded records on disk in arrival order
func (s *boundedStore) eachRaw(fn func(body []byte) bool) {
	r := s.records.section(0, s.records.off)
	var buf []byte
	var err error
	for {
		buf, err = readFrame(r, buf)
		if err == io.EOF {
			return
		}
		if err != nil {
			boundedFatal(err)
		}
		if !fn(buf) {
			return
		}
	}
}

func (s *boundedStore) decode(body []byte) *FileJob {
	job, err := decodeRecord(body)
	if err != nil {
		boundedFatal(err)
	}
	if s.weighted {
		job.WeightedComplexity = weightedComplexity(job)
	}
	s.acquire(1)
	return job
}

// all yields every record in arrival order, holding at most one decoded record at a time when spilled
func (s *boundedStore) all() iter.Seq[*FileJob] {
	return func(yield func(*FileJob) bool) {
		if !s.spilled {
			for _, job := range s.buf {
				if !yield(job) {
					return
				}
			}
			return
		}

		s.eachRaw(func(body []byte) bool {
			job := s.decode(body)
			ok := yield(job)
			s.release(1)
			return ok
		})
	}
}

// replay feeds every record to one of the channel based formatters
func (s *boundedStore) replay() chan *FileJob {
	ch := make(chan *FileJob)
	go func() {
		for job := range s.all() {
			ch <- job
		}
		close(ch)
	}()
	return ch
}

type fileKeyFunc func(key []byte, job *FileJob, seq uint64) []byte

type sortEntry struct {
	key  []byte
	body []byte
	job  *FileJob
}

type segment struct {
	start, end int64
}

// sorted yields every record ordered by the bytewise comparison of the keys built by keyFn
func (s *boundedStore) sorted(keyFn fileKeyFunc) iter.Seq[*FileJob] {
	return func(yield func(*FileJob) bool) {
		if !s.spilled {
			entries := make([]sortEntry, 0, len(s.buf))
			for i, job := range s.buf {
				entries = append(entries, sortEntry{key: keyFn(nil, job, uint64(i)), job: job})
			}
			slices.SortFunc(entries, func(a, b sortEntry) int { return bytes.Compare(a.key, b.key) })
			for _, e := range entries {
				if !yield(e.job) {
					return
				}
			}
			return
		}

		s.externalSort(keyFn, yield)
	}
}

func (s *boundedStore) externalSort(keyFn fileKeyFunc, yield func(*FileJob) bool) {
	runs := s.newSpillFile("sort")
	var segs []segment
	chunk := make([]sortEntry, 0, min(s.max, s.count))

	flushChunk := func() {
		slices.SortFunc(chunk, func(a, b sortEntry) int { return bytes.Compare(a.key, b.key) })
		start := runs.off
		for i := range chunk {
			runs.writeFrame(chunk[i].key)
			runs.writeFrame(chunk[i].body)
			chunk[i] = sortEntry{}
		}
		runs.flush()
		segs = append(segs, segment{start, runs.off})
		s.release(len(chunk))
		chunk = chunk[:0]
		s.addSpill()
	}

	var seq uint64
	s.eachRaw(func(body []byte) bool {
		if len(chunk) == s.max {
			flushChunk()
		}
		job, err := decodeRecord(body)
		if err != nil {
			boundedFatal(err)
		}
		s.acquire(1)
		chunk = append(chunk, sortEntry{key: keyFn(nil, job, seq), body: bytes.Clone(body)})
		seq++
		return true
	})
	if len(chunk) != 0 {
		flushChunk()
	}

	for len(segs) > boundedMergeFanIn {
		out := s.newSpillFile("sort")
		next := make([]segment, 0, len(segs)/boundedMergeFanIn+1)
		var body []byte
		for i := 0; i < len(segs); i += boundedMergeFanIn {
			start := out.off
			mergeSegments(runs, segs[i:min(i+boundedMergeFanIn, len(segs))], func(key []byte, r *bufio.Reader) bool {
				var err error
				body, err = readFrame(r, body)
				if err != nil {
					boundedFatal(err)
				}
				out.writeFrame(key)
				out.writeFrame(body)
				return true
			})
			next = append(next, segment{start, out.off})
		}
		out.flush()
		runs.remove()
		runs, segs = out, next
	}

	defer runs.remove()
	var body []byte
	mergeSegments(runs, segs, func(_ []byte, r *bufio.Reader) bool {
		var err error
		body, err = readFrame(r, body)
		if err != nil {
			boundedFatal(err)
		}
		job := s.decode(body)
		ok := yield(job)
		s.release(1)
		return ok
	})
}

type mergeHead struct {
	r    *bufio.Reader
	key  []byte
	done bool
}

func (h *mergeHead) advance() {
	var err error
	h.key, err = readFrame(h.r, h.key)
	if err == io.EOF {
		h.done = true
		return
	}
	if err != nil {
		boundedFatal(err)
	}
}

// mergeSegments merges sorted runs holding only their keys, emit must consume the body frame from the reader
func mergeSegments(sf *spillFile, segs []segment, emit func(key []byte, r *bufio.Reader) bool) {
	heads := make([]*mergeHead, 0, len(segs))
	for _, seg := range segs {
		h := &mergeHead{r: sf.section(seg.start, seg.end)}
		h.advance()
		if !h.done {
			heads = append(heads, h)
		}
	}

	for len(heads) != 0 {
		best := 0
		for i := 1; i < len(heads); i++ {
			if bytes.Compare(heads[i].key, heads[best].key) < 0 {
				best = i
			}
		}
		h := heads[best]
		if !emit(h.key, h.r) {
			return
		}
		h.advance()
		if h.done {
			heads = slices.Delete(heads, best, best+1)
		}
	}
}

// languageFileSource yields the per-file records of each language summary without them being retained on the summary
type languageFileSource struct {
	ranks map[string]uint32
	keyFn fileKeyFunc
	next  func() (*FileJob, bool)
	stop  func()
	head  *FileJob
}

func newLanguageFileSource(language []LanguageSummary, keyFn fileKeyFunc) *languageFileSource {
	fs := &languageFileSource{keyFn: keyFn}
	if boundedActive != nil {
		fs.ranks = make(map[string]uint32, len(language))
		for i, l := range language {
			fs.ranks[l.Name] = uint32(i)
		}
	}
	return fs
}

// files yields the files for summary, languages must be requested in the order they were passed in
func (fs *languageFileSource) files(summary *LanguageSummary) iter.Seq[*FileJob] {
	if boundedActive == nil {
		sortSummaryFiles(summary)
		return slices.Values(summary.Files)
	}

	if fs.next == nil {
		fs.next, fs.stop = iter.Pull(boundedActive.sorted(func(key []byte, job *FileJob, seq uint64) []byte {
			key = binary.BigEndian.AppendUint32(key, fs.ranks[job.Language])
			return fs.keyFn(key, job, seq)
		}))
		fs.head, _ = fs.next()
	}

	return func(yield func(*FileJob) bool) {
		for fs.head != nil && fs.head.Language == summary.Name {
			job := fs.head
			if !yield(job) {
				return
			}
			fs.head, _ = fs.next()
		}
	}
}

func (fs *languageFileSource) close() {
	if fs.stop != nil {
		fs.stop()
	}
}

// appendSummaryFile tracks a file against its language summary unless running in bounded memory mode
func appendSummaryFile(files []*FileJob, res *FileJob) []*FileJob {
	if boundedActive != nil {
		return files
	}
	return append(files, res)
}

func weightedComplexity(job *FileJob) float64 {
	if job.Code != 0 {
		return (float64(job.Complexity) / float64(job.Code)) * 100
	}
	return 0
}

// Order preserving key encodings, strings are escaped so concatenated keys compare like the fields they hold
func appendKeyString(key []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			key = append(key, 0, 0xFF)
		} else {
			key = append(key, s[i])
		}
	}
	return append(key, 0, 1)
}

func appendKeyDesc(key []byte, v int64) []byte {
	return binary.BigEndian.AppendUint64(key, ^(uint64(v) ^ (1 << 63)))
}

func appendKeySeq(key []byte, seq uint64) []byte {
	return binary.BigEndian.AppendUint64(key, seq)
}

// summaryFileKey matches the ordering of sortSummaryFiles
func summaryFileKey(key []byte, job *FileJob, seq uint64) []byte {
	switch SortBy {
	case "name", "names", "language", "languages", "lang", "langs":
		key = appendKeyString(key, job.Location)
	case "line", "lines":
		key = appendKeyDesc(key, job.Lines)
	case "blank", "blanks":
		key = appendKeyDesc(key, job.Blank)
	case "code", "codes":
		key = appendKeyDesc(key, job.Code)
	case "comment", "comments":
		key = appendKeyDesc(key, job.Comment)
	case "complexity", "complexitys", "comp":
		key = appendKeyDesc(key, job.Complexity)
	default:
		key = appendKeyDesc(key, job.Lines)
	}
	key = appendKeyString(key, job.Location)
	return appendKeySeq(key, seq)
}

// csvFileKey matches the ordering of compareCSVFileRecords
func csvFileKey(key []byte, job *FileJob, seq uint64) []byte {
	switch SortBy {
	case "name", "names":
		key = appendKeyString(key, job.Filename)
	case "language", "languages", "lang", "langs":
		key = appendKeyString(key, job.Language)
	case "line", "lines":
		key = appendKeyDesc(key, job.Lines)
	case "blank", "blanks":
		key = appendKeyDesc(key, job.Blank)
	case "code", "codes":
		key = appendKeyDesc(key, job.Code)
	case "comment", "comments":
		key = appendKeyDesc(key, job.Comment)
	case "complexity", "complexitys":
		key = appendKeyDesc(key, job.Complexity)
	case "byte", "bytes":
		key = appendKeyDesc(key, job.Bytes)
	default:
		key = appendKeyString(key, job.Filename)
	}
	key = appendKeyString(key, job.Location)
	return appendKeySeq(key, seq)
}

// arrivalFileKey keeps files in the order they were processed
func arrivalFileKey(key []byte, _ *FileJob, seq uint64) []byte {
	return appendKeySeq(key, seq)
}

func encodeString(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func encodeRecord(b []byte, job *FileJob) []byte {
	b = encodeString(b, job.Language)
	if job.PossibleLanguages == nil {
		b = binary.AppendUvarint(b, 0)
	} else {
		b = binary.AppendUvarint(b, uint64(len(job.PossibleLanguages))+1)
		for _, l := range job.PossibleLanguages {
			b = encodeString(b, l)
		}
	}
	b = encodeString(b, job.Filename)
	b = encodeString(b, job.Extension)
	b = encodeString(b, job.Location)
	b = encodeString(b, job.Symlocation)
	for _, v := range []int64{job.Bytes, job.Lines, job.Code, job.Comment, job.Blank, job.Complexity, int64(job.EndPoint), int64(job.Uloc)} {
		b = binary.AppendVarint(b, v)
	}
	b = binary.BigEndian.AppendUint64(b, math.Float64bits(job.WeightedComplexity))

	var flags byte
	for i, f := range []bool{job.Hash != nil, job.Binary, job.Minified, job.Generated} {
		if f {
			flags |= 1 << i
		}
	}
	b = append(b, flags)

	if job.LineLength == nil {
		b = binary.AppendUvarint(b, 0)
	} else {
		b = binary.AppendUvarint(b, uint64(len(job.LineLength))+1)
		for _, l := range job.LineLength {
			b = binary.AppendVarint(b, int64(l))
		}
	}
	return b
}

var errCorruptRecord = errors.New("corrupt spilled record")

type recordDecoder struct {
	b   []byte
	err error
}

func (d *recordDecoder) uvarint() uint64 {
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.err = errCorruptRecord
		d.b = nil
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *recordDecoder) varint() int64 {
	v, n := binary.Varint(d.b)
	if n <= 0 {
		d.err = errCorruptRecord
		d.b = nil
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *recordDecoder) string() string {
	n := d.uvarint()
	if uint64(len(d.b)) < n {
		d.err = errCorruptRecord
		d.b = nil
		return ""
	}
	s := string(d.b[:n])
	d.b = d.b[n:]
	return s
}

func (d *recordDecoder) bytes(n int) []byte {
	if len(d.b) < n {
		d.err = errCorruptRecord
		d.b = nil
		return make([]byte, n)
	}
	v := d.b[:n]
	d.b = d.b[n:]
	return v
}

func decodeRecord(b []byte) (*FileJob, error) {
	d := &recordDecoder{b: b}
	job := &FileJob{}
	job.Language = d.string()
	if n := d.uvarint(); n != 0 && d.err == nil {
		job.PossibleLanguages = make([]string, 0, n-1)
		for i := uint64(1); i < n && d.err == nil; i++ {
			job.PossibleLanguages = append(job.PossibleLanguages, d.string())
		}
	}
	job.Filename = d.string()
	job.Extension = d.string()
	job.Location = d.string()
	job.Symlocation = d.string()
	job.Bytes = d.varint()
	job.Lines = d.varint()
	job.Code = d.varint()
	job.Comment = d.varint()
	job.Blank = d.varint()
	job.Complexity = d.varint()
	job.EndPoint = int(d.varint())
	job.Uloc = int(d.varint())
	job.WeightedComplexity = math.Float64frombits(binary.BigEndian.Uint64(d.bytes(8)))

	flags := d.bytes(1)[0]
	if flags&1 != 0 {
		job.Hash, _ = blake2b.New256(nil)
	}
	job.Binary = flags&2 != 0
	job.Minified = flags&4 != 0
	job.Generated = flags&8 != 0

	if n := d.uvarint(); n != 0 && d.err == nil {
		job.LineLength = make([]int, 0, n-1)
		for i := uint64(1); i < n && d.err == nil; i++ {
			job.LineLength = append(job.LineLength, int(d.varint()))
		}
	}

	if d.err == nil && len(d.b) != 0 {
		d.err = errCorruptRecord
	}
	return job, d.err
}

// boundedFileSummarize is the bounded memory equivalent of fileSummarize
func boundedFileSummarize(input chan *FileJob) string {
	s := newBoundedStore(BoundedMemoryDir, BoundedMemoryMaxInMemoryFiles)
	boundedActive = s
	defer func() { boundedActive = nil }()

	s.ingest(input)

	var result string
	if FormatMulti != "" {
		result = s.summarizeMulti()
	} else {
		result = s.summarizeSingle()
	}

	if BoundedMemoryStats {
		_, _ = fmt.Fprintln(os.Stderr, s.stats())
	}
	return result
}

func (s *boundedStore) summarizeSingle() string {
	switch {
	case More || strings.EqualFold(Format, "wide"):
		return s.render("wide")
	case strings.EqualFold(Format, "cloc-yml"):
		return s.render("cloc-yaml")
	case strings.EqualFold(Format, "csv-stream"):
		s.csvStream(os.Stdout)
		return ""
	}

	for _, f := range []string{"json", "json2", "cloc-yaml", "csv", "html", "html-table", "sql", "sql-insert", "openmetrics"} {
		if strings.EqualFold(Format, f) {
			return s.render(f)
		}
	}
	return s.render("tabular")
}

// summarizeMulti mirrors fileSummarizeMulti, except csv-stream honours its destination
func (s *boundedStore) summarizeMulti() string {
	var str strings.Builder

	for spec := range strings.SplitSeq(FormatMulti, ",") {
		t := strings.Split(spec, ":")
		if len(t) != 2 {
			continue
		}

		if strings.ToLower(t[0]) == "csv-stream" {
			if t[1] == "stdout" {
				s.csvStream(os.Stdout)
				continue
			}
			f, err := os.OpenFile(t[1], os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			if err != nil {
				fmt.Printf("%s unable to be written to for format %s: %s", t[1], t[0], err)
				continue
			}
			s.csvStream(f)
			if err := f.Close(); err != nil {
				fmt.Printf("%s unable to be written to for format %s: %s", t[1], t[0], err)
			}
			continue
		}

		val := s.render(strings.ToLower(t[0]))

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

	return str.String()
}

func (s *boundedStore) render(format string) string {
	switch format {
	case "tabular":
		return fileSummarizeShort(s.replay())
	case "wide":
		s.weighted = true
		return fileSummarizeLong(s.replay())
	case "json":
		if Files {
			return s.languageSummaryJSON()
		}
		return toJSON(s.replay())
	case "json2":
		if Files {
			return s.json2()
		}
		return toJSON2(s.replay())
	case "cloc-yaml", "cloc-yml":
		return toClocYAML(s.replay())
	case "csv":
		if Files {
			return s.csvFiles()
		}
		return toCSVSummary(s.replay())
	case "html":
		return toHtml(s.replay())
	case "html-table":
		return toHtmlTable(s.replay())
	case "sql":
		return toSql(s.replay())
	case "sql-insert":
		return toSqlInsert(s.replay())
	case "openmetrics":
		return toOpenMetrics(s.replay())
	}
	return ""
}

func (s *boundedStore) csvStream(out io.Writer) {
	w := bufio.NewWriter(out)
	_, _ = w.WriteString(csvStreamHeader)

	records := s.all()
	if SortBySet {
		records = s.sorted(csvFileKey)
	}
	for job := range records {
		_, _ = w.WriteString(formatCSVStreamRecord(csvFileRecord(job)))
	}
	if err := w.Flush(); err != nil {
		printError(err.Error())
	}
}

func (s *boundedStore) csvFiles() string {
	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	_ = w.Write(csvFilesHeader)
	for job := range s.sorted(csvFileKey) {
		_ = w.Write(csvFileRecord(job))
	}
	w.Flush()
	return b.String()
}

// languageSummaryJSON produces the same bytes as marshalling the aggregated summaries with their files attached
func (s *boundedStore) languageSummaryJSON() string {
	json := jsoniter.ConfigCompatibleWithStandardLibrary
	language := sortLanguageSummary(aggregateLanguageSummary(s.replay()))

	fs := newLanguageFileSource(language, arrivalFileKey)
	defer fs.close()

	var str strings.Builder
	str.WriteByte('[')
	for i := range language {
		if i != 0 {
			str.WriteByte(',')
		}

		summary := language[i]
		summary.Files = []*FileJob{}
		encoded, err := json.Marshal(summary)
		if err != nil {
			boundedFatal(err)
		}
		marker := []byte(`,"Files":[],"LineLength":`)
		idx := bytes.LastIndex(encoded, marker)
		if idx == -1 {
			boundedFatal(errors.New("unexpected language summary encoding"))
		}

		str.Write(encoded[:idx])
		str.WriteString(`,"Files":[`)
		first := true
		for job := range fs.files(&language[i]) {
			if !first {
				str.WriteByte(',')
			}
			first = false
			f, err := json.Marshal(job)
			if err != nil {
				boundedFatal(err)
			}
			str.Write(f)
		}
		str.WriteString(`],"LineLength":`)
		str.Write(encoded[idx+len(marker):])
	}
	str.WriteByte(']')
	return str.String()
}

func (s *boundedStore) json2() string {
	json := jsoniter.ConfigCompatibleWithStandardLibrary
	languageJSON := s.languageSummaryJSON()

	var sumCode, sumComplexity int64
	for job := range s.all() {
		sumCode += job.Code
		sumComplexity += job.Complexity
	}

	j2 := buildJson2(nil, sumCode, sumComplexity)
	encoded, err := json.Marshal(j2)
	if err != nil {
		boundedFatal(err)
	}
	prefix := []byte(`{"languageSummary":null`)
	if !bytes.HasPrefix(encoded, prefix) {
		boundedFatal(errors.New("unexpected json2 encoding"))
	}
	return `{"languageSummary":` + languageJSON + string(encoded[len(prefix):])
}
