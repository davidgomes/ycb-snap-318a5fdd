// Package embed provides the embed.FS type used by the interpreter to
// implement the //go:embed directive.
package embed

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"time"
)

// FS is a read-only collection of files, usually initialized with a //go:embed directive.
// The zero value is an empty file system.
type FS struct {
	entries map[string]*entry
}

type entry struct {
	name     string // base name
	data     string
	isDir    bool
	children []*entry // sorted by name, for directories only
}

// New returns a FS containing the given files, indexed by slash-separated
// paths. Parent directories are created implicitly.
func New(files map[string][]byte) FS {
	root := &entry{name: ".", isDir: true}
	entries := map[string]*entry{".": root}

	var mkdir func(name string) *entry
	mkdir = func(name string) *entry {
		if e, ok := entries[name]; ok {
			return e
		}
		e := &entry{name: path.Base(name), isDir: true}
		entries[name] = e
		parent := mkdir(path.Dir(name))
		parent.children = append(parent.children, e)
		return e
	}

	for name, data := range files {
		e := &entry{name: path.Base(name), data: string(data)}
		entries[name] = e
		parent := mkdir(path.Dir(name))
		parent.children = append(parent.children, e)
	}

	for _, e := range entries {
		if e.isDir {
			sort.Slice(e.children, func(i, j int) bool { return e.children[i].name < e.children[j].name })
		}
	}
	return FS{entries: entries}
}

func (f FS) lookup(name string) *entry {
	if f.entries == nil {
		if name == "." {
			return &entry{name: ".", isDir: true}
		}
		return nil
	}
	return f.entries[name]
}

// Open opens the named file for reading and returns it as an fs.File.
// The returned file implements io.Seeker and io.ReaderAt when the file
// is not a directory, and fs.ReadDirFile when it is.
func (f FS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	e := f.lookup(name)
	if e == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if e.isDir {
		return &openDir{e: e, path: name}, nil
	}
	return &openFile{e: e, path: name}, nil
}

// ReadFile reads and returns the content of the named file.
func (f FS) ReadFile(name string) ([]byte, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	of, ok := file.(*openFile)
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
	}
	return []byte(of.e.data), nil
}

// ReadDir reads and returns the entire named directory, sorted by file name.
func (f FS) ReadDir(name string) ([]fs.DirEntry, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	dir, ok := file.(*openDir)
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("not a directory")}
	}
	list := make([]fs.DirEntry, len(dir.e.children))
	for i, c := range dir.e.children {
		list[i] = fileInfo{c}
	}
	return list, nil
}

// fileInfo implements fs.FileInfo and fs.DirEntry.
type fileInfo struct{ e *entry }

func (fi fileInfo) Name() string               { return fi.e.name }
func (fi fileInfo) Size() int64                { return int64(len(fi.e.data)) }
func (fi fileInfo) ModTime() time.Time         { return time.Time{} }
func (fi fileInfo) IsDir() bool                { return fi.e.isDir }
func (fi fileInfo) Sys() any                   { return nil }
func (fi fileInfo) Type() fs.FileMode          { return fi.Mode().Type() }
func (fi fileInfo) Info() (fs.FileInfo, error) { return fi, nil }
func (fi fileInfo) String() string             { return fs.FormatFileInfo(fi) }

func (fi fileInfo) Mode() fs.FileMode {
	if fi.e.isDir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}

type openFile struct {
	e      *entry
	path   string
	offset int64
}

var (
	_ io.Seeker   = (*openFile)(nil)
	_ io.ReaderAt = (*openFile)(nil)
)

func (f *openFile) Close() error               { return nil }
func (f *openFile) Stat() (fs.FileInfo, error) { return fileInfo{f.e}, nil }

func (f *openFile) Read(b []byte) (int, error) {
	if f.offset >= int64(len(f.e.data)) {
		return 0, io.EOF
	}
	if f.offset < 0 {
		return 0, &fs.PathError{Op: "read", Path: f.path, Err: fs.ErrInvalid}
	}
	n := copy(b, f.e.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *openFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.offset
	case io.SeekEnd:
		offset += int64(len(f.e.data))
	default:
		return 0, &fs.PathError{Op: "seek", Path: f.path, Err: fs.ErrInvalid}
	}
	if offset < 0 {
		return 0, &fs.PathError{Op: "seek", Path: f.path, Err: fs.ErrInvalid}
	}
	f.offset = offset
	return offset, nil
}

func (f *openFile) ReadAt(b []byte, offset int64) (int, error) {
	if offset < 0 || offset > int64(len(f.e.data)) {
		return 0, &fs.PathError{Op: "read", Path: f.path, Err: fs.ErrInvalid}
	}
	n := copy(b, f.e.data[offset:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

type openDir struct {
	e      *entry
	path   string
	offset int
}

var _ fs.ReadDirFile = (*openDir)(nil)

func (d *openDir) Close() error               { return nil }
func (d *openDir) Stat() (fs.FileInfo, error) { return fileInfo{d.e}, nil }

func (d *openDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: errors.New("is a directory")}
}

func (d *openDir) ReadDir(count int) ([]fs.DirEntry, error) {
	n := len(d.e.children) - d.offset
	if n == 0 && count > 0 {
		return nil, io.EOF
	}
	if count > 0 && n > count {
		n = count
	}
	list := make([]fs.DirEntry, n)
	for i := range list {
		list[i] = fileInfo{d.e.children[d.offset+i]}
	}
	d.offset += n
	return list, nil
}
