package tengo

import (
	"fmt"

	"github.com/d5/tengo/v2/parser"
	"github.com/d5/tengo/v2/token"
)

func (c *Compiler) compileDestructure(stmt *parser.DestructureStmt) error {
	if stmt.Token != token.Define {
		return c.errorf(stmt, "cannot use destructuring with =")
	}
	if len(stmt.RHS) != 1 {
		return c.errorf(stmt, "tuple assignment not allowed")
	}
	if err := c.Compile(stmt.RHS[0]); err != nil {
		return err
	}
	return c.emitPattern(stmt.Pattern)
}

func (c *Compiler) prepareFuncParams(
	fn *parser.FuncLit,
) (numParams int, acceptFewer bool, err error) {
	params := fn.Type.Params
	if params == nil {
		return 0, false, nil
	}
	if params.Patterns == nil {
		for _, p := range params.List {
			s := c.symbolTable.Define(p.Name)
			// function arguments are not assigned by user code directly
			s.LocalAssigned = true
		}
		return len(params.List), false, nil
	}

	type paramSlot struct {
		sym *Symbol
		pat *parser.Pattern
	}
	slots := make([]paramSlot, len(params.Patterns))
	seen := make(map[string]bool)
	for i, pat := range params.Patterns {
		if pat.Default != nil && !pat.Rest {
			acceptFewer = true
		}
		var sym *Symbol
		if pat.Kind == parser.PatternIdent && pat.Name != nil {
			name := pat.Name.Name
			if name != "_" {
				if seen[name] || c.symbolTable.IsDefined(name) {
					return 0, false, c.errorf(pat.Name,
						"'%s' redeclared in this block", name)
				}
				seen[name] = true
			}
			sym = c.symbolTable.Define(name)
		} else {
			sym = c.symbolTable.Define(fmt.Sprintf(":p%d", i))
			sym.LocalAssigned = true
		}
		slots[i] = paramSlot{sym: sym, pat: pat}
	}

	for _, sl := range slots {
		pat := sl.pat
		if pat.Kind == parser.PatternIdent {
			if pat.Rest {
				sl.sym.LocalAssigned = true
				continue
			}
			if pat.Default != nil {
				if err = c.emitReplaceIfMissing(pat, sl.sym, pat.Default); err != nil {
					return
				}
			} else if acceptFewer {
				if err = c.emitReplaceIfMissing(pat, sl.sym, nil); err != nil {
					return
				}
			}
			sl.sym.LocalAssigned = true
			continue
		}
		if pat.Default != nil {
			if err = c.emitReplaceIfMissing(pat, sl.sym, pat.Default); err != nil {
				return
			}
		}
		c.emitGetSymbol(pat, sl.sym)
		if err = c.emitPattern(pat); err != nil {
			return
		}
	}
	return len(params.Patterns), acceptFewer, nil
}

// emitReplaceIfMissing replaces sym when it holds the missing sentinel.
// A nil default stores undefined. The stack is left empty.
func (c *Compiler) emitReplaceIfMissing(
	node parser.Node,
	sym *Symbol,
	def parser.Expr,
) error {
	c.emitGetSymbol(node, sym)
	jumpKeep := c.emit(node, parser.OpJumpIfNotMissing, 0)
	if def != nil {
		// Hide this binding so its own default resolves outward.
		was := sym.LocalAssigned
		sym.LocalAssigned = false
		err := c.Compile(def)
		sym.LocalAssigned = was
		if err != nil {
			return err
		}
	} else {
		c.emit(node, parser.OpNull)
	}
	c.emitSetSymbol(node, sym, false)
	jumpDone := c.emit(node, parser.OpJump, 0)
	keep := len(c.currentInstructions())
	c.changeOperand(jumpKeep, keep)
	c.emit(node, parser.OpPop)
	c.changeOperand(jumpDone, len(c.currentInstructions()))
	return nil
}

func (c *Compiler) emitPattern(pat *parser.Pattern) error {
	if pat == nil {
		c.emit(nil, parser.OpPop)
		return nil
	}
	switch pat.Kind {
	case parser.PatternArray:
		return c.emitArrayPattern(pat)
	case parser.PatternMap:
		return c.emitMapPattern(pat)
	case parser.PatternIdent:
		return c.bindPatternValue(pat)
	default:
		return c.errorf(pat, "invalid destructuring pattern")
	}
}

