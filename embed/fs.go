// Package embed provides an embed.FS implementation for the Yaegi interpreter.
package embed

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

// FS is a read-only collection of embedded files.
type FS struct {
	files map[string][]byte
}

// NewFS returns an FS populated with the given files.
func NewFS(files map[string][]byte) FS {
	data := make(map[string][]byte, len(files))
	for name, content := range files {
		data[name] = append([]byte(nil), content...)
	}
	return FS{files: data}
}

var (
	_ fs.FS         = FS{}
	_ fs.ReadFileFS = FS{}
	_ fs.ReadDirFS  = FS{}
)

// Open opens the named file for reading.
func (f FS) Open(name string) (fs.File, error) {
	name = path.Clean(name)
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return &dirFile{name: ".", fs: f}, nil
	}
	if data, ok := f.files[name]; ok {
		return &regularFile{name: path.Base(name), data: data}, nil
	}
	entries, err := f.readDirEntries(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if entries != nil {
		return &dirFile{name: name, fs: f}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// ReadFile reads and returns the content of the named file.
func (f FS) ReadFile(name string) ([]byte, error) {
	name = path.Clean(name)
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrInvalid}
	}
	data, ok := f.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	return append([]byte(nil), data...), nil
}

// ReadDir reads and returns the entire named directory.
func (f FS) ReadDir(name string) ([]fs.DirEntry, error) {
	name = path.Clean(name)
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	entries, err := f.readDirEntries(name)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	return entries, nil
}

func (f FS) readDirEntries(dir string) ([]fs.DirEntry, error) {
	prefix := dir
	if prefix == "." {
		prefix = ""
	} else {
		prefix += "/"
	}
	seen := map[string]fs.DirEntry{}
	for name := range f.files {
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		if rest == "" {
			continue
		}
		elem, remainder, _ := strings.Cut(rest, "/")
		if elem == "" || elem == "." || elem == ".." {
			continue
		}
		if _, ok := seen[elem]; ok {
			continue
		}
		if remainder == "" {
			seen[elem] = dirEntry{name: elem, data: f.files[name], dir: false}
		} else {
			seen[elem] = dirEntry{name: elem, dir: true}
		}
	}
	if len(seen) == 0 && dir != "." {
		if _, ok := f.files[dir]; ok {
			return nil, &fs.PathError{Op: "readdir", Path: dir, Err: errors.New("not a directory")}
		}
		return nil, nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]fs.DirEntry, len(names))
	for i, name := range names {
		entries[i] = seen[name]
	}
	return entries, nil
}

type dirEntry struct {
	name string
	data []byte
	dir  bool
}

func (e dirEntry) Name() string               { return e.name }
func (e dirEntry) IsDir() bool                { return e.dir }
func (e dirEntry) Type() fs.FileMode          { return e.Mode().Type() }
func (e dirEntry) Info() (fs.FileInfo, error) { return e, nil }
func (e dirEntry) Mode() fs.FileMode {
	if e.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (e dirEntry) Size() int64        { return int64(len(e.data)) }
func (e dirEntry) ModTime() time.Time { return time.Time{} }
func (e dirEntry) Sys() any           { return nil }

type regularFile struct {
	name string
	data []byte
	off  int64
}

func (f *regularFile) Close() error               { return nil }
func (f *regularFile) Stat() (fs.FileInfo, error) { return dirEntry{name: f.name, data: f.data}, nil }
func (f *regularFile) Read(b []byte) (int, error) {
	if f.off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(b, f.data[f.off:])
	f.off += int64(n)
	return n, nil
}

type dirFile struct {
	name string
	fs   FS
}

func (d *dirFile) Close() error               { return nil }
func (d *dirFile) Stat() (fs.FileInfo, error) { return dirEntry{name: path.Base(d.name), dir: true}, nil }
func (d *dirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: errors.New("is a directory")}
}

func (d *dirFile) ReadDir(count int) ([]fs.DirEntry, error) {
	entries, err := d.fs.readDirEntries(d.name)
	if err != nil {
		return nil, err
	}
	if count <= 0 || count >= len(entries) {
		return entries, nil
	}
	return entries[:count], nil
}
