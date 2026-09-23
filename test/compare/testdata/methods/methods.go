// run

package main

import "fmt"

type Counter struct{ n int }

func (c *Counter) Inc()       { c.n++ }
func (c Counter) Get() int    { return c.n }
func (Counter) Name() string  { return "counter" }
func (*Counter) Kind() string { return "ptr" }

type Int int

func (i Int) String() string { return fmt.Sprint("Int(", int(i), ")") }
func (i Int) Double() Int    { return i * 2 }

type Strings []string

func (s Strings) Len() int { return len(s) }

type M map[string]int

func (m M) Set(k string, v int) { m[k] = v }

type F func() int

func (f F) Call() int { return f() }

type Other int

func (o Other) Get() int { return 42 }
func (o *Other) Inc()    { *o = *o + 10 }

func main() {
	var c Counter
	c.Inc()
	c.Inc()
	fmt.Println(c.Get(), c.Name(), c.Kind())
	p := &c
	p.Inc()
	fmt.Println(p.Get(), p.n)

	var i Int = 3
	fmt.Println(i.Double().String())
	fmt.Println(Strings{"a", "b"}.Len())
	m := M{}
	m.Set("a", 1)
	fmt.Println(m["a"])
	fmt.Println(F(func() int { return 7 }).Call())

	var o Other
	o.Inc()
	fmt.Println(o.Get(), int(o))

	get := Counter.Get
	fmt.Println(get(c), Counter.Get(c))
	inc := (*Counter).Inc
	inc(&c)
	(*Counter).Inc(&c)
	fmt.Println(c.Get())

	func() { c.Inc() }()
	fmt.Println(c.n)

	var s fmt.Stringer = i
	fmt.Println(s.String())
	var e interface{} = i
	if st, ok := e.(fmt.Stringer); ok {
		fmt.Println("assert", st.String())
	}
}
