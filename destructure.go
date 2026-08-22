package tengo

import (
	"strconv"

	"github.com/d5/tengo/v2/parser"
	"github.com/d5/tengo/v2/token"
)

func (c *Compiler) freshTemp(prefix string) string {
	name := prefix + strconv.Itoa(c.destrTempID)
	c.destrTempID++
	return name
}

func (c *Compiler) emitGetSymbol(node parser.Node, symbol *Symbol) {
	switch symbol.Scope {
	case ScopeGlobal:
		c.emit(node, parser.OpGetGlobal, symbol.Index)
	case ScopeLocal:
		c.emit(node, parser.OpGetLocal, symbol.Index)
	case ScopeFree:
		c.emit(node, parser.OpGetFree, symbol.Index)
	default:
		panic("invalid symbol scope for get")
	}
}

func (c *Compiler) emitSetDefineSymbol(node parser.Node, symbol *Symbol) {
	switch symbol.Scope {
	case ScopeGlobal:
		c.emit(node, parser.OpSetGlobal, symbol.Index)
	case ScopeLocal:
		if !symbol.LocalAssigned {
			c.emit(node, parser.OpDefineLocal, symbol.Index)
		} else {
			c.emit(node, parser.OpSetLocal, symbol.Index)
		}
	case ScopeFree:
		c.emit(node, parser.OpSetFree, symbol.Index)
	default:
		panic("invalid symbol scope for set")
	}
	symbol.LocalAssigned = true
}

func (c *Compiler) compileDestrAssign(node *parser.AssignStmt) error {
	if node.Token != token.Define {
		return c.errorf(node, "cannot use destructuring with =")
	}
	if len(node.RHS) != 1 {
		return c.errorf(node, "invalid destructuring assignment")
	}
	if node.Pattern == nil {
		return c.errorf(node, "invalid destructuring assignment")
	}

	if err := c.Compile(node.RHS[0]); err != nil {
		return err
	}

	temp := c.symbolTable.Define(c.freshTemp("$destr"))
	c.emitSetDefineSymbol(node, temp)

	return c.compileDestrPattern(node, node.Pattern, temp, true)
}

func (c *Compiler) compileParamDestructure(
	node parser.Node,
	params *parser.IdentList,
) error {
	if params == nil {
		return nil
	}

	for i, param := range params.Params {
		if isSimpleIdentPattern(param) {
			continue
		}
		symbol, _, ok := c.symbolTable.Resolve(formatArgName(i), false)
		if !ok {
			return c.errorf(node, "invalid function parameter")
		}
		if err := c.compileDestrPattern(node, param, symbol, true); err != nil {
			return err
		}
	}

	return nil
}

func isSimpleIdentPattern(pat parser.Pattern) bool {
	_, ok := pat.(*parser.IdentPattern)
	return ok
}

