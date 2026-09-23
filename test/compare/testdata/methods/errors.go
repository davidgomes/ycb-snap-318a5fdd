// errorcheck

package main

import "fmt"

type T struct{ f int }

func (t T) Value() int    { return t.f }
func (t *T) Pointer() int { return t.f }

func (i int) M() {} // ERROR `cannot define new methods on non-local type int`

type P *T

func (p P) M() {} // ERROR `invalid receiver type P (pointer or interface type)`

func (t T) Value() {} // ERROR `method T.Value already declared`

func (t T) f() {} // ERROR `field and method with the same name f`

func main() {
	t := T{}
	_ = t
	var _ fmt.Stringer
	_ = T.Value
	_ = (*T).Value
	_ = (*T).Pointer
	_ = T.Pointer // ERROR `invalid method expression T.Pointer (needs pointer receiver: (*T).Pointer)`
	T{}.Pointer() // ERROR `cannot call pointer method Pointer on T`
	t.Missing()   // ERROR `t.Missing undefined (type T has no field or method Missing)`
	var _ fmt.Stringer = t // ERROR `cannot use t (type T) as type fmt.Stringer in assignment`
}
