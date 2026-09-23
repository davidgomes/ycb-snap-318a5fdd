package snapshot_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/experimental/snapshot"
)

// memory page exported as "memory"
var wasmMemory = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x07, 0x0a, 0x01, 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
}

func instantiate(t *testing.T) (api.Module, func()) {
	t.Helper()
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	mod, err := r.Instantiate(ctx, wasmMemory)
	if err != nil {
		t.Fatal(err)
	}
	return mod, func() { _ = r.Close(ctx) }
}

func TestCaptureRestoreAndIncremental(t *testing.T) {
	ctx := context.Background()
	a, doneA := instantiate(t)
	defer doneA()
	b, doneB := instantiate(t)
	defer doneB()

	if !a.Memory().WriteByte(0, 1) || !b.Memory().WriteByte(2, 9) {
		t.Fatal("write")
	}

	c := experimental.NewSnapshotCoordinator()
	if _, err := c.CaptureSnapshot(); err == nil || !bytes.Contains([]byte(err.Error()), []byte("no modules")) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := c.CaptureSnapshot(nil); err == nil || !bytes.Contains([]byte(err.Error()), []byte("module closed")) {
		t.Fatalf("nil: %v", err)
	}
	_ = a.Close(ctx)
	if _, err := c.CaptureSnapshot(a); err == nil || !bytes.Contains([]byte(err.Error()), []byte("module closed")) {
		t.Fatalf("closed: %v", err)
	}

	a, doneA = instantiate(t)
	defer doneA()
	if !a.Memory().WriteByte(0, 1) {
		t.Fatal("rewrite")
	}

	full, err := c.CaptureSnapshot(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if full.Version() != 1 {
		t.Fatalf("version %d", full.Version())
	}
	data := full.Data()
	if len(data) != 2 || data[0][0] != 1 || data[1][2] != 9 {
		t.Fatalf("data mismatch")
	}
	data[0][0] = 0
	if full.Data()[0][0] != 1 {
		t.Fatal("Data not copied")
	}
	raw := concat(full.Data())
	got, err := gunzip(full.CompressedData())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("compressed full mismatch")
	}

	full.SetTag("k", "v")
	tags := full.Tags()
	tags["k"] = "mutated"
	if full.Tags()["k"] != "v" {
		t.Fatal("tags not copied")
	}

	if !a.Memory().WriteByte(0, 7) || !a.Memory().WriteByte(1, 8) {
		t.Fatal("mutate")
	}
	inc, err := c.CaptureIncremental(full, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if inc.Version() != 2 {
		t.Fatalf("inc version %d", inc.Version())
	}
	if len(inc.CompressedData()) >= len(full.CompressedData()) {
		t.Fatalf("incremental not smaller: %d vs %d", len(inc.CompressedData()), len(full.CompressedData()))
	}
	if inc.Data()[0][0] != 7 || inc.Data()[0][1] != 8 || inc.Data()[1][2] != 9 {
		t.Fatal("reconstructed incremental")
	}
	sum := snapshot.Summarize(inc)
	if sum.TotalModules != 2 || sum.Version != 2 || sum.ModifiedBytes != 2 {
		t.Fatalf("summary %+v", sum)
	}
	if snapshot.Summarize(full).ModifiedBytes != 0 {
		t.Fatal("full modified")
	}
	diffs := full.Compare(inc)
	if len(diffs) != 2 || diffs[0].Offset != 0 || diffs[0].OldValue != 1 || diffs[0].NewValue != 7 {
		t.Fatalf("diffs %+v", diffs)
	}
	if diffs[1].Offset != 1 || diffs[1].OldValue != 0 || diffs[1].NewValue != 8 {
		t.Fatalf("diffs1 %+v", diffs[1])
	}

	if _, err := c.CaptureIncremental(nil, a); err == nil || !bytes.Contains([]byte(err.Error()), []byte("baseline snapshot is nil")) {
		t.Fatalf("nil base %v", err)
	}
	if _, err := c.CaptureIncremental(full, a); err == nil || !bytes.Contains([]byte(err.Error()), []byte("module count mismatch")) {
		t.Fatalf("count %v", err)
	}

	// nested incremental
	if !b.Memory().WriteByte(2, 3) {
		t.Fatal("b")
	}
	inc2, err := c.CaptureIncremental(inc, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if inc2.Version() != 3 || inc2.Data()[1][2] != 3 {
		t.Fatal("nested")
	}
	if snapshot.Summarize(inc2).ModifiedBytes != 1 {
		t.Fatalf("mod %d", snapshot.Summarize(inc2).ModifiedBytes)
	}
	if len(inc2.CompressedData()) >= len(inc.CompressedData()) {
		t.Fatal("nested compressed size")
	}

	// restore identity, permuted
	if err := c.RestoreSnapshot(full, b, a); err != nil {
		t.Fatal(err)
	}
	v, _ := a.Memory().ReadByte(0)
	if v != 1 {
		t.Fatalf("restored a %d", v)
	}
	v, _ = b.Memory().ReadByte(2)
	if v != 9 {
		t.Fatalf("restored b %d", v)
	}

	// fewer modules: identity only, miss is nil error
	other, doneO := instantiate(t)
	defer doneO()
	if err := c.RestoreSnapshot(full, other); err != nil {
		t.Fatal(err)
	}
	ov, _ := other.Memory().ReadByte(0)
	if ov != 0 {
		t.Fatal("unmatched module written")
	}

	if err := c.RestoreSnapshot(full, a, b, other); err == nil || !bytes.Contains([]byte(err.Error()), []byte("incompatible module")) {
		t.Fatalf("extra %v", err)
	}

	// positional when identities differ but count matches: use fresh modules
	p1, d1 := instantiate(t)
	defer d1()
	p2, d2 := instantiate(t)
	defer d2()
	if err := c.RestoreSnapshot(full, p1, p2); err != nil {
		t.Fatal(err)
	}
	v, _ = p1.Memory().ReadByte(0)
	v2, _ := p2.Memory().ReadByte(2)
	if v != 1 || v2 != 9 {
		t.Fatalf("positional %d %d", v, v2)
	}

	blob, err := snapshot.MarshalSnapshot(inc2)
	if err != nil {
		t.Fatal(err)
	}
	back, err := snapshot.UnmarshalSnapshot(blob)
	if err != nil {
		t.Fatal(err)
	}
	if back.Version() != inc2.Version() || snapshot.Summarize(back).ModifiedBytes != 0 {
		t.Fatal("unmarshal kind")
	}
	if !bytes.Equal(back.Data()[0], inc2.Data()[0]) || !bytes.Equal(back.Data()[1], inc2.Data()[1]) {
		t.Fatal("unmarshal data")
	}
	if _, err := snapshot.UnmarshalSnapshot([]byte("nope")); err == nil {
		t.Fatal("expected unmarshal error")
	}

	ch := snapshot.NewChain()
	if ch.Head() != nil || ch.Len() != 0 || len(ch.Snapshots()) != 0 {
		t.Fatal("empty chain")
	}
	ch.Push(full)
	ch.Push(inc)
	if ch.Head().Version() != inc.Version() || ch.Len() != 2 {
		t.Fatal("chain head")
	}
	snaps := ch.Snapshots()
	snaps[0] = nil
	if ch.Snapshots()[0].Version() != full.Version() {
		t.Fatal("chain copy")
	}

	snapshot.Register("main", c)
	gotC, ok := snapshot.Get("main")
	if !ok || gotC != c {
		t.Fatal("registry")
	}
	snapshot.Register("main", experimental.NewSnapshotCoordinator())
	gotC, _ = snapshot.Get("main")
	if gotC == c {
		t.Fatal("replace")
	}
	snapshot.Unregister("main")
	if _, ok := snapshot.Get("main"); ok {
		t.Fatal("unregister")
	}

	ctx = snapshot.WithCoordinator(context.Background(), c)
	if snapshot.GetCoordinator(ctx) != c || snapshot.GetCoordinator(context.Background()) != nil {
		t.Fatal("context")
	}
}

func TestConcurrentVersions(t *testing.T) {
	mod, done := instantiate(t)
	defer done()
	c := snapshot.NewCoordinator()
	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.CaptureSnapshot(mod); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	s, err := c.CaptureSnapshot(mod)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version() != 17 {
		t.Fatalf("version %d", s.Version())
	}
}

func concat(parts [][]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func gunzip(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
