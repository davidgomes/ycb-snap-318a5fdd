// errorcheck

package main

import "fmt"

var _ = fmt.Sprint

type T int

func (t *T) M() {}

type S struct{}

func (s *S) String() string { return "" }

func main() {
	_ = T.M                  // ERROR `invalid method expression T.M (needs pointer receiver: (*T).M)`
	var _ fmt.Stringer = S{} // ERROR `cannot use S{} (type S) as type fmt.Stringer in assignment`
}
