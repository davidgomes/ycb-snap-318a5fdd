// Copyright 2015-2019 Brett Vickers.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package etree

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const patchNS = "urn:ietf:params:xml:ns:patch-ops"

var errNilDocument = errors.New("etree: nil document")

// OpType identifies the kind of change represented by a DiffOperation.
type OpType int

const (
	OpAdd OpType = iota
	OpRemove
	OpReplace
	OpMove
	OpUpdateAttr
	OpUpdateText
)

func (o OpType) String() string {
	switch o {
	case OpAdd:
		return "add"
	case OpRemove:
		return "remove"
	case OpReplace:
		return "replace"
	case OpMove:
		return "move"
	case OpUpdateAttr:
		return "update-attr"
	case OpUpdateText:
		return "update-text"
	default:
		return "unknown"
	}
}

// IdentityMode controls how child elements are matched during a diff.
type IdentityMode int

const (
	IdentityPosition IdentityMode = iota
	IdentityKeyAttribute
	IdentityContentHash
)

// DiffOperation describes a single change between two XML documents.
type DiffOperation struct {
	Type     OpType
	Path     string
	OldPath  string
	NewPath  string
	AttrName string
	OldValue interface{}
	NewValue interface{}
}

func (o DiffOperation) String() string {
	switch o.Type {
	case OpMove:
		return fmt.Sprintf("%s %s -> %s", strings.ToUpper(o.Type.String()), o.OldPath, o.NewPath)
	case OpUpdateAttr:
		return fmt.Sprintf("%s %s @%s", strings.ToUpper(o.Type.String()), o.Path, o.AttrName)
	default:
		return fmt.Sprintf("%s %s", strings.ToUpper(o.Type.String()), o.Path)
	}
}

// DiffOptions configure document comparison behavior.
type DiffOptions struct {
	IdentityMode     IdentityMode
	KeyAttributes    map[string]string
	IgnoreAttrs      []string
	IgnoreWhitespace bool
	IgnoreOrder      bool
}

// DefaultDiffOptions returns the default diff configuration.
func DefaultDiffOptions() DiffOptions {
	return DiffOptions{
		IdentityMode:     IdentityPosition,
		KeyAttributes:    nil,
		IgnoreWhitespace: true,
		IgnoreOrder:        false,
	}
}

// DiffSummary aggregates statistics about a set of diff operations.
type DiffSummary struct {
	ops []DiffOperation
}

// NewDiffSummary creates a summary from diff operations.
func NewDiffSummary(ops []DiffOperation) *DiffSummary {
	return &DiffSummary{ops: ops}
}

func (s *DiffSummary) Additions() int {
	n := 0
	for _, op := range s.ops {
		if op.Type == OpAdd {
			n++
		}
	}
	return n
}

func (s *DiffSummary) Removals() int {
	n := 0
	for _, op := range s.ops {
		if op.Type == OpRemove {
			n++
		}
	}
	return n
}

func (s *DiffSummary) Modifications() int {
	n := 0
	for _, op := range s.ops {
		switch op.Type {
		case OpUpdateText, OpUpdateAttr, OpReplace:
			n++
		}
	}
	return n
}

func (s *DiffSummary) Moves() int {
	n := 0
	for _, op := range s.ops {
		if op.Type == OpMove {
			n++
		}
	}
	return n
}

func (s *DiffSummary) Total() int {
	return len(s.ops)
}

func (s *DiffSummary) HasChanges() bool {
	return len(s.ops) > 0
}

func (s *DiffSummary) String() string {
	return fmt.Sprintf("%d additions, %d removals, %d modifications, %d moves",
		s.Additions(), s.Removals(), s.Modifications(), s.Moves())
}

// ConflictType classifies a three-way merge conflict.
type ConflictType int

const (
	ConflictBothModified ConflictType = iota
	ConflictModifyDelete
	ConflictStructural
)

func (c ConflictType) String() string {
	switch c {
	case ConflictBothModified:
		return "both-modified"
	case ConflictModifyDelete:
		return "modify-delete"
	case ConflictStructural:
		return "structural"
	default:
		return "unknown"
	}
}

// Resolution selects how a merge conflict should be resolved.
type Resolution int

const (
	ResolutionOurs Resolution = iota
	ResolutionTheirs
	ResolutionCustom
)

