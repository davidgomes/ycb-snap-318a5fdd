// run

package main

import "fmt"

type N int

// Recursive method.
func (n N) Fib() int {
	if n < 2 {
		return int(n)
	}
	return (n - 1).Fib() + (n - 2).Fib()
}

type Counter struct{ n int }

func (c *Counter) Inc() { c.n++ }

// Closures that capture the receiver.
func (c *Counter) Adder() func()      { return func() { c.n++ } }
func (c Counter) Getter() func() int { return func() int { return c.n } }

// Method value returned by a method.
func (c *Counter) Bound() func() { return c.Inc }

// Named results modified by a deferred function.
func (c Counter) Double() (res int) {
	defer func() { res *= 2 }()
	return c.n + 1
}

// Assignment to the receiver.
func (c Counter) Replace() int {
	c = Counter{99}
	return c.n
}

type Worker struct{ id int }

func (w Worker) Do(ch chan<- int) { ch <- w.id * 10 }

func main() {

	fmt.Println(N(15).Fib())

	var c Counter
	add := c.Adder()
	add()
	add()
	get := c.Getter()
	c.n = 50
	fmt.Println(c.n, get())
	fmt.Println(c.Double())
	b := c.Bound()
	b()
	fmt.Println(c.n)
	fmt.Println(c.Replace(), c.n)

	ch := make(chan int, 3)
	for i := 1; i <= 3; i++ {
		go Worker{i}.Do(ch)
	}
	sum := 0
	for i := 0; i < 3; i++ {
		sum += <-ch
	}
	fmt.Println(sum)

	cs := []Counter{{1}, {2}}
	for i := range cs {
		cs[i].Inc()
	}
	fmt.Println(cs[0].n, cs[1].n)

	// Calling a method on a nil interface value panics.
	defer func() {
		fmt.Println("recovered:", recover() != nil)
	}()
	var s fmt.Stringer
	_ = s.String()

}
