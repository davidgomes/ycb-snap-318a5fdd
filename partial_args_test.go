package genai

import (
	"reflect"
	"testing"
)

func TestAccumulateFunctionCall(t *testing.T) {
	continued := true
	finished := false
	states := map[string]*partialArgsState{}
	first := &FunctionCall{
		ID:   "call-1",
		Args: map[string]any{"existing": true},
		PartialArgs: []*PartialArg{{
			JsonPath:     "$.input.text",
			StringValue:  "hel",
			WillContinue: &continued,
		}},
		WillContinue: &continued,
	}
	got, err := accumulateFunctionCall(states, first)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"existing": true, "input": map[string]any{"text": "hel"}}; !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("first args = %#v, want %#v", got.Args, want)
	}

	second := &FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{{
			JsonPath:     "$['input']['text']",
			StringValue:  "lo",
			WillContinue: &finished,
		}},
		WillContinue: &finished,
	}
	got, err = accumulateFunctionCall(states, second)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"existing": true, "input": map[string]any{"text": "hello"}}
	if !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("second args = %#v, want %#v", got.Args, want)
	}

	third, err := accumulateFunctionCall(states, &FunctionCall{
		ID:          "call-1",
		PartialArgs: []*PartialArg{{JsonPath: "$.input.text", StringValue: "new"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := third.Args["input"].(map[string]any)["text"]; got != "new" {
		t.Fatalf("reused call id args = %#v, want new", got)
	}
}

func TestAccumulateFunctionCallRejectsShapeConflict(t *testing.T) {
	continued := true
	states := map[string]*partialArgsState{}
	_, err := accumulateFunctionCall(states, &FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{{
			JsonPath:     "$.value",
			StringValue:  "text",
			WillContinue: &continued,
		}},
		WillContinue: &continued,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = accumulateFunctionCall(states, &FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{{
			JsonPath:     "$.value.child",
			StringValue:  "bad-shape",
			WillContinue: &continued,
		}},
		WillContinue: &continued,
	})
	if err == nil {
		t.Fatal("expected incompatible shape error")
	}
}
