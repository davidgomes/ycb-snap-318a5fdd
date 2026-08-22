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

func (jsonPathModule) Query(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := jsonPathArgs(args)
	if err != nil {
		return starlark.None, err
	}

	results, err := orderedmap.Query(doc, path)
	if err != nil {
		return starlark.None, err
	}

	vals := make([]starlark.Value, 0, len(results))
	for _, r := range results {
		vals = append(vals, core.NewGoValue(normalizeJSONPathValue(r)).AsStarlarkValue())
	}
	return starlark.NewList(vals), nil
}

func (jsonPathModule) QueryOne(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	doc, path, err := jsonPathArgs(args)
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
	return core.NewGoValue(normalizeJSONPathValue(result)).AsStarlarkValue(), nil
}

func jsonPathArgs(args starlark.Tuple) (interface{}, string, error) {
	if args.Len() != 2 {
		return nil, "", fmt.Errorf("expected exactly two arguments")
	}
	doc, err := core.NewStarlarkValue(args.Index(0)).AsGoValue()
	if err != nil {
		return nil, "", err
	}
	doc = goDocForJSONPath(doc)
	path, err := core.NewStarlarkValue(args.Index(1)).AsString()
	if err != nil {
		return nil, "", err
	}
	return doc, path, nil
}

func goDocForJSONPath(v interface{}) interface{} {
	switch t := v.(type) {
	case *yamlmeta.DocumentSet:
		out := make([]interface{}, len(t.Items))
		for i, d := range t.Items {
			out[i] = yamlmeta.NewGoFromAST(d.Value)
		}
		return out
	case *yamlmeta.Document:
		return yamlmeta.NewGoFromAST(t.Value)
	default:
		return yamlmeta.NewGoFromAST(v)
	}
}

func normalizeJSONPathValue(v interface{}) interface{} {
	switch t := v.(type) {
	case *orderedmap.Map:
		out := orderedmap.NewMap()
		t.Iterate(func(k, val interface{}) {
			out.Set(k, normalizeJSONPathValue(val))
		})
		return out
	case map[string]interface{}:
		out := orderedmap.NewMap()
		for k, val := range t {
			out.Set(k, normalizeJSONPathValue(val))
		}
		return out
	case map[interface{}]interface{}:
		out := orderedmap.NewMap()
		for k, val := range t {
			out.Set(k, normalizeJSONPathValue(val))
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, item := range t {
			out[i] = normalizeJSONPathValue(item)
		}
		return out
	default:
		return t
	}
}
