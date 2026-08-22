package genai

import "testing"

func TestStreamedCallArgs(t *testing.T) {
	continued := true
	done := false
	number := 3.0
	call := &FunctionCall{
		ID:   "call-1",
		Args: map[string]any{"existing": true},
		PartialArgs: []*PartialArg{
			{JsonPath: "$.text", StringValue: "hel", WillContinue: &continued},
			{JsonPath: "$['nested.value']", StringValue: "x", WillContinue: &done},
			{JsonPath: "$.items[1]", NumberValue: &number, WillContinue: &done},
		},
	}
	state := newStreamedCallArgs(call.Args)
	if err := state.apply(call); err != nil {
		t.Fatal(err)
	}
	call.PartialArgs = []*PartialArg{{JsonPath: "$.text", StringValue: "lo", WillContinue: &done}}
	if err := state.apply(call); err != nil {
		t.Fatal(err)
	}
	if got := call.Args["text"]; got != "hello" {
		t.Errorf("text = %v, want hello", got)
	}
	if got := call.Args["nested.value"]; got != "x" {
		t.Errorf("bracket field = %v, want x", got)
	}
	if got := call.Args["items"].([]any)[1]; got != 3.0 {
		t.Errorf("array item = %v, want 3", got)
	}
}

func TestStreamedCallArgsRejectsShapeConflict(t *testing.T) {
	continued := true
	state := newStreamedCallArgs(nil)
	if err := state.apply(&FunctionCall{PartialArgs: []*PartialArg{
		{JsonPath: "$.value", StringValue: "x", WillContinue: &continued},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := state.apply(&FunctionCall{PartialArgs: []*PartialArg{
		{JsonPath: "$.value.child", StringValue: "y"},
	}}); err == nil {
		t.Fatal("expected incompatible shape error")
	}
}

func TestConsolidateStreamedFunctionCalls(t *testing.T) {
	continued := true
	done := false
	got := consolidateStreamedFunctionCallContents([]*Content{
		{Parts: []*Part{{FunctionCall: &FunctionCall{ID: "a", Name: "one", Args: map[string]any{"x": "a"}, WillContinue: &continued}}}},
		{Parts: []*Part{{FunctionCall: &FunctionCall{ID: "a", Name: "one", Args: map[string]any{"x": "ab"}, WillContinue: &done}}}},
		{Parts: []*Part{{FunctionCall: &FunctionCall{ID: "b", Name: "two", Args: map[string]any{"y": true}}}}},
	})
	if len(got) != 1 || len(got[0].Parts) != 2 || got[0].Parts[0].FunctionCall.Args["x"] != "ab" {
		t.Fatalf("unexpected consolidated calls: %#v", got)
	}
}
