package vm

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mattn/anko/env"
)

func TestTypedBindings(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		options *Options
		want    interface{}
	}{
		{
			name:    "typed declaration",
			script:  `var x: int64 = 10; x`,
			options: &Options{TypedBindings: true},
			want:    int64(10),
		},
		{
			name:    "multiple typed declarations",
			script:  `var a, b: int64 = 1, 2; a + b`,
			options: &Options{TypedBindings: true},
			want:    int64(3),
		},
		{
			name:    "zero value",
			script:  `var x: int64; x`,
			options: &Options{TypedBindings: true},
			want:    int64(0),
		},
		{
			name:    "interface accepts any value",
			script:  `var x: interface = 10; x = "value"; x`,
			options: &Options{TypedBindings: true},
			want:    "value",
		},
		{
			name:    "typed nil slice",
			script:  `var x: []int64; x = nil; x`,
			options: &Options{TypedBindings: true},
			want:    []int64(nil),
		},
		{
			name:    "typed nil map",
			script:  `var x: map[string]int64; x = nil; x`,
			options: &Options{TypedBindings: true},
			want:    map[string]int64(nil),
		},
		{
			name:    "typed nil pointer",
			script:  `var x: *int64; x = nil; x`,
			options: &Options{TypedBindings: true},
			want:    (*int64)(nil),
		},
		{
			name:    "typed nil channel",
			script:  `var x: chan int64; x = nil; x`,
			options: &Options{TypedBindings: true},
			want:    (chan int64)(nil),
		},
		{
			name:    "disabled enforcement",
			script:  `var x: int64 = 10; x = "value"; x`,
			options: &Options{},
			want:    "value",
		},
		{
			name:    "untyped declaration stays dynamic",
			script:  `var x = 10; x = "value"; x`,
			options: &Options{TypedBindings: true},
			want:    "value",
		},
		{
			name:    "new binding clears constraint",
			script:  `var x: int64 = 10; var x = "value"; x`,
			options: &Options{TypedBindings: true},
			want:    "value",
		},
		{
			name:    "module binding",
			script:  `module m { var x: int64 = 10 }; m.x = 20; m.x`,
			options: &Options{TypedBindings: true},
			want:    int64(20),
		},
		{
			name:    "blank identifier",
			script:  `var _: int64 = "value"`,
			options: &Options{TypedBindings: true},
			want:    "value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := Execute(env.NewEnv(), test.options, test.script)
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if !reflect.DeepEqual(value, test.want) {
				t.Fatalf("Execute returned %#v (%T), want %#v (%T)", value, value, test.want, test.want)
			}
		})
	}
}

type typedStringer struct{}

func (typedStringer) String() string {
	return "typed"
}

func TestTypedBindingInterface(t *testing.T) {
	e := env.NewEnv()
	if err := e.DefineType("stringer", reflect.TypeOf((*fmt.Stringer)(nil)).Elem()); err != nil {
		t.Fatal(err)
	}
	if err := e.Define("value", typedStringer{}); err != nil {
		t.Fatal(err)
	}

	value, err := Execute(e, &Options{TypedBindings: true}, `var x: stringer = value; x`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got, want := value.(typedStringer), (typedStringer{}); got != want {
		t.Fatalf("Execute returned %#v, want %#v", got, want)
	}
}

func TestTypedBindingErrors(t *testing.T) {
	tests := []struct {
		name   string
		script string
		parts  []string
	}{
		{
			name:   "initial value",
			script: `var x: int64 = "value"`,
			parts:  []string{"type error", "x", "string", "int64"},
		},
		{
			name:   "assignment",
			script: `var x: int64 = 10; x = "value"`,
			parts:  []string{"type error", "x", "string", "int64"},
		},
		{
			name:   "assignment in child scope",
			script: `var x: int64 = 10; func(){ x = "value" }()`,
			parts:  []string{"type error", "x", "string", "int64"},
		},
		{
			name:   "assignment through module",
			script: `module m { var x: int64 = 10 }; m.x = "value"`,
			parts:  []string{"type error", "x", "string", "int64"},
		},
		{
			name:   "numeric types do not convert",
			script: `var x: int = 10`,
			parts:  []string{"type error", "x", "int64", "int"},
		},
		{
			name:   "rune uses reflected name",
			script: `var x: rune = 10`,
			parts:  []string{"type error", "x", "int64", "int32"},
		},
		{
			name:   "nil primitive",
			script: `var x: int64 = nil`,
			parts:  []string{"type error", "x", "<nil>", "int64"},
		},
		{
			name:   "unknown type",
			script: `var x: unknownType`,
			parts:  []string{"undefined type"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Execute(env.NewEnv(), &Options{TypedBindings: true}, test.script)
			if err == nil {
				t.Fatal("Execute returned nil error")
			}
			for _, part := range test.parts {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("error %q does not contain %q", err, part)
				}
			}
		})
	}
}
