// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package genai

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFunctionCallAccumulator(t *testing.T) {
	acc := &functionCallAccumulator{}
	calls := []*FunctionCall{
		{ID: "1", Name: "f", Args: map[string]any{"a": 1.0}, WillContinue: Ptr(true), PartialArgs: []*PartialArg{
			{JsonPath: "$.s", StringValue: "hel", WillContinue: Ptr(true)},
		}},
		{ID: "1", WillContinue: Ptr(true), PartialArgs: []*PartialArg{
			{JsonPath: "$.s", StringValue: "lo"},
			{JsonPath: "$['x'].list[1]", NumberValue: Ptr(2.0)},
			{JsonPath: "$.n", NULLValue: "NULL_VALUE"},
		}},
		{ID: "1"},
	}
	for _, c := range calls {
		if err := acc.apply(c); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]any{"a": 1.0, "s": "hello", "x": map[string]any{"list": []any{nil, 2.0}}, "n": nil}
	if diff := cmp.Diff(want, calls[2].Args); diff != "" {
		t.Error(diff)
	}
	if len(acc.inProgress) != 0 {
		t.Error("state not cleared")
	}
	fresh := &FunctionCall{ID: "1", PartialArgs: []*PartialArg{{JsonPath: "$.b", BoolValue: Ptr(true)}}}
	if err := acc.apply(fresh); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]any{"b": true}, fresh.Args); diff != "" {
		t.Error(diff)
	}

	bad := &functionCallAccumulator{}
	err := bad.apply(&FunctionCall{ID: "2", PartialArgs: []*PartialArg{
		{JsonPath: "$.a", StringValue: "x"},
		{JsonPath: "$.a.b", StringValue: "y"},
	}})
	if err == nil {
		t.Error("expected incompatible shape error")
	}
}

func TestMergeStreamedFunctionCallTurn(t *testing.T) {
	in := []*Content{
		{Role: RoleModel, Parts: []*Part{{FunctionCall: &FunctionCall{ID: "a", Name: "f", Args: map[string]any{"x": "1"}, WillContinue: Ptr(true), PartialArgs: []*PartialArg{{JsonPath: "$.x", StringValue: "1"}}}}}},
		{Role: RoleModel, Parts: []*Part{{FunctionCall: &FunctionCall{ID: "b", Name: "g", Args: map[string]any{}}}}},
		{Role: RoleModel, Parts: []*Part{{FunctionCall: &FunctionCall{ID: "a", Args: map[string]any{"x": "12"}, PartialArgs: []*PartialArg{{JsonPath: "$.x", StringValue: "2"}}}}}},
	}
	want := []*Content{{Role: RoleModel, Parts: []*Part{
		{FunctionCall: &FunctionCall{ID: "a", Name: "f", Args: map[string]any{"x": "12"}}},
		{FunctionCall: &FunctionCall{ID: "b", Name: "g", Args: map[string]any{}}},
	}}}
	if diff := cmp.Diff(want, mergeStreamedFunctionCallTurn(in)); diff != "" {
		t.Error(diff)
	}
}
