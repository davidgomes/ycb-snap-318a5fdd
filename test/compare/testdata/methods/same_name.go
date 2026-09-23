// run

package main

import "fmt"

type Dog struct{ name string }
type Cat struct{ name string }
type Fish int

func (d Dog) Speak() string  { return d.name + " says woof" }
func (c *Cat) Speak() string { return c.name + " says meow" }
func (Fish) Speak() string   { return "..." }

func (d Dog) Name() string  { return d.name }
func (c *Cat) Name() string { return c.name }

// A function can have the same name as a method.
func Speak() string { return "function" }

// A method can have the same name as a field of another type.
type Named struct{ Name string }

func main() {
	d := Dog{"rex"}
	c := &Cat{"tom"}
	var f Fish
	fmt.Println(d.Speak())
	fmt.Println(c.Speak())
	fmt.Println(f.Speak())
	fmt.Println(Speak())
	fmt.Println(d.Name(), c.Name(), Named{"n"}.Name)

	fs := []func() string{d.Speak, c.Speak, f.Speak, Speak}
	for _, s := range fs {
		fmt.Println(s())
	}

	fmt.Println(Dog.Speak(Dog{"a"}), (*Cat).Speak(&Cat{"b"}), Fish.Speak(0))
}
