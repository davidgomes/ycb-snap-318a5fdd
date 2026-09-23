// SPDX-License-Identifier: MIT

package processor

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	jsoniter "github.com/json-iterator/go"
	"golang.org/x/crypto/blake2b"
)

// BoundedMemory enables spilling per file results to disk so that formatting never holds
// more than BoundedMemoryMaxInMemoryFiles of them in memory at once
var BoundedMemory = false

// BoundedMemoryDir is the directory spill files are written to when BoundedMemory is enabled
var BoundedMemoryDir = ""

// BoundedMemoryMaxInMemoryFiles is the most file records held in memory at once when BoundedMemory is enabled
var BoundedMemoryMaxInMemoryFiles = 0

// BoundedMemoryStats prints spill statistics to stderr when BoundedMemory is enabled
var BoundedMemoryStats = false

// boundedMergeFanIn is the most sorted runs merged at once, each costs a read buffer and a sort key
const boundedMergeFanIn = 16

// boundedRunHeaderSize is the size of the header of a sorted run, its record count then byte length
const boundedRunHeaderSize = 16

// boundedMaxFrame guards against allocating absurd buffers when reading a damaged spill file
const boundedMaxFrame = 1 << 30

const (
	boundedFlagBinary = 1 << iota
	boundedFlagMinified
	boundedFlagGenerated
	boundedFlagHash
)

var errCorruptSpill = errors.New("corrupt bounded memory spill file")

var emptyFilesJSON = []byte(`"Files":[]`)

// boundedHash stands in for the duplicate detection hash of held and spilled jobs. Formatting
// only ever encodes it to JSON where every hash encodes the same way.
var boundedHash = sync.OnceValue(func() hash.Hash {
	h, _ := blake2b.New256(nil)
	return h
})

func validateBoundedMemory() error {
	if !BoundedMemory {
		return nil
	}

	if BoundedMemoryDir == "" {
		return errors.New("--bounded-memory-dir is required when --bounded-memory is enabled")
	}

	if BoundedMemoryMaxInMemoryFiles <= 0 {
		return errors.New("--bounded-memory-max-in-memory-files is required when --bounded-memory is enabled and must be greater than 0")
	}

	if err := os.MkdirAll(BoundedMemoryDir, 0755); err != nil {
		return fmt.Errorf("unable to create --bounded-memory-dir: %w", err)
	}

	return nil
}

// boundedMemorySpillPrefixes returns how locations of files inside the spill directory begin
// for each walked directory containing it, so that spill files are never counted
func boundedMemorySpillPrefixes(dirPaths []string) []string {
	if !BoundedMemory {
		return nil
	}

	spill, err := realPath(BoundedMemoryDir)
	if err != nil {
		return nil
	}

	var prefixes []string
	for _, dir := range dirPaths {
		root, err := realPath(dir)
		if err != nil {
			continue
		}

		rel, err := filepath.Rel(root, spill)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}

		prefixes = append(prefixes, filepath.Join(dir, rel)+string(filepath.Separator))
	}

	return prefixes
}

func realPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func boundedFatal(err error) {
	printError("bounded memory: " + err.Error())
	os.Exit(1)
}

// fileSortKey orders records without needing the full record. Fields an ordering does
// not use are left zero.
type fileSortKey struct {
	group int    // position of the language in the formatter output when grouped by language
	num   int64  // numeric column, sorted descending
	str   string // text column, sorted ascending
	loc   string // location, so ties do not depend on the order files were processed in
	seq   uint64 // order the file arrived to be formatted in
}

func newFileSortKey(group int, column func(string, *FileJob) (int64, string), job *FileJob, seq uint64) fileSortKey {
	num, str := column(SortBy, job)
	return fileSortKey{group: group, num: num, str: str, loc: job.Location, seq: seq}
}

func compareFileSortKeys(a, b *fileSortKey) int {
	if order := cmp.Compare(a.group, b.group); order != 0 {
		return order
	}
	if order := cmp.Compare(b.num, a.num); order != 0 {
		return order
	}
	if order := strings.Compare(a.str, b.str); order != 0 {
		return order
	}
	if order := strings.Compare(a.loc, b.loc); order != 0 {
		return order
	}
	return cmp.Compare(a.seq, b.seq)
}

