package tengo

import (
	"github.com/d5/tengo/v2/parser"
	"github.com/d5/tengo/v2/token"
)

func (c *Compiler) compileDestructureAssign(stmt *parser.AssignStmt) error {
	if stmt.Token == token.Assign {
		return c.errorf(stmt, "cannot use destructuring with =")
	}
	if stmt.Token != token.Define {
		return c.errorf(stmt, "cannot use destructuring with %s",
			stmt.Token.String())
	}
	if len(stmt.RHS) != 1 {
		return c.errorf(stmt, "tuple assignment not allowed")
	}
	if err := c.validatePattern(stmt, stmt.Pattern); err != nil {
		return err
	}
	if err := c.Compile(stmt.RHS[0]); err != nil {
		return err
	}
	return c.compileDestructure(stmt, stmt.Pattern)
}

// validatePattern rejects rest elements that the language does not allow.
func (c *Compiler) validatePattern(
	node parser.Node,
	pat parser.BindingPattern,
) error {
	switch pat := pat.(type) {
	case *parser.Ident, nil:
		return nil
	case *parser.ArrayBinding:
		for i, elem := range pat.Elements {
			if elem.Rest && i != len(pat.Elements)-1 {
				return c.errorf(node, "rest element must be last")
			}
			if elem.Rest && elem.Default != nil {
				return c.errorf(node, "rest element cannot have a default")
			}
			if err := c.validatePattern(node, elem.Pattern); err != nil {
				return err
			}
		}
	case *parser.MapBinding:
		for _, elem := range pat.Elements {
			if elem.Rest {
				return c.errorf(node, "rest element not allowed in map pattern")
			}
			if err := c.validatePattern(node, elem.Pattern); err != nil {
				return err
			}
		}
	}
	return nil
}

// compileDestructure consumes one stack value and binds pat to it.
func (c *Compiler) compileDestructure(
	node parser.Node,
	pat parser.BindingPattern,
) error {
	switch pat := pat.(type) {
	case *parser.Ident:
		return c.bindStackedValue(node, pat.Name)
	case *parser.ArrayBinding:
		return c.compileArrayBinding(node, pat)
	case *parser.MapBinding:
		return c.compileMapBinding(node, pat)
	default:
		return c.errorf(node, "invalid destructuring pattern")
	}
}

func (c *Compiler) compileArrayBinding(
	node parser.Node,
	pat *parser.ArrayBinding,
) error {
	if len(pat.Elements) == 0 {
		c.emit(node, parser.OpPop)
		return nil
	}
	for i, elem := range pat.Elements {
		if elem.Rest {
			if _, ok := elem.Pattern.(*parser.Ident); !ok {
				return c.errorf(node, "invalid rest element")
			}
			c.emit(node, parser.OpDup)
			if err := c.emitInt(node, int64(i)); err != nil {
				return err
			}
			c.emit(node, parser.OpArrayTail)
			if err := c.compileDestructure(node, elem.Pattern); err != nil {
				return err
			}
			continue
		}
		if err := c.compileKeyedBinding(node, func() error {
			return c.emitInt(node, int64(i))
		}, elem.Pattern, elem.Default); err != nil {
			return err
		}
	}
	c.emit(node, parser.OpPop)
	return nil
}

func (c *Compiler) compileMapBinding(
	node parser.Node,
	pat *parser.MapBinding,
) error {
	if len(pat.Elements) == 0 {
		c.emit(node, parser.OpPop)
		return nil
	}
	for _, elem := range pat.Elements {
		key := elem.Key
		if err := c.compileKeyedBinding(node, func() error {
			return c.emitString(node, key)
		}, elem.Pattern, elem.Default); err != nil {
			return err
		}
	}
	c.emit(node, parser.OpPop)
	return nil
}

// compileKeyedBinding reads one key from the source value left on the stack.
// The source stays on the stack. A missing position or key uses def when it
// is non-nil; otherwise the binding receives undefined (nested patterns bind
// their leaves as missing, so their defaults still apply).
func (c *Compiler) compileKeyedBinding(
	node parser.Node,
	emitKey func() error,
	pat parser.BindingPattern,
	def parser.Expr,
) error {
	c.emit(node, parser.OpDup)
	if err := emitKey(); err != nil {
		return err
	}
	c.emit(node, parser.OpHasIndex)
	miss := c.emit(node, parser.OpJumpFalsy, 0)

	c.emit(node, parser.OpDup)
	if err := emitKey(); err != nil {
		return err
	}
	c.emit(node, parser.OpIndex)
	done := c.emit(node, parser.OpJump, 0)

	c.changeOperand(miss, len(c.currentInstructions()))
	if def != nil {
		if err := c.Compile(def); err != nil {
			return err
		}
	} else {
		c.emit(node, parser.OpNull)
	}
	c.changeOperand(done, len(c.currentInstructions()))
	return c.compileDestructure(node, pat)
}

func (c *Compiler) bindStackedValue(node parser.Node, name string) error {
	if name == "_" {
		c.emit(node, parser.OpPop)
		return nil
	}
	_, depth, exists := c.symbolTable.Resolve(name, false)
	if exists && depth == 0 {
		return c.errorf(node, "'%s' redeclared in this block", name)
	}
	symbol := c.symbolTable.Define(name)
	switch symbol.Scope {
	case ScopeGlobal:
		c.emit(node, parser.OpSetGlobal, symbol.Index)
	case ScopeLocal:
		c.emit(node, parser.OpDefineLocal, symbol.Index)
		symbol.LocalAssigned = true
	case ScopeFree:
		c.emit(node, parser.OpSetFree, symbol.Index)
	default:
		return c.errorf(node, "invalid assignment variable scope: %s",
			symbol.Scope)
	}
	return nil
}

func (c *Compiler) emitInt(node parser.Node, v int64) error {
	c.emit(node, parser.OpConstant, c.addConstant(&Int{Value: v}))
	return nil
}

func (c *Compiler) emitString(node parser.Node, s string) error {
	if len(s) > MaxStringLen {
		return c.error(node, ErrStringLimit)
	}
	c.emit(node, parser.OpConstant, c.addConstant(&String{Value: s}))
	return nil
}
