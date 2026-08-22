package etree

import (
	"strings"
	"testing"
)

func docFromXML(t *testing.T, s string) *Document {
	t.Helper()
	doc := NewDocument()
	if err := doc.ReadFromString(s); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return doc
}

func TestElementsDeepEqual(t *testing.T) {
	a := NewElement("root")
	a.CreateElement("child").SetText("hi")

	b := NewElement("root")
	b.CreateElement("child").SetText("hi")

	if !ElementsDeepEqual(a, b) {
		t.Fatal("expected equal elements")
	}
	if !ElementsDeepEqual(nil, nil) {
		t.Fatal("two nil elements should be equal")
	}
	if ElementsDeepEqual(a, nil) || ElementsDeepEqual(nil, b) {
		t.Fatal("nil vs non-nil should not be equal")
	}

	b.ChildElements()[0].SetText("bye")
	if ElementsDeepEqual(a, b) {
		t.Fatal("expected different text to differ")
	}
}

func TestDeepEqualNilReceiver(t *testing.T) {
	var e *Element
	if !e.DeepEqual(nil) {
		t.Fatal("nil receiver DeepEqual(nil) should be true")
	}
	other := NewElement("x")
	if e.DeepEqual(other) || other.DeepEqual(nil) {
		t.Fatal("nil vs non-nil DeepEqual should be false")
	}
}

func TestOpTypeString(t *testing.T) {
	cases := map[OpType]string{
		OpAdd:         "add",
		OpRemove:      "remove",
		OpReplace:     "replace",
		OpMove:        "move",
		OpUpdateAttr:  "update-attr",
		OpUpdateText:  "update-text",
	}
	for op, want := range cases {
		if got := op.String(); got != want {
			t.Fatalf("OpType.String() = %q, want %q", got, want)
		}
	}
}

func TestDiffOperationString(t *testing.T) {
	op := DiffOperation{Type: OpUpdateAttr, Path: "/a[1]", AttrName: "id"}
	if got := op.String(); got != "UPDATE-ATTR /a[1] @id" {
		t.Fatalf("unexpected string: %q", got)
	}
	move := DiffOperation{Type: OpMove, OldPath: "/a[1]", NewPath: "/b[2]"}
	if got := move.String(); !strings.Contains(got, "MOVE") || !strings.Contains(got, "->") {
		t.Fatalf("unexpected move string: %q", got)
	}
}

func TestDiffNilDocuments(t *testing.T) {
	doc := NewDocument()
	if _, err := Diff(nil, doc, DefaultDiffOptions()); err == nil {
		t.Fatal("expected error for nil base")
	}
	if _, err := Diff(doc, nil, DefaultDiffOptions()); err == nil {
		t.Fatal("expected error for nil target")
	}
}

func TestDiffAddRemoveTextAttr(t *testing.T) {
	base := docFromXML(t, `<store><book id="1"><title>Old</title></book></store>`)
	target := docFromXML(t, `<store><book id="1"><title>New</title><author>Me</author></book></store>`)

	ops, err := Diff(base, target, DefaultDiffOptions())
	if err != nil {
		t.Fatal(err)
	}

	summary := NewDiffSummary(ops)
	if !summary.HasChanges() {
		t.Fatal("expected changes")
	}
	if summary.Modifications() < 1 {
		t.Fatalf("expected text modification, got %v", summary)
	}
	if summary.Additions() < 1 {
		t.Fatalf("expected element addition, got %v", summary)
	}
	if got := summary.String(); !strings.Contains(got, "additions") {
		t.Fatalf("unexpected summary: %s", got)
	}
}

func TestGenerateAndApplyPatch(t *testing.T) {
	base := docFromXML(t, `<root><item>one</item></root>`)
	target := docFromXML(t, `<root><item>two</item><extra/></root>`)

	ops, err := Diff(base, target, DefaultDiffOptions())
	if err != nil {
		t.Fatal(err)
	}

	patch := GeneratePatch(ops)
	if patch.Root().Tag != "diff" {
		t.Fatalf("expected diff root, got %s", patch.Root().Tag)
	}
	if patch.Root().SelectAttrValue("xmlns", "") != patchNS {
		t.Fatal("missing patch namespace")
	}

	result := base.Copy()
	if err := ApplyPatch(result, patch); err != nil {
		t.Fatal(err)
	}

	if !result.Root().DeepEqual(target.Root()) {
		got, _ := result.WriteToString()
		want, _ := target.WriteToString()
		t.Fatalf("patch apply mismatch\nGot:\n%s\nWant:\n%s", got, want)
	}
}

