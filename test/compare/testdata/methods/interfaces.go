// run

package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type argError struct {
	arg  int
	prob string
}

func (e *argError) Error() string { return fmt.Sprintf("%d - %s", e.arg, e.prob) }

func f2(arg int) (int, error) {
	if arg == 42 {
		return -1, &argError{arg, "can't work with it"}
	}
	return arg + 3, nil
}

type byLength []string

func (s byLength) Len() int           { return len(s) }
func (s byLength) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s byLength) Less(i, j int) bool { return len(s[i]) < len(s[j]) }

type Dog struct{ name string }

func (d Dog) String() string { return "Dog " + d.name }

type Cat struct{ lives int }

func (c *Cat) String() string { return fmt.Sprintf("Cat with %d lives", c.lives) }

type Temp float64

func (t Temp) String() string { return fmt.Sprintf("%.1f°", float64(t)) }

type Stringer fmt.Stringer

var global fmt.Stringer = Dog{"global"}

func describe(i interface{}) string {
	switch v := i.(type) {
	case fmt.Stringer:
		return "stringer " + v.String()
	case error:
		return "error " + v.Error()
	case int:
		return "int"
	default:
		return "other"
	}
}

func main() {
	for _, i := range []int{7, 42} {
		if r, e := f2(i); e != nil {
			fmt.Println("f2 failed:", e)
		} else {
			fmt.Println("f2 worked:", r)
		}
	}
	_, e := f2(42)
	if ae, ok := e.(*argError); ok {
		fmt.Println(ae.arg)
		fmt.Println(ae.prob)
	}
	var err error = &argError{1, "one"}
	fmt.Println(err.Error(), err)

	fruits := []string{"peach", "banana", "kiwi"}
	sort.Sort(byLength(fruits))
	fmt.Println(fruits)

	var s fmt.Stringer
	s = Dog{"rex"}
	fmt.Println(s.String())
	s = &Cat{9}
	fmt.Println(s.String())
	s = Temp(36.6)
	fmt.Println(s.String())
	str := s.String
	s = Dog{"other"}
	fmt.Println(str(), s)

	animals := []fmt.Stringer{Dog{"rex"}, &Cat{9}, Temp(36.6)}
	for _, a := range animals {
		fmt.Println(a.String())
	}
	fmt.Println(animals[0], animals[1], animals[2])

	fmt.Println(global.String(), global)
	global = &Cat{3}
	fmt.Println(global)

	m := map[string]fmt.Stringer{"d": Dog{"m"}}
	fmt.Println(m["d"].String())

	var st Stringer = Dog{"defined"}
	fmt.Println(st.String())

	fmt.Println(describe(Dog{"x"}), describe(&Cat{1}), describe(Cat{2}), describe(&argError{1, "p"}), describe(5))

	var sd fmt.Stringer = Dog{"y"}
	if d, ok := sd.(Dog); ok {
		fmt.Println("is dog", d.name)
	}
	if _, ok := sd.(*Cat); !ok {
		fmt.Println("not cat")
	}
	var any interface{} = Cat{4}
	_, ok := any.(fmt.Stringer)
	fmt.Println("Cat is a fmt.Stringer:", ok)
	any = &Cat{4}
	_, ok = any.(fmt.Stringer)
	fmt.Println("*Cat is a fmt.Stringer:", ok)

	closure := func() string { return sd.String() }
	sd = &Cat{5}
	fmt.Println(closure())

	fmt.Println(strings.ToUpper(Temp(1).String()))
	fmt.Printf("%v %s\n", Dog{"p"}, &Cat{7})
	fmt.Println(errors.New("native") != error(&argError{}))
}
