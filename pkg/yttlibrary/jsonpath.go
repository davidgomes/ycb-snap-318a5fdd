// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package yttlibrary

import (
	"carvel.dev/ytt/pkg/orderedmap"
	"carvel.dev/ytt/pkg/template/core"
	"github.com/k14s/starlark-go/starlark"
	"github.com/k14s/starlark-go/starlarkstruct"
)

var (
	// JSONPathAPI is the @ytt:jsonpath module.
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

func (jsonPathModule) Query(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := jsonPathArgs(fn.Name(), args, kwargs)
	if err != nil {
		return starlark.None, err
	}
	matches, err := orderedmap.Query(doc, path)
	if err != nil {
		return starlark.None, err
	}
	return core.NewGoValue(matches).AsStarlarkValue(), nil
}

func (jsonPathModule) QueryOne(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := jsonPathArgs(fn.Name(), args, kwargs)
	if err != nil {
		return starlark.None, err
	}
	match, ok, err := orderedmap.QueryOne(doc, path)
	if err != nil {
		return starlark.None, err
	}
	if !ok {
		return starlark.None, nil
	}
	return core.NewGoValue(match).AsStarlarkValue(), nil
}

func jsonPathArgs(fn string, args starlark.Tuple, kwargs []starlark.Tuple) (interface{}, string, error) {
	var docVal starlark.Value
	var path string
	if err := starlark.UnpackArgs(fn, args, kwargs, "doc", &docVal, "path", &path); err != nil {
		return nil, "", err
	}
	doc, err := core.NewStarlarkValue(docVal).AsGoValue()
	if err != nil {
		return nil, "", err
	}
	return doc, path, nil
}
