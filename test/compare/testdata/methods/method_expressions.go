// run

package main

import (
	"fmt"
	"strings"
)

type T struct {
	n int
}

func (t T) Value() int             { return t.n }
func (t T) Add(a, b int) int       { return t.n + a + b }
func (t *T) Inc()                  { t.n++ }
func (t *T) Set(n int) *T          { t.n = n; return t }
func (t T) Join(sep string, s ...string) string {
	return fmt.Sprint(t.n) + sep + strings.Join(s, sep)
}

type Upper string

func (u Upper) String() string { return strings.ToUpper(string(u)) }

func apply(f func(T) int, t T) int { return f(t) }

func main() {

	t := T{1}

	// Direct calls.
	fmt.Println(T.Value(t), T.Add(t, 2, 3))
	(*T).Inc(&t)
	fmt.Println((*T).Set(&t, 10).n, t.n)

	// (*T).M where M has a value receiver.
	fmt.Println((*T).Value(&t), (*T).Add(&t, 1, 1))

	// Variadic methods.
	fmt.Println(T.Join(t, "-", "a", "b"), (*T).Join(&t, "+", []string{"c", "d"}...))

	// Function values.
	value := T.Value
	inc := (*T).Inc
	ptrValue := (*T).Value
	inc(&t)
	fmt.Println(value(t), ptrValue(&t))

	// Method expressions as arguments, in composite literals and in maps.
	fmt.Println(apply(T.Value, T{42}))
	fs := []func(T) int{T.Value, func(t T) int { return -t.n }}
	for _, f := range fs {
		fmt.Print(f(T{5}), " ")
	}
	fmt.Println()
	m := map[string]func(Upper) string{"upper": Upper.String}
	fmt.Println(m["upper"]("scriggo"))

	// Method values and method expressions passed to native functions.
	fmt.Println(strings.Map(Shift(1).Rune, "abc"))
	fmt.Println(strings.Map(func(r rune) rune { return Shift.Rune(2, r) }, "abc"))

}

type Shift rune

func (s Shift) Rune(r rune) rune { return r + rune(s) }
