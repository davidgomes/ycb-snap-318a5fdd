// run

package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Celsius float64

func (c Celsius) String() string { return fmt.Sprintf("%.1f°C", float64(c)) }

type Point struct{ X, Y int }

func (p *Point) String() string { return fmt.Sprintf("(%d,%d)", p.X, p.Y) }

type NotFoundError struct{ Name string }

func (e NotFoundError) Error() string { return e.Name + " not found" }

type ByLength []string

func (s ByLength) Len() int           { return len(s) }
func (s ByLength) Less(i, j int) bool { return len(s[i]) < len(s[j]) }
func (s ByLength) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func lookup(name string) error {
	if name == "" {
		return nil
	}
	return NotFoundError{name}
}

func describe(s fmt.Stringer) string { return "<" + s.String() + ">" }

var globalStringer fmt.Stringer = Celsius(-4)

type Holder struct {
	S   fmt.Stringer
	Err error
}

func main() {

	// Calls through interface variables.
	var s fmt.Stringer = Celsius(21.5)
	fmt.Println(s.String())
	s = &Point{1, 2}
	fmt.Println(s.String())
	fmt.Println(describe(Celsius(3)), describe(&Point{3, 4}))

	// Method values from interface values.
	str := s.String
	s = Celsius(0)
	fmt.Println(str(), s.String())

	// Errors.
	if err := lookup("key"); err != nil {
		fmt.Println(err.Error())
		fmt.Println(err)
		nf, ok := err.(NotFoundError)
		fmt.Println(ok, nf.Name)
	}
	fmt.Println(lookup("") == nil)
	wrapped := fmt.Errorf("lookup: %w", lookup("x"))
	fmt.Println(wrapped)
	if e, ok := errors.Unwrap(wrapped).(NotFoundError); ok {
		fmt.Println("unwrapped", e.Name)
	}

	// Type assertions and type switches.
	values := []interface{}{Celsius(1), &Point{5, 6}, Point{7, 8}, NotFoundError{"y"}, 42}
	for _, v := range values {
		if st, ok := v.(fmt.Stringer); ok {
			fmt.Println("stringer", st.String())
		}
		switch v := v.(type) {
		case error:
			fmt.Println("error", v.Error())
		case fmt.Stringer:
			fmt.Println("stringer", v)
		default:
			fmt.Println("other")
		}
	}

	// Values stored in variables, slices, maps, struct fields and channels
	// with interface types.
	fmt.Println(globalStringer.String())
	stringers := []fmt.Stringer{Celsius(1), &Point{1, 1}}
	stringers = append(stringers, Celsius(2))
	for _, st := range stringers {
		fmt.Println(st.String())
	}
	fmt.Println(stringers)
	m := map[string]fmt.Stringer{"c": Celsius(3)}
	fmt.Println(m["c"].String())
	h := Holder{S: Celsius(4), Err: NotFoundError{"z"}}
	fmt.Println(h.S.String(), h.Err.Error())
	ch := make(chan error, 1)
	ch <- NotFoundError{"w"}
	fmt.Println((<-ch).Error())

	// Values passed to native code.
	fmt.Println(Celsius(5), &Point{9, 9})
	fmt.Printf("%v %s\n", Celsius(6), NotFoundError{"v"})
	fmt.Println(strings.Repeat(Celsius(7).String(), 2))
	words := ByLength{"banana", "kiwi", "apple", "fig"}
	sort.Sort(words)
	fmt.Println([]string(words))
	sort.Sort(sort.Reverse(words))
	fmt.Println([]string(words))

}