func (c *Compiler) emitArrayPattern(pat *parser.Pattern) error {
	elems := pat.Elements
	if len(elems) == 0 {
		c.emit(pat, parser.OpPop)
		return nil
	}
	for i, el := range elems {
		if el.Rest && i != len(elems)-1 {
			return c.errorf(el, "rest element must be last")
		}
	}
	for i, el := range elems {
		last := i == len(elems)-1
		if el.Rest {
			if el.Kind != parser.PatternIdent {
				return c.errorf(el, "rest element must be last")
			}
			c.emit(el, parser.OpArrayTail, i)
			if err := c.bindIdent(el); err != nil {
				return err
			}
			return nil
		}
		if !last {
			c.emit(el, parser.OpDup)
		}
		c.emit(el, parser.OpConstant, c.addConstant(&Int{Value: int64(i)}))
		c.emit(el, parser.OpIndexOrMissing, parser.DestructureArray)
		if err := c.bindPatternValue(el); err != nil {
			return err
		}
	}
	return nil
}

func (c *Compiler) emitMapPattern(pat *parser.Pattern) error {
	elems := pat.Elements
	if len(elems) == 0 {
		c.emit(pat, parser.OpPop)
		return nil
	}
	for i, el := range elems {
		if el.Rest {
			return c.errorf(el, "rest element not supported in map pattern")
		}
		last := i == len(elems)-1
		if !last {
			c.emit(el, parser.OpDup)
		}
		if len(el.Key) > MaxStringLen {
			return c.error(el, ErrStringLimit)
		}
		c.emit(el, parser.OpConstant, c.addConstant(&String{Value: el.Key}))
		c.emit(el, parser.OpIndexOrMissing, parser.DestructureMap)
		if err := c.bindPatternValue(el); err != nil {
			return err
		}
	}
	return nil
}

// bindPatternValue consumes a matched value. Defaults run only for the
// missing sentinel. Nested patterns destructure that value.
func (c *Compiler) bindPatternValue(el *parser.Pattern) error {
	if el.Rest {
		return c.bindIdent(el)
	}
	if el.Default != nil {
		jump := c.emit(el, parser.OpJumpIfNotMissing, 0)
		if err := c.Compile(el.Default); err != nil {
			return err
		}
		c.changeOperand(jump, len(c.currentInstructions()))
	} else if el.Kind == parser.PatternIdent {
		jump := c.emit(el, parser.OpJumpIfNotMissing, 0)
		c.emit(el, parser.OpNull)
		c.changeOperand(jump, len(c.currentInstructions()))
	}
	if el.Kind == parser.PatternIdent {
		return c.bindIdent(el)
	}
	return c.emitPattern(el)
}

func (c *Compiler) bindIdent(pat *parser.Pattern) error {
	if pat.Name == nil || pat.Name.Name == "_" {
		c.emit(pat, parser.OpPop)
		return nil
	}
	name := pat.Name.Name
	if c.symbolTable.IsDefined(name) {
		return c.errorf(pat.Name, "'%s' redeclared in this block", name)
	}
	sym := c.symbolTable.Define(name)
	c.emitSetSymbol(pat.Name, sym, true)
	sym.LocalAssigned = true
	return nil
}

func (c *Compiler) emitGetSymbol(node parser.Node, sym *Symbol) {
	switch sym.Scope {
	case ScopeGlobal:
		c.emit(node, parser.OpGetGlobal, sym.Index)
	case ScopeLocal:
		c.emit(node, parser.OpGetLocal, sym.Index)
	case ScopeFree:
		c.emit(node, parser.OpGetFree, sym.Index)
	default:
		panic(fmt.Errorf("invalid symbol scope: %s", sym.Scope))
	}
}

func (c *Compiler) emitSetSymbol(node parser.Node, sym *Symbol, define bool) {
	switch sym.Scope {
	case ScopeGlobal:
		c.emit(node, parser.OpSetGlobal, sym.Index)
	case ScopeLocal:
		if define && !sym.LocalAssigned {
			c.emit(node, parser.OpDefineLocal, sym.Index)
		} else {
			c.emit(node, parser.OpSetLocal, sym.Index)
		}
	case ScopeFree:
		c.emit(node, parser.OpSetFree, sym.Index)
	default:
		panic(fmt.Errorf("invalid symbol scope: %s", sym.Scope))
	}
}
