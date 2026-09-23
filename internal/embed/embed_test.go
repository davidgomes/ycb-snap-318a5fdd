package embed

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestFS(t *testing.T) {
	fsys := New(map[string][]byte{
		"a.txt":       []byte("a"),
		"dir/b.txt":   []byte("bb"),
		"dir/c/d.txt": []byte("ddd"),
		"z.txt":       []byte(""),
	})
	if err := fstest.TestFS(fsys, "a.txt", "dir/b.txt", "dir/c/d.txt", "z.txt"); err != nil {
		t.Fatal(err)
	}

	var _ fs.ReadFileFS = fsys
	var _ fs.ReadDirFS = fsys

	b, _ := fsys.ReadFile("a.txt")
	b[0] = 'x'
	if b, _ = fsys.ReadFile("a.txt"); string(b) != "a" {
		t.Errorf("ReadFile content was modified: %q", b)
	}

	if _, err := fsys.ReadFile("dir"); err == nil {
		t.Error("expected error reading a directory")
	}
	if _, err := fsys.ReadDir("a.txt"); err == nil {
		t.Error("expected error reading a file as directory")
	}
}

func TestZeroFS(t *testing.T) {
	var fsys FS
	if err := fstest.TestFS(fsys); err != nil {
		t.Fatal(err)
	}
}