func csvFileSortKey(r boundedRecord) fileSortKey {
	return newFileSortKey(0, csvSortKey, r.job, r.seq)
}

// boundedRecord is a file result held or spilled by bounded memory mode
type boundedRecord struct {
	seq uint64
	job *FileJob
}

type boundedLanguage struct {
	summary         LanguageSummary // only the totals are set
	lineLengthMax   int
	lineLengthSum   int
	lineLengthCount int
}

// boundedStore collects file results holding at most max of them in memory, spilling the
// rest to disk, and formats them the same way the in memory formatters do
type boundedStore struct {
	max int
	dir string

	// records holds every record while nothing has been spilled, otherwise records
	// waiting to be spilled
	records []boundedRecord
	count   uint64
	spill   *os.File
	spillW  *bufio.Writer
	scratch []byte

	live   int
	peak   int
	spills int

	languages map[string]*boundedLanguage
	totals    summaryTotals

	// fileSummarizeLong stores the weighted complexity on every job, which outputs
	// formatted after it then include, so once wide is formatted jobs carry it too
	weighted bool
}

func newBoundedStore() *boundedStore {
	return &boundedStore{
		max:       max(BoundedMemoryMaxInMemoryFiles, 1),
		dir:       BoundedMemoryDir,
		languages: map[string]*boundedLanguage{},
	}
}

func fileSummarizeBounded(input chan *FileJob) string {
	s := newBoundedStore()
	defer s.close()

	for res := range input {
		s.add(res)
	}
	s.finishIngest()

	var result string
	if FormatMulti != "" {
		result = s.formatMulti()
	} else {
		result = s.formatSingle()
	}

	if BoundedMemoryStats {
		_, _ = fmt.Fprintf(os.Stderr, "bounded-memory: spills=%d peak_in_memory_files=%d max_in_memory_files=%d files=%d\n", s.spills, s.peak, s.max, s.count)
	}

	return result
}

func (s *boundedStore) hold(n int) {
	s.live += n
	s.peak = max(s.peak, s.live)
}

func (s *boundedStore) release(n int) {
	s.live -= n
}

func (s *boundedStore) add(job *FileJob) {
	s.aggregate(job)

	// Formatting never reads these and they can be far larger than everything else in the job
	job.Content = nil
	job.ContentByteType = nil
	job.ComplexityLine = nil
	job.LineLength = nil
	job.Callback = nil
	if job.Hash != nil {
		job.Hash = boundedHash()
	}

	if len(s.records) >= s.max {
		s.spillRecords()
	}

	s.records = append(s.records, boundedRecord{seq: s.count, job: job})
	s.count++
	s.hold(1)
}

func (s *boundedStore) aggregate(job *FileJob) {
	weightedComplexity := fileWeightedComplexity(job)
	s.totals.add(job)
	s.totals.WeightedComplexity += weightedComplexity

	l, ok := s.languages[job.Language]
	if !ok {
		l = &boundedLanguage{summary: LanguageSummary{Name: job.Language}}
		s.languages[job.Language] = l
	}

	l.summary.Lines += job.Lines
	l.summary.Code += job.Code
	l.summary.Comment += job.Comment
	l.summary.Blank += job.Blank
	l.summary.Complexity += job.Complexity
	l.summary.Count++
	l.summary.Bytes += job.Bytes
	l.summary.WeightedComplexity += weightedComplexity

	l.lineLengthCount += len(job.LineLength)
	for _, length := range job.LineLength {
		l.lineLengthMax = max(l.lineLengthMax, length)
		l.lineLengthSum += length
	}
}

func (s *boundedStore) spillRecords() {
	if s.spill == nil {
		f, err := os.CreateTemp(s.dir, "scc-bounded-memory-*.spill")
		if err != nil {
			boundedFatal(err)
		}
		s.spill = f
		s.spillW = bufio.NewWriter(f)
	}

	for _, r := range s.records {
		s.scratch = appendBoundedRecord(s.scratch[:0], r)
		writeFramed(s.spillW, s.scratch)
	}
	if err := s.spillW.Flush(); err != nil {
		boundedFatal(err)
	}

	s.release(len(s.records))
	clear(s.records)
	s.records = s.records[:0]
	s.spills++
}

