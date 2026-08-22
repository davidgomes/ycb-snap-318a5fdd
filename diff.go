package etree

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// OpType identifies a document difference.
type OpType int

const (
	OpAdd OpType = iota
	OpRemove
	OpReplace
	OpMove
	OpUpdateAttr
	OpUpdateText
)

func (t OpType) String() string {
	switch t {
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

// DiffOperation describes one change from a base tree to a target tree.
type DiffOperation struct {
	Type     OpType
	Path     string
	OldPath  string
	NewPath  string
	AttrName string
	OldValue interface{}
	NewValue interface{}
}

func (op DiffOperation) String() string {
	switch op.Type {
	case OpMove:
		return fmt.Sprintf("%s %s -> %s", strings.ToUpper(op.Type.String()), op.OldPath, op.NewPath)
	case OpUpdateAttr:
		return fmt.Sprintf("%s %s (%s)", strings.ToUpper(op.Type.String()), op.Path, op.AttrName)
	default:
		return fmt.Sprintf("%s %s", strings.ToUpper(op.Type.String()), op.Path)
	}
}

// IdentityMode controls how corresponding child elements are identified.
type IdentityMode int

const (
	IdentityPosition IdentityMode = iota
	IdentityKeyAttribute
	IdentityContentHash
)

// DiffOptions controls XML comparison and difference generation.
type DiffOptions struct {
	IdentityMode     IdentityMode
	KeyAttributes    map[string]string
	IgnoreAttrs      []string
	IgnoreWhitespace bool
	IgnoreOrder      bool
}

func DefaultDiffOptions() DiffOptions {
	return DiffOptions{
		IdentityMode:     IdentityPosition,
		IgnoreWhitespace: true,
		IgnoreOrder:      false,
	}
}

// MergeConflict describes a difference that cannot be selected automatically.
type MergeConflict struct {
	Path        string
	BaseValue   interface{}
	OursValue   interface{}
	TheirsValue interface{}
	Resolution  interface{}
	Type        ConflictType
	Resolved    bool
}

// ConflictType identifies the shape of a merge conflict.
type ConflictType int

const (
	ConflictBothModified ConflictType = iota
	ConflictModifyDelete
	ConflictStructural
)

func (t ConflictType) String() string {
	switch t {
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

// Resolution chooses one side of a merge conflict.
type Resolution int

const (
	ResolutionOurs Resolution = iota
	ResolutionTheirs
	ResolutionCustom
)

func (c *MergeConflict) Resolve(resolution Resolution, customValue interface{}) {
	c.Resolved = true
	switch resolution {
	case ResolutionOurs:
		c.Resolution = c.OursValue
	case ResolutionTheirs:
		c.Resolution = c.TheirsValue
	case ResolutionCustom:
		c.Resolution = customValue
	default:
		c.Resolution = customValue
	}
}

// MergeOptions controls three-way merge conflict handling.
type MergeOptions struct {
	DefaultResolution Resolution
	AutoResolve       bool
}

func DefaultMergeOptions() MergeOptions {
	return MergeOptions{DefaultResolution: ResolutionOurs}
}

// DiffSummary is a count of the different kinds of changes.
type DiffSummary struct {
	additions, removals, modifications, moves int
}

func NewDiffSummary(ops []DiffOperation) *DiffSummary {
	s := &DiffSummary{}
	for _, op := range ops {
		switch op.Type {
		case OpAdd:
			s.additions++
		case OpRemove:
			s.removals++
		case OpUpdateText, OpUpdateAttr, OpReplace:
			s.modifications++
		case OpMove:
			s.moves++
		}
	}
	return s
}

func (s *DiffSummary) Additions() int     { return s.additions }
func (s *DiffSummary) Removals() int      { return s.removals }
func (s *DiffSummary) Modifications() int { return s.modifications }
func (s *DiffSummary) Moves() int         { return s.moves }
func (s *DiffSummary) Total() int {
	return s.additions + s.removals + s.modifications + s.moves
}
func (s *DiffSummary) HasChanges() bool { return s.Total() != 0 }
func (s *DiffSummary) String() string {
	return fmt.Sprintf("%d additions, %d removals, %d modifications, %d moves",
		s.additions, s.removals, s.modifications, s.moves)
}

// DeepEqual compares two elements without considering their parent pointers.
// It is safe for nil receivers.
func (e *Element) DeepEqual(other *Element) bool {
	if e == nil || other == nil {
		return e == other
	}
	if e.Tag != other.Tag || e.Space != other.Space ||
		e.NamespaceURI() != other.NamespaceURI() || len(e.Attr) != len(other.Attr) ||
		len(e.Child) != len(other.Child) {
		return false
	}

	// XML attributes are unordered. Keep duplicate attributes meaningful by
	// matching each attribute in one element to one in the other.
	used := make([]bool, len(other.Attr))
	for _, a := range e.Attr {
		found := false
		for i, b := range other.Attr {
			if !used[i] && a.Space == b.Space && a.Key == b.Key &&
				a.Value == b.Value && attrNamespaceURI(a) == attrNamespaceURI(b) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	for i, child := range e.Child {
		if !tokensDeepEqual(child, other.Child[i]) {
			return false
		}
	}
	return true
}

func attrNamespaceURI(a Attr) string {
	if a.element == nil {
		return ""
	}
	return a.NamespaceURI()
}

func ElementsDeepEqual(a, b *Element) bool { return a.DeepEqual(b) }

func tokensDeepEqual(a, b Token) bool {
	switch aa := a.(type) {
	case *Element:
		bb, ok := b.(*Element)
		return ok && aa.DeepEqual(bb)
	case *CharData:
		bb, ok := b.(*CharData)
		return ok && aa.Data == bb.Data && aa.IsCData() == bb.IsCData()
	case *Comment:
		bb, ok := b.(*Comment)
		return ok && aa.Data == bb.Data
	case *Directive:
		bb, ok := b.(*Directive)
		return ok && aa.Data == bb.Data
	case *ProcInst:
		bb, ok := b.(*ProcInst)
		return ok && aa.Target == bb.Target && aa.Inst == bb.Inst
	default:
		return a == nil && b == nil
	}
}

// Diff returns operations which transform base into target.
func Diff(base, target *Document, opts DiffOptions) ([]DiffOperation, error) {
	if base == nil || target == nil {
		return nil, errors.New("etree: cannot diff a nil document")
	}
	b, t := base.Root(), target.Root()
	if b == nil && t == nil {
		return nil, nil
	}
	if b == nil {
		return []DiffOperation{{Type: OpAdd, Path: "/", NewValue: t.Copy()}}, nil
	}
	if t == nil {
		return []DiffOperation{{Type: OpRemove, Path: elementPath(b), OldValue: b.Copy()}}, nil
	}
	if !sameElementName(b, t) {
		return []DiffOperation{{Type: OpReplace, Path: elementPath(b), OldValue: b.Copy(), NewValue: t.Copy()}}, nil
	}
	var ops []DiffOperation
	diffElement(b, t, normalizeDiffOptions(opts), &ops)
	return ops, nil
}

func normalizeDiffOptions(opts DiffOptions) DiffOptions {
	if opts.IdentityMode < IdentityPosition || opts.IdentityMode > IdentityContentHash {
		opts.IdentityMode = IdentityPosition
	}
	return opts
}

func sameElementName(a, b *Element) bool {
	return a.Tag == b.Tag && a.Space == b.Space && a.NamespaceURI() == b.NamespaceURI()
}

func diffElement(base, target *Element, opts DiffOptions, ops *[]DiffOperation) {
	path := elementPath(base)
	diffAttrs(base, target, path, opts, ops)
	oldText, newText := comparableText(base, opts), comparableText(target, opts)
	if oldText != newText {
		*ops = append(*ops, DiffOperation{
			Type: OpUpdateText, Path: path, OldValue: oldText, NewValue: newText,
		})
	}

	bchildren, tchildren := childElements(base, opts), childElements(target, opts)
	switch opts.IdentityMode {
	case IdentityKeyAttribute:
		diffKeyChildren(base, target, bchildren, tchildren, opts, ops)
	case IdentityContentHash:
		diffHashChildren(base, target, bchildren, tchildren, opts, ops)
	default:
		diffPositionChildren(base, target, bchildren, tchildren, opts, ops)
	}
}

func diffAttrs(base, target *Element, path string, opts DiffOptions, ops *[]DiffOperation) {
	ignored := make(map[string]bool, len(opts.IgnoreAttrs))
	for _, name := range opts.IgnoreAttrs {
		ignored[name] = true
	}
	battrs, tattrs := make(map[string]Attr), make(map[string]Attr)
	for _, a := range base.Attr {
		if !ignored[a.FullKey()] && !ignored[a.Key] {
			battrs[a.FullKey()] = a
		}
	}
	for _, a := range target.Attr {
		if !ignored[a.FullKey()] && !ignored[a.Key] {
			tattrs[a.FullKey()] = a
		}
	}
	bnames := make([]string, 0, len(battrs))
	for name := range battrs {
		bnames = append(bnames, name)
	}
	sort.Strings(bnames)
	for _, name := range bnames {
		a := battrs[name]
		if _, ok := tattrs[name]; !ok {
			*ops = append(*ops, DiffOperation{
				Type: OpUpdateAttr, Path: path, AttrName: name,
				OldValue: a.Value, NewValue: nil,
			})
		}
	}
	tnames := make([]string, 0, len(tattrs))
	for name := range tattrs {
		tnames = append(tnames, name)
	}
	sort.Strings(tnames)
	for _, name := range tnames {
		a := tattrs[name]
		old, ok := battrs[name]
		if !ok {
			*ops = append(*ops, DiffOperation{
				Type: OpUpdateAttr, Path: path, AttrName: name,
				NewValue: a.Value,
			})
		} else if old.Value != a.Value {
			*ops = append(*ops, DiffOperation{
				Type: OpUpdateAttr, Path: path, AttrName: name,
				OldValue: old.Value, NewValue: a.Value,
			})
		}
	}
}

func comparableText(e *Element, opts DiffOptions) string {
	text := e.Text()
	if opts.IgnoreWhitespace && isWhitespace(text) {
		return ""
	}
	return text
}

func childElements(e *Element, opts DiffOptions) []*Element {
	result := make([]*Element, 0)
	for _, token := range e.Child {
		if child, ok := token.(*Element); ok {
			result = append(result, child)
		}
	}
	return result
}

func diffPositionChildren(base, target *Element, bchildren, tchildren []*Element,
	opts DiffOptions, ops *[]DiffOperation) {
	if opts.IgnoreOrder {
		diffUnorderedChildren(base, target, bchildren, tchildren, opts, ops)
		return
	}
	common := len(bchildren)
	if len(tchildren) < common {
		common = len(tchildren)
	}
	for i := 0; i < common; i++ {
		b, t := bchildren[i], tchildren[i]
		if !sameElementName(b, t) {
			*ops = append(*ops, DiffOperation{
				Type: OpReplace, Path: elementPath(b), OldValue: b.Copy(), NewValue: t.Copy(),
			})
		} else {
			diffElement(b, t, opts, ops)
		}
	}
	for i := len(bchildren) - 1; i >= common; i-- {
		b := bchildren[i]
		*ops = append(*ops, DiffOperation{
			Type: OpRemove, Path: elementPath(b), OldValue: b.Copy(),
		})
	}
	for i := common; i < len(tchildren); i++ {
		*ops = append(*ops, DiffOperation{
			Type: OpAdd, Path: elementPath(base), NewValue: tchildren[i].Copy(),
		})
	}
}

func diffUnorderedChildren(base, target *Element, bchildren, tchildren []*Element,
	opts DiffOptions, ops *[]DiffOperation) {
	used := make([]bool, len(bchildren))
	for _, t := range tchildren {
		match := -1
		for i, b := range bchildren {
			if !used[i] && b.DeepEqual(t) {
				match = i
				break
			}
		}
		if match >= 0 {
			used[match] = true
			continue
		}
		*ops = append(*ops, DiffOperation{Type: OpAdd, Path: elementPath(base), NewValue: t.Copy()})
	}
	for i := len(bchildren) - 1; i >= 0; i-- {
		if !used[i] {
			*ops = append(*ops, DiffOperation{
				Type: OpRemove, Path: elementPath(bchildren[i]),
				OldValue: bchildren[i].Copy(),
			})
		}
	}
}

func keyFor(e *Element, opts DiffOptions) (string, bool) {
	name := e.Tag
	attrName, ok := opts.KeyAttributes[name]
	if !ok {
		attrName, ok = opts.KeyAttributes[e.FullTag()]
	}
	if !ok {
		attrName, ok = opts.KeyAttributes["*"]
	}
	if !ok && len(opts.KeyAttributes) == 1 {
		// A single configured key is also useful when the element name changes:
		// the key identifies the node, not its tag.
		for _, configured := range opts.KeyAttributes {
			attrName, ok = configured, true
		}
	}
	if !ok {
		return "", false
	}
	attr := e.SelectAttr(attrName)
	if attr == nil {
		return "", false
	}
	return attr.Value, true
}

func diffKeyChildren(base, target *Element, bchildren, tchildren []*Element,
	opts DiffOptions, ops *[]DiffOperation) {
	byKey := make(map[string][]int)
	for i, b := range bchildren {
		if key, ok := keyFor(b, opts); ok {
			byKey[key] = append(byKey[key], i)
		}
	}
	used := make([]bool, len(bchildren))
	for ti, t := range tchildren {
		key, keyed := keyFor(t, opts)
		match := -1
		if keyed {
			for _, i := range byKey[key] {
				if !used[i] {
					match = i
					break
				}
			}
		}
		if match < 0 && ti < len(bchildren) && !used[ti] && !keyed {
			match = ti
		}
		if match < 0 {
			*ops = append(*ops, DiffOperation{Type: OpAdd, Path: elementPath(base), NewValue: t.Copy()})
			continue
		}
		used[match] = true
		b := bchildren[match]
		if !sameElementName(b, t) {
			*ops = append(*ops, DiffOperation{
				Type: OpReplace, Path: elementPath(b), OldValue: b.Copy(), NewValue: t.Copy(),
			})
		} else {
			diffElement(b, t, opts, ops)
		}
		if !opts.IgnoreOrder && match != ti && keyed {
			*ops = append(*ops, DiffOperation{
				Type: OpMove, OldPath: elementPath(b), NewPath: elementPath(t),
				Path: elementPath(b), OldValue: b.Copy(), NewValue: t.Copy(),
			})
		}
	}
	for i := len(bchildren) - 1; i >= 0; i-- {
		if !used[i] {
			*ops = append(*ops, DiffOperation{
				Type: OpRemove, Path: elementPath(bchildren[i]),
				OldValue: bchildren[i].Copy(),
			})
		}
	}
}

func diffHashChildren(base, target *Element, bchildren, tchildren []*Element,
	opts DiffOptions, ops *[]DiffOperation) {
	byHash := make(map[string][]int)
	for i, b := range bchildren {
		byHash[elementHash(b)] = append(byHash[elementHash(b)], i)
	}
	used := make([]bool, len(bchildren))
	for _, t := range tchildren {
		hash := elementHash(t)
		match := -1
		for _, i := range byHash[hash] {
			if !used[i] {
				match = i
				break
			}
		}
		if match >= 0 {
			used[match] = true
		} else {
			*ops = append(*ops, DiffOperation{Type: OpAdd, Path: elementPath(base), NewValue: t.Copy()})
		}
	}
	for i := len(bchildren) - 1; i >= 0; i-- {
		if !used[i] {
			*ops = append(*ops, DiffOperation{
				Type: OpRemove, Path: elementPath(bchildren[i]),
				OldValue: bchildren[i].Copy(),
			})
		}
	}
}

func elementHash(e *Element) string {
	var b strings.Builder
	writeHashElement(&b, e)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func writeHashElement(b *strings.Builder, e *Element) {
	b.WriteString(e.Space)
	b.WriteByte(':')
	b.WriteString(e.Tag)
	attrs := append([]Attr(nil), e.Attr...)
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].FullKey() < attrs[j].FullKey() })
	for _, a := range attrs {
		b.WriteByte('|')
		b.WriteString(a.FullKey())
		b.WriteByte('=')
		b.WriteString(a.Value)
	}
	for _, child := range e.Child {
		switch c := child.(type) {
		case *Element:
			b.WriteByte('{')
			writeHashElement(b, c)
			b.WriteByte('}')
		case *CharData:
			b.WriteString(c.Data)
		}
	}
}

func elementPath(e *Element) string {
	if e == nil {
		return ""
	}
	var parts []string
	for current := e; current != nil && current.Tag != ""; current = current.Parent() {
		part := current.FullTag()
		if parent := current.Parent(); parent != nil && parent.Tag != "" {
			position := 0
			for _, child := range parent.Child {
				c, ok := child.(*Element)
				if !ok || !sameElementName(c, current) {
					continue
				}
				position++
				if c == current {
					break
				}
			}
			part += "[" + strconv.Itoa(position) + "]"
		}
		parts = append(parts, part)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/")
}

func (d *Document) Diff(other *Document, opts DiffOptions) ([]DiffOperation, error) {
	return Diff(d, other, opts)
}

func (d *Document) Patch(patch *Document) error { return ApplyPatch(d, patch) }

func (d *Document) Merge3Way(ours, theirs *Document, opts MergeOptions) (*Document, []MergeConflict, error) {
	return Merge3Way(d, ours, theirs, opts)
}

const patchNamespace = "urn:ietf:params:xml:ns:patch-ops"

// GeneratePatch converts operations to the XML patch-ops representation.
func GeneratePatch(ops []DiffOperation) *Document {
	doc := NewDocument()
	root := doc.CreateElement("diff")
	root.CreateAttr("xmlns", patchNamespace)
	for _, op := range ops {
		switch op.Type {
		case OpAdd:
			appendPatchAdd(root, op.Path, op.AttrName, op.NewValue)
		case OpRemove:
			value := op.OldValue
			if value == nil {
				value = op.NewValue
			}
			appendPatchRemove(root, op.Path, value)
		case OpReplace:
			appendPatchReplace(root, op.Path, op.NewValue)
		case OpMove:
			appendPatchRemove(root, op.OldPath, op.OldValue)
			parent, _ := splitParentPath(op.NewPath)
			appendPatchAdd(root, parent, "", op.NewValue)
		case OpUpdateAttr:
			if op.OldValue == nil {
				add := root.CreateElement("add")
				add.CreateAttr("sel", op.Path)
				add.CreateAttr("type", "attribute")
				add.CreateAttr("name", op.AttrName)
				if value, ok := op.NewValue.(string); ok {
					add.CreateText(value)
				}
			} else if op.NewValue == nil {
				appendPatchRemove(root, joinAttrPath(op.Path, op.AttrName), op.OldValue)
			} else {
				appendPatchReplace(root, joinAttrPath(op.Path, op.AttrName), op.NewValue)
			}
		case OpUpdateText:
			appendPatchReplace(root, strings.TrimSuffix(op.Path, "/text()")+"/text()", op.NewValue)
		}
	}
	return doc
}

func appendPatchAdd(root *Element, path, attrName string, value interface{}) {
	add := root.CreateElement("add")
	add.CreateAttr("sel", path)
	if attrName != "" {
		add.CreateAttr("type", "attribute")
		add.CreateAttr("name", attrName)
	}
	appendPatchValue(add, value)
}

func appendPatchRemove(root *Element, path string, value interface{}) {
	remove := root.CreateElement("remove")
	remove.CreateAttr("sel", path)
	appendPatchValue(remove, value)
}

func appendPatchReplace(root *Element, path string, value interface{}) {
	replace := root.CreateElement("replace")
	replace.CreateAttr("sel", path)
	appendPatchValue(replace, value)
}

func appendPatchValue(parent *Element, value interface{}) {
	switch v := value.(type) {
	case *Element:
		if v != nil {
			parent.AddChild(v.Copy())
		}
	case string:
		parent.CreateText(v)
	}
}

// ApplyPatch applies an XML patch-ops document to doc.
func ApplyPatch(doc, patch *Document) error {
	if doc == nil || patch == nil {
		return errors.New("etree: cannot apply a patch to a nil document")
	}
	root := patch.Root()
	if root == nil || root.Tag != "diff" {
		return errors.New("etree: invalid patch document")
	}
	for _, op := range root.ChildElements() {
		sel := op.SelectAttrValue("sel", "")
		if sel == "" {
			return errors.New("etree: patch operation has no sel attribute")
		}
		switch op.Tag {
		case "add":
			if err := applyPatchAdd(doc, op, sel); err != nil {
				return err
			}
		case "remove":
			if err := applyPatchRemove(doc, op, sel); err != nil {
				return err
			}
		case "replace":
			if err := applyPatchReplace(doc, op, sel); err != nil {
				return err
			}
		default:
			return fmt.Errorf("etree: unsupported patch operation %q", op.Tag)
		}
	}
	return nil
}

func applyPatchAdd(doc *Document, op *Element, sel string) error {
	if op.SelectAttrValue("type", "") == "attribute" {
		target, err := patchElement(doc, sel)
		if err != nil {
			return err
		}
		target.CreateAttr(op.SelectAttrValue("name", ""), patchText(op))
		return nil
	}
	if sel == "/" {
		for _, child := range op.Child {
			if e, ok := child.(*Element); ok {
				doc.SetRoot(e.Copy())
				return nil
			}
		}
		return errors.New("etree: root add has no element")
	}
	parent, err := patchElement(doc, sel)
	if err != nil {
		return err
	}
	for _, child := range op.Child {
		switch c := child.(type) {
		case *Element:
			parent.AddChild(c.Copy())
		case *CharData:
			parent.AddChild(c.dup(nil))
		}
	}
	return nil
}

func applyPatchRemove(doc *Document, op *Element, sel string) error {
	if strings.HasSuffix(sel, "/text()") {
		target, err := patchElement(doc, strings.TrimSuffix(sel, "/text()"))
		if err != nil {
			return err
		}
		target.SetText("")
		return nil
	}
	if attrPath, attrName, ok := splitAttrPath(sel); ok {
		target, err := patchElement(doc, attrPath)
		if err != nil {
			return err
		}
		if target.RemoveAttr(attrName) == nil {
			return fmt.Errorf("etree: patch selector did not match attribute %q", sel)
		}
		return nil
	}
	target, err := patchElement(doc, sel)
	if err != nil {
		return err
	}
	if target.Parent() == nil {
		return errors.New("etree: cannot remove an unparented element")
	}
	target.Parent().RemoveChild(target)
	return nil
}

func applyPatchReplace(doc *Document, op *Element, sel string) error {
	if strings.HasSuffix(sel, "/text()") {
		target, err := patchElement(doc, strings.TrimSuffix(sel, "/text()"))
		if err != nil {
			return err
		}
		target.SetText(patchText(op))
		return nil
	}
	if attrPath, attrName, ok := splitAttrPath(sel); ok {
		target, err := patchElement(doc, attrPath)
		if err != nil {
			return err
		}
		if target.SelectAttr(attrName) == nil {
			return fmt.Errorf("etree: patch selector did not match attribute %q", sel)
		}
		target.CreateAttr(attrName, patchText(op))
		return nil
	}
	target, err := patchElement(doc, sel)
	if err != nil {
		return err
	}
	var replacement *Element
	for _, child := range op.Child {
		if e, ok := child.(*Element); ok {
			replacement = e.Copy()
			break
		}
	}
	if replacement == nil {
		return errors.New("etree: element replacement has no element value")
	}
	parent := target.Parent()
	if parent == nil {
		return errors.New("etree: cannot replace an unparented element")
	}
	parent.InsertChildAt(target.Index(), replacement)
	parent.RemoveChild(target)
	return nil
}

func patchText(op *Element) string {
	var b strings.Builder
	for _, child := range op.Child {
		if c, ok := child.(*CharData); ok {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}

func patchElement(doc *Document, sel string) (*Element, error) {
	if sel == "" || strings.HasSuffix(sel, "/text()") {
		return nil, fmt.Errorf("etree: invalid patch selector %q", sel)
	}
	element := doc.FindElement(sel)
	if element == nil {
		return nil, fmt.Errorf("etree: patch selector did not match %q", sel)
	}
	return element, nil
}

func splitAttrPath(path string) (string, string, bool) {
	i := strings.LastIndex(path, "/@")
	if i < 0 || i+2 >= len(path) {
		return "", "", false
	}
	return path[:i], path[i+2:], true
}

func joinAttrPath(path, attr string) string {
	return strings.TrimSuffix(path, "/") + "/@" + attr
}

func splitParentPath(path string) (string, string) {
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return "/", strings.TrimPrefix(path, "/")
	}
	return path[:i], path[i+1:]
}

// ReversePatch returns a patch with operation order and direction reversed.
func ReversePatch(patch *Document) (*Document, error) {
	if patch == nil {
		return nil, errors.New("etree: cannot reverse a nil patch")
	}
	root := patch.Root()
	if root == nil || root.Tag != "diff" {
		return nil, errors.New("etree: invalid patch document")
	}
	reversed := NewDocument()
	out := reversed.CreateElement("diff")
	out.CreateAttr("xmlns", patchNamespace)
	ops := root.ChildElements()
	for i := len(ops) - 1; i >= 0; i-- {
		op := ops[i]
		sel := op.SelectAttrValue("sel", "")
		switch op.Tag {
		case "add":
			if op.SelectAttrValue("type", "") == "attribute" {
				appendPatchRemove(out, joinAttrPath(sel, op.SelectAttrValue("name", "")), nil)
			} else {
				appendPatchRemove(out, sel, nil)
			}
		case "remove":
			if strings.HasSuffix(sel, "/text()") {
				appendPatchReplace(out, sel, opTextValue(op))
			} else if attrPath, attrName, ok := splitAttrPath(sel); ok {
				add := out.CreateElement("add")
				add.CreateAttr("sel", attrPath)
				add.CreateAttr("type", "attribute")
				add.CreateAttr("name", attrName)
				appendPatchValue(add, opTextValue(op))
			} else {
				parent, _ := splitParentPath(sel)
				appendPatchAdd(out, parent, "", firstPatchValue(op))
			}
		case "replace":
			appendPatchReplace(out, sel, firstPatchValue(op))
		default:
			return nil, fmt.Errorf("etree: unsupported patch operation %q", op.Tag)
		}
	}
	return reversed, nil
}

func opTextValue(op *Element) string { return patchText(op) }

func firstPatchValue(op *Element) interface{} {
	for _, child := range op.Child {
		switch c := child.(type) {
		case *Element:
			return c.Copy()
		case *CharData:
			return c.Data
		}
	}
	return nil
}

// Merge3Way merges ours and theirs against base.
func Merge3Way(base, ours, theirs *Document, opts MergeOptions) (*Document, []MergeConflict, error) {
	if base == nil || ours == nil || theirs == nil {
		return nil, nil, errors.New("etree: cannot merge a nil document")
	}
	bopts := DefaultDiffOptions()
	left, err := Diff(base, ours, bopts)
	if err != nil {
		return nil, nil, err
	}
	right, err := Diff(base, theirs, bopts)
	if err != nil {
		return nil, nil, err
	}
	merged := base.Copy()
	conflicts, pairs := mergeConflicts(base, left, right)
	blockedLeft, blockedRight := make(map[int]bool), make(map[int]bool)
	for _, pair := range pairs {
		conflict := &conflicts[pair.conflict]
		if opts.AutoResolve {
			conflict.Resolve(opts.DefaultResolution, nil)
			if opts.DefaultResolution == ResolutionOurs {
				blockedRight[pair.right] = true
			} else {
				blockedLeft[pair.left] = true
			}
		} else {
			blockedLeft[pair.left] = true
			blockedRight[pair.right] = true
		}
	}
	selected := make([]DiffOperation, 0, len(left)+len(right))
	for i, op := range left {
		if !blockedLeft[i] {
			selected = append(selected, op)
		}
	}
	for i, op := range right {
		if !blockedRight[i] {
			selected = append(selected, op)
		}
	}
	applyOperations(merged, selected)
	if merged.Metadata == nil {
		merged.Metadata = make(map[string]string)
	}
	merged.Metadata["merge.base"] = rootTag(base)
	merged.Metadata["merge.ours"] = rootTag(ours)
	merged.Metadata["merge.theirs"] = rootTag(theirs)
	return merged, conflicts, nil
}

type conflictPair struct{ left, right, conflict int }

func mergeConflicts(base *Document, left, right []DiffOperation) ([]MergeConflict, []conflictPair) {
	var conflicts []MergeConflict
	var pairs []conflictPair
	for li, a := range left {
		for ri, b := range right {
			kind, path, ok := operationConflict(a, b)
			if !ok {
				continue
			}
			if sameOperationResult(a, b) {
				continue
			}
			conflict := MergeConflict{
				Path: path, BaseValue: operationBaseValue(base, a),
				OursValue: operationValue(a), TheirsValue: operationValue(b),
				Type: kind,
			}
			conflicts = append(conflicts, conflict)
			pairs = append(pairs, conflictPair{li, ri, len(conflicts) - 1})
		}
	}
	return conflicts, pairs
}

func operationConflict(a, b DiffOperation) (ConflictType, string, bool) {
	ta, tb := operationTarget(a), operationTarget(b)
	if ta == tb {
		if a.Type == OpRemove || b.Type == OpRemove {
			if a.Type == b.Type {
				return ConflictBothModified, ta, true
			}
			if isTextOrAttr(a) || isTextOrAttr(b) {
				return ConflictModifyDelete, ta, true
			}
			return ConflictStructural, ta, true
		}
		if a.Type == OpAdd && b.Type == OpAdd {
			return ConflictStructural, ta, false
		}
		return ConflictBothModified, ta, true
	}
	if isAncestorPath(a.Path, b.Path) || isAncestorPath(b.Path, a.Path) {
		ancestor := a
		child := b
		if !isAncestorPath(a.Path, b.Path) {
			ancestor, child = b, a
		}
		if ancestor.Type == OpRemove {
			if isTextOrAttr(child) {
				return ConflictModifyDelete, ancestor.Path, true
			}
			if child.Type == OpAdd || child.Type == OpRemove {
				return ConflictStructural, ancestor.Path, true
			}
		}
	}
	return ConflictBothModified, "", false
}

func operationTarget(op DiffOperation) string {
	if op.Type == OpUpdateAttr {
		return joinAttrPath(op.Path, op.AttrName)
	}
	if op.Type == OpMove {
		return op.OldPath
	}
	return op.Path
}

func isTextOrAttr(op DiffOperation) bool {
	return op.Type == OpUpdateText || op.Type == OpUpdateAttr
}

func isAncestorPath(a, b string) bool {
	return a != "" && b != "" && strings.HasPrefix(b, strings.TrimSuffix(a, "/")+"/")
}

func sameOperationResult(a, b DiffOperation) bool {
	return a.Type == b.Type && operationTarget(a) == operationTarget(b) &&
		valuesDeepEqual(a.NewValue, b.NewValue)
}

func valuesDeepEqual(a, b interface{}) bool {
	switch aa := a.(type) {
	case *Element:
		bb, ok := b.(*Element)
		return ok && aa.DeepEqual(bb)
	default:
		return fmt.Sprint(a) == fmt.Sprint(b)
	}
}

func operationValue(op DiffOperation) interface{} {
	if op.Type == OpRemove {
		return op.OldValue
	}
	return op.NewValue
}

func operationBaseValue(base *Document, op DiffOperation) interface{} {
	if op.Type == OpAdd {
		return nil
	}
	if op.Type == OpUpdateAttr {
		if element := base.FindElement(op.Path); element != nil {
			if attr := element.SelectAttr(op.AttrName); attr != nil {
				return attr.Value
			}
		}
		return nil
	}
	if op.Type == OpUpdateText {
		if element := base.FindElement(op.Path); element != nil {
			return element.Text()
		}
		return nil
	}
	if element := base.FindElement(op.Path); element != nil {
		return element.Copy()
	}
	return nil
}

func applyOperations(doc *Document, ops []DiffOperation) {
	sort.SliceStable(ops, func(i, j int) bool {
		ri, rj := operationRank(ops[i]), operationRank(ops[j])
		if ri != rj {
			return ri < rj
		}
		if ri == 3 {
			return ops[i].Path > ops[j].Path
		}
		return i < j
	})
	for _, op := range ops {
		applyOperation(doc, op)
	}
}

func operationRank(op DiffOperation) int {
	switch op.Type {
	case OpUpdateAttr, OpUpdateText, OpReplace:
		return 0
	case OpMove:
		return 1
	case OpAdd:
		return 2
	case OpRemove:
		return 3
	default:
		return 4
	}
}

func applyOperation(doc *Document, op DiffOperation) {
	switch op.Type {
	case OpAdd:
		if op.Path == "/" {
			if e, ok := op.NewValue.(*Element); ok {
				doc.SetRoot(e.Copy())
			}
			return
		}
		if parent := doc.FindElement(op.Path); parent != nil {
			if e, ok := op.NewValue.(*Element); ok {
				parent.AddChild(e.Copy())
			}
		}
	case OpRemove:
		if e := doc.FindElement(op.Path); e != nil && e.Parent() != nil {
			e.Parent().RemoveChild(e)
		}
	case OpReplace:
		if e, replacement := doc.FindElement(op.Path), elementValue(op.NewValue); e != nil && replacement != nil && e.Parent() != nil {
			e.Parent().InsertChildAt(e.Index(), replacement.Copy())
			e.Parent().RemoveChild(e)
		}
	case OpUpdateText:
		if e := doc.FindElement(op.Path); e != nil {
			if value, ok := op.NewValue.(string); ok {
				e.SetText(value)
			}
		}
	case OpUpdateAttr:
		if e := doc.FindElement(op.Path); e != nil {
			if op.NewValue == nil {
				e.RemoveAttr(op.AttrName)
			} else if value, ok := op.NewValue.(string); ok {
				e.CreateAttr(op.AttrName, value)
			}
		}
	case OpMove:
		e := doc.FindElement(op.OldPath)
		parentPath, _ := splitParentPath(op.NewPath)
		parent := doc.FindElement(parentPath)
		if e != nil && parent != nil {
			parent.AddChild(e)
		}
	}
}

func elementValue(value interface{}) *Element {
	e, _ := value.(*Element)
	return e
}

func rootTag(d *Document) string {
	if d == nil || d.Root() == nil {
		return ""
	}
	return d.Root().Tag
}
