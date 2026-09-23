package interp

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

// embedFS is the interpreter implementation of embed.FS: a read-only
// collection of files, initialized by a //go:embed directive.
// As for embed.FS, the zero value is an empty file system.
type embedFS struct {
	root *embedFile
}

var (
	_ fs.ReadDirFS  = embedFS{}
	_ fs.ReadFileFS = embedFS{}
)

// embedFile is a file or a directory of an embedFS. It implements both
// fs.FileInfo and fs.DirEntry.
type embedFile struct {
	name    string       // base name
	data    string       // file content
	dir     bool         // true if directory
	entries []*embedFile // directory entries, sorted by name
}

var emptyEmbedRoot = &embedFile{name: ".", dir: true}

// newEmbedFS returns a file system holding files, indexed by slash separated
// paths. Parent directories are created implicitly.
func newEmbedFS(files map[string]string) embedFS {
	root := &embedFile{name: ".", dir: true}
	dirs := map[string]*embedFile{".": root}

	var dirOf func(name string) *embedFile
	dirOf = func(name string) *embedFile {
		if d, ok := dirs[name]; ok {
			return d
		}
		parent := dirOf(path.Dir(name))
		d := &embedFile{name: path.Base(name), dir: true}
		parent.entries = append(parent.entries, d)
		dirs[name] = d
		return d
	}

	for name, data := range files {
		d := dirOf(path.Dir(name))
		d.entries = append(d.entries, &embedFile{name: path.Base(name), data: data})
	}
	for _, d := range dirs {
		sort.Slice(d.entries, func(i, j int) bool { return d.entries[i].name < d.entries[j].name })
	}
	return embedFS{root: root}
}

func (f embedFS) lookup(name string) *embedFile {
	if !fs.ValidPath(name) {
		return nil
	}
	file := f.root
	if file == nil {
		file = emptyEmbedRoot
	}
	if name == "." {
		return file
	}
	for _, elem := range strings.Split(name, "/") {
		entries := file.entries
		i := sort.Search(len(entries), func(i int) bool { return entries[i].name >= elem })
		if i == len(entries) || entries[i].name != elem {
			return nil
		}
		file = entries[i]
	}
	return file
}

// Open opens the named file for reading. Directories implement fs.ReadDirFile.
func (f embedFS) Open(name string) (fs.File, error) {
	file := f.lookup(name)
	if file == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if file.dir {
		return &embedOpenDir{file: file, path: name}, nil
	}
	return &embedOpenFile{file: file, path: name}, nil
}

// ReadDir reads the named directory and returns its entries sorted by name.
func (f embedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	dir, ok := file.(*embedOpenDir)
	if !ok {
		return nil, embedReadError(name, errors.New("not a directory"))
	}
	list := make([]fs.DirEntry, len(dir.file.entries))
	for i, e := range dir.file.entries {
		list[i] = e
	}
	return list, nil
}

// ReadFile reads and returns the content of the named file.
// The returned slice is a copy which can be modified by the caller.
func (f embedFS) ReadFile(name string) ([]byte, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	ofile, ok := file.(*embedOpenFile)
	if !ok {
		return nil, embedReadError(name, errEmbedIsDir)
	}
	return []byte(ofile.file.data), nil
}

var errEmbedIsDir = errors.New("is a directory")

func embedReadError(path string, err error) error {
	return &fs.PathError{Op: "read", Path: path, Err: err}
}

func (f *embedFile) Name() string               { return f.name }
func (f *embedFile) Size() int64                { return int64(len(f.data)) }
func (f *embedFile) ModTime() time.Time         { return time.Time{} }
func (f *embedFile) IsDir() bool                { return f.dir }
func (f *embedFile) Sys() interface{}           { return nil }
func (f *embedFile) Type() fs.FileMode          { return f.Mode().Type() }
func (f *embedFile) Info() (fs.FileInfo, error) { return f, nil }
func (f *embedFile) String() string             { return fs.FormatFileInfo(f) }

func (f *embedFile) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}

// embedOpenFile is a regular file of an embedFS, opened for reading.
type embedOpenFile struct {
	file   *embedFile
	path   string
	offset int64
}

var (
	_ io.Seeker   = (*embedOpenFile)(nil)
	_ io.ReaderAt = (*embedOpenFile)(nil)
)

func (f *embedOpenFile) Close() error               { return nil }
func (f *embedOpenFile) Stat() (fs.FileInfo, error) { return f.file, nil }

func (f *embedOpenFile) Read(b []byte) (int, error) {
	if f.offset >= int64(len(f.file.data)) {
		return 0, io.EOF
	}
	if f.offset < 0 {
		return 0, embedReadError(f.path, fs.ErrInvalid)
	}
	n := copy(b, f.file.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *embedOpenFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += f.offset
	case io.SeekEnd:
		offset += int64(len(f.file.data))
	}
	if offset < 0 || offset > int64(len(f.file.data)) {
		return 0, &fs.PathError{Op: "seek", Path: f.path, Err: fs.ErrInvalid}
	}
	f.offset = offset
	return offset, nil
}

func (f *embedOpenFile) ReadAt(b []byte, offset int64) (int, error) {
	if offset < 0 || offset > int64(len(f.file.data)) {
		return 0, embedReadError(f.path, fs.ErrInvalid)
	}
	n := copy(b, f.file.data[offset:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

// embedOpenDir is a directory of an embedFS, opened for reading.
type embedOpenDir struct {
	file   *embedFile
	path   string
	offset int // number of entries already read
}

var _ fs.ReadDirFile = (*embedOpenDir)(nil)

func (d *embedOpenDir) Close() error               { return nil }
func (d *embedOpenDir) Stat() (fs.FileInfo, error) { return d.file, nil }

func (d *embedOpenDir) Read([]byte) (int, error) {
	return 0, embedReadError(d.path, errEmbedIsDir)
}

func (d *embedOpenDir) ReadDir(count int) ([]fs.DirEntry, error) {
	n := len(d.file.entries) - d.offset
	if n == 0 {
		if count <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if count > 0 && n > count {
		n = count
	}
	list := make([]fs.DirEntry, n)
	for i := range list {
		list[i] = d.file.entries[d.offset+i]
	}
	d.offset += n
	return list, nil
}
