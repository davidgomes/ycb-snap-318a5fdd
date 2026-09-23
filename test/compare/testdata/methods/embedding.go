// run

package main

import (
	"fmt"
	"strings"
)

type base struct {
	num int
}

func (b base) describe() string {
	return fmt.Sprintf("base with num=%v", b.num)
}

func (b *base) bump() { b.num++ }

type container struct {
	base
	str string
}

type Named struct{ Name string }

func (n Named) String() string   { return "named " + n.Name }
func (n *Named) Rename(s string) { n.Name = s }

type Employee struct {
	*Named
	Role string
}

type Manager struct {
	Employee
	Reports int
}

type Loud struct{ Named }

func (l Loud) String() string { return strings.ToUpper(l.Named.String()) }

type Both struct {
	Named
	base
}

func main() {
	co := container{
		base: base{num: 1},
		str:  "some name",
	}
	fmt.Printf("co={num: %v, str: %v}\n", co.num, co.str)
	fmt.Println("also num:", co.base.num)
	fmt.Println("describe:", co.describe())
	co.bump()
	fmt.Println("describe:", co.describe())
	d := co.describe
	co.bump()
	fmt.Println(d(), co.describe())
	pc := &co
	pc.bump()
	fmt.Println(pc.describe())

	e := Employee{&Named{"ann"}, "dev"}
	fmt.Println(e.String())
	e.Rename("bob")
	fmt.Println(e.String(), e.Name)
	var s fmt.Stringer = e
	fmt.Println(s.String(), s)

	m := Manager{Employee{&Named{"carl"}, "boss"}, 3}
	fmt.Println(m.String())
	m.Rename("dan")
	s = m
	fmt.Println(s)
	s = &m
	fmt.Println(s.String())

	l := Loud{Named{"eve"}}
	fmt.Println(l.String(), l.Named.String())
	s = l
	fmt.Println(s)

	b := Both{Named{"fay"}, base{7}}
	fmt.Println(b.String(), b.describe())
	b.Rename("gus")
	b.bump()
	fmt.Println(b.String(), b.describe())
	s = &b
	fmt.Println(s)

	var nv Named
	var i interface{} = nv
	_, ok := i.(fmt.Stringer)
	fmt.Println(ok)
	type anon = struct{ Named }
	var a anon
	a.Rename("hal")
	fmt.Println(a.String())
	i = a
	_, ok = i.(fmt.Stringer)
	fmt.Println(ok)
}
