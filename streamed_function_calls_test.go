package genai

import (
	"reflect"
	"testing"
)

func TestStreamedFunctionCallAccumulator(t *testing.T) {
	continueCall := true
	endCall := false
	continuePart := true
	endPart := false
	number := 42.0

	first := &FunctionCall{
		ID:   "call-1",
		Name: "test",
		Args: map[string]any{"existing": "value"},
		PartialArgs: []*PartialArg{
			{JsonPath: "$.message", StringValue: "hel", WillContinue: &continuePart},
			{JsonPath: "$['a.b']", StringValue: "quoted"},
			{JsonPath: "$.items[1].name", StringValue: "item"},
			{JsonPath: "$.number", NumberValue: &number},
		},
		WillContinue: &continueCall,
	}
	second := &FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.message", StringValue: "lo", WillContinue: &endPart},
			{JsonPath: "$.nullable", NULLValue: "NULL_VALUE"},
		},
		WillContinue: &endCall,
	}

	var accumulator streamedFunctionCallAccumulator
	if err := accumulator.addFunctionCall(first); err != nil {
		t.Fatal(err)
	}
	if got, want := first.Args["message"], "hel"; got != want {
		t.Fatalf("first response message = %v, want %v", got, want)
	}
	if err := accumulator.addFunctionCall(second); err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"existing": "value",
		"message":  "hello",
		"a.b":      "quoted",
		"items": []any{
			nil,
			map[string]any{"name": "item"},
		},
		"number":   42.0,
		"nullable": nil,
	}
	if !reflect.DeepEqual(second.Args, want) {
		t.Fatalf("second response args = %#v, want %#v", second.Args, want)
	}
	if len(accumulator.calls) != 0 {
		t.Fatalf("accumulator retained completed call: %#v", accumulator.calls)
	}
}

func TestStreamedFunctionCallAccumulatorRejectsIncompatibleShapes(t *testing.T) {
	continueCall := true
	endPart := false
	var accumulator streamedFunctionCallAccumulator

	if err := accumulator.addFunctionCall(&FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.value", StringValue: "text", WillContinue: &endPart},
		},
		WillContinue: &continueCall,
	}); err != nil {
		t.Fatal(err)
	}
	err := accumulator.addFunctionCall(&FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.value.child", StringValue: "invalid"},
		},
		WillContinue: &continueCall,
	})
	if err == nil {
		t.Fatal("expected incompatible JSON shapes to return an error")
	}
}

func TestStreamedFunctionCallAccumulatorResetsCallID(t *testing.T) {
	endCall := false
	var accumulator streamedFunctionCallAccumulator

	first := &FunctionCall{ID: "call-1", Args: map[string]any{"value": "first"}, WillContinue: &endCall}
	second := &FunctionCall{ID: "call-1", Args: map[string]any{"value": "second"}, WillContinue: &endCall}
	if err := accumulator.addFunctionCall(first); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.addFunctionCall(second); err != nil {
		t.Fatal(err)
	}
	if got, want := second.Args["value"], "second"; got != want {
		t.Fatalf("reused call ID args = %v, want %v", got, want)
	}
}

func TestStreamedFunctionCallAccumulatorLiveToolCall(t *testing.T) {
	continueCall := true
	endCall := false
	continuePart := true
	live := &LiveServerMessage{
		ToolCall: &LiveServerToolCall{FunctionCalls: []*FunctionCall{{
			ID: "tool-1",
			PartialArgs: []*PartialArg{{
				JsonPath: "$.query", StringValue: "part", WillContinue: &continuePart,
			}},
			WillContinue: &continueCall,
		}}},
	}
	var accumulator streamedFunctionCallAccumulator
	if err := accumulator.addLiveServerMessage(live); err != nil {
		t.Fatal(err)
	}
	live.ToolCall.FunctionCalls[0].PartialArgs = []*PartialArg{{
		JsonPath: "$.query", StringValue: "ial", WillContinue: &endCall,
	}}
	live.ToolCall.FunctionCalls[0].WillContinue = &endCall
	if err := accumulator.addLiveServerMessage(live); err != nil {
		t.Fatal(err)
	}
	if got, want := live.ToolCall.FunctionCalls[0].Args["query"], "partial"; got != want {
		t.Fatalf("live tool args = %v, want %v", got, want)
	}
}

func TestMergeStreamedFunctionCallContents(t *testing.T) {
	continueCall := true
	endCall := false
	contents := []*Content{
		{Role: RoleModel, Parts: []*Part{{FunctionCall: &FunctionCall{
			ID: "first", Name: "one", Args: map[string]any{"value": "a"}, WillContinue: &continueCall,
			PartialArgs: []*PartialArg{{JsonPath: "$.value", StringValue: "a"}},
		}}}},
		{Role: RoleModel, Parts: []*Part{{FunctionCall: &FunctionCall{
			ID: "second", Name: "two", Args: map[string]any{"value": "b"}, WillContinue: &endCall,
		}}}},
		{Role: RoleModel, Parts: []*Part{{FunctionCall: &FunctionCall{
			ID: "first", Name: "one", Args: map[string]any{"value": "final"}, WillContinue: &endCall,
			PartialArgs: []*PartialArg{{JsonPath: "$.ignored", StringValue: "ignored"}},
		}}}},
	}

	merged, ok := mergeStreamedFunctionCallContents(contents)
	if !ok {
		t.Fatal("expected function-call-only contents to merge")
	}
	if len(merged) != 1 || len(merged[0].Parts) != 2 {
		t.Fatalf("merged contents = %#v, want one content with two calls", merged)
	}
	gotIDs := []string{
		merged[0].Parts[0].FunctionCall.ID,
		merged[0].Parts[1].FunctionCall.ID,
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("merged call order = %v, want %v", gotIDs, want)
	}
	if got, want := merged[0].Parts[0].FunctionCall.Args["value"], "final"; got != want {
		t.Fatalf("merged first call args = %v, want %v", got, want)
	}
	for _, part := range merged[0].Parts {
		if part.FunctionCall.PartialArgs != nil || part.FunctionCall.WillContinue != nil {
			t.Fatalf("merged call retained streaming fields: %#v", part.FunctionCall)
		}
	}
}
