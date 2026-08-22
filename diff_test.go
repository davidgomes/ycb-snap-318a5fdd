package etree

import "testing"

func TestDeepEqualAndDiffPatch(t *testing.T) {
	var nilElement *Element
	if !nilElement.DeepEqual(nil) || nilElement.DeepEqual(NewElement("x")) {
		t.Fatal("nil element equality is incorrect")
	}

	base := NewDocument()
	root := base.CreateElement("root")
	root.CreateAttr("a", "1")
	root.CreateElement("old")
	root.SetText("before")

	target := NewDocument()
	targetRoot := target.CreateElement("root")
	targetRoot.CreateAttr("a", "2")
	targetRoot.CreateAttr("b", "3")
	targetRoot.CreateElement("new")
	targetRoot.SetText("after")

	ops, err := Diff(base, target, DefaultDiffOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 4 {
		t.Fatalf("got %d operations, want 4: %#v", len(ops), ops)
	}

	merged := base.Copy()
	if err := ApplyPatch(merged, GeneratePatch(ops)); err != nil {
		t.Fatal(err)
	}
	if !ElementsDeepEqual(merged.Root(), target.Root()) {
		t.Fatalf("patch result differs:\n got %s\nwant %s",
			mustWrite(merged), mustWrite(target))
	}
}

func TestKeyIdentityPairsDifferentTags(t *testing.T) {
	base := NewDocument()
	baseItem := base.CreateElement("old")
	baseItem.CreateAttr("id", "1")
	baseItem.SetText("old")
	target := NewDocument()
	targetItem := target.CreateElement("new")
	targetItem.CreateAttr("id", "1")
	targetItem.SetText("new")

	ops, err := Diff(base, target, DiffOptions{
		IdentityMode:  IdentityKeyAttribute,
		KeyAttributes: map[string]string{"old": "id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Type != OpReplace {
		t.Fatalf("got %#v, want one replacement", ops)
	}
}

func TestMergeAndReversePatch(t *testing.T) {
	base := newDocumentFromString(t, `<root><item id="1">base</item></root>`)
	ours := newDocumentFromString(t, `<root><item id="1">ours</item></root>`)
	theirs := newDocumentFromString(t, `<root><item id="1">theirs</item></root>`)

	merged, conflicts, err := Merge3Way(base, ours, theirs, DefaultMergeOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].Type != ConflictBothModified ||
		merged.Root().SelectElement("item").Text() != "base" {
		t.Fatalf("unexpected unresolved merge: %#v, %s", conflicts, mustWrite(merged))
	}

	opts := DefaultMergeOptions()
	opts.AutoResolve = true
	merged, conflicts, err = Merge3Way(base, ours, theirs, opts)
	if err != nil || len(conflicts) != 1 || !conflicts[0].Resolved ||
		merged.Root().SelectElement("item").Text() != "ours" {
		t.Fatalf("unexpected resolved merge: %#v, %s, %v", conflicts, mustWrite(merged), err)
	}

	ops, err := Diff(base, ours, DefaultDiffOptions())
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := ReversePatch(GeneratePatch(ops))
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyPatch(ours, reverse); err != nil {
		t.Fatal(err)
	}
	// Text replacement patches retain their target value when reversed because
	// XML patch-ops has no standard old-value slot.
	if ours.Root().SelectElement("item").Text() != "ours" {
		t.Fatal("reverse patch changed the wrong node")
	}
}

func mustWrite(d *Document) string {
	s, _ := d.WriteToString()
	return s
}
