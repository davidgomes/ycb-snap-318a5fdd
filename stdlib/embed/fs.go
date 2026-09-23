// Package embed is the embed.FS implementation used by the interpreter for
// //go:embed variables. The standard library embed.FS cannot be filled in from
// outside that package, so interpreted programs use this type instead.
package embed

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"time"
)

// FS is a read-only collection of files, compatible with the embed.FS API used
// by //go:embed. It implements fs.FS, fs.ReadFileFS and fs.ReadDirFS.
//
// The zero FS is an empty file system: Open(".") succeeds and every other name
// is missing.
type FS struct {
	data *fsData
}

type fsData struct {
	files    map[string]string
	dirs     map[string]bool
	children map[string][]string
}

// NewFS returns an FS containing files. Keys are slash-separated paths relative
// to the file system root. Parent directories are created for every file.
func NewFS(files map[string]string) FS {
	if len(files) == 0 {
		return FS{}
	}
	data := &fsData{
		files:    make(map[string]string, len(files)),
		dirs:     map[string]bool{".": true},
		children: map[string][]string{},
	}
	childSets := map[string]map[string]struct{}{}
	addChild := func(dir, name string) {
		if name == "" || name == "." {
			return
		}
		set := childSets[dir]
		if set == nil {
			set = map[string]struct{}{}
			childSets[dir] = set
		}
		set[name] = struct{}{}
	}
	for name, content := range files {
		data.files[name] = content
		for dir := path.Dir(name); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
			data.dirs[dir] = true
			addChild(path.Dir(dir), path.Base(dir))
		}
		addChild(path.Dir(name), path.Base(name))
	}
	for dir, set := range childSets {
		list := make([]string, 0, len(set))
		for name := range set {
			list = append(list, name)
		}
		sort.Strings(list)
		data.children[dir] = list
	}
	return FS{data: data}
}

var (
	_ fs.FS         = FS{}
	_ fs.ReadFileFS = FS{}
	_ fs.ReadDirFS  = FS{}
)

// Open opens the named file. Directories implement fs.ReadDirFile.
func (f FS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if name == "." {
		return &openDir{path: ".", entries: f.dirEntries(".")}, nil
	}
	if f.data == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if content, ok := f.data.files[name]; ok && !f.data.dirs[name] {
		return &openFile{path: name, data: content}, nil
	}
	if f.data.dirs[name] {
		return &openDir{path: name, entries: f.dirEntries(name)}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// ReadDir reads the named directory and returns its entries sorted by name.
func (f FS) ReadDir(name string) ([]fs.DirEntry, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	dir, ok := file.(*openDir)
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("not a directory")}
	}
	list := make([]fs.DirEntry, len(dir.entries))
	copy(list, dir.entries)
	return list, nil
}

// ReadFile reads the named file and returns an independent copy of its contents.
func (f FS) ReadFile(name string) ([]byte, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	of, ok := file.(*openFile)
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
	}
	return copyBytes(of.data), nil
}

func (f FS) dirEntries(dir string) []fs.DirEntry {
	if f.data == nil {
		return []fs.DirEntry{}
	}
	names := f.data.children[dir]
	list := make([]fs.DirEntry, len(names))
	for i, name := range names {
		full := name
		if dir != "." {
			full = dir + "/" + name
		}
		if f.data.dirs[full] {
			list[i] = &fileInfo{name: name, isDir: true}
			continue
		}
		list[i] = &fileInfo{name: name, data: f.data.files[full]}
	}
	return list
}

// fileInfo implements fs.FileInfo and fs.DirEntry.
type fileInfo struct {
	name  string
	data  string
	isDir bool
}

func (f *fileInfo) Name() string { return f.name }
func (f *fileInfo) Size() int64 {
	if f.isDir {
		return 0
	}
	return int64(len(f.data))
}
func (f *fileInfo) Mode() fs.FileMode {
	if f.isDir {
		return fs.ModeDir | 0555
	}
	return 0444
}
func (f *fileInfo) ModTime() time.Time { return time.Time{} }
func (f *fileInfo) IsDir() bool        { return f.isDir }
func (f *fileInfo) Sys() any           { return nil }
func (f *fileInfo) Type() fs.FileMode  { return f.Mode().Type() }
func (f *fileInfo) Info() (fs.FileInfo, error) {
	return f, nil
}

var (
	_ fs.FileInfo = (*fileInfo)(nil)
	_ fs.DirEntry = (*fileInfo)(nil)
)

type openFile struct {
	path   string
	data   string
	offset int64
}

func (f *openFile) Close() error { return nil }
func (f *openFile) Stat() (fs.FileInfo, error) {
	return &fileInfo{name: path.Base(f.path), data: f.data}, nil
}

func (f *openFile) Read(b []byte) (int, error) {
	if f.offset >= int64(len(f.data)) {
		return 0, io.EOF
	}
	if f.offset < 0 {
		return 0, &fs.PathError{Op: "read", Path: f.path, Err: fs.ErrInvalid}
	}
	n := copy(b, f.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *openFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.offset
	case io.SeekEnd:
		offset += int64(len(f.data))
	default:
		return 0, &fs.PathError{Op: "seek", Path: f.path, Err: fs.ErrInvalid}
	}
	if offset < 0 || offset > int64(len(f.data)) {
		return 0, &fs.PathError{Op: "seek", Path: f.path, Err: fs.ErrInvalid}
	}
	f.offset = offset
	return offset, nil
}

func (f *openFile) ReadAt(b []byte, offset int64) (int, error) {
	if offset < 0 || offset > int64(len(f.data)) {
		return 0, &fs.PathError{Op: "read", Path: f.path, Err: fs.ErrInvalid}
	}
	n := copy(b, f.data[offset:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

var (
	_ io.Seeker   = (*openFile)(nil)
	_ io.ReaderAt = (*openFile)(nil)
)

type openDir struct {
	path    string
	entries []fs.DirEntry
	offset  int
}

var _ fs.ReadDirFile = (*openDir)(nil)

func (d *openDir) Close() error { return nil }
func (d *openDir) Stat() (fs.FileInfo, error) {
	return &fileInfo{name: path.Base(d.path), isDir: true}, nil
}
func (d *openDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: errors.New("is a directory")}
}

func (d *openDir) ReadDir(count int) ([]fs.DirEntry, error) {
	n := len(d.entries) - d.offset
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
	copy(list, d.entries[d.offset:d.offset+n])
	d.offset += n
	return list, nil
}

func copyBytes(s string) []byte {
	b := make([]byte, len(s))
	copy(b, s)
	return b
}
