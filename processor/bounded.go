// SPDX-License-Identifier: MIT

package processor

import (
	"bufio"
	"bytes"
	"cmp"
	"container/heap"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	jsoniter "github.com/json-iterator/go"
)

// maximum number of spill runs merged at once, keeps the number of open files reasonable
const boundedMaxMergeFanIn = 64

var boundedJSON = jsoniter.ConfigCompatibleWithStandardLibrary

// absolute forms of BoundedMemoryDir, files inside are never counted
var boundedMemoryDirs []string

func validateBoundedMemory() error {
	if BoundedMemoryDir == "" {
		return errors.New("--bounded-memory-dir is required when --bounded-memory is enabled")
	}
	if BoundedMemoryMaxInMemoryFiles <= 0 {
		return errors.New("--bounded-memory-max-in-memory-files must be greater than 0 when --bounded-memory is enabled")
	}
	return nil
}

func setupBoundedMemory() error {
	if err := validateBoundedMemory(); err != nil {
		return err
	}

	if err := os.MkdirAll(BoundedMemoryDir, 0755); err != nil {
		return fmt.Errorf("unable to create --bounded-memory-dir %s: %w", BoundedMemoryDir, err)
	}

	boundedMemoryDirs = nil
	abs, err := filepath.Abs(BoundedMemoryDir)
	if err != nil {
		return fmt.Errorf("unable to resolve --bounded-memory-dir %s: %w", BoundedMemoryDir, err)
	}
	boundedMemoryDirs = append(boundedMemoryDirs, abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && resolved != abs {
		boundedMemoryDirs = append(boundedMemoryDirs, resolved)
	}

	return nil
}

func inBoundedMemoryDir(path string) bool {
	if len(boundedMemoryDirs) == 0 {
		return false
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	for _, dir := range boundedMemoryDirs {
		if abs == dir || strings.HasPrefix(abs, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator)) {
			return true
		}
	}

	return false
}

// boundedRecord mirrors the JSON visible fields of FileJob, in the same order, so that
// marshalling it produces the same bytes as marshalling the original FileJob
type boundedRecord struct {
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
	Hash               json.RawMessage
	Binary             bool
	Minified           bool
	Generated          bool
	EndPoint           int
	Uloc               int
}

type boundedEntry struct {
	Seq    int64         `json:"q"`
	Record boundedRecord `json:"r"`

	job       *FileJob
	csvRecord []string
}

func newBoundedEntry(seq int64, res *FileJob) *boundedEntry {
	var hashJSON json.RawMessage
	if res.Hash != nil {
		hashJSON, _ = boundedJSON.Marshal(res.Hash)
	}

	return &boundedEntry{
		Seq: seq,
		Record: boundedRecord{
			Language:           res.Language,
			PossibleLanguages:  res.PossibleLanguages,
			Filename:           res.Filename,
			Extension:          res.Extension,
			Location:           res.Location,
			Symlocation:        res.Symlocation,
			Bytes:              res.Bytes,
			Lines:              res.Lines,
			Code:               res.Code,
			Comment:            res.Comment,
			Blank:              res.Blank,
			Complexity:         res.Complexity,
			WeightedComplexity: res.WeightedComplexity,
			Hash:               hashJSON,
			Binary:             res.Binary,
			Minified:           res.Minified,
			Generated:          res.Generated,
			EndPoint:           res.EndPoint,
			Uloc:               res.Uloc,
		},
	}
}

func fileWeightedComplexity(complexity, code int64) float64 {
	if code == 0 {
		return 0
	}
	return (float64(complexity) / float64(code)) * 100
}

// toFileJob rebuilds the FileJob, weighted mirrors the wide formatter which overwrites
// the WeightedComplexity of every file it sees
func (en *boundedEntry) toFileJob(weighted bool) *FileJob {
	r := &en.Record
	fj := &FileJob{
		Language:           r.Language,
		PossibleLanguages:  r.PossibleLanguages,
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
	}
	if weighted {
		fj.WeightedComplexity = fileWeightedComplexity(r.Complexity, r.Code)
	}
	return fj
}

func (en *boundedEntry) outputRecord(weighted bool) boundedRecord {
	r := en.Record
	if len(r.Hash) == 0 {
		r.Hash = json.RawMessage("null")
	}
	if weighted {
		r.WeightedComplexity = fileWeightedComplexity(r.Complexity, r.Code)
	}
	return r
}

func (en *boundedEntry) fileJob() *FileJob {
	if en.job == nil {
		en.job = en.toFileJob(false)
	}
	return en.job
}

func (en *boundedEntry) csv() []string {
	if en.csvRecord == nil {
		en.csvRecord = csvFilesRecord(en.fileJob())
	}
	return en.csvRecord
}

func writeBoundedEntry(w *bufio.Writer, en *boundedEntry) error {
	b, err := boundedJSON.Marshal(en)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	return w.WriteByte('\n')
}

type boundedReader struct {
	file   *os.File
	reader *bufio.Reader
}

func openBoundedReader(path string) (*boundedReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &boundedReader{file: f, reader: bufio.NewReader(f)}, nil
}

// next returns nil once the file is exhausted
func (r *boundedReader) next() (*boundedEntry, error) {
	line, err := r.reader.ReadBytes('\n')
	if len(line) == 0 {
		if err == io.EOF {
			return nil, nil
		}
		return nil, err
	}
	if err != nil && err != io.EOF {
		return nil, err
	}

	en := &boundedEntry{}
	if err := boundedJSON.Unmarshal(line, en); err != nil {
		return nil, err
	}
	return en, nil
}

func (r *boundedReader) close() {
	_ = r.file.Close()
}

// boundedSource yields entries in a fixed order, the entries are transient and only
// retained if the consumer keeps them
type boundedSource func(yield func(*boundedEntry) error) error

func memorySource(entries []*boundedEntry, keep func(*boundedEntry) bool) boundedSource {
	return func(yield func(*boundedEntry) error) error {
		for _, en := range entries {
			if keep != nil && !keep(en) {
				continue
			}
			if err := yield(en); err != nil {
				return err
			}
		}
		return nil
	}
}

func fileSource(path string) boundedSource {
	return func(yield func(*boundedEntry) error) error {
		r, err := openBoundedReader(path)
		if err != nil {
			return err
		}
		defer r.close()

		for {
			en, err := r.next()
			if err != nil {
				return err
			}
			if en == nil {
				return nil
			}
			if err := yield(en); err != nil {
				return err
			}
		}
	}
}

type boundedLanguage struct {
	summary   LanguageSummary
	maxLine   int
	lineSum   int
	lineCount int
}

type boundedEngine struct {
	dir        string
	max        int
	spills     int
	peak       int
	memory     []*boundedEntry // every entry when nothing had to be spilled
	spillPath  string          // every entry in arrival order once anything was spilled
	languages  map[string]*boundedLanguage
	totals     summaryTotals
	partitions map[string]string
	tempFiles  []string
	wideSeen   bool
}

func newBoundedEngine(dir string, maxInMemory int) *boundedEngine {
	return &boundedEngine{
		dir:       dir,
		max:       maxInMemory,
		languages: map[string]*boundedLanguage{},
	}
}

func (e *boundedEngine) track(inMemory int) {
	e.peak = max(e.peak, inMemory)
}

func (e *boundedEngine) check(err error) {
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "unable to spill results to %s: %s\n", e.dir, err)
		os.Exit(1)
	}
}

