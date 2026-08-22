package checker_test

import (
	"fmt"
	printing "fmt"
	"strings"

	. "github.com/go-critic/go-critic/checkers/testdata/brokenDocLink/dotpkg"
)

type ExistingTypeNegative struct{}

func ExistingFuncNegative() {}

type EmbedBaseNegative struct{}

func (EmbedBaseNegative) EmbeddedMethod() {}

type WithEmbedNegative struct {
	EmbedBaseNegative
}

type WithFieldNegative struct {
	ExportedField int
}

// Valid [ExistingTypeNegative] reference.
type validLocalType int

// Calls [ExistingFuncNegative].
func validLocalFuncRef() {}

// See [WithEmbedNegative.EmbeddedMethod].
type validEmbeddedMethod int

// See [WithFieldNegative.ExportedField].
type validFieldRef int

// See [fmt.Println].
func validImportedRef() {}

// See [printing.Println].
func validRenamedImportRef() {}

// See [strings.Contains].
func validQualifiedMethodRef() {}

// Builtins: [len], [error], [append].
func validBuiltinRefs(x []int) {
	_ = len(x)
}

// Not a symbol link: [not a link].
func validSpaceInBrackets() {}

// Package [fmt] reference.
func validPkgRef() {}

// See [DotType].
type validDotImportType int

// See [DotType.DotMethod].
type validDotImportMethod int

// See [DotType.DotMethod] via embed.
type validDotImportEmbed struct {
	DotType
}

// map[ast.Expr]TypeAndValue is not a link.
func validMapSyntax() {}

func init() {
	_ = fmt.Sprintf
	_ = strings.Contains
	_, _ = printing.Println, printing.Sprintf
	_ = DotType{}
}