// MergeConflict records an unresolved or resolved merge disagreement.
type MergeConflict struct {
	Path        string
	BaseValue   interface{}
	OursValue   interface{}
	TheirsValue interface{}
	Resolution  interface{}
	Type        ConflictType
	Resolved    bool
}

// Resolve marks the conflict resolved using the chosen resolution strategy.
func (c *MergeConflict) Resolve(resolution Resolution, customValue interface{}) {
	c.Resolved = true
	switch resolution {
	case ResolutionOurs:
		c.Resolution = c.OursValue
	case ResolutionTheirs:
		c.Resolution = c.TheirsValue
	case ResolutionCustom:
		c.Resolution = customValue
	}
}

// MergeOptions configure three-way merge behavior.
type MergeOptions struct {
	DefaultResolution Resolution
	AutoResolve       bool
}

// DefaultMergeOptions returns the default merge configuration.
func DefaultMergeOptions() MergeOptions {
	return MergeOptions{
		DefaultResolution: ResolutionOurs,
		AutoResolve:       false,
	}
}

// ElementsDeepEqual reports whether two elements are structurally equal.
func ElementsDeepEqual(a, b *Element) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.DeepEqual(b)
}

// DeepEqual reports whether this element and other are structurally equal.
func (e *Element) DeepEqual(other *Element) bool {
	if e == nil && other == nil {
		return true
	}
	if e == nil || other == nil {
		return false
	}
	if e.Space != other.Space || e.Tag != other.Tag {
		return false
	}
	if !attrsDeepEqual(e.Attr, other.Attr) {
		return false
	}
	if e.Text() != other.Text() {
		return false
	}
	baseKids := e.ChildElements()
	otherKids := other.ChildElements()
	if len(baseKids) != len(otherKids) {
		return false
	}
	for i := range baseKids {
		if !baseKids[i].DeepEqual(otherKids[i]) {
			return false
		}
	}
	return true
}

