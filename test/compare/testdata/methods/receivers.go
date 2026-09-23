// run

package main

import "fmt"

type Counter struct {
	n int
}

// Pointer receiver.
func (c *Counter) Inc() { c.n++ }

// Value receiver.
func (c Counter) Get() int { return c.n }

// Value receiver that modifies a copy.
func (c Counter) Reset() { c.n = 0 }

// Unnamed value receiver.
func (Counter) Kind() string { return "counter" }

// Unnamed pointer receiver.
func (*Counter) PtrKind() string { return "*counter" }

// Blank receiver.
func (_ Counter) Blank() string { return "blank" }

// Methods with parameters and results.
func (c *Counter) Add(delta int) (old, new int) {
	old = c.n
	c.n += delta
	return old, c.n
}

func (c *Counter) AddAll(values ...int) int {
	for _, v := range values {
		c.n += v
	}
	return c.n
}

// A method can call other methods on its receiver.
func (c *Counter) IncTwice() {
	c.Inc()
	c.Inc()
}

func (c Counter) Twice() int { return c.Get() * 2 }

// A method can be declared after its first use and can refer to package
// variables and functions.
func useLater() string { return Later(3).Describe() }

type Later int

var prefix = "later:"

func (l Later) Describe() string { return prefix + fmt.Sprint(int(l)) }

func main() {

	var c Counter
	c.Inc()
	c.Inc()
	fmt.Println(c.Get(), c.n)

	c.Reset()
	fmt.Println(c.Get())

	fmt.Println(c.Kind(), c.PtrKind(), c.Blank())

	old, n := c.Add(10)
	fmt.Println(old, n)

	fmt.Println(c.AddAll(1, 2, 3))
	fmt.Println(c.AddAll([]int{4, 5}...))
	fmt.Println(c.AddAll())

	c.IncTwice()
	fmt.Println(c.Get(), c.Twice())

	// Pointer values.
	p := &Counter{n: 100}
	p.Inc()
	fmt.Println(p.Get(), p.Kind(), p.PtrKind(), p.Twice())

	// Composite literal values.
	fmt.Println(Counter{n: 7}.Get(), (&Counter{n: 8}).Get())

	fmt.Println(useLater())

	// Methods in closures.
	inc := func() { c.Inc() }
	inc()
	inc()
	fmt.Println(c.Get())

	// Methods with defer.
	func() {
		defer c.Inc()
		defer fmt.Println("deferred", c.Get())
	}()
	fmt.Println(c.Get())

	// Receivers are evaluated once.
	calls := 0
	get := func() *Counter {
		calls++
		return &c
	}
	get().Inc()
	fmt.Println(calls, c.Get())

}
