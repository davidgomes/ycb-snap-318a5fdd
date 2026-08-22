// errorcheck

package main

type T int

func (t *T) Ptr() {}

func main() {
	_ = T.Ptr // ERROR `invalid method expression T.Ptr (needs pointer receiver: \(\*T\).Ptr)`
}