func (c *Compiler) defineParamSymbols(params *parser.IdentList) error {
	if params == nil {
		return nil
	}

	for i, param := range params.Params {
		switch p := param.(type) {
		case *parser.IdentPattern:
			s := c.symbolTable.Define(p.Name)
			s.LocalAssigned = true
			_ = i
		default:
			s := c.symbolTable.Define(formatArgName(i))
			s.LocalAssigned = true
			if err := c.definePatternBindings(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatArgName(i int) string {
	return "$arg" + strconv.Itoa(i)
}

func (c *Compiler) definePatternBindings(pat parser.Pattern) error {
	switch p := pat.(type) {
	case *parser.IdentPattern:
		s := c.symbolTable.Define(p.Name)
		s.LocalAssigned = false
	case *parser.RestPattern:
		s := c.symbolTable.Define(p.Name)
		s.LocalAssigned = false
	case *parser.ArrayPattern:
		for _, elem := range p.Elements {
			if err := c.definePatternBindings(elem); err != nil {
				return err
			}
		}
	case *parser.MapPattern:
		for _, elem := range p.Elements {
			if err := c.definePatternBindings(elem.Pattern); err != nil {
				return err

			}
		}
	}
	return nil
}

func (c *Compiler) compileDestrPattern(
	node parser.Node,
	pat parser.Pattern,
	src *Symbol,
	define bool,
) error {
	switch p := pat.(type) {
	case *parser.IdentPattern:
		c.emitGetSymbol(node, src)
		return c.compileDestrBind(node, p, define)
	case *parser.ArrayPattern:
		return c.compileArrayDestr(node, p, src, define)
	case *parser.MapPattern:
		return c.compileMapDestr(node, p, src, define)
	default:
		return c.errorf(node, "invalid destructuring pattern")
	}
}

func (c *Compiler) compileArrayDestr(
	node parser.Node,
	pat *parser.ArrayPattern,
	src *Symbol,
	define bool,
) error {
	idx := 0
	for _, elem := range pat.Elements {
		if rest, ok := elem.(*parser.RestPattern); ok {
			c.emitGetSymbol(node, src)
			c.emit(node, parser.OpConstant, c.addConstant(&Int{Value: int64(idx)}))
			c.emit(node, parser.OpNull)
			c.emit(node, parser.OpSliceIndex)
			ip := &parser.IdentPattern{Name: rest.Name, NamePos: rest.NamePos}
			return c.compileDestrBind(node, ip, define)
		}

		c.emitGetSymbol(node, src)
		c.emit(node, parser.OpConstant, c.addConstant(&Int{Value: int64(idx)}))
		c.emit(node, parser.OpIndex)
		if err := c.compileDestrElement(node, elem, define); err != nil {
			return err
		}
		idx++
	}
	return nil
}

func (c *Compiler) compileMapDestr(
	node parser.Node,
	pat *parser.MapPattern,
	src *Symbol,
	define bool,
) error {
	for _, elem := range pat.Elements {
		c.emitGetSymbol(node, src)
		c.emit(node, parser.OpConstant, c.addConstant(&String{Value: elem.Key}))
		c.emit(node, parser.OpIndex)
		if err := c.compileDestrElement(node, elem.Pattern, define); err != nil {
			return err
		}
	}
	return nil
}

func (c *Compiler) compileDestrElement(
	node parser.Node,
	elem parser.Pattern,
	define bool,
) error {
	switch p := elem.(type) {
	case *parser.IdentPattern:
		return c.compileDestrBind(node, p, define)
	case *parser.ArrayPattern:
		valTemp := c.symbolTable.Define(c.freshTemp("$destr"))
		c.emitSetDefineSymbol(node, valTemp)
		return c.compileDestrPattern(node, p, valTemp, define)
	case *parser.MapPattern:
		valTemp := c.symbolTable.Define(c.freshTemp("$destr"))
		c.emitSetDefineSymbol(node, valTemp)
		return c.compileDestrPattern(node, p, valTemp, define)
	default:
		return c.errorf(node, "invalid destructuring pattern")
	}
}

func (c *Compiler) compileDestrBind(
	node parser.Node,
	ip *parser.IdentPattern,
	define bool,
) error {
	if ip.Default != nil {
		if err := c.compileDestrDefault(node, ip); err != nil {
			return err
		}
	}

	symbol, depth, exists := c.symbolTable.Resolve(ip.Name, false)
	if define {
		if depth == 0 && exists && symbol.LocalAssigned {
			return c.errorf(node, "'%s' redeclared in this block", ip.Name)
		}
		if !exists {
			symbol = c.symbolTable.Define(ip.Name)
		}
	} else {
		if !exists {
			return c.errorf(node, "unresolved reference '%s'", ip.Name)
		}
	}

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
		panic("invalid destructuring bind scope")
	}
	symbol.LocalAssigned = true
	return nil
}

func (c *Compiler) compileDestrDefault(
	node parser.Node,
	ip *parser.IdentPattern,
) error {
	valTemp := c.symbolTable.Define(c.freshTemp("$val"))
	c.emitSetDefineSymbol(node, valTemp)

	c.emitGetSymbol(node, valTemp)
	c.emit(node, parser.OpNull)
	c.emit(node, parser.OpEqual)

	jmpDefined := c.emit(node, parser.OpJumpFalsy, 0)

	if err := c.Compile(ip.Default); err != nil {
		return err
	}

	jmpEnd := c.emit(node, parser.OpJump, 0)
	c.changeOperand(jmpDefined, len(c.currentInstructions()))

	c.emitGetSymbol(node, valTemp)
	c.changeOperand(jmpEnd, len(c.currentInstructions()))
	return nil
}