// finishIngest spills what remains in memory once anything has been spilled, so formatting
// can read every record back from disk without also holding the unspilled ones
func (s *boundedStore) finishIngest() {
	if s.spill != nil && len(s.records) != 0 {
		s.spillRecords()
	}
}

// close leaves the spill file in place so it remains inspectable after the run
func (s *boundedStore) close() {
	if s.spill != nil {
		_ = s.spill.Close()
	}
}

func (s *boundedStore) prepare(job *FileJob) *FileJob {
	job.WeightedComplexity = 0
	if s.weighted {
		job.WeightedComplexity = fileWeightedComplexity(job)
	}
	return job
}

func (s *boundedStore) spillReader() *bufio.Reader {
	return bufio.NewReader(io.NewSectionReader(s.spill, 0, math.MaxInt64))
}

// eachArrival calls fn with every record in the order they arrived
func (s *boundedStore) eachArrival(fn func(*FileJob)) {
	if s.spill == nil {
		for _, r := range s.records {
			fn(s.prepare(r.job))
		}
		return
	}

	in := s.spillReader()
	for {
		payload, err := readFramed(in, &s.scratch)
		if err == io.EOF {
			return
		}
		rec := decodeBoundedRecordOrFatal(payload, err)
		s.hold(1)
		fn(s.prepare(rec.job))
		s.release(1)
	}
}

// sorted returns every record ordered by key
func (s *boundedStore) sorted(key func(boundedRecord) fileSortKey) recordStream {
	if s.spill == nil {
		records := slices.Clone(s.records)
		slices.SortFunc(records, func(a, b boundedRecord) int {
			ka, kb := key(a), key(b)
			return compareFileSortKeys(&ka, &kb)
		})
		return &memoryStream{store: s, records: records, key: key}
	}

	return s.externalSort(key)
}

// externalSort writes sorted runs of at most max records then merges them. Runs being merged
// are represented by the sort key of their next record, so records are decoded one at a time.
func (s *boundedStore) externalSort(key func(boundedRecord) fileSortKey) recordStream {
	in := s.spillReader()
	out := s.newRunWriter()
	runs := 0

	var chunk []boundedRecord
	for done := false; !done; {
		for len(chunk) < s.max {
			payload, err := readFramed(in, &s.scratch)
			if err == io.EOF {
				done = true
				break
			}
			chunk = append(chunk, decodeBoundedRecordOrFatal(payload, err))
			s.hold(1)
		}

		if len(chunk) == 0 {
			break
		}

		slices.SortFunc(chunk, func(a, b boundedRecord) int {
			ka, kb := key(a), key(b)
			return compareFileSortKeys(&ka, &kb)
		})

		out.beginRun()
		for _, rec := range chunk {
			k := key(rec)
			out.writeEntry(&k, rec)
		}
		out.endRun()

		s.release(len(chunk))
		clear(chunk)
		chunk = chunk[:0]
		s.spills++
		runs++
	}

	f := out.finish()
	for runs > boundedMergeFanIn {
		next := s.newRunWriter()
		var off int64
		for merged := 0; merged < runs; merged += boundedMergeFanIn {
			var m *mergeStream
			m, off = s.openRuns(f, off, min(boundedMergeFanIn, runs-merged))
			next.beginRun()
			m.copyTo(next)
			next.endRun()
		}

		removeRunFile(f)
		f = next.finish()
		runs = (runs + boundedMergeFanIn - 1) / boundedMergeFanIn
	}

	m, _ := s.openRuns(f, 0, runs)
	m.file = f
	return m
}

