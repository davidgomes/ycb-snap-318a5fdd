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

func mustWrite(d *Document) string {
	s, _ := d.WriteToString()
	return s
}
