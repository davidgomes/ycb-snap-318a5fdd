// run

package main

import "fmt"

type Int int
type Str string
type Pair struct{ A, B int }
type Slice []int

func (i Int) Double() Int { return i * 2 }

func (i *Int) Inc() { *i = *i + 1 }

func (Int) Unnamed() string { return "unnamed" }

func (s Str) Len() int { return len(s) }

func (p Pair) Sum() int { return p.A + p.B }

func (s Slice) First() int { return s[0] }

type Other int

func (Other) Double() Other { return 99 }

func main() {
	var i Int = 3
	fmt.Print(i.Double())
	i.Inc()
	fmt.Print(i)
	fmt.Print(i.Unnamed())

	var s Str = "abcd"
	fmt.Print(s.Len())

	p := Pair{2, 5}
	fmt.Print(p.Sum())

	sl := Slice{7, 8, 9}
	fmt.Print(sl.First())

	var o Other = 1
	fmt.Print(o.Double())
}