func TestPatchAttributeOperations(t *testing.T) {
	base := docFromXML(t, `<root><node/></root>`)
	target := docFromXML(t, `<root><node id="5" name="x"/></root>`)

	ops, _ := Diff(base, target, DefaultDiffOptions())
	patch := GeneratePatch(ops)

	var hasAttrAdd bool
	for _, child := range patch.Root().Child {
		el, ok := child.(*Element)
		if !ok {
			continue
		}
		if el.Tag == "add" && el.SelectAttrValue("type", "") == "attribute" {
			hasAttrAdd = true
		}
	}
	if !hasAttrAdd {
		t.Fatal("expected attribute add in patch")
	}

	result := base.Copy()
	if err := ApplyPatch(result, patch); err != nil {
		t.Fatal(err)
	}
	node := result.Root().SelectElement("node")
	if node.SelectAttrValue("id", "") != "5" {
		t.Fatal("attribute not applied")
	}
}

func TestReversePatch(t *testing.T) {
	base := docFromXML(t, `<root><item>one</item></root>`)
	target := docFromXML(t, `<root><item>two</item></root>`)

	ops, _ := Diff(base, target, DefaultDiffOptions())
	patch := GeneratePatch(ops)

	reversed, err := ReversePatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	if reversed == nil {
		t.Fatal("expected reversed patch")
	}
	if _, err := ReversePatch(nil); err == nil {
		t.Fatal("expected error reversing nil patch")
	}
}

func TestMerge3Way(t *testing.T) {
	base := docFromXML(t, `<doc><line>base</line></doc>`)
	ours := docFromXML(t, `<doc><line>ours</line></doc>`)
	theirs := docFromXML(t, `<doc><line>theirs</line></doc>`)

	merged, conflicts, err := Merge3Way(base, ours, theirs, DefaultMergeOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) == 0 {
		t.Fatal("expected merge conflict")
	}
	if merged.Metadata["merge.base"] != "doc" {
		t.Fatalf("metadata missing: %v", merged.Metadata)
	}
	if merged.Metadata["merge.ours"] != "doc" || merged.Metadata["merge.theirs"] != "doc" {
		t.Fatalf("unexpected metadata: %v", merged.Metadata)
	}

	opts := DefaultMergeOptions()
	opts.AutoResolve = true
	opts.DefaultResolution = ResolutionTheirs
	merged2, conflicts2, err := Merge3Way(base, ours, theirs, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts2) == 0 || !conflicts2[0].Resolved {
		t.Fatal("expected auto-resolved conflict")
	}
	if merged2.Root().SelectElement("line").Text() != "theirs" {
		t.Fatal("expected theirs resolution")
	}
}

func TestMerge3WayNil(t *testing.T) {
	doc := NewDocument()
	if _, _, err := Merge3Way(nil, doc, doc, DefaultMergeOptions()); err == nil {
		t.Fatal("expected nil error")
	}
}

func TestDocumentConvenienceMethods(t *testing.T) {
	base := docFromXML(t, `<a><b>1</b></a>`)
	other := docFromXML(t, `<a><b>2</b></a>`)

	ops, err := base.Diff(other, DefaultDiffOptions())
	if err != nil || len(ops) == 0 {
		t.Fatalf("Diff convenience failed: %v %d", err, len(ops))
	}

	patch := GeneratePatch(ops)
	copy := base.Copy()
	if err := copy.Patch(patch); err != nil {
		t.Fatal(err)
	}
}

func TestConflictTypeString(t *testing.T) {
	if ConflictBothModified.String() != "both-modified" {
		t.Fatal("unexpected conflict type string")
	}
	if ConflictModifyDelete.String() != "modify-delete" {
		t.Fatal("unexpected conflict type string")
	}
	if ConflictStructural.String() != "structural" {
		t.Fatal("unexpected conflict type string")
	}
}

func TestMergeConflictResolve(t *testing.T) {
	c := MergeConflict{OursValue: "o", TheirsValue: "t"}
	c.Resolve(ResolutionOurs, nil)
	if !c.Resolved || c.Resolution != "o" {
		t.Fatal("Resolve ours failed")
	}
	c = MergeConflict{OursValue: "o", TheirsValue: "t"}
	c.Resolve(ResolutionCustom, "custom")
	if c.Resolution != "custom" {
		t.Fatal("Resolve custom failed")
	}
}

func TestDiffKeyAttributeMode(t *testing.T) {
	base := docFromXML(t, `<list><item id="a"><name>A</name></item></list>`)
	target := docFromXML(t, `<list><item id="a"><name>B</name></item><item id="b"><name>C</name></item></list>`)

	opts := DefaultDiffOptions()
	opts.IdentityMode = IdentityKeyAttribute
	opts.KeyAttributes = map[string]string{"item": "id"}

	ops, err := Diff(base, target, opts)
	if err != nil {
		t.Fatal(err)
	}
	summary := NewDiffSummary(ops)
	if summary.Additions() != 1 {
		t.Fatalf("expected 1 addition, got %d", summary.Additions())
	}
}

func TestDefaultOptions(t *testing.T) {
	d := DefaultDiffOptions()
	if d.IdentityMode != IdentityPosition || !d.IgnoreWhitespace || d.IgnoreOrder {
		t.Fatal("unexpected default diff options")
	}
	m := DefaultMergeOptions()
	if m.DefaultResolution != ResolutionOurs || m.AutoResolve {
		t.Fatal("unexpected default merge options")
	}
}
