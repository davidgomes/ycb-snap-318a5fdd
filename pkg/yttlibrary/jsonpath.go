// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package yttlibrary

import (
	"errors"

	"carvel.dev/ytt/pkg/orderedmap"
	"carvel.dev/ytt/pkg/template/core"
	"github.com/k14s/starlark-go/starlark"
	"github.com/k14s/starlark-go/starlarkstruct"
)

// JSONPathAPI is the @ytt:jsonpath module.
// It maps "jsonpath" to query and query_one.
var JSONPathAPI = starlark.StringDict{
	"jsonpath": &starlarkstruct.Module{
		Name: "jsonpath",
		Members: starlark.StringDict{
			"query": starlark.NewBuiltin(
				"jsonpath.query",
				core.ErrWrapper(jsonPathModule{}.Query),
			),
			"query_one": starlark.NewBuiltin(
				"jsonpath.query_one",
				core.ErrWrapper(jsonPathModule{}.QueryOne),
			),
		},
	},
}

const (
	jsonPathArgCount = 2
	emptyPath        = ""
)

type jsonPathModule struct{}

// Query evaluates a JSONPath expression and returns every match as a list.
func (jsonPathModule) Query(
	_ *starlark.Thread,
	_ *starlark.Builtin,
	args starlark.Tuple,
	kwargs []starlark.Tuple,
) (starlark.Value, error) {
	doc, path, err := jsonPathArgs(args, kwargs)
	if err != nil {
		return starlark.None, err
	}
	matches, err := orderedmap.Query(doc, path)
	if err != nil {
		return starlark.None, err
	}
	return core.NewGoValue(matches).AsStarlarkValue(), nil
}

// QueryOne evaluates a JSONPath expression and returns the first match.
// It returns None when nothing matches.
func (jsonPathModule) QueryOne(
	_ *starlark.Thread,
	_ *starlark.Builtin,
	args starlark.Tuple,
	kwargs []starlark.Tuple,
) (starlark.Value, error) {
	doc, path, err := jsonPathArgs(args, kwargs)
	if err != nil {
		return starlark.None, err
	}
	match, found, err := orderedmap.QueryOne(doc, path)
	if err != nil {
		return starlark.None, err
	}
	if !found {
		return starlark.None, nil
	}
	return core.NewGoValue(match).AsStarlarkValue(), nil
}

func jsonPathArgs(
	args starlark.Tuple,
	kwargs []starlark.Tuple,
) (any, string, error) {
	if len(kwargs) > 0 {
		return nil, emptyPath, errors.New("unexpected keyword arguments")
	}
	if args.Len() != jsonPathArgCount {
		return nil, emptyPath, errors.New("expected exactly two arguments")
	}
	doc, err := core.NewStarlarkValue(args.Index(0)).AsGoValue()
	if err != nil {
		return nil, emptyPath, err
	}
	path, err := core.NewStarlarkValue(args.Index(1)).AsString()
	if err != nil {
		return nil, emptyPath, err
	}
	return doc, path, nil
}