func attrsDeepEqual(a, b []Attr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Space != b[i].Space || a[i].Key != b[i].Key || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

// Diff compares two documents and returns the operations needed to transform
// base into target.
func Diff(base, target *Document, opts DiffOptions) ([]DiffOperation, error) {
	if base == nil || target == nil {
		return nil, errNilDocument
	}
	baseRoot := base.Root()
	targetRoot := target.Root()
	var ops []DiffOperation
	switch {
	case baseRoot == nil && targetRoot == nil:
		return ops, nil
	case baseRoot == nil:
		ops = append(ops, DiffOperation{Type: OpAdd, Path: "/", NewValue: targetRoot.Copy()})
	case targetRoot == nil:
		ops = append(ops, DiffOperation{Type: OpRemove, Path: elementSelPath(baseRoot)})
	default:
		diffElementPair(baseRoot, targetRoot, opts, &ops)
	}
	return ops, nil
}

func (d *Document) Diff(other *Document, opts DiffOptions) ([]DiffOperation, error) {
	return Diff(d, other, opts)
}

func diffElementPair(base, target *Element, opts DiffOptions, ops *[]DiffOperation) {
	path := elementSelPath(base)
	if base.Space != target.Space || base.Tag != target.Tag {
		*ops = append(*ops, DiffOperation{
			Type:     OpReplace,
			Path:     path,
			OldValue: base.Copy(),
			NewValue: target.Copy(),
		})
		return
	}
	diffAttrs(base, target, path, opts, ops)
	diffText(base, target, path, opts, ops)
	diffChildren(base, target, path, opts, ops)
}

func diffElementContent(base, target *Element, opts DiffOptions, ops *[]DiffOperation) {
	path := elementSelPath(base)
	diffAttrs(base, target, path, opts, ops)
	diffText(base, target, path, opts, ops)
	diffChildren(base, target, path, opts, ops)
}

func diffAttrs(base, target *Element, path string, opts DiffOptions, ops *[]DiffOperation) {
	baseAttrs := filterAttrs(base.Attr, opts.IgnoreAttrs)
	targetAttrs := filterAttrs(target.Attr, opts.IgnoreAttrs)

	targetByKey := make(map[string]Attr, len(targetAttrs))
	for _, a := range targetAttrs {
		targetByKey[attrKey(a)] = a
	}
	baseByKey := make(map[string]Attr, len(baseAttrs))
	for _, a := range baseAttrs {
		baseByKey[attrKey(a)] = a
	}

	for key, ta := range targetByKey {
		ba, ok := baseByKey[key]
		if !ok {
			*ops = append(*ops, DiffOperation{
				Type:     OpUpdateAttr,
				Path:     path,
				AttrName: ta.FullKey(),
				OldValue: nil,
				NewValue: ta.Value,
			})
			continue
		}
		if ba.Value != ta.Value {
			*ops = append(*ops, DiffOperation{
				Type:     OpUpdateAttr,
				Path:     path,
				AttrName: ta.FullKey(),
				OldValue: ba.Value,
				NewValue: ta.Value,
			})
		}
	}
	for key, ba := range baseByKey {
		if _, ok := targetByKey[key]; !ok {
			*ops = append(*ops, DiffOperation{
				Type:     OpUpdateAttr,
				Path:     path,
				AttrName: ba.FullKey(),
				OldValue: ba.Value,
				NewValue: nil,
			})
		}
	}
}

func diffText(base, target *Element, path string, opts DiffOptions, ops *[]DiffOperation) {
	bt, tt := base.Text(), target.Text()
	if opts.IgnoreWhitespace {
		if normalizeWhitespace(bt) == normalizeWhitespace(tt) {
			return
		}
	} else if bt == tt {
		return
	}
	*ops = append(*ops, DiffOperation{
		Type:     OpUpdateText,
		Path:     path,
		OldValue: bt,
		NewValue: tt,
	})
}

func diffChildren(base, target *Element, parentPath string, opts DiffOptions, ops *[]DiffOperation) {
	if opts.IgnoreOrder && opts.IdentityMode == IdentityKeyAttribute {
		diffChildrenByKey(base, target, parentPath, opts, ops, false)
		return
	}
	switch opts.IdentityMode {
	case IdentityKeyAttribute:
		diffChildrenByKey(base, target, parentPath, opts, ops, !opts.IgnoreOrder)
	case IdentityContentHash:
		diffChildrenByHash(base, target, parentPath, opts, ops)
	default:
		diffChildrenByPosition(base, target, parentPath, opts, ops)
	}
}

func diffChildrenByPosition(base, target *Element, parentPath string, opts DiffOptions, ops *[]DiffOperation) {
	baseKids := base.ChildElements()
	targetKids := target.ChildElements()
	maxLen := max(len(baseKids), len(targetKids))
	for i := 0; i < maxLen; i++ {
		switch {
		case i >= len(baseKids):
			*ops = append(*ops, DiffOperation{
				Type:     OpAdd,
				Path:     parentPath,
				NewValue: targetKids[i].Copy(),
			})
		case i >= len(targetKids):
			*ops = append(*ops, DiffOperation{
				Type: OpRemove,
				Path: elementSelPath(baseKids[i]),
			})
		default:
			diffElementPair(baseKids[i], targetKids[i], opts, ops)
		}
	}
}

func diffChildrenByKey(base, target *Element, parentPath string, opts DiffOptions, ops *[]DiffOperation, detectMoves bool) {
	baseKids := base.ChildElements()
	targetKids := target.ChildElements()

	baseMap := make(map[string]*Element)
	for _, e := range baseKids {
		k := elementIdentityKey(e, opts)
		baseMap[k] = e
	}
	targetMap := make(map[string]*Element)
	for _, e := range targetKids {
		k := elementIdentityKey(e, opts)
		targetMap[k] = e
	}

	for key, te := range targetMap {
		be, ok := baseMap[key]
		if !ok {
			*ops = append(*ops, DiffOperation{
				Type:     OpAdd,
				Path:     parentPath,
				NewValue: te.Copy(),
			})
			continue
		}
		if be.Tag != te.Tag || be.Space != te.Space {
			*ops = append(*ops, DiffOperation{
				Type:     OpReplace,
				Path:     elementSelPath(be),
				OldValue: be.Copy(),
				NewValue: te.Copy(),
			})
			continue
		}
		if detectMoves && siblingElementIndex(be) != siblingElementIndex(te) {
			*ops = append(*ops, DiffOperation{
				Type:     OpMove,
				Path:     elementSelPath(be),
				OldPath:  elementSelPath(be),
				NewPath:  elementSelPath(te),
				NewValue: te.Copy(),
			})
		}
		diffElementContent(be, te, opts, ops)
	}
	for key, be := range baseMap {
		if _, ok := targetMap[key]; !ok {
			*ops = append(*ops, DiffOperation{
				Type: OpRemove,
				Path: elementSelPath(be),
			})
		}
	}
}

func diffChildrenByHash(base, target *Element, parentPath string, opts DiffOptions, ops *[]DiffOperation) {
	baseKids := base.ChildElements()
	targetKids := target.ChildElements()

	baseMap := make(map[string]*Element)
	for _, e := range baseKids {
		baseMap[elementContentHash(e)] = e
	}
	targetMap := make(map[string]*Element)
	for _, e := range targetKids {
		targetMap[elementContentHash(e)] = e
	}

	for hash, te := range targetMap {
		be, ok := baseMap[hash]
		if !ok {
			*ops = append(*ops, DiffOperation{
				Type:     OpAdd,
				Path:     parentPath,
				NewValue: te.Copy(),
			})
			continue
		}
		diffElementContent(be, te, opts, ops)
	}
	for hash, be := range baseMap {
		if _, ok := targetMap[hash]; !ok {
			*ops = append(*ops, DiffOperation{
				Type: OpRemove,
				Path: elementSelPath(be),
			})
		}
	}
}

func elementIdentityKey(e *Element, opts DiffOptions) string {
	if attrName, ok := opts.KeyAttributes[e.Tag]; ok {
		return e.SelectAttrValue(attrName, "")
	}
	return fmt.Sprintf("%s:%s:%d", e.Space, e.Tag, siblingElementIndex(e))
}

func elementContentHash(e *Element) string {
	h := sha256.New()
	writeElementHash(h, e)
	return hex.EncodeToString(h.Sum(nil))
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeElementHash(w hashWriter, e *Element) {
	fmt.Fprintf(w, "%s\x00%s\x00", e.Space, e.Tag)
	for _, a := range e.Attr {
		fmt.Fprintf(w, "%s\x00%s\x00%s\x00", a.Space, a.Key, a.Value)
	}
	fmt.Fprintf(w, "%s\x00", e.Text())
	for _, child := range e.ChildElements() {
		writeElementHash(w, child)
	}
}

func filterAttrs(attrs []Attr, ignore []string) []Attr {
	if len(ignore) == 0 {
		return attrs
	}
	out := make([]Attr, 0, len(attrs))
	for _, a := range attrs {
		if !slices.Contains(ignore, a.FullKey()) && !slices.Contains(ignore, a.Key) {
			out = append(out, a)
		}
	}
	return out
}

func attrKey(a Attr) string {
	return a.Space + "\x00" + a.Key
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func siblingElementIndex(e *Element) int {
	if e == nil || e.Parent() == nil {
		return 1
	}
	idx := 0
	for _, t := range e.Parent().Child {
		c, ok := t.(*Element)
		if !ok {
			continue
		}
		if c.Tag == e.Tag && spaceMatch(c.Space, e.Space) {
			idx++
			if c == e {
				return idx
			}
		}
	}
	return 1
}

func elementSelPath(e *Element) string {
	if e == nil {
		return ""
	}
	var parts []string
	for seg := e; seg != nil && seg.Tag != ""; seg = seg.Parent() {
		parts = append(parts, fmt.Sprintf("%s[%d]", seg.Tag, siblingElementIndex(seg)))
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/")
}

func parentSelPath(path string) string {
	if path == "" || path == "/" {
		return "/"
	}
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return "/"
	}
	return path[:i]
}

// GeneratePatch builds an RFC 5261-style patch document from diff operations.
func GeneratePatch(ops []DiffOperation) *Document {
	doc := NewDocument()
	diff := doc.CreateElement("diff")
	diff.CreateAttr("xmlns", patchNS)

	for _, op := range ops {
		switch op.Type {
		case OpAdd:
			add := diff.CreateElement("add")
			add.CreateAttr("sel", op.Path)
			if el, ok := op.NewValue.(*Element); ok {
				add.AddChild(el.Copy())
			}
		case OpRemove:
			rem := diff.CreateElement("remove")
			rem.CreateAttr("sel", op.Path)
		case OpReplace:
			rep := diff.CreateElement("replace")
			rep.CreateAttr("sel", op.Path)
			if el, ok := op.NewValue.(*Element); ok {
				rep.AddChild(el.Copy())
			}
		case OpUpdateText:
			rep := diff.CreateElement("replace")
			rep.CreateAttr("sel", op.Path+"/text()")
			if s, ok := op.NewValue.(string); ok {
				rep.SetText(s)
			}
		case OpUpdateAttr:
			if op.OldValue == nil {
				add := diff.CreateElement("add")
				add.CreateAttr("sel", op.Path)
				add.CreateAttr("type", "attribute")
				add.CreateAttr("name", op.AttrName)
				if s, ok := op.NewValue.(string); ok {
					add.SetText(s)
				}
			} else {
				rep := diff.CreateElement("replace")
				rep.CreateAttr("sel", op.Path+"/@"+op.AttrName)
				if s, ok := op.NewValue.(string); ok {
					rep.SetText(s)
				}
			}
		case OpMove:
			rem := diff.CreateElement("remove")
			rem.CreateAttr("sel", op.OldPath)
			add := diff.CreateElement("add")
			add.CreateAttr("sel", parentSelPath(op.NewPath))
			if el, ok := op.NewValue.(*Element); ok {
				add.AddChild(el.Copy())
			}
		}
	}
	return doc
}

// ApplyPatch applies a patch document to doc.
func ApplyPatch(doc *Document, patch *Document) error {
	if doc == nil || patch == nil {
		return errNilDocument
	}
	root := patch.Root()
	if root == nil || root.Tag != "diff" {
		return errors.New("etree: invalid patch document")
	}
	for _, t := range root.Child {
		op, ok := t.(*Element)
		if !ok {
			continue
		}
		if err := applyPatchOp(doc, op); err != nil {
			return err
		}
	}
	return nil
}

func (d *Document) Patch(patch *Document) error {
	return ApplyPatch(d, patch)
}

func applyPatchOp(doc *Document, op *Element) error {
	sel := op.SelectAttrValue("sel", "")
	switch op.Tag {
	case "add":
		if op.SelectAttrValue("type", "") == "attribute" {
			name := op.SelectAttrValue("name", "")
			el := findElementForPatch(doc, sel)
			if el == nil {
				return fmt.Errorf("etree: patch target not found: %s", sel)
			}
			el.CreateAttr(name, op.Text())
			return nil
		}
		parent := findElementForPatch(doc, sel)
		if parent == nil {
			if sel == "/" || sel == "" {
				for _, child := range op.Child {
					if el, ok := child.(*Element); ok {
						doc.SetRoot(el.Copy())
						return nil
					}
				}
			}
			return fmt.Errorf("etree: patch target not found: %s", sel)
		}
		for _, child := range op.Child {
			if el, ok := child.(*Element); ok {
				parent.AddChild(el.Copy())
			}
		}
	case "remove":
		if strings.HasSuffix(sel, "/text()") {
			baseSel := strings.TrimSuffix(sel, "/text()")
			el := findElementForPatch(doc, baseSel)
			if el == nil {
				return fmt.Errorf("etree: patch target not found: %s", sel)
			}
			el.SetText("")
			return nil
		}
		if i := strings.LastIndex(sel, "/@"); i >= 0 {
			el := findElementForPatch(doc, sel[:i])
			if el == nil {
				return fmt.Errorf("etree: patch target not found: %s", sel)
			}
			el.RemoveAttr(sel[i+2:])
			return nil
		}
		el := findElementForPatch(doc, sel)
		if el == nil {
			return fmt.Errorf("etree: patch target not found: %s", sel)
		}
		if el.Parent() != nil {
			el.Parent().RemoveChild(el)
		} else if doc.Root() == el {
			doc.SetRoot(NewElement(el.Tag))
		}
	case "replace":
		if strings.HasSuffix(sel, "/text()") {
			baseSel := strings.TrimSuffix(sel, "/text()")
			el := findElementForPatch(doc, baseSel)
			if el == nil {
				return fmt.Errorf("etree: patch target not found: %s", sel)
			}
			el.SetText(op.Text())
			return nil
		}
		if i := strings.LastIndex(sel, "/@"); i >= 0 {
			el := findElementForPatch(doc, sel[:i])
			if el == nil {
				return fmt.Errorf("etree: patch target not found: %s", sel)
			}
			el.CreateAttr(sel[i+2:], op.Text())
			return nil
		}
		el := findElementForPatch(doc, sel)
		if el == nil {
			return fmt.Errorf("etree: patch target not found: %s", sel)
		}
		parent := el.Parent()
		replacement := firstChildElement(op)
		if replacement == nil {
			replacement = NewElement(el.Tag)
		} else {
			replacement = replacement.Copy()
		}
		if parent == nil {
			doc.SetRoot(replacement)
		} else {
			parent.InsertChildAt(el.Index(), replacement)
			parent.RemoveChild(el)
		}
	}
	return nil
}

func firstChildElement(e *Element) *Element {
	for _, t := range e.Child {
		if el, ok := t.(*Element); ok {
			return el
		}
	}
	return nil
}

func findElementForPatch(doc *Document, sel string) *Element {
	if sel == "" || sel == "/" {
		return doc.Root()
	}
	if doc.Root() == nil {
		return nil
	}
	if el := doc.Root().FindElement(sel); el != nil {
		return el
	}
	return doc.FindElement(sel)
}

// ReversePatch inverts a patch document so it undoes the original changes.
func ReversePatch(patch *Document) (*Document, error) {
	if patch == nil {
		return nil, errNilDocument
	}
	root := patch.Root()
	if root == nil || root.Tag != "diff" {
		return nil, errors.New("etree: invalid patch document")
	}

	ops := make([]*Element, 0)
	for _, t := range root.Child {
		if el, ok := t.(*Element); ok {
			ops = append(ops, el)
		}
	}

	doc := NewDocument()
	diff := doc.CreateElement("diff")
	diff.CreateAttr("xmlns", patchNS)

	for i := len(ops) - 1; i >= 0; i-- {
		op := ops[i]
		sel := op.SelectAttrValue("sel", "")
		switch op.Tag {
		case "add":
			if op.SelectAttrValue("type", "") == "attribute" {
				name := op.SelectAttrValue("name", "")
				rem := diff.CreateElement("remove")
				rem.CreateAttr("sel", sel+"/@"+name)
			} else {
				child := firstChildElement(op)
				targetSel := sel
				if child != nil {
					targetSel = sel + "/" + child.Tag + "[" + fmt.Sprintf("%d", siblingElementIndex(child)) + "]"
				}
				rem := diff.CreateElement("remove")
				rem.CreateAttr("sel", targetSel)
			}
		case "remove":
			if strings.HasSuffix(sel, "/text()") {
				rep := diff.CreateElement("replace")
				rep.CreateAttr("sel", sel)
			} else if strings.Contains(sel, "/@") {
				add := diff.CreateElement("add")
				if i := strings.LastIndex(sel, "/@"); i >= 0 {
					add.CreateAttr("sel", sel[:i])
					add.CreateAttr("type", "attribute")
					add.CreateAttr("name", sel[i+2:])
				}
			} else {
				add := diff.CreateElement("add")
				add.CreateAttr("sel", parentSelPath(sel))
				tag := pathTagFromSel(sel)
				add.AddChild(NewElement(tag))
			}
		case "replace":
			rep := diff.CreateElement("replace")
			rep.CreateAttr("sel", sel)
			if strings.HasSuffix(sel, "/text()") {
				// keep empty replace placeholder
			} else if strings.Contains(sel, "/@") {
				// keep empty replace placeholder
			} else if child := firstChildElement(op); child != nil {
				rep.AddChild(child.Copy())
			}
		}
	}
	return doc, nil
}

func pathTagFromSel(sel string) string {
	if sel == "" || sel == "/" {
		return ""
	}
	last := sel
	if i := strings.LastIndex(sel, "/"); i >= 0 {
		last = sel[i+1:]
	}
	if j := strings.Index(last, "["); j >= 0 {
		last = last[:j]
	}
	return last
}

// Merge3Way merges ours and theirs relative to a common base document.
func Merge3Way(base, ours, theirs *Document, opts MergeOptions) (*Document, []MergeConflict, error) {
	if base == nil || ours == nil || theirs == nil {
		return nil, nil, errNilDocument
	}

	diffOpts := DefaultDiffOptions()
	ourOps, err := Diff(base, ours, diffOpts)
	if err != nil {
		return nil, nil, err
	}
	theirOps, err := Diff(base, theirs, diffOpts)
	if err != nil {
		return nil, nil, err
	}

	merged := base.Copy()
	conflicts := detectMergeConflicts(ourOps, theirOps, base)

	if opts.AutoResolve {
		for i := range conflicts {
			conflicts[i].Resolve(opts.DefaultResolution, nil)
		}
	}

	applyMergeOps(merged, ourOps, theirOps, conflicts, opts)
	merged.Metadata = map[string]string{
		"merge.base":   rootTag(base),
		"merge.ours":   rootTag(ours),
		"merge.theirs": rootTag(theirs),
	}
	return merged, conflicts, nil
}

func (d *Document) Merge3Way(ours, theirs *Document, opts MergeOptions) (*Document, []MergeConflict, error) {
	return Merge3Way(d, ours, theirs, opts)
}

func rootTag(d *Document) string {
	if d == nil || d.Root() == nil {
		return ""
	}
	return d.Root().Tag
}

func detectMergeConflicts(ourOps, theirOps []DiffOperation, base *Document) []MergeConflict {
	oursByKey := map[opKey]DiffOperation{}
	theirsByKey := map[opKey]DiffOperation{}
	for _, op := range ourOps {
		oursByKey[mergeOpKey(op)] = op
	}
	for _, op := range theirOps {
		theirsByKey[mergeOpKey(op)] = op
	}

	var conflicts []MergeConflict
	seen := map[string]bool{}

	for key, oOp := range oursByKey {
		tOp, ok := theirsByKey[key]
		if !ok {
			continue
		}
		if mergeOpsEqual(oOp, tOp) {
			continue
		}
		id := key.path + "|" + key.kind
		if seen[id] {
			continue
		}
		seen[id] = true

		conflict := MergeConflict{
			Path:        key.path,
			BaseValue:   mergeBaseValue(base, key.path, oOp),
			OursValue:   mergeSideValue(oOp, true),
			TheirsValue: mergeSideValue(tOp, false),
			Type:        classifyConflict(oOp, tOp),
		}
		conflicts = append(conflicts, conflict)
	}

	for key, oOp := range oursByKey {
		for tKey, tOp := range theirsByKey {
			if key == tKey {
				continue
			}
			if !pathsRelated(key.path, tKey.path) {
				continue
			}
			if isModifyDelete(oOp, tOp) || isModifyDelete(tOp, oOp) {
				id := key.path + "|" + tKey.path + "|modify-delete"
				if seen[id] {
					continue
				}
				seen[id] = true
				conflicts = append(conflicts, MergeConflict{
					Path:        key.path,
					BaseValue:   mergeBaseValue(base, key.path, oOp),
					OursValue:   mergeSideValue(oOp, true),
					TheirsValue: mergeSideValue(tOp, false),
					Type:        ConflictModifyDelete,
				})
			} else if isStructuralConflict(oOp, tOp) {
				id := key.path + "|" + tKey.path + "|structural"
				if seen[id] {
					continue
				}
				seen[id] = true
				conflicts = append(conflicts, MergeConflict{
					Path:        key.path,
					BaseValue:   mergeBaseValue(base, key.path, oOp),
					OursValue:   mergeSideValue(oOp, true),
					TheirsValue: mergeSideValue(tOp, false),
					Type:        ConflictStructural,
				})
			}
		}
	}
	return conflicts
}

func mergeOpKey(op DiffOperation) opKey {
	kind := op.Type.String()
	if op.Type == OpUpdateAttr {
		kind += ":" + op.AttrName
	}
	return opKey{path: op.Path, kind: kind}
}

type opKey struct {
	path string
	kind string
}

func mergeOpsEqual(a, b DiffOperation) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case OpUpdateAttr:
		return a.AttrName == b.AttrName && a.NewValue == b.NewValue
	case OpUpdateText:
		return a.NewValue == b.NewValue
	case OpAdd, OpReplace:
		ae, aok := a.NewValue.(*Element)
		be, bok := b.NewValue.(*Element)
		if aok && bok {
			return ae.DeepEqual(be)
		}
		return a.NewValue == b.NewValue
	default:
		return a.Path == b.Path && a.OldPath == b.OldPath && a.NewPath == b.NewPath
	}
}

func classifyConflict(oOp, tOp DiffOperation) ConflictType {
	if isModifyDelete(oOp, tOp) || isModifyDelete(tOp, oOp) {
		return ConflictModifyDelete
	}
	if isStructuralConflict(oOp, tOp) {
		return ConflictStructural
	}
	return ConflictBothModified
}

func isModifyDelete(a, b DiffOperation) bool {
	if a.Type == OpRemove && (b.Type == OpUpdateText || b.Type == OpUpdateAttr) {
		return true
	}
	if b.Type == OpRemove && (a.Type == OpUpdateText || a.Type == OpUpdateAttr) {
		return true
	}
	return false
}

func isStructuralConflict(a, b DiffOperation) bool {
	if a.Type == OpRemove && (b.Type == OpAdd || b.Type == OpRemove) {
		return true
	}
	if b.Type == OpRemove && (a.Type == OpAdd || a.Type == OpRemove) {
		return true
	}
	return false
}

func pathsRelated(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func mergeBaseValue(base *Document, path string, op DiffOperation) interface{} {
	if base == nil || base.Root() == nil {
		return nil
	}
	el := findElementForPatch(base, path)
	if el == nil {
		return nil
	}
	switch op.Type {
	case OpUpdateText:
		return el.Text()
	case OpUpdateAttr:
		return el.SelectAttrValue(op.AttrName, "")
	default:
		return el.Copy()
	}
}

func mergeSideValue(op DiffOperation, _ bool) interface{} {
	switch op.Type {
	case OpUpdateText, OpUpdateAttr:
		return op.NewValue
	case OpAdd, OpReplace:
		return op.NewValue
	case OpRemove:
		return nil
	default:
		return op.NewValue
	}
}

func applyMergeOps(merged *Document, ourOps, theirOps []DiffOperation, conflicts []MergeConflict, opts MergeOptions) {
	conflictPaths := map[string]bool{}
	for _, c := range conflicts {
		if !c.Resolved {
			conflictPaths[c.Path] = true
			continue
		}
		applyConflictResolution(merged, c)
	}

	allOps := append([]DiffOperation{}, ourOps...)
	allOps = append(allOps, theirOps...)
	for _, op := range allOps {
		if conflictPaths[op.Path] {
			continue
		}
		applyDiffOp(merged, op)
	}
}

func applyConflictResolution(merged *Document, c MergeConflict) {
	switch v := c.Resolution.(type) {
	case nil:
		return
	case string:
		el := findElementForPatch(merged, c.Path)
		if el != nil {
			el.SetText(v)
		}
	case *Element:
		applyDiffOp(merged, DiffOperation{Type: OpReplace, Path: c.Path, NewValue: v})
	default:
		applyDiffOp(merged, DiffOperation{Type: OpReplace, Path: c.Path, NewValue: v})
	}
}

func applyDiffOp(doc *Document, op DiffOperation) {
	switch op.Type {
	case OpAdd:
		parent := findElementForPatch(doc, op.Path)
		if el, ok := op.NewValue.(*Element); ok {
			if parent == nil && (op.Path == "/" || op.Path == "") {
				doc.SetRoot(el.Copy())
			} else if parent != nil {
				parent.AddChild(el.Copy())
			}
		}
	case OpRemove:
		el := findElementForPatch(doc, op.Path)
		if el != nil && el.Parent() != nil {
			el.Parent().RemoveChild(el)
		}
	case OpReplace:
		el := findElementForPatch(doc, op.Path)
		if el == nil {
			return
		}
		if newEl, ok := op.NewValue.(*Element); ok {
			parent := el.Parent()
			copyEl := newEl.Copy()
			if parent == nil {
				doc.SetRoot(copyEl)
			} else {
				parent.InsertChildAt(el.Index(), copyEl)
				parent.RemoveChild(el)
			}
		}
	case OpUpdateText:
		el := findElementForPatch(doc, op.Path)
		if el != nil {
			if s, ok := op.NewValue.(string); ok {
				el.SetText(s)
			}
		}
	case OpUpdateAttr:
		el := findElementForPatch(doc, op.Path)
		if el == nil {
			return
		}
		if op.NewValue == nil {
			el.RemoveAttr(op.AttrName)
		} else if s, ok := op.NewValue.(string); ok {
			el.CreateAttr(op.AttrName, s)
		}
	case OpMove:
		src := findElementForPatch(doc, op.OldPath)
		dstParent := findElementForPatch(doc, parentSelPath(op.NewPath))
		if src != nil && dstParent != nil && src.Parent() != nil {
			moved := src.Copy()
			src.Parent().RemoveChild(src)
			dstParent.AddChild(moved)
		}
	}
}
