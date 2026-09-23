package checker_test

// Documented is a test type. See [Documented.Method], [Documented.PtrMethod],
// [Documented.Field] and [Documented.Embedded].
type Documented struct {
	// Field is a plain field. See [Inner.Embedded].
	Field int

	Inner
}

// Inner is embedded into [Documented].
type Inner struct {
	Embedded int
}

// M is promoted through embedding.
func (Inner) M() {}

// Method has a value receiver.
func (Documented) Method() {}

// PtrMethod has a pointer receiver.
func (*Documented) PtrMethod() {}

// PlainFunc is a package-level function. See [PlainFunc].
func PlainFunc() {}

// PlainConst is a package-level constant.
const PlainConst = 1

// PlainVar is a package-level variable.
var PlainVar Documented

// Alias is an alias of [Documented]. See [Alias.Method] and [Alias.Embedded].
type Alias = Documented

// GenericBox is a generic struct. See [GenericBox.V] and [GenericBox.Get].
type GenericBox[T any] struct {
	V T
}

// Get returns the box value.
func (g GenericBox[T]) Get() T { return g.V }

// ReaderIface embeds a method set. See [ReaderIface.Read].
type ReaderIface interface {
	Read([]byte) (int, error)
}
