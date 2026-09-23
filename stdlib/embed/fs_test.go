package embed

import (
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestFSReadFileCopy(t *testing.T) {
	f := NewFS(map[string]string{"a.txt": "hello"})
	b1, err := f.ReadFile("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b1[0] = 'X'
	b2, err := f.ReadFile("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != "Xello" || string(b2) != "hello" {
		t.Fatalf("copies are not independent: %q %q", b1, b2)
	}
}

func TestFSEmpty(t *testing.T) {
	var f FS
	file, err := f.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	dir, ok := file.(fs.ReadDirFile)
	if !ok {
		t.Fatal("root is not a ReadDirFile")
	}
	ents, err := dir.ReadDir(0)
	if err != nil || ents != nil {
		t.Fatalf("ReadDir(0) on empty = %v, %v", ents, err)
	}
	list, err := f.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("ReadDir(.) = %#v", list)
	}
	if _, err := f.Open("missing"); err == nil {
		t.Fatal("missing file opened")
	}
}

func TestFSDirsAndModes(t *testing.T) {
	f := NewFS(map[string]string{
		"d/b.txt": "bb",
		"d/a.txt": "aa",
		"z.txt":   "",
	})
	ents, err := f.ReadDir("d")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 || ents[0].Name() != "a.txt" || ents[1].Name() != "b.txt" {
		t.Fatalf("entries = %v %v", ents[0].Name(), ents[1].Name())
	}
	info, err := ents[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != 0444 || !info.ModTime().IsZero() || info.Size() != 2 {
		t.Fatalf("file info mode=%v size=%d", info.Mode(), info.Size())
	}
	di, err := fs.Stat(f, "d")
	if err != nil {
		t.Fatal(err)
	}
	if di.Name() != "d" || di.Mode() != fs.ModeDir|0555 {
		t.Fatalf("dir stat name=%s mode=%v", di.Name(), di.Mode())
	}
	root, err := fs.Stat(f, ".")
	if err != nil || root.Name() != "." || !root.IsDir() {
		t.Fatalf("root stat = %v %v", root, err)
	}

	empty, err := f.ReadFile("z.txt")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty file = %#v %v", empty, err)
	}

	if _, err := f.ReadFile("d"); err == nil || err.Error() != "read d: is a directory" {
		t.Fatalf("read dir: %v", err)
	}
	if _, err := f.ReadDir("d/a.txt"); err == nil || err.Error() != "read d/a.txt: not a directory" {
		t.Fatalf("readdir file: %v", err)
	}

	file, err := f.Open("d")
	if err != nil {
		t.Fatal(err)
	}
	rd := file.(fs.ReadDirFile)
	one, err := rd.ReadDir(1)
	if err != nil || len(one) != 1 || one[0].Name() != "a.txt" {
		t.Fatalf("first = %v %v", one, err)
	}
	two, err := rd.ReadDir(1)
	if err != nil || len(two) != 1 || two[0].Name() != "b.txt" {
		t.Fatalf("second = %v %v", two, err)
	}
	rest, err := rd.ReadDir(1)
	if err != io.EOF || rest != nil {
		t.Fatalf("eof = %v %v", rest, err)
	}
	rest, err = rd.ReadDir(-1)
	if err != nil || rest != nil {
		t.Fatalf("exhausted = %v %v", rest, err)
	}

	of, err := f.Open("d/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	seeker := of.(io.Seeker)
	if _, err := seeker.Seek(1, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if n, err := of.Read(buf); n != 1 || err != nil || buf[0] != 'a' {
		t.Fatalf("read after seek = %d %q %v", n, buf, err)
	}
	at, err := of.(io.ReaderAt).ReadAt([]byte{0, 0}, 0)
	if at != 2 || err != nil {
		t.Fatalf("ReadAt = %d %v", at, err)
	}
}

func TestFSCompliance(t *testing.T) {
	f := NewFS(map[string]string{
		"d/b.txt": "bb",
		"d/a.txt": "aa",
		"z.txt":   "z",
	})
	if err := fstest.TestFS(f, "d/a.txt", "d/b.txt", "z.txt"); err != nil {
		t.Fatal(err)
	}
	if err := fstest.TestFS(FS{}); err != nil {
		t.Fatal(err)
	}
}
