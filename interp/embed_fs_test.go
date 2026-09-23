package interp

import (
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestEmbedFSReadDirFile(t *testing.T) {
	fsys := fstest.MapFS{
		"root/a.txt":     {Data: []byte("A")},
		"root/b.txt":     {Data: []byte("B")},
		"root/sub/c.txt": {Data: []byte("C")},
		"root/.hid":      {Data: []byte("H")},
	}
	matched, err := matchEmbed(fsys, "root", embedPattern{pattern: "*"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := matched[".hid"]; ok {
		t.Fatal("hidden file included")
	}
	if _, ok := matched["sub/c.txt"]; !ok {
		t.Fatalf("directory tree not embedded: %#v", matched)
	}
	val, err := embedValue(&itype{cat: valueT, rtype: embedFSType}, matched)
	if err != nil {
		t.Fatal(err)
	}
	efs := val.Interface().(embedFS)
	var (
		_ fs.FS         = efs
		_ fs.ReadFileFS = efs
		_ fs.ReadDirFS  = efs
	)
	ents, err := efs.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 3 || ents[0].Name() != "a.txt" || ents[1].Name() != "b.txt" || ents[2].Name() != "sub" {
		t.Fatalf("%v", names(ents))
	}
	f, err := efs.Open("sub")
	if err != nil {
		t.Fatal(err)
	}
	rdf, ok := f.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("%T", f)
	}
	dents, err := rdf.ReadDir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dents) != 1 || dents[0].Name() != "c.txt" {
		t.Fatalf("%v", names(dents))
	}
	b1, err := efs.ReadFile("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b1[0] = 'Z'
	b2, err := efs.ReadFile("a.txt")
	if err != nil || string(b2) != "A" {
		t.Fatalf("%q %v", b2, err)
	}
	rf, err := efs.Open("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	buf, err := io.ReadAll(rf)
	if err != nil || string(buf) != "A" {
		t.Fatalf("%q %v", buf, err)
	}
}

func names(ents []fs.DirEntry) []string {
	out := make([]string, len(ents))
	for i, e := range ents {
		out[i] = e.Name()
	}
	return out
}
