package genai

import (
	"reflect"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestAccumulateStreamedFunctionCallArgs(t *testing.T) {
	acc := newStreamedFunctionCallAccumulator()
	continueTrue := true
	brightness := 50.0

	first := &GenerateContentResponse{
		Candidates: []*Candidate{{
			Content: &Content{Role: RoleModel, Parts: []*Part{{
				FunctionCall: &FunctionCall{
					ID:   "call-1",
					Name: "controlLight",
					Args: map[string]any{"room": "living"},
					PartialArgs: []*PartialArg{
						{JsonPath: "$.brightness", NumberValue: &brightness},
						{JsonPath: "$.colorTemperature", StringValue: "wa", WillContinue: &continueTrue},
						{JsonPath: "$['nested'].value", StringValue: "x"},
						{JsonPath: "$.items[0].name", StringValue: "lamp"},
						{JsonPath: "$.optional", NULLValue: "NULL_VALUE"},
					},
					WillContinue: &continueTrue,
				},
			}}},
		}},
	}
	if err := acc.accumulateResponse(first); err != nil {
		t.Fatalf("accumulate first: %v", err)
	}
	fc := first.Candidates[0].Content.Parts[0].FunctionCall
	if got := first.FunctionCalls(); len(got) != 1 || got[0] != fc {
		t.Fatalf("FunctionCalls() = %#v", got)
	}

	second := &FunctionCall{
		ID:   "call-1",
		Name: "controlLight",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.colorTemperature", StringValue: "rm"},
		},
	}
	if err := acc.accumulateFunctionCall(second); err != nil {
		t.Fatalf("accumulate second: %v", err)
	}

	wantFirst := map[string]any{
		"room":             "living",
		"brightness":       50.0,
		"colorTemperature": "wa",
		"nested":           map[string]any{"value": "x"},
		"items":            []any{map[string]any{"name": "lamp"}},
		"optional":         nil,
	}
	wantSecond := map[string]any{
		"room":             "living",
		"brightness":       50.0,
		"colorTemperature": "warm",
		"nested":           map[string]any{"value": "x"},
		"items":            []any{map[string]any{"name": "lamp"}},
		"optional":         nil,
	}
	if !reflect.DeepEqual(fc.Args, wantFirst) {
		t.Fatalf("first args = %#v, want %#v", fc.Args, wantFirst)
	}
	if !reflect.DeepEqual(second.Args, wantSecond) {
		t.Fatalf("second args = %#v, want %#v", second.Args, wantSecond)
	}

	// The call finished, so a reused id starts empty.
	reused := &FunctionCall{
		ID:   "call-1",
		Name: "controlLight",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.brightness", NumberValue: &brightness},
		},
	}
	if err := acc.accumulateFunctionCall(reused); err != nil {
		t.Fatalf("accumulate reused: %v", err)
	}
	if !reflect.DeepEqual(reused.Args, map[string]any{"brightness": 50.0}) {
		t.Fatalf("reused args = %#v", reused.Args)
	}
}

func TestAccumulateStreamedFunctionCallConflict(t *testing.T) {
	acc := newStreamedFunctionCallAccumulator()
	continueFalse := false
	fc := &FunctionCall{
		ID:   "call-1",
		Name: "controlLight",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.colorTemperature", StringValue: "warm", WillContinue: &continueFalse},
			{JsonPath: "$.colorTemperature", StringValue: "cool"},
		},
		WillContinue: &continueFalse,
	}
	if err := acc.accumulateFunctionCall(fc); err == nil {
		t.Fatal("expected path conflict")
	}
	if fc.Args != nil {
		t.Fatalf("args mutated on error: %#v", fc.Args)
	}

	bad := &FunctionCall{
		Name: "controlLight",
		PartialArgs: []*PartialArg{{
			JsonPath:    "$.flag",
			StringValue: "yes",
			BoolValue:   boolPtr(true),
		}},
	}
	if err := acc.accumulateFunctionCall(bad); err == nil {
		t.Fatal("expected multiple value field error")
	}
}

func TestMergeStreamedFunctionCallTurn(t *testing.T) {
	continueTrue := true
	chunks := []*Content{
		{Role: RoleModel, Parts: []*Part{{
			FunctionCall: &FunctionCall{
				ID: "b", Name: "second", Args: map[string]any{"n": 1.0},
				PartialArgs:  []*PartialArg{{JsonPath: "$.n", NumberValue: floatPtr(1)}},
				WillContinue: &continueTrue,
			},
		}}},
		{Role: RoleModel, Parts: []*Part{{
			FunctionCall: &FunctionCall{
				ID: "a", Name: "first", Args: map[string]any{"color": "warm"},
				PartialArgs: []*PartialArg{{JsonPath: "$.color", StringValue: "warm"}},
			},
		}}},
		{Role: RoleModel, Parts: []*Part{{
			FunctionCall: &FunctionCall{
				ID: "b", Name: "second", Args: map[string]any{"n": 1.0, "on": true},
			},
		}}},
		{Role: RoleModel, Parts: []*Part{{
			FunctionCall: &FunctionCall{
				ID: "a", Name: "first", Args: map[string]any{"color": "cool"},
			},
		}}},
	}
	merged, ok := mergeStreamedFunctionCallTurn(chunks)
	if !ok {
		t.Fatal("expected merged turn")
	}
	if merged.Role != RoleModel || len(merged.Parts) != 3 {
		t.Fatalf("merged = %#v", merged)
	}
	got := []string{merged.Parts[0].FunctionCall.ID, merged.Parts[1].FunctionCall.ID, merged.Parts[2].FunctionCall.ID}
	wantIDs := []string{"b", "a", "a"}
	if !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("order = %v", got)
	}
	if merged.Parts[0].FunctionCall.PartialArgs != nil || merged.Parts[0].FunctionCall.WillContinue != nil {
		t.Fatalf("stored call still has partial state: %#v", merged.Parts[0].FunctionCall)
	}
	if !reflect.DeepEqual(merged.Parts[0].FunctionCall.Args, map[string]any{"n": 1.0, "on": true}) {
		t.Fatalf("final args = %#v", merged.Parts[0].FunctionCall.Args)
	}
	if !reflect.DeepEqual(merged.Parts[2].FunctionCall.Args, map[string]any{"color": "cool"}) {
		t.Fatalf("reused id args = %#v", merged.Parts[2].FunctionCall.Args)
	}

	mixed := append(chunks, &Content{Role: RoleModel, Parts: []*Part{{Text: "done"}}})
	if _, ok := mergeStreamedFunctionCallTurn(mixed); ok {
		t.Fatal("mixed turn should keep chunked history")
	}
}

func floatPtr(v float64) *float64 { return &v }