func (s *boundedStore) openRuns(f *os.File, off int64, n int) (*mergeStream, int64) {
	m := &mergeStream{store: s}
	for range n {
		var header [boundedRunHeaderSize]byte
		if _, err := f.ReadAt(header[:], off); err != nil {
			boundedFatal(err)
		}
		count := binary.LittleEndian.Uint64(header[:8])
		length := int64(binary.LittleEndian.Uint64(header[8:]))
		start := off + boundedRunHeaderSize

		run := &runReader{r: bufio.NewReader(io.NewSectionReader(f, start, length)), left: count}
		if run.advance() {
			m.runs = append(m.runs, run)
		}
		off = start + length
	}
	return m, off
}

func languageGroups(language []LanguageSummary) map[string]int {
	groups := make(map[string]int, len(language))
	for i, l := range language {
		groups[l.Name] = i
	}
	return groups
}

// languageSummaries returns the summaries a formatter builds for itself in its sorted order
func (s *boundedStore) languageSummaries(view func(LanguageSummary) LanguageSummary) []LanguageSummary {
	language := make([]LanguageSummary, 0, len(s.languages))
	for _, l := range s.languages {
		language = append(language, view(l.summary))
	}
	return sortLanguageSummary(language)
}

// jsonLanguageView matches aggregateLanguageSummary
func jsonLanguageView(l LanguageSummary) LanguageSummary {
	return LanguageSummary{
		Name:       l.Name,
		Lines:      l.Lines,
		Code:       l.Code,
		Comment:    l.Comment,
		Blank:      l.Blank,
		Complexity: l.Complexity,
		Count:      l.Count,
		Files:      []*FileJob{},
		Bytes:      l.Bytes,
		ULOC:       len(ulocLanguageCount[l.Name]),
	}
}

// shortLanguageView matches fileSummarizeShort
func shortLanguageView(l LanguageSummary) LanguageSummary {
	return LanguageSummary{
		Name:       l.Name,
		Lines:      l.Lines,
		Code:       l.Code,
		Comment:    l.Comment,
		Blank:      l.Blank,
		Complexity: l.Complexity,
		Count:      l.Count,
	}
}

// longLanguageView matches fileSummarizeLong
func longLanguageView(l LanguageSummary) LanguageSummary {
	v := shortLanguageView(l)
	v.WeightedComplexity = l.WeightedComplexity
	return v
}

// htmlLanguageView matches toHtmlTable
func htmlLanguageView(l LanguageSummary) LanguageSummary {
	v := shortLanguageView(l)
	v.Bytes = l.Bytes
	return v
}

func (s *boundedStore) clocLanguages() map[string]languageSummaryCloc {
	langs := make(map[string]languageSummaryCloc, len(s.languages))
	for name, l := range s.languages {
		langs[name] = languageSummaryCloc{
			Name:    name,
			Code:    l.summary.Code,
			Comment: l.summary.Comment,
			Blank:   l.summary.Blank,
			Count:   l.summary.Count,
		}
	}
	return langs
}

func (s *boundedStore) formatSingle() string {
	name := strings.ToLower(Format)
	if More {
		name = "wide"
	}

	if name == "csv-stream" {
		s.writeCSVStreamStdout()
		return ""
	}

	if val, ok := s.format(name); ok {
		return val
	}
	val, _ := s.format("tabular")
	return val
}

