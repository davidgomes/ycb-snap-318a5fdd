package tengo

import (
	"fmt"

	"github.com/d5/tengo/v2/parser"
	"github.com/d5/tengo/v2/token"
)

func (c *Compiler) destructTempName() string {
	name := fmt.Sprintf("$d:%d", c.destructTempSeq)
	c.destructTempSeq++
	return name
}

func (c *Compiler) paramSlotName(i int) string {
	return fmt.Sprintf(":$p:%d", i)
}

func (c *Compiler) compileDestructAssign(node *parser.AssignStmt) error {
	if node.Token != token.Define {
		return c.errorf(node, "cannot use destructuring with =")
	}
	if len(node.Patterns) > 1 || len(node.RHS) > 1 {
		return c.errorf(node, "tuple assignment not allowed")
	}

	if err := c.Compile(node.RHS[0]); err != nil {
		return err
	}

	temp := c.symbolTable.Define(c.destructTempName())
	c.emitStoreSymbol(node, temp, true)
	if temp.Scope == ScopeLocal {
		temp.LocalAssigned = true
	}

	return c.compilePatternBindings(node, node.Patterns[0], temp, true)
}

func (c *Compiler) compilePatternBindings(
	node parser.Node,
	pat parser.Pattern,
	source *Symbol,
	define bool,
) error {
	switch p := pat.(type) {
	case *parser.IdentPattern:
		return c.compileIdentPatternBinding(node, p, source, define)
	case *parser.ArrayPattern:
		return c.compileArrayPatternBinding(node, p, source, define)
	case *parser.MapPattern:
		return c.compileMapPatternBinding(node, p, source, define)
	default:
		return c.errorf(node, "invalid destructuring pattern")
	}
}

func (c *Compiler) compileIdentPatternBinding(
	node parser.Node,
	pat *parser.IdentPattern,
	source *Symbol,
	define bool,
) error {
	c.emitLoadSymbol(node, source)
	if err := c.emitDefaultBinding(node, pat.Default); err != nil {
		return err
	}
	return c.emitPatternDefine(node, pat.Name, define)
}

func (c *Compiler) compileArrayPatternBinding(
	node parser.Node,
	pat *parser.ArrayPattern,
	source *Symbol,
	define bool,
) error {
	for i, elem := range pat.Elements {
		c.emitLoadSymbol(node, source)
		c.emit(node, parser.OpConstant, c.addConstant(&Int{Value: int64(i)}))
		c.emit(node, parser.OpIndex)

		if ip, ok := elem.(*parser.IdentPattern); ok {
			if err := c.emitDefaultBinding(node, ip.Default); err != nil {
				return err
			}
			if err := c.emitPatternDefine(node, ip.Name, define); err != nil {
				return err
			}
			continue
		}

		tmp := c.symbolTable.Define(c.destructTempName())
		c.emitStoreSymbol(node, tmp, true)
		if tmp.Scope == ScopeLocal {
			tmp.LocalAssigned = true
		}
		if err := c.compilePatternBindings(node, elem, tmp, define); err != nil {
			return err
		}
	}

	if pat.Rest != nil {
		c.emitLoadSymbol(node, source)
		c.emit(node, parser.OpConstant,
			c.addConstant(&Int{Value: int64(len(pat.Elements))}))
		c.emit(node, parser.OpNull)
		c.emit(node, parser.OpSliceIndex)
		if err := c.emitDefaultBinding(node, pat.Rest.Default); err != nil {
			return err
		}
		if err := c.emitPatternDefine(node, pat.Rest.Name, define); err != nil {
			return err
		}
	}
	return nil
}

func (c *Compiler) compileMapPatternBinding(
	node parser.Node,
	pat *parser.MapPattern,
	source *Symbol,
	define bool,
) error {
	for _, elem := range pat.Elements {
		c.emitLoadSymbol(node, source)
		c.emit(node, parser.OpConstant, c.addConstant(&String{Value: elem.Key}))
		c.emit(node, parser.OpIndex)

		if elem.Nested != nil {
			tmp := c.symbolTable.Define(c.destructTempName())
			c.emitStoreSymbol(node, tmp, true)
			if tmp.Scope == ScopeLocal {
				tmp.LocalAssigned = true
			}
			if err := c.compilePatternBindings(node, elem.Nested, tmp, define); err != nil {
				return err
			}
			continue
		}

		name := elem.Name
		if name == "" {
			name = elem.Key
		}
		if err := c.emitDefaultBinding(node, elem.Default); err != nil {
			return err
		}
		if err := c.emitPatternDefine(node, name, define); err != nil {
			return err
		}
	}
	return nil
}

func (c *Compiler) emitDefaultBinding(
	node parser.Node,
	defaultExpr parser.Expr,
) error {
	if defaultExpr == nil {
		return nil
	}

	tmp := c.symbolTable.Define(c.destructTempName())
	c.emitStoreSymbol(node, tmp, true)
	if tmp.Scope == ScopeLocal {
		tmp.LocalAssigned = true
	}

	c.emitLoadSymbol(node, tmp)
	c.emit(node, parser.OpNull)
	c.emit(node, parser.OpEqual)
	useDefaultPos := c.emit(node, parser.OpJumpFalsy, 0)

	if err := c.Compile(defaultExpr); err != nil {
		return err
	}
	skipDefaultPos := c.emit(node, parser.OpJump, 0)

	c.changeOperand(useDefaultPos, len(c.currentInstructions()))
	c.emitLoadSymbol(node, tmp)
	c.changeOperand(skipDefaultPos, len(c.currentInstructions()))
	return nil
}

func (c *Compiler) emitPatternDefine(
	node parser.Node,
	name string,
	define bool,
) error {
	if define {
		_, depth, exists := c.symbolTable.Resolve(name, false)
		if depth == 0 && exists {
			return c.errorf(node, "'%s' redeclared in this block", name)
		}
		symbol := c.symbolTable.Define(name)
		c.emitStoreSymbol(node, symbol, true)
		if symbol.Scope == ScopeLocal {
			symbol.LocalAssigned = true
		}
		return nil
	}

	symbol, _, ok := c.symbolTable.Resolve(name, false)
	if !ok {
		return c.errorf(node, "unresolved reference '%s'", name)
	}
	c.emitStoreSymbol(node, symbol, false)
	if symbol.Scope == ScopeLocal {
		symbol.LocalAssigned = true
	}
	return nil
}

func (c *Compiler) emitLoadSymbol(node parser.Node, symbol *Symbol) {
	switch symbol.Scope {
	case ScopeGlobal:
		c.emit(node, parser.OpGetGlobal, symbol.Index)
	case ScopeLocal:
		c.emit(node, parser.OpGetLocal, symbol.Index)
	case ScopeFree:
		c.emit(node, parser.OpGetFree, symbol.Index)
	default:
		panic(fmt.Errorf("invalid symbol scope for load: %s", symbol.Scope))
	}
}

func (c *Compiler) emitStoreSymbol(node parser.Node, symbol *Symbol, define bool) {
	switch symbol.Scope {
	case ScopeGlobal:
		c.emit(node, parser.OpSetGlobal, symbol.Index)
	case ScopeLocal:
		if define && !symbol.LocalAssigned {
			c.emit(node, parser.OpDefineLocal, symbol.Index)
		} else {
			c.emit(node, parser.OpSetLocal, symbol.Index)
		}
	case ScopeFree:
		c.emit(node, parser.OpSetFree, symbol.Index)
	default:
		panic(fmt.Errorf("invalid symbol scope for store: %s", symbol.Scope))
	}
}
