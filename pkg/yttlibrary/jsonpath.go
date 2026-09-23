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
	// JSONPathAPI is the @ytt:jsonpath module.
	// query(doc, path) returns a list of matches (empty when nothing matches).
	// query_one(doc, path) returns the first match, or None when nothing matches.
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

func (jsonpathModule) Query(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := jsonpathArgs(args, kwargs)
	if err != nil {
		return starlark.None, err
	}
	results, err := orderedmap.Query(doc, path)
	if err != nil {
		return starlark.None, err
	}
	return core.NewGoValue(results).AsStarlarkValue(), nil
}

func (jsonpathModule) QueryOne(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := jsonpathArgs(args, kwargs)
	if err != nil {
		return starlark.None, err
	}
	val, ok, err := orderedmap.QueryOne(doc, path)
	if err != nil {
		return starlark.None, err
	}
	if !ok {
		return starlark.None, nil
	}
	return core.NewGoValue(val).AsStarlarkValue(), nil
}

func jsonpathArgs(args starlark.Tuple, kwargs []starlark.Tuple) (interface{}, string, error) {
	if args.Len() != 2 || len(kwargs) != 0 {
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
