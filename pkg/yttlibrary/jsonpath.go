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
	// JSONPathAPI contains the definition of the @ytt:jsonpath module.
	JSONPathAPI = starlark.StringDict{
		"jsonpath": &starlarkstruct.Module{
			Name: "jsonpath",
			Members: starlark.StringDict{
				"query":     starlark.NewBuiltin("jsonpath.query", core.ErrWrapper(jsonpathModule{}.Query)),
				"query_one": starlark.NewBuiltin("jsonpath.query_one", core.ErrWrapper(jsonpathModule{}.QueryOne)),
			},
		},
	}
)

type jsonpathModule struct{}

func (jsonpathModule) docArg(args starlark.Tuple) (interface{}, string, error) {
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

// Query evaluates a JSONPath expression and returns a list of matches.
func (m jsonpathModule) Query(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := m.docArg(args)
	if err != nil {
		return starlark.None, err
	}
	res, err := orderedmap.Query(doc, path)
	if err != nil {
		return starlark.None, err
	}
	vals := make([]starlark.Value, 0, len(res))
	for _, v := range res {
		vals = append(vals, core.NewGoValue(v).AsStarlarkValue())
	}
	return starlark.NewList(vals), nil
}

// QueryOne returns the first JSONPath match, or None when nothing matches.
func (m jsonpathModule) QueryOne(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := m.docArg(args)
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
