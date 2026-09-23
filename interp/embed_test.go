package interp

import (
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestNewEmbedFSMatchesStdlib(t *testing.T) {
	files := fstest.MapFS{
		"p":       &fstest.MapFile{Data: []byte("P")},
		"w":       &fstest.MapFile{Data: []byte("W")},
		"q/r.txt": &fstest.MapFile{Data: []byte("RR")},
		"q/.hid":  &fstest.MapFile{Data: []byte("H")},
	}
	list, err := resolveEmbedPatterns(files, []string{"*", "all:q"})
	if err != nil {
		t.Fatal(err)
	}
	val, err := embedValue(files, list, embedFiles)
	if err != nil {
		t.Fatal(err)
	}
	fsys := val.Interface().(fs.ReadFileFS)
	if _, ok := fsys.(fs.ReadDirFS); !ok {
		t.Fatal("embed.FS does not implement fs.ReadDirFS")
	}

	b, err := fsys.ReadFile("p")
	if err != nil || string(b) != "P" {
		t.Fatalf("ReadFile p = %q %v", b, err)
	}
	b[0] = 'X'
	b2, err := fsys.ReadFile("p")
	if err != nil || string(b2) != "P" {
		t.Fatalf("ReadFile returned a shared buffer %q %v", b2, err)
	}

	dirFS := fsys.(fs.ReadDirFS)
	ents, err := dirFS.ReadDir("q")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	want := []string{".hid", "r.txt"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("ReadDir q = %v, want %v", names, want)
	}

	f, err := fsys.(fs.FS).Open("q")
	if err != nil {
		t.Fatal(err)
	}
	rd, ok := f.(fs.ReadDirFile)
	if !ok {
		t.Fatalf("opened directory type %T does not implement fs.ReadDirFile", f)
	}
	ents, err = rd.ReadDir(-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 || ents[0].Name() != ".hid" || ents[1].Name() != "r.txt" {
		t.Fatalf("ReadDirFile = %v", ents)
	}

	plain, err := resolveEmbedPatterns(files, []string{"q"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 1 || plain[0] != "q/r.txt" {
		t.Fatalf("directory pattern included hidden files: %v", plain)
	}
}
