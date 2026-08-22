package vm

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/mattn/anko/ast"
)

var vmFuncExprs sync.Map

func registerVMFunc(f reflect.Value, expr *ast.FuncExpr) {
	vmFuncExprs.Store(f, expr)
}

func getVMFuncExpr(f reflect.Value) (*ast.FuncExpr, bool) {
	v, ok := vmFuncExprs.Load(f)
	if !ok {
		return nil, false
	}
	return v.(*ast.FuncExpr), true
}

func hasDefaultParams(funcExpr *ast.FuncExpr) bool {
	for _, p := range funcExpr.Params {
		if p.Default != nil {
			return true
		}
	}
	return false
}

func minRequiredParams(funcExpr *ast.FuncExpr) int {
	minRequired := 0
	last := len(funcExpr.Params) - 1
	for i, p := range funcExpr.Params {
		if funcExpr.VarArg && i == last {
			continue
		}
		if p.Default == nil {
			minRequired++
		}
	}
	return minRequired
}

func (runInfo *runInfoStruct) expandCallSubExprs(funcExpr *ast.FuncExpr, callExpr *ast.CallExpr) ([]ast.Expr, bool) {
	if callExpr.VarArg {
		return runInfo.expandCallSubExprsVariadicCall(funcExpr, callExpr)
	}

	numParams := len(funcExpr.Params)
	numExprs := len(callExpr.SubExprs)
	minRequired := minRequiredParams(funcExpr)

	fixedCount := numParams
	if funcExpr.VarArg {
		fixedCount--
	}

	if numExprs < minRequired {
		runInfo.err = newStringError(callExpr, fmt.Sprintf("function wants %v arguments but received %v", minRequired, numExprs))
		runInfo.rv = nilValue
		return nil, false
	}
	if !funcExpr.VarArg && numExprs > numParams {
		runInfo.err = newStringError(callExpr, fmt.Sprintf("function wants %v arguments but received %v", numParams, numExprs))
		runInfo.rv = nilValue
		return nil, false
	}

	paramEnv := runInfo.env.NewEnv()
	expanded := make([]ast.Expr, 0, numParams+numExprs)
	exprIndex := 0

	for i := 0; i < fixedCount; i++ {
		if exprIndex < numExprs {
			runInfo.expr = callExpr.SubExprs[exprIndex]
			runInfo.invokeExpr()
			if runInfo.err != nil {
				return nil, false
			}
			expanded = append(expanded, callExpr.SubExprs[exprIndex])
			paramEnv.DefineValue(funcExpr.Params[i].Name, runInfo.rv)
			exprIndex++
			continue
		}
		if funcExpr.Params[i].Default == nil {
			runInfo.err = newStringError(callExpr, fmt.Sprintf("function wants %v arguments but received %v", minRequired, numExprs))
			runInfo.rv = nilValue
			return nil, false
		}
		savedEnv := runInfo.env
		runInfo.env = paramEnv
		runInfo.expr = funcExpr.Params[i].Default
		runInfo.invokeExpr()
		runInfo.env = savedEnv
		if runInfo.err != nil {
			return nil, false
		}
		expanded = append(expanded, &ast.LiteralExpr{Literal: runInfo.rv})
		paramEnv.DefineValue(funcExpr.Params[i].Name, runInfo.rv)
	}

	for exprIndex < numExprs {
		expanded = append(expanded, callExpr.SubExprs[exprIndex])
		exprIndex++
	}

	return expanded, true
}

func (runInfo *runInfoStruct) expandCallSubExprsVariadicCall(funcExpr *ast.FuncExpr, callExpr *ast.CallExpr) ([]ast.Expr, bool) {
	numExprs := len(callExpr.SubExprs)
	minRequired := minRequiredParams(funcExpr)

	if numExprs == 0 {
		runInfo.err = newStringError(callExpr, fmt.Sprintf("function wants %v arguments but received %v", minRequired, numExprs))
		runInfo.rv = nilValue
		return nil, false
	}

	runInfo.expr = callExpr.SubExprs[numExprs-1]
	runInfo.invokeExpr()
	if runInfo.err != nil {
		return nil, false
	}
	if runInfo.rv.Kind() != reflect.Slice && runInfo.rv.Kind() != reflect.Array {
		runInfo.err = newStringError(callExpr, "call is variadic but last parameter is of type "+runInfo.rv.Type().String())
		runInfo.rv = nilValue
		return nil, false
	}

	expanded := make([]ast.Expr, 0, numExprs-1+runInfo.rv.Len())
	for i := 0; i < numExprs-1; i++ {
		expanded = append(expanded, callExpr.SubExprs[i])
	}
	for i := 0; i < runInfo.rv.Len(); i++ {
		expanded = append(expanded, &ast.LiteralExpr{Literal: runInfo.rv.Index(i)})
	}

	expandedCall := &ast.CallExpr{Func: callExpr.Func, Name: callExpr.Name, SubExprs: expanded}
	return runInfo.expandCallSubExprs(funcExpr, expandedCall)
}

func (runInfo *runInfoStruct) makeVMCallArgsWithDefaults(funcExpr *ast.FuncExpr, callExpr *ast.CallExpr, rt reflect.Type) ([]reflect.Value, bool) {
	expanded, ok := runInfo.expandCallSubExprs(funcExpr, callExpr)
	if !ok {
		return nil, false
	}
	expandedCall := &ast.CallExpr{Func: callExpr.Func, Name: callExpr.Name, SubExprs: expanded, VarArg: callExpr.VarArg, Go: callExpr.Go}
	return runInfo.makeCallArgsInner(rt, true, expandedCall)
}