func (s *boundedStore) formatMulti() string {
	var str strings.Builder

	for output := range strings.SplitSeq(FormatMulti, ",") {
		t := strings.Split(output, ":")
		if len(t) != 2 {
			continue
		}

		name := strings.ToLower(t[0])
		if name == "csv-stream" {
			if t[1] == "stdout" {
				s.writeCSVStreamStdout()
			} else if err := s.writeCSVStreamFile(t[1]); err != nil {
				fmt.Printf("%s unable to be written to for format %s: %s", t[1], t[0], err)
			}
			continue
		}

		val, _ := s.format(name)
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

// format renders a single output format, reporting false for unknown formats
func (s *boundedStore) format(name string) (string, bool) {
	switch name {
	case "tabular":
		language := s.languageSummaries(shortLanguageView)
		files := s.summaryFiles(language)
		defer files.close()
		return formatSummaryShort(language, s.totals, files), true
	case "wide":
		s.weighted = true
		language := s.languageSummaries(longLanguageView)
		files := s.summaryFiles(language)
		defer files.close()
		return formatSummaryLong(language, s.totals, files), true
	case "json":
		language := s.languageSummaries(jsonLanguageView)
		return s.formatJSON(language, language), true
	case "json2":
		language := s.languageSummaries(jsonLanguageView)
		return s.formatJSON(newJSON2(language), language), true
	case "cloc-yaml", "cloc-yml":
		return formatClocYAML(s.clocLanguages(), s.totals, makeTimestampMilli()), true
	case "csv":
		if Files {
			return s.formatCSVFiles(), true
		}
		return formatCSVSummary(s.languageSummaries(jsonLanguageView)), true
	case "html":
		return wrapHtml(s.formatHtmlTable()), true
	case "html-table":
		return s.formatHtmlTable(), true
	case "sql":
		return sqlSchema() + s.formatSQLInsert(), true
	case "sql-insert":
		return s.formatSQLInsert(), true
	case "openmetrics":
		if Files {
			return s.formatOpenMetricsFiles(), true
		}
		return formatOpenMetricsSummary(s.languageSummaries(jsonLanguageView)), true
	}

	return "", false
}

// formatJSON encodes v, whose language summaries have no files, then streams the files of
// each summary into its empty list in the same order aggregateLanguageSummary adds them
func (s *boundedStore) formatJSON(v any, language []LanguageSummary) string {
	json := jsoniter.ConfigCompatibleWithStandardLibrary
	encoded, _ := json.Marshal(v)
	if !Files {
		return string(encoded)
	}

	groups := languageGroups(language)
	stream := s.sorted(func(r boundedRecord) fileSortKey {
		return fileSortKey{group: groups[r.job.Language], seq: r.seq}
	})
	defer stream.close()

	var str strings.Builder
	for group := range language {
		i := bytes.Index(encoded, emptyFilesJSON)
		str.Write(encoded[:i+len(emptyFilesJSON)-1])

		first := true
		emitGroup(stream, group, func(job *FileJob) {
			if !first {
				str.WriteByte(',')
			}
			first = false
			b, _ := json.Marshal(job)
			str.Write(b)
		})

		str.WriteByte(']')
		encoded = encoded[i+len(emptyFilesJSON):]
	}
	str.Write(encoded)

	return str.String()
}

func (s *boundedStore) formatCSVFiles() string {
	stream := s.sorted(csvFileSortKey)
	defer stream.close()

	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	_ = w.Write(csvFilesHeader)
	emitAll(stream, func(job *FileJob) {
		_ = w.Write(csvFileRecord(job))
	})
	w.Flush()

	return b.String()
}

func (s *boundedStore) formatHtmlTable() string {
	language := s.languageSummaries(htmlLanguageView)
	files := s.summaryFiles(language)
	defer files.close()
	return formatHtmlTable(language, s.totals, files)
}

func (s *boundedStore) formatSQLInsert() string {
	w := newSQLInsertWriter()
	s.eachArrival(w.add)
	return w.finish()
}

func (s *boundedStore) formatOpenMetricsFiles() string {
	sb := &strings.Builder{}
	sb.WriteString(openMetricsMetadata)
	s.eachArrival(func(job *FileJob) {
		writeOpenMetricsFile(sb, job)
	})
	sb.WriteString("# EOF\n")
	return sb.String()
}

// writeCSVStream writes the same rows as toCSVStream, sorted like toCSVFiles when a sort was requested
func (s *boundedStore) writeCSVStream(w io.Writer) {
	_, _ = fmt.Fprintln(w, csvStreamHeader)

	row := func(job *FileJob) {
		writeCSVStreamRow(w, job)
	}

	if !SortBySet {
		s.eachArrival(row)
		return
	}

	stream := s.sorted(csvFileSortKey)
	defer stream.close()
	emitAll(stream, row)
}

func (s *boundedStore) writeCSVStreamStdout() {
	w := bufio.NewWriter(os.Stdout)
	s.writeCSVStream(w)
	_ = w.Flush()
}

func (s *boundedStore) writeCSVStreamFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(f)
	s.writeCSVStream(w)
	err = w.Flush()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// boundedSummaryFiles provides the per file details of a formatter's language summaries
// from the records of a bounded store
type boundedSummaryFiles struct {
	store  *boundedStore
	groups map[string]int
	stream recordStream
}

func (s *boundedStore) summaryFiles(language []LanguageSummary) *boundedSummaryFiles {
	return &boundedSummaryFiles{store: s, groups: languageGroups(language)}
}

// eachFile must be called for languages in the order of the summaries it was created with
func (f *boundedSummaryFiles) eachFile(summary *LanguageSummary, fn func(*FileJob)) {
	if f.stream == nil {
		f.stream = f.store.sorted(func(r boundedRecord) fileSortKey {
			return newFileSortKey(f.groups[r.job.Language], summarySortKey, r.job, r.seq)
		})
	}
	emitGroup(f.stream, f.groups[summary.Name], fn)
}

func (f *boundedSummaryFiles) lineLengthStats(summary *LanguageSummary) (int, int) {
	l := f.store.languages[summary.Name]
	if l.lineLengthCount == 0 {
		return 0, 0
	}
	return l.lineLengthMax, l.lineLengthSum / l.lineLengthCount
}

func (f *boundedSummaryFiles) close() {
	if f.stream != nil {
		f.stream.close()
	}
}

// recordStream yields records in sort key order. The record emit passes to fn is no longer
// held by the stream once fn returns.
type recordStream interface {
	peek() (*fileSortKey, bool)
	emit(fn func(*FileJob))
	close()
}

func emitAll(stream recordStream, fn func(*FileJob)) {
	for _, ok := stream.peek(); ok; _, ok = stream.peek() {
		stream.emit(fn)
	}
}

func emitGroup(stream recordStream, group int, fn func(*FileJob)) {
	for key, ok := stream.peek(); ok && key.group == group; key, ok = stream.peek() {
		stream.emit(fn)
	}
}

type memoryStream struct {
	store   *boundedStore
	records []boundedRecord
	key     func(boundedRecord) fileSortKey
	head    fileSortKey
}

func (m *memoryStream) peek() (*fileSortKey, bool) {
	if len(m.records) == 0 {
		return nil, false
	}
	m.head = m.key(m.records[0])
	return &m.head, true
}

func (m *memoryStream) emit(fn func(*FileJob)) {
	job := m.records[0].job
	m.records = m.records[1:]
	fn(m.store.prepare(job))
}

func (m *memoryStream) close() {}

type runReader struct {
	r    *bufio.Reader
	left uint64 // entries whose key has not been read yet
	key  fileSortKey
}

// advance reads the key of the next entry, leaving its record unread
func (run *runReader) advance() bool {
	if run.left == 0 {
		return false
	}
	if err := readFileSortKey(run.r, &run.key); err != nil {
		boundedFatal(err)
	}
	run.left--
	return true
}

type mergeStream struct {
	store *boundedStore
	runs  []*runReader
	file  *os.File // removed on close when set
}

func (m *mergeStream) minRun() int {
	best := 0
	for i := 1; i < len(m.runs); i++ {
		if compareFileSortKeys(&m.runs[i].key, &m.runs[best].key) < 0 {
			best = i
		}
	}
	return best
}

func (m *mergeStream) advance(i int) {
	if !m.runs[i].advance() {
		m.runs = slices.Delete(m.runs, i, i+1)
	}
}

func (m *mergeStream) peek() (*fileSortKey, bool) {
	if len(m.runs) == 0 {
		return nil, false
	}
	return &m.runs[m.minRun()].key, true
}

func (m *mergeStream) emit(fn func(*FileJob)) {
	i := m.minRun()
	payload, err := readFramed(m.runs[i].r, &m.store.scratch)
	rec := decodeBoundedRecordOrFatal(payload, err)

	m.store.hold(1)
	fn(m.store.prepare(rec.job))
	m.store.release(1)

	m.advance(i)
}

// copyTo merges the runs into a single run without decoding any records
func (m *mergeStream) copyTo(w *runWriter) {
	for len(m.runs) != 0 {
		i := m.minRun()
		run := m.runs[i]

		w.buf = appendFileSortKey(w.buf[:0], &run.key)
		w.write(w.buf)

		n, err := binary.ReadUvarint(run.r)
		if err != nil {
			boundedFatal(err)
		}
		w.writeUvarint(n)
		w.copyN(run.r, n)
		w.count++

		m.advance(i)
	}
}

func (m *mergeStream) close() {
	if m.file != nil {
		removeRunFile(m.file)
	}
}

// runWriter writes sorted runs, each a fixed size header followed by entries of a sort
// key and the framed record it belongs to
type runWriter struct {
	f        *os.File
	w        *bufio.Writer
	off      int64
	runStart int64
	count    uint64
	buf      []byte
}

func (s *boundedStore) newRunWriter() *runWriter {
	f, err := os.CreateTemp(s.dir, "scc-bounded-memory-*.run")
	if err != nil {
		boundedFatal(err)
	}
	return &runWriter{f: f, w: bufio.NewWriter(f)}
}

// write leaves errors to be reported when the buffer is flushed
func (w *runWriter) write(p []byte) {
	_, _ = w.w.Write(p)
	w.off += int64(len(p))
}

func (w *runWriter) writeUvarint(n uint64) {
	var b [binary.MaxVarintLen64]byte
	w.write(b[:binary.PutUvarint(b[:], n)])
}

func (w *runWriter) copyN(r io.Reader, n uint64) {
	if _, err := io.CopyN(w.w, r, int64(n)); err != nil {
		boundedFatal(err)
	}
	w.off += int64(n)
}

func (w *runWriter) writeEntry(key *fileSortKey, rec boundedRecord) {
	w.buf = appendFileSortKey(w.buf[:0], key)
	w.write(w.buf)
	w.buf = appendBoundedRecord(w.buf[:0], rec)
	w.writeUvarint(uint64(len(w.buf)))
	w.write(w.buf)
	w.count++
}

func (w *runWriter) beginRun() {
	w.runStart = w.off
	w.count = 0
	w.write(make([]byte, boundedRunHeaderSize))
}

func (w *runWriter) endRun() {
	if err := w.w.Flush(); err != nil {
		boundedFatal(err)
	}

	var header [boundedRunHeaderSize]byte
	binary.LittleEndian.PutUint64(header[:8], w.count)
	binary.LittleEndian.PutUint64(header[8:], uint64(w.off-w.runStart-boundedRunHeaderSize))
	if _, err := w.f.WriteAt(header[:], w.runStart); err != nil {
		boundedFatal(err)
	}
}

func (w *runWriter) finish() *os.File {
	if err := w.w.Flush(); err != nil {
		boundedFatal(err)
	}
	return w.f
}

func removeRunFile(f *os.File) {
	_ = f.Close()
	_ = os.Remove(f.Name())
}

func writeFramed(w *bufio.Writer, p []byte) {
	var b [binary.MaxVarintLen64]byte
	_, _ = w.Write(b[:binary.PutUvarint(b[:], uint64(len(p)))])
	_, _ = w.Write(p)
}

// readFramed returns io.EOF only when there are no more frames
func readFramed(r *bufio.Reader, buf *[]byte) ([]byte, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if n > boundedMaxFrame {
		return nil, errCorruptSpill
	}

	if uint64(cap(*buf)) < n {
		*buf = make([]byte, n)
	}
	p := (*buf)[:n]
	if _, err := io.ReadFull(r, p); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return p, nil
}

func appendSpillString(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func readSpillString(r *bufio.Reader) (string, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return "", err
	}
	if n > boundedMaxFrame {
		return "", errCorruptSpill
	}

	var sb strings.Builder
	sb.Grow(int(n))
	if _, err := io.CopyN(&sb, r, int64(n)); err != nil {
		return "", err
	}
	return sb.String(), nil
}

func appendFileSortKey(b []byte, k *fileSortKey) []byte {
	b = binary.AppendVarint(b, int64(k.group))
	b = binary.AppendVarint(b, k.num)
	b = appendSpillString(b, k.str)
	b = appendSpillString(b, k.loc)
	return binary.AppendUvarint(b, k.seq)
}

func readFileSortKey(r *bufio.Reader, k *fileSortKey) error {
	group, err := binary.ReadVarint(r)
	if err != nil {
		return err
	}
	k.group = int(group)

	if k.num, err = binary.ReadVarint(r); err != nil {
		return err
	}
	if k.str, err = readSpillString(r); err != nil {
		return err
	}
	if k.loc, err = readSpillString(r); err != nil {
		return err
	}
	k.seq, err = binary.ReadUvarint(r)
	return err
}

// appendBoundedRecord encodes every field of the job that any formatter outputs
func appendBoundedRecord(b []byte, r boundedRecord) []byte {
	job := r.job

	b = binary.AppendUvarint(b, r.seq)
	b = appendSpillString(b, job.Language)
	if job.PossibleLanguages == nil {
		b = binary.AppendUvarint(b, 0)
	} else {
		b = binary.AppendUvarint(b, uint64(len(job.PossibleLanguages))+1)
		for _, l := range job.PossibleLanguages {
			b = appendSpillString(b, l)
		}
	}
	b = appendSpillString(b, job.Filename)
	b = appendSpillString(b, job.Extension)
	b = appendSpillString(b, job.Location)
	b = appendSpillString(b, job.Symlocation)
	b = binary.AppendVarint(b, job.Bytes)
	b = binary.AppendVarint(b, job.Lines)
	b = binary.AppendVarint(b, job.Code)
	b = binary.AppendVarint(b, job.Comment)
	b = binary.AppendVarint(b, job.Blank)
	b = binary.AppendVarint(b, job.Complexity)
	b = binary.AppendVarint(b, int64(job.EndPoint))
	b = binary.AppendVarint(b, int64(job.Uloc))

	var flags byte
	if job.Binary {
		flags |= boundedFlagBinary
	}
	if job.Minified {
		flags |= boundedFlagMinified
	}
	if job.Generated {
		flags |= boundedFlagGenerated
	}
	if job.Hash != nil {
		flags |= boundedFlagHash
	}
	return append(b, flags)
}

type spillDecoder struct {
	b   []byte
	err error
}

func (d *spillDecoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.err = errCorruptSpill
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *spillDecoder) varint() int64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Varint(d.b)
	if n <= 0 {
		d.err = errCorruptSpill
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *spillDecoder) string() string {
	n := d.uvarint()
	if d.err != nil {
		return ""
	}
	if uint64(len(d.b)) < n {
		d.err = errCorruptSpill
		return ""
	}
	s := string(d.b[:n])
	d.b = d.b[n:]
	return s
}

func (d *spillDecoder) byte() byte {
	if d.err != nil {
		return 0
	}
	if len(d.b) == 0 {
		d.err = errCorruptSpill
		return 0
	}
	v := d.b[0]
	d.b = d.b[1:]
	return v
}

func decodeBoundedRecord(b []byte) (boundedRecord, error) {
	d := spillDecoder{b: b}
	job := &FileJob{}
	rec := boundedRecord{seq: d.uvarint(), job: job}

	job.Language = d.string()
	if n := d.uvarint(); n != 0 {
		job.PossibleLanguages = []string{}
		for range n - 1 {
			if d.err != nil {
				break
			}
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

	flags := d.byte()
	job.Binary = flags&boundedFlagBinary != 0
	job.Minified = flags&boundedFlagMinified != 0
	job.Generated = flags&boundedFlagGenerated != 0
	if flags&boundedFlagHash != 0 {
		job.Hash = boundedHash()
	}

	if d.err == nil && len(d.b) != 0 {
		d.err = errCorruptSpill
	}
	return rec, d.err
}

func decodeBoundedRecordOrFatal(payload []byte, err error) boundedRecord {
	if err != nil {
		boundedFatal(err)
	}
	rec, err := decodeBoundedRecord(payload)
	if err != nil {
		boundedFatal(err)
	}
	return rec
}
