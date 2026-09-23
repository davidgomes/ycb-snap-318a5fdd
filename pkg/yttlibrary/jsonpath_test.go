// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package yttlibrary_test

import (
	"testing"

	"carvel.dev/ytt/pkg/yttlibrary"
	"github.com/k14s/starlark-go/starlark"
	"github.com/k14s/starlark-go/starlarkstruct"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callJSONPath(t *testing.T, funcName string, doc starlark.Value, path string) (starlark.Value, error) {
	t.Helper()
	module, isModule := yttlibrary.JSONPathAPI["jsonpath"].(*starlarkstruct.Module)
	require.True(t, isModule)
	fn, found := module.Members[funcName]
	require.True(t, found, "jsonpath.%s is not defined", funcName)

	thread := &starlark.Thread{Name: "test"}
	return starlark.Call(thread, fn, starlark.Tuple{doc, starlark.String(path)}, nil)
}

func testDoc(t *testing.T) *starlark.Dict {
	t.Helper()
	books := starlark.NewList(nil)
	for _, title := range []string{"A", "B", "C"} {
		book := starlark.NewDict(1)
		require.NoError(t, book.SetKey(starlark.String("title"), starlark.String(title)))
		require.NoError(t, books.Append(book))
	}
	doc := starlark.NewDict(1)
	require.NoError(t, doc.SetKey(starlark.String("books"), books))
	return doc
}

func TestJSONPathQuery(t *testing.T) {
	result, err := callJSONPath(t, "query", testDoc(t), "$.books[*].title")
	require.NoError(t, err)
	list, isList := result.(*starlark.List)
	require.True(t, isList, "expected *starlark.List, got %T", result)
	assert.Equal(t, `["A", "B", "C"]`, list.String())

	result, err = callJSONPath(t, "query", testDoc(t), "$.books[(@.length-1)]")
	require.NoError(t, err)
	assert.Equal(t, `[{"title": "C"}]`, result.String())

	result, err = callJSONPath(t, "query", testDoc(t), "$.books.length()")
	require.NoError(t, err)
	assert.Equal(t, `[3]`, result.String())

	result, err = callJSONPath(t, "query", testDoc(t), "$.missing")
	require.NoError(t, err)
	list, isList = result.(*starlark.List)
	require.True(t, isList, "expected *starlark.List, got %T", result)
	assert.Equal(t, 0, list.Len())
}

func TestJSONPathQueryList(t *testing.T) {
	doc := starlark.NewList([]starlark.Value{starlark.MakeInt(1), starlark.MakeInt(5), starlark.Float(7.5)})

	result, err := callJSONPath(t, "query", doc, "$[?(@ > 2)]")
	require.NoError(t, err)
	assert.Equal(t, `[5, 7.5]`, result.String())
}

func TestJSONPathQueryOne(t *testing.T) {
	result, err := callJSONPath(t, "query_one", testDoc(t), "$.books[1].title")
	require.NoError(t, err)
	assert.Equal(t, starlark.String("B"), result)

	result, err = callJSONPath(t, "query_one", testDoc(t), "$.books[5]")
	require.NoError(t, err)
	assert.Equal(t, starlark.None, result)
}

func TestJSONPathSyntaxError(t *testing.T) {
	_, err := callJSONPath(t, "query", testDoc(t), "books")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "syntax error at position 0: path must start with '$'")

	_, err = callJSONPath(t, "query_one", testDoc(t), "$[")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "syntax error at position 2")
}
