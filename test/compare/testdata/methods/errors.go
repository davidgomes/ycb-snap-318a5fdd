// errorcheck

package main

import "fmt"

type T int

func (t T) Value() int { return int(t) }
func (t *T) Ptr()      {}

func (t *T) String() string { return "T" }

type S struct {
	F int
}

func (S) F() {} // ERROR `type S has both field and method named F`

func (T) Value() int { return 0 } // ERROR `method redeclared: T.Value`

func (*T) Value() int { return 0 } // ERROR `method redeclared: T.Value`

func (i int) M() {} // ERROR `cannot define new methods on non-local type int`

func (s fmt.Stringer) M() {} // ERROR `cannot define new methods on non-local type fmt.Stringer`

type P *int

func (P) M() {} // ERROR `invalid receiver type P (pointer or interface type)`

type I interface{}

func (I) M() {} // ERROR `invalid receiver type I (pointer or interface type)`

func (**T) M() {} // ERROR `invalid receiver type **T`

func (t T) Dup(t int) {} // ERROR `duplicate argument t`

func (u *U) M() {} // ERROR `undefined: U`

func main() {
	_ = T.Ptr // ERROR `invalid method expression T.Ptr (needs pointer receiver: (*T).Ptr)`
	T(1).Ptr() // ERROR `cannot call pointer method Ptr on T`
	var t T
	t.Missing() // ERROR `t.Missing undefined (type T has no field or method Missing)`
	_ = T.Missing // ERROR `T.Missing undefined (type T has no method Missing)`
	var _ fmt.Stringer = t // ERROR `cannot use t (type T) as type fmt.Stringer in assignment:`
	var _ fmt.Stringer = &t
}
