// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package yttlibrary

import (
	"fmt"

	"carvel.dev/ytt/pkg/orderedmap"
	"carvel.dev/ytt/pkg/template/core"
	"carvel.dev/ytt/pkg/yamlmeta"
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

// Query is a core.StarlarkFunc that returns a list of all values in the document matching the JSONPath
func (b jsonPathModule) Query(thread *starlark.Thread, f *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := b.args(args)
	if err != nil {
		return starlark.None, err
	}

	results, err := orderedmap.Query(doc, path)
	if err != nil {
		return starlark.None, err
	}

	values := make([]starlark.Value, 0, len(results))
	for _, result := range results {
		values = append(values, core.NewGoValue(result).AsStarlarkValue())
	}
	return starlark.NewList(values), nil
}

// QueryOne is a core.StarlarkFunc that returns the first value in the document matching the JSONPath, or None
func (b jsonPathModule) QueryOne(thread *starlark.Thread, f *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := b.args(args)
	if err != nil {
		return starlark.None, err
	}

	result, found, err := orderedmap.QueryOne(doc, path)
	if err != nil {
		return starlark.None, err
	}
	if !found {
		return starlark.None, nil
	}
	return core.NewGoValue(result).AsStarlarkValue(), nil
}

func (b jsonPathModule) args(args starlark.Tuple) (interface{}, string, error) {
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

	return yamlmeta.NewGoFromAST(doc), path, nil
}