func (e *boundedEngine) createTemp(pattern string) (*os.File, error) {
	if err := os.MkdirAll(e.dir, 0755); err != nil {
		return nil, err
	}
	return os.CreateTemp(e.dir, pattern)
}

func (e *boundedEngine) aggregate(res *FileJob) {
	lang, ok := e.languages[res.Language]
	if !ok {
		lang = &boundedLanguage{summary: LanguageSummary{Name: res.Language}}
		e.languages[res.Language] = lang
	}

	weighted := fileWeightedComplexity(res.Complexity, res.Code)

	s := &lang.summary
	s.Count++
	s.Lines += res.Lines
	s.Code += res.Code
	s.Comment += res.Comment
	s.Blank += res.Blank
	s.Complexity += res.Complexity
	s.Bytes += res.Bytes
	s.WeightedComplexity += weighted

	for _, l := range res.LineLength {
		lang.maxLine = max(lang.maxLine, l)
		lang.lineSum += l
	}
	lang.lineCount += len(res.LineLength)

	e.totals.Files++
	e.totals.Lines += res.Lines
	e.totals.Code += res.Code
	e.totals.Comment += res.Comment
	e.totals.Blank += res.Blank
	e.totals.Complexity += res.Complexity
	e.totals.Bytes += res.Bytes
	e.totals.WeightedComplexity += weighted
}

