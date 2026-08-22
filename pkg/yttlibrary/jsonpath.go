package yttlibrary

import (
	"fmt"

	"carvel.dev/ytt/pkg/orderedmap"
	"carvel.dev/ytt/pkg/template/core"
	"github.com/k14s/starlark-go/starlark"
	"github.com/k14s/starlark-go/starlarkstruct"
)

var JSONPathAPI = starlark.StringDict{
	"jsonpath": &starlarkstruct.Module{
		Name: "jsonpath",
		Members: starlark.StringDict{
			"query":     starlark.NewBuiltin("jsonpath.query", core.ErrWrapper(jsonPathModule{}.Query)),
			"query_one": starlark.NewBuiltin("jsonpath.query_one", core.ErrWrapper(jsonPathModule{}.QueryOne)),
		},
	},
}

type jsonPathModule struct{}

func (jsonPathModule) Query(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if args.Len() != 2 {
		return starlark.None, fmt.Errorf("expected exactly two arguments")
	}
	doc, err := core.NewStarlarkValue(args.Index(0)).AsGoValue()
	if err != nil {
		return starlark.None, err
	}
	path, err := core.NewStarlarkValue(args.Index(1)).AsString()
	if err != nil {
		return starlark.None, err
	}
	r, err := orderedmap.Query(doc, path)
	if err != nil {
		return starlark.None, err
	}
	vals := make([]starlark.Value, len(r))
	for i, v := range r {
		vals[i] = core.NewGoValue(v).AsStarlarkValue()
	}
	return starlark.NewList(vals), nil
}
func (m jsonPathModule) QueryOne(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if args.Len() != 2 {
		return starlark.None, fmt.Errorf("expected exactly two arguments")
	}
	doc, err := core.NewStarlarkValue(args.Index(0)).AsGoValue()
	if err != nil {
		return starlark.None, err
	}
	path, err := core.NewStarlarkValue(args.Index(1)).AsString()
	if err != nil {
		return starlark.None, err
	}
	v, ok, err := orderedmap.QueryOne(doc, path)
	if err != nil {
		return starlark.None, err
	}
	if !ok {
		return starlark.None, nil
	}
	return core.NewGoValue(v).AsStarlarkValue(), nil
}
