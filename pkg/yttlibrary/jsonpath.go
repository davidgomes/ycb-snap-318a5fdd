// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package yttlibrary

import (
	"fmt"

	"carvel.dev/ytt/pkg/orderedmap"
	"carvel.dev/ytt/pkg/template/core"
	"github.com/k14s/starlark-go/starlark"
	"github.com/k14s/starlark-go/starlarkstruct"
)

var (
	// JSONPathAPI contains the definition of the @ytt:jsonpath module
	JSONPathAPI = starlark.StringDict{
		"jsonpath": &starlarkstruct.Module{
			Name: "jsonpath",
			Members: starlark.StringDict{
				"query":     starlark.NewBuiltin("jsonpath.query", core.ErrWrapper(jsonPathModule{}.Query)),
				"query_one": starlark.NewBuiltin("jsonpath.query_one", core.ErrWrapper(jsonPathModule{}.QueryOne)),
			},
		},
	}
)

type jsonPathModule struct{}

func (jsonPathModule) args(args starlark.Tuple) (interface{}, string, error) {
	if args.Len() != 2 {
		return nil, "", fmt.Errorf("expected exactly two arguments")
	}
	doc, err := core.NewStarlarkValue(args.Index(0)).AsGoValue()
	if err != nil {
		return nil, "", err
	}
	path, err := core.NewStarlarkValue(args.Index(1)).AsString()
	if err != nil {
		return nil, "", err
	}
	return doc, path, nil
}

// Query is a core.StarlarkFunc that returns all values matching a JSONPath expression
func (m jsonPathModule) Query(thread *starlark.Thread, f *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := m.args(args)
	if err != nil {
		return starlark.None, err
	}
	res, err := orderedmap.Query(doc, path)
	if err != nil {
		return starlark.None, err
	}
	vals := make([]starlark.Value, 0, len(res))
	for _, r := range res {
		vals = append(vals, core.NewGoValue(r).AsStarlarkValue())
	}
	return starlark.NewList(vals), nil
}

// QueryOne is a core.StarlarkFunc that returns the first value matching a JSONPath expression, or None
func (m jsonPathModule) QueryOne(thread *starlark.Thread, f *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := m.args(args)
	if err != nil {
		return starlark.None, err
	}
	res, ok, err := orderedmap.QueryOne(doc, path)
	if err != nil {
		return starlark.None, err
	}
	if !ok {
		return starlark.None, nil
	}
	return core.NewGoValue(res).AsStarlarkValue(), nil
}