// ingest consumes every result, aggregating language totals and holding at most max
// records in memory by appending full buffers to the spill file
func (e *boundedEngine) ingest(input chan *FileJob) error {
	var spill *os.File
	var w *bufio.Writer
	buffer := make([]*boundedEntry, 0, min(e.max, 4096))

	flush := func() error {
		if spill == nil {
			f, err := e.createTemp("scc-bounded-memory-*.jsonl")
			if err != nil {
				return err
			}
			spill = f
			e.spillPath = f.Name()
			w = bufio.NewWriter(f)
		}

		for i, en := range buffer {
			if err := writeBoundedEntry(w, en); err != nil {
				return err
			}
			buffer[i] = nil
		}
		buffer = buffer[:0]
		e.spills++

		return w.Flush()
	}

	var seq int64
	for res := range input {
		e.aggregate(res)

		if len(buffer) == e.max {
			if err := flush(); err != nil {
				for range input {
				}
				return err
			}
		}

		buffer = append(buffer, newBoundedEntry(seq, res))
		seq++
		e.track(len(buffer))
	}

	if spill == nil {
		e.memory = buffer
		return nil
	}

	if len(buffer) > 0 {
		if err := flush(); err != nil {
			return err
		}
	}

	return spill.Close()
}

func (e *boundedEngine) allSource() boundedSource {
	if e.spillPath == "" {
		return memorySource(e.memory, nil)
	}
	return fileSource(e.spillPath)
}

// languageSource yields the entries of one language in arrival order
func (e *boundedEngine) languageSource(name string) boundedSource {
	if e.spillPath == "" {
		return memorySource(e.memory, func(en *boundedEntry) bool {
			return en.Record.Language == name
		})
	}

	if e.partitions == nil {
		e.check(e.partition())
	}

	path, ok := e.partitions[name]
	if !ok {
		return memorySource(nil, nil)
	}
	return fileSource(path)
}

// partition splits the spill file into one file per language preserving arrival order
func (e *boundedEngine) partition() error {
	e.partitions = map[string]string{}
	files := map[string]*os.File{}
	writers := map[string]*bufio.Writer{}

	err := e.allSource()(func(en *boundedEntry) error {
		w, ok := writers[en.Record.Language]
		if !ok {
			f, err := e.createTemp("scc-bounded-memory-lang-*.jsonl")
			if err != nil {
				return err
			}
			e.tempFiles = append(e.tempFiles, f.Name())
			e.partitions[en.Record.Language] = f.Name()
			files[en.Record.Language] = f
			w = bufio.NewWriter(f)
			writers[en.Record.Language] = w
		}
		return writeBoundedEntry(w, en)
	})

	for lang, f := range files {
		if flushErr := writers[lang].Flush(); flushErr != nil && err == nil {
			err = flushErr
		}
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}

	return err
}

func (e *boundedEngine) writeRun(entries []*boundedEntry) (string, error) {
	f, err := e.createTemp("scc-bounded-memory-run-*.jsonl")
	if err != nil {
		return "", err
	}

	w := bufio.NewWriter(f)
	for _, en := range entries {
		if err := writeBoundedEntry(w, en); err != nil {
			_ = f.Close()
			return f.Name(), err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return f.Name(), err
	}
	e.spills++

	return f.Name(), f.Close()
}

type boundedHeap struct {
	heads   []*boundedEntry
	readers []int
	compare func(a, b *boundedEntry) int
}

func (h *boundedHeap) Len() int           { return len(h.heads) }
func (h *boundedHeap) Less(i, j int) bool { return h.compare(h.heads[i], h.heads[j]) < 0 }
func (h *boundedHeap) Swap(i, j int) {
	h.heads[i], h.heads[j] = h.heads[j], h.heads[i]
	h.readers[i], h.readers[j] = h.readers[j], h.readers[i]
}
func (h *boundedHeap) Push(x any) {
	p := x.(boundedHead)
	h.heads = append(h.heads, p.entry)
	h.readers = append(h.readers, p.reader)
}
func (h *boundedHeap) Pop() any {
	n := len(h.heads) - 1
	p := boundedHead{entry: h.heads[n], reader: h.readers[n]}
	h.heads[n] = nil
	h.heads = h.heads[:n]
	h.readers = h.readers[:n]
	return p
}

type boundedHead struct {
	entry  *boundedEntry
	reader int
}

// merge combines sorted runs holding only the current head of each run in memory
func (e *boundedEngine) merge(runs []string, compare func(a, b *boundedEntry) int, out func(*boundedEntry) error) error {
	readers := make([]*boundedReader, 0, len(runs))
	defer func() {
		for _, r := range readers {
			r.close()
		}
	}()

	h := &boundedHeap{compare: compare}
	for i, run := range runs {
		r, err := openBoundedReader(run)
		if err != nil {
			return err
		}
		readers = append(readers, r)

		en, err := r.next()
		if err != nil {
			return err
		}
		if en != nil {
			heap.Push(h, boundedHead{entry: en, reader: i})
		}
	}
	e.track(h.Len())

	for h.Len() > 0 {
		p := heap.Pop(h).(boundedHead)
		if err := out(p.entry); err != nil {
			return err
		}

		en, err := readers[p.reader].next()
		if err != nil {
			return err
		}
		if en != nil {
			heap.Push(h, boundedHead{entry: en, reader: p.reader})
		}
	}

	return nil
}

// sorted emits the entries of src ordered by compare (ties in arrival order) using an
// external merge sort that never holds more than max entries in memory
func (e *boundedEngine) sorted(src boundedSource, compare func(a, b *boundedEntry) int, emit func(*boundedEntry)) error {
	total := func(a, b *boundedEntry) int {
		if order := compare(a, b); order != 0 {
			return order
		}
		return cmp.Compare(a.Seq, b.Seq)
	}

	// merging needs at least two heads in memory, so with a single slot select one entry per pass instead
	if e.max == 1 {
		return e.selectionSorted(src, total, emit)
	}

	var runs []string
	defer func() {
		for _, run := range runs {
			_ = os.Remove(run)
		}
	}()

	chunk := make([]*boundedEntry, 0, min(e.max, 4096))
	err := src(func(en *boundedEntry) error {
		if len(chunk) == e.max {
			slices.SortFunc(chunk, total)
			run, err := e.writeRun(chunk)
			if run != "" {
				runs = append(runs, run)
			}
			if err != nil {
				return err
			}
			clear(chunk)
			chunk = chunk[:0]
		}
		chunk = append(chunk, en)
		e.track(len(chunk))
		return nil
	})
	if err != nil {
		return err
	}

	slices.SortFunc(chunk, total)
	if len(runs) == 0 {
		for _, en := range chunk {
			emit(en)
		}
		return nil
	}

	if len(chunk) > 0 {
		run, err := e.writeRun(chunk)
		if run != "" {
			runs = append(runs, run)
		}
		if err != nil {
			return err
		}
	}
	chunk = nil

	fanIn := min(e.max, boundedMaxMergeFanIn)
	for len(runs) > fanIn {
		var next []string
		for i := 0; i < len(runs); i += fanIn {
			group := runs[i:min(i+fanIn, len(runs))]
			if len(group) == 1 {
				next = append(next, group[0])
				continue
			}

			merged, err := e.mergeToRun(group, total)
			if merged != "" {
				next = append(next, merged)
			}
			for _, run := range group {
				_ = os.Remove(run)
			}
			if err != nil {
				runs = append(next, runs[min(i+fanIn, len(runs)):]...)
				return err
			}
		}
		runs = next
	}

	return e.merge(runs, total, func(en *boundedEntry) error {
		emit(en)
		return nil
	})
}

func (e *boundedEngine) mergeToRun(runs []string, compare func(a, b *boundedEntry) int) (string, error) {
	f, err := e.createTemp("scc-bounded-memory-run-*.jsonl")
	if err != nil {
		return "", err
	}

	w := bufio.NewWriter(f)
	err = e.merge(runs, compare, func(en *boundedEntry) error {
		return writeBoundedEntry(w, en)
	})
	if err == nil {
		err = w.Flush()
	}
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	e.spills++

	return f.Name(), err
}

func (e *boundedEngine) selectionSorted(src boundedSource, total func(a, b *boundedEntry) int, emit func(*boundedEntry)) error {
	var last *boundedEntry
	for {
		var best *boundedEntry
		err := src(func(en *boundedEntry) error {
			if last != nil && total(en, last) <= 0 {
				return nil
			}
			if best == nil || total(en, best) < 0 {
				best = en
			}
			return nil
		})
		if err != nil {
			return err
		}
		if best == nil {
			return nil
		}

		e.track(1)
		emit(best)
		last = best
	}
}

// channel streams entries into the existing formatters which do not retain files
func (e *boundedEngine) channel(produce func(emit func(*boundedEntry)) error) chan *FileJob {
	ch := make(chan *FileJob)
	weighted := e.wideSeen
	go func() {
		defer close(ch)
		e.check(produce(func(en *boundedEntry) {
			ch <- en.toFileJob(weighted)
		}))
	}()
	return ch
}

func (e *boundedEngine) all(emit func(*boundedEntry)) error {
	return e.allSource()(func(en *boundedEntry) error {
		emit(en)
		return nil
	})
}

func (e *boundedEngine) csvSorted(emit func(*boundedEntry)) error {
	sortFunc := getCSVFilesTotalSortFunc(SortBy)
	return e.sorted(e.allSource(), func(a, b *boundedEntry) int {
		return sortFunc(a.csv(), b.csv())
	}, emit)
}

// summaries builds the per language summaries populating only the fields the formatter would
func (e *boundedEngine) summaries(fill func(lang *boundedLanguage) LanguageSummary) []LanguageSummary {
	language := make([]LanguageSummary, 0, len(e.languages))
	for _, lang := range e.languages {
		language = append(language, fill(lang))
	}
	return sortLanguageSummary(language)
}

func tabularSummary(lang *boundedLanguage) LanguageSummary {
	s := lang.summary
	return LanguageSummary{
		Name:       s.Name,
		Lines:      s.Lines,
		Code:       s.Code,
		Comment:    s.Comment,
		Blank:      s.Blank,
		Complexity: s.Complexity,
		Count:      s.Count,
	}
}

func wideSummary(lang *boundedLanguage) LanguageSummary {
	summary := tabularSummary(lang)
	summary.WeightedComplexity = lang.summary.WeightedComplexity
	return summary
}

func htmlSummary(lang *boundedLanguage) LanguageSummary {
	summary := tabularSummary(lang)
	summary.Bytes = lang.summary.Bytes
	return summary
}

func jsonSummary(lang *boundedLanguage) LanguageSummary {
	summary := htmlSummary(lang)
	summary.Files = []*FileJob{}
	summary.ULOC = len(ulocLanguageCount[summary.Name])
	return summary
}

func (e *boundedEngine) lineLength(summary LanguageSummary) (int, int) {
	lang := e.languages[summary.Name]
	if lang == nil || lang.lineCount == 0 {
		return 0, 0
	}
	return lang.maxLine, lang.lineSum / lang.lineCount
}

func (e *boundedEngine) languageFiles(weighted bool) languageFilesFunc {
	sortFunc := getSummaryFilesSortFunc(SortBy)
	return func(summary LanguageSummary, emit func(*FileJob)) {
		e.check(e.sorted(e.languageSource(summary.Name), func(a, b *boundedEntry) int {
			return sortFunc(a.fileJob(), b.fileJob())
		}, func(en *boundedEntry) {
			emit(en.toFileJob(weighted))
		}))
	}
}

// writeJSONLanguages writes the same bytes as marshalling the summaries with their Files
// populated, streaming the files of each language from the spill
func (e *boundedEngine) writeJSONLanguages(str *strings.Builder, language []LanguageSummary) {
	const filesKey = `"Files":[`

	str.WriteString("[")
	for i, summary := range language {
		if i > 0 {
			str.WriteString(",")
		}

		summary.Files = []*FileJob{}
		b, _ := boundedJSON.Marshal(summary)
		idx := bytes.Index(b, []byte(filesKey+"]"))
		str.Write(b[:idx+len(filesKey)])

		count := 0
		e.check(e.languageSource(summary.Name)(func(en *boundedEntry) error {
			if count > 0 {
				str.WriteString(",")
			}
			count++
			rb, err := boundedJSON.Marshal(en.outputRecord(e.wideSeen))
			str.Write(rb)
			return err
		}))

		str.Write(b[idx+len(filesKey):])
	}
	str.WriteString("]")
}

func (e *boundedEngine) toJSONFiles() string {
	str := &strings.Builder{}
	e.writeJSONLanguages(str, e.summaries(jsonSummary))
	return str.String()
}

func (e *boundedEngine) toJSON2Files() string {
	const summaryKey = `"languageSummary":`

	language := e.summaries(jsonSummary)
	j2 := buildJSON2(language)
	j2.LanguageSummary = []LanguageSummary{}
	b, _ := boundedJSON.Marshal(j2)
	idx := bytes.Index(b, []byte(summaryKey+"[]"))

	str := &strings.Builder{}
	str.Write(b[:idx+len(summaryKey)])
	e.writeJSONLanguages(str, language)
	str.Write(b[idx+len(summaryKey)+2:])
	return str.String()
}

func (e *boundedEngine) toCSVFiles() string {
	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	_ = w.Write(csvFilesHeader())
	e.check(e.csvSorted(func(en *boundedEntry) {
		_ = w.Write(en.csv())
	}))
	w.Flush()
	return b.String()
}

func (e *boundedEngine) htmlTable() string {
	return renderHtmlTable(e.summaries(htmlSummary), e.totals, e.languageFiles(e.wideSeen))
}

func (e *boundedEngine) writeCSVStream(w io.Writer) {
	produce := e.all
	if SortBySet {
		produce = e.csvSorted
	}
	writeCSVStream(w, e.channel(produce))
}

func (e *boundedEngine) format(name string) string {
	switch name {
	case "tabular":
		return renderShort(e.summaries(tabularSummary), e.totals, e.languageFiles(e.wideSeen), e.lineLength)
	case "wide":
		e.wideSeen = true
		return renderLong(e.summaries(wideSummary), e.totals, e.languageFiles(true), e.lineLength)
	case "json":
		if Files {
			return e.toJSONFiles()
		}
		return toJSON(e.channel(e.all))
	case "json2":
		if Files {
			return e.toJSON2Files()
		}
		return toJSON2(e.channel(e.all))
	case "cloc-yaml", "cloc-yml":
		return toClocYAML(e.channel(e.all))
	case "csv":
		if Files {
			return e.toCSVFiles()
		}
		return toCSV(e.channel(e.all))
	case "html":
		return htmlDocument(e.htmlTable())
	case "html-table":
		return e.htmlTable()
	case "sql":
		return toSql(e.channel(e.all))
	case "sql-insert":
		return toSqlInsert(e.channel(e.all))
	case "openmetrics":
		return toOpenMetrics(e.channel(e.all))
	}
	return ""
}

func (e *boundedEngine) single() string {
	name := strings.ToLower(Format)
	if More {
		name = "wide"
	}

	switch name {
	case "csv-stream":
		e.writeCSVStream(os.Stdout)
		return ""
	case "wide", "json", "json2", "cloc-yaml", "cloc-yml", "csv", "html", "html-table", "sql", "sql-insert", "openmetrics":
		return e.format(name)
	}
	return e.format("tabular")
}

// multi mirrors fileSummarizeMulti, except csv-stream honours its destination
func (e *boundedEngine) multi() string {
	var str strings.Builder

	for s := range strings.SplitSeq(FormatMulti, ",") {
		t := strings.Split(s, ":")
		if len(t) != 2 {
			continue
		}

		name := strings.ToLower(t[0])
		if name == "csv-stream" {
			if t[1] == "stdout" {
				w := bufio.NewWriter(os.Stdout)
				e.writeCSVStream(w)
				_ = w.Flush()
				continue
			}

			if err := e.writeCSVStreamFile(t[1]); err != nil {
				fmt.Printf("%s unable to be written to for format %s: %s", t[1], t[0], err)
			}
			continue
		}

		val := e.format(name)

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

func (e *boundedEngine) writeCSVStreamFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(f)
	e.writeCSVStream(w)
	err = w.Flush()
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
}

// cleanup removes intermediate files, the spill file itself is left in place
func (e *boundedEngine) cleanup() {
	for _, f := range e.tempFiles {
		_ = os.Remove(f)
	}
	e.tempFiles = nil
}

func boundedFileSummarize(input chan *FileJob) string {
	if err := validateBoundedMemory(); err != nil {
		for range input {
		}
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	e := newBoundedEngine(BoundedMemoryDir, BoundedMemoryMaxInMemoryFiles)
	e.check(e.ingest(input))

	var result string
	if FormatMulti != "" {
		result = e.multi()
	} else {
		result = e.single()
	}
	e.cleanup()

	if BoundedMemoryStats {
		_, _ = fmt.Fprintf(os.Stderr, "bounded-memory: spills=%d peak_in_memory_files=%d max_in_memory_files=%d\n", e.spills, e.peak, e.max)
	}

	return result
}
