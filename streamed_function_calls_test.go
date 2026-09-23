// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package genai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"
)

func TestStreamedFunctionCallAccumulator(t *testing.T) {
	acc := newStreamedFunctionCallAccumulator()

	first := &FunctionCall{
		ID:   "call-1",
		Name: "controlLight",
		Args: map[string]any{"brightness": 50.0},
		PartialArgs: []*PartialArg{
			{JsonPath: "$.colorTemperature", StringValue: "wa", WillContinue: Ptr(true)},
			{JsonPath: "$['room']", StringValue: "living"},
			{JsonPath: "$.zones[1]", StringValue: "north"},
			{JsonPath: "$.zones[0].name", StringValue: "south"},
			{JsonPath: "$.power", NULLValue: "NULL_VALUE"},
			{JsonPath: "$.enabled", BoolValue: Ptr(false)},
		},
		WillContinue: Ptr(true),
	}
	if err := acc.applyFunctionCall(first); err != nil {
		t.Fatalf("apply first call: %v", err)
	}
	wantFirst := map[string]any{
		"brightness":       50.0,
		"colorTemperature": "wa",
		"room":             "living",
		"zones":            []any{map[string]any{"name": "south"}, "north"},
		"power":            nil,
		"enabled":          false,
	}
	if diff := cmp.Diff(wantFirst, first.Args); diff != "" {
		t.Fatalf("first args mismatch (-want +got):\n%s", diff)
	}

	second := &FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.colorTemperature", StringValue: "rm"},
			{JsonPath: `$.labels["color.temp"]`, StringValue: "warm"},
		},
		WillContinue: Ptr(false),
	}
	if err := acc.applyFunctionCall(second); err != nil {
		t.Fatalf("apply second call: %v", err)
	}
	if got := second.Args["colorTemperature"]; got != "warm" {
		t.Fatalf("colorTemperature = %#v, want warm", got)
	}
	if got := second.Args["brightness"]; got != 50.0 {
		t.Fatalf("brightness = %#v, want 50", got)
	}
	labels, _ := second.Args["labels"].(map[string]any)
	if labels["color.temp"] != "warm" {
		t.Fatalf("bracket key = %#v", labels)
	}
	if _, ok := second.Args["power"]; !ok || second.Args["power"] != nil {
		t.Fatalf("power = %#v, want JSON null", second.Args["power"])
	}

	reused := &FunctionCall{
		ID:          "call-1",
		Name:        "controlLight",
		PartialArgs: []*PartialArg{{JsonPath: "$.colorTemperature", StringValue: "cool"}},
	}
	if err := acc.applyFunctionCall(reused); err != nil {
		t.Fatalf("apply reused id: %v", err)
	}
	if diff := cmp.Diff(map[string]any{"colorTemperature": "cool"}, reused.Args); diff != "" {
		t.Fatalf("reused id args mismatch (-want +got):\n%s", diff)
	}
}

func TestStreamedFunctionCallAppendRequiresWillContinue(t *testing.T) {
	acc := newStreamedFunctionCallAccumulator()
	call := &FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.color", StringValue: "war"},
		},
		WillContinue: Ptr(true),
	}
	if err := acc.applyFunctionCall(call); err != nil {
		t.Fatalf("apply: %v", err)
	}
	next := &FunctionCall{
		ID:          "call-1",
		PartialArgs: []*PartialArg{{JsonPath: "$.color", StringValue: "m"}},
	}
	err := acc.applyFunctionCall(next)
	if err == nil || !strings.Contains(err.Error(), "incompatible shapes") {
		t.Fatalf("expected incompatible shapes error, got %v", err)
	}
	if next.Args != nil {
		t.Fatalf("failed call mutated args: %#v", next.Args)
	}

	continued := &FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.extra", NumberValue: Ptr(1.0)},
		},
	}
	if err := acc.applyFunctionCall(continued); err != nil {
		t.Fatalf("state was overwritten after conflict: %v", err)
	}
	if continued.Args["color"] != "war" || continued.Args["extra"] != 1.0 {
		t.Fatalf("args = %#v", continued.Args)
	}
}

func TestStreamedFunctionCallShapeConflict(t *testing.T) {
	acc := newStreamedFunctionCallAccumulator()
	call := &FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.location.latitude", NumberValue: Ptr(1.5)},
		},
		WillContinue: Ptr(true),
	}
	if err := acc.applyFunctionCall(call); err != nil {
		t.Fatalf("apply: %v", err)
	}
	conflict := &FunctionCall{
		ID:           "call-1",
		PartialArgs:  []*PartialArg{{JsonPath: "$.location", StringValue: "Paris"}},
		WillContinue: Ptr(true),
	}
	err := acc.applyFunctionCall(conflict)
	if err == nil || !strings.Contains(err.Error(), "incompatible shapes") {
		t.Fatalf("expected incompatible shapes error, got %v", err)
	}
	again := &FunctionCall{
		ID:          "call-1",
		PartialArgs: []*PartialArg{{JsonPath: "$.location.longitude", NumberValue: Ptr(2.5)}},
	}
	if err := acc.applyFunctionCall(again); err != nil {
		t.Fatalf("apply after conflict: %v", err)
	}
	location, _ := again.Args["location"].(map[string]any)
	if location["latitude"] != 1.5 || location["longitude"] != 2.5 {
		t.Fatalf("location = %#v", location)
	}
}

func TestStreamedFunctionCallsOnResponseAndLiveToolCall(t *testing.T) {
	acc := newStreamedFunctionCallAccumulator()
	response := &GenerateContentResponse{
		Candidates: []*Candidate{{
			Content: &Content{
				Role: RoleModel,
				Parts: []*Part{
					{FunctionCall: &FunctionCall{
						ID:   "a",
						Name: "alpha",
						PartialArgs: []*PartialArg{
							{JsonPath: "$.value", StringValue: "one", WillContinue: Ptr(true)},
						},
						WillContinue: Ptr(true),
					}},
					{FunctionCall: &FunctionCall{
						ID:   "b",
						Name: "beta",
						PartialArgs: []*PartialArg{
							{JsonPath: "$.value", StringValue: "two"},
						},
					}},
				},
			},
		}},
	}
	if err := acc.applyGenerateContentResponse(response); err != nil {
		t.Fatalf("apply response: %v", err)
	}
	next := &GenerateContentResponse{
		Candidates: []*Candidate{{
			Content: &Content{
				Role: RoleModel,
				Parts: []*Part{{
					FunctionCall: &FunctionCall{
						ID:          "a",
						Name:        "alpha",
						PartialArgs: []*PartialArg{{JsonPath: "$.value", StringValue: "-done"}},
					},
				}},
			},
		}},
	}
	if err := acc.applyGenerateContentResponse(next); err != nil {
		t.Fatalf("apply next response: %v", err)
	}

	fromParts := response.Candidates[0].Content.Parts[0].FunctionCall.Args
	if fromParts["value"] != "one" {
		t.Fatalf("in-progress part args = %#v", fromParts)
	}
	calls := next.FunctionCalls()
	if len(calls) != 1 || calls[0].Args["value"] != "one-done" {
		t.Fatalf("FunctionCalls() = %#v", calls)
	}
	if !reflect.DeepEqual(calls[0].Args, next.Candidates[0].Content.Parts[0].FunctionCall.Args) {
		t.Fatalf("public paths diverged")
	}

	live := newStreamedFunctionCallAccumulator()
	message := &LiveServerMessage{ToolCall: &LiveServerToolCall{FunctionCalls: []*FunctionCall{{
		ID:   "tool-1",
		Name: "lookup",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.query", StringValue: "par", WillContinue: Ptr(true)},
		},
		WillContinue: Ptr(true),
	}}}}
	if err := live.applyLiveServerMessage(message); err != nil {
		t.Fatalf("live: %v", err)
	}
	message2 := &LiveServerMessage{ToolCall: &LiveServerToolCall{FunctionCalls: []*FunctionCall{{
		ID:          "tool-1",
		PartialArgs: []*PartialArg{{JsonPath: "$.query", StringValue: "is"}},
	}}}}
	if err := live.applyLiveServerMessage(message2); err != nil {
		t.Fatalf("live 2: %v", err)
	}
	if message2.ToolCall.FunctionCalls[0].Args["query"] != "paris" {
		t.Fatalf("live args = %#v", message2.ToolCall.FunctionCalls[0].Args)
	}
}

func TestMergeStreamedFunctionCallHistory(t *testing.T) {
	chunks := []*Content{
		{Role: RoleModel, Parts: []*Part{{
			ThoughtSignature: []byte("sig"),
			FunctionCall: &FunctionCall{
				ID:           "b",
				Name:         "beta",
				Args:         map[string]any{"n": "first"},
				PartialArgs:  []*PartialArg{{JsonPath: "$.n", StringValue: "first"}},
				WillContinue: Ptr(true),
			},
		}}},
		{Role: RoleModel, Parts: []*Part{{
			FunctionCall: &FunctionCall{
				ID:          "a",
				Name:        "alpha",
				Args:        map[string]any{"n": "A"},
				PartialArgs: []*PartialArg{{JsonPath: "$.n", StringValue: "A"}},
			},
		}}},
		{Role: RoleModel, Parts: []*Part{{
			FunctionCall: &FunctionCall{
				ID:          "b",
				Name:        "beta",
				Args:        map[string]any{"n": "second"},
				PartialArgs: []*PartialArg{{JsonPath: "$.n", StringValue: "second"}},
			},
		}}},
		{Role: RoleModel, Parts: []*Part{{
			FunctionCall: &FunctionCall{
				ID:          "b",
				Name:        "beta",
				Args:        map[string]any{"n": "fresh"},
				PartialArgs: []*PartialArg{{JsonPath: "$.n", StringValue: "fresh"}},
			},
		}}},
	}
	merged := mergeStreamedFunctionCallContents(chunks)
	if len(merged) != 1 || merged[0].Role != RoleModel || len(merged[0].Parts) != 3 {
		t.Fatalf("merged = %#v", merged)
	}
	wantNames := []string{"beta", "alpha", "beta"}
	wantArgs := []string{"second", "A", "fresh"}
	for i, part := range merged[0].Parts {
		call := part.FunctionCall
		if call.Name != wantNames[i] || call.Args["n"] != wantArgs[i] {
			t.Fatalf("part %d = %#v", i, call)
		}
		if call.PartialArgs != nil || call.WillContinue != nil {
			t.Fatalf("part %d kept partial state: %#v", i, call)
		}
	}
	if string(merged[0].Parts[0].ThoughtSignature) != "sig" {
		t.Fatalf("thought signature = %q", merged[0].Parts[0].ThoughtSignature)
	}

	text := []*Content{{Role: RoleModel, Parts: []*Part{{Text: "hello"}}}, {Role: RoleModel, Parts: []*Part{{Text: " world"}}}}
	if diff := cmp.Diff(text, mergeStreamedFunctionCallContents(text)); diff != "" {
		t.Fatalf("text history changed (-want +got):\n%s", diff)
	}
}

func TestGenerateContentStreamAccumulatesFunctionCallArgs(t *testing.T) {
	var requests [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, body)
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(r.URL.Path, "streamGenerateContent") {
			_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"id\":\"call-1\",\"name\":\"controlLight\",\"willContinue\":true,\"args\":{\"brightness\":50},\"partialArgs\":[{\"jsonPath\":\"$.colorTemperature\",\"stringValue\":\"wa\",\"willContinue\":true},{\"jsonPath\":\"$.power\",\"nullValue\":null}]}}]}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"id\":\"call-2\",\"name\":\"openDoor\",\"partialArgs\":[{\"jsonPath\":\"$.door\",\"stringValue\":\"front\"}]}}]},\"finishReason\":\"STOP\"}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"id\":\"call-1\",\"name\":\"controlLight\",\"partialArgs\":[{\"jsonPath\":\"$.colorTemperature\",\"stringValue\":\"rm\"}]}}]},\"finishReason\":\"STOP\"}]}\n\n")
			return
		}
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	}))
	defer server.Close()

	cc := &ClientConfig{
		Backend:     BackendVertexAI,
		HTTPOptions: HTTPOptions{BaseURL: server.URL, APIVersion: "v1"},
		HTTPClient:  server.Client(),
		Credentials: nil,
	}
	ac := &apiClient{clientConfig: cc}
	client := &Client{
		clientConfig: *cc,
		Models:       &Models{apiClient: ac},
		Chats:        &Chats{apiClient: ac},
	}

	var got []*GenerateContentResponse
	for response, err := range client.Models.GenerateContentStream(context.Background(), "gemini-2.5-pro", Text("dim the lights"), nil) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		got = append(got, response)
	}
	if len(got) != 3 {
		t.Fatalf("got %d chunks", len(got))
	}
	if got[0].FunctionCalls()[0].Args["colorTemperature"] != "wa" || got[0].FunctionCalls()[0].Args["brightness"] != 50.0 {
		t.Fatalf("first FunctionCalls() = %#v", got[0].FunctionCalls()[0].Args)
	}
	if _, ok := got[0].Candidates[0].Content.Parts[0].FunctionCall.Args["power"]; !ok || got[0].Candidates[0].Content.Parts[0].FunctionCall.Args["power"] != nil {
		t.Fatalf("power = %#v", got[0].Candidates[0].Content.Parts[0].FunctionCall.Args)
	}
	if got[2].FunctionCalls()[0].Args["colorTemperature"] != "warm" || got[2].Candidates[0].Content.Parts[0].FunctionCall.Args["brightness"] != 50.0 {
		t.Fatalf("final args = %#v", got[2].FunctionCalls()[0].Args)
	}

	chat, err := client.Chats.Create(context.Background(), "gemini-2.5-pro", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range chat.SendMessageStream(context.Background(), Part{Text: "dim the lights"}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	history := chat.History(true)
	if len(history) != 2 {
		t.Fatalf("history len = %d", len(history))
	}
	modelTurn := history[1]
	if modelTurn.Role != RoleModel || len(modelTurn.Parts) != 2 {
		t.Fatalf("model turn = %#v", modelTurn)
	}
	if modelTurn.Parts[0].FunctionCall.Name != "controlLight" || modelTurn.Parts[1].FunctionCall.Name != "openDoor" {
		t.Fatalf("call order = %#v %#v", modelTurn.Parts[0].FunctionCall, modelTurn.Parts[1].FunctionCall)
	}
	if modelTurn.Parts[0].FunctionCall.Args["colorTemperature"] != "warm" || modelTurn.Parts[0].FunctionCall.PartialArgs != nil {
		t.Fatalf("stored call = %#v", modelTurn.Parts[0].FunctionCall)
	}

	if _, err := chat.SendMessage(context.Background(), Part{Text: "thanks"}); err != nil {
		t.Fatal(err)
	}
	replay := string(requests[len(requests)-1])
	if !strings.Contains(replay, `"colorTemperature":"warm"`) && !strings.Contains(replay, `"colorTemperature": "warm"`) {
		t.Fatalf("replay body missing accumulated args: %s", replay)
	}
	if strings.Contains(replay, "partialArgs") || strings.Contains(replay, "willContinue") {
		t.Fatalf("replay body kept partial fragments: %s", replay)
	}
}

func TestReceiveAccumulatesLiveToolCallArgs(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		messages := []string{
			`{"toolCall":{"functionCalls":[{"id":"tool-1","name":"lookup","willContinue":true,"partialArgs":[{"jsonPath":"$.query","stringValue":"par","willContinue":true}]}]}}`,
			`{"toolCall":{"functionCalls":[{"id":"tool-1","name":"lookup","partialArgs":[{"jsonPath":"$.query","stringValue":"is"}]}]}}`,
		}
		for _, message := range messages {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	session := &Session{
		conn:      conn,
		apiClient: &apiClient{clientConfig: &ClientConfig{}},
	}
	first, err := session.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if first.ToolCall.FunctionCalls[0].Args["query"] != "par" {
		t.Fatalf("first live args = %#v", first.ToolCall.FunctionCalls[0].Args)
	}
	second, err := session.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if second.ToolCall.FunctionCalls[0].Args["query"] != "paris" {
		t.Fatalf("second live args = %#v", second.ToolCall.FunctionCalls[0].Args)
	}
}

func TestPartialArgNullValueJSON(t *testing.T) {
	var partial PartialArg
	if err := json.Unmarshal([]byte(`{"jsonPath":"$.power","nullValue":null}`), &partial); err != nil {
		t.Fatal(err)
	}
	if partial.NULLValue == "" {
		t.Fatal("nullValue was not captured")
	}
	acc := newStreamedFunctionCallAccumulator()
	call := &FunctionCall{ID: "1", PartialArgs: []*PartialArg{&partial}}
	if err := acc.applyFunctionCall(call); err != nil {
		t.Fatal(err)
	}
	value, ok := call.Args["power"]
	if !ok || value != nil {
		t.Fatalf("power = %#v, ok %v", value, ok)
	}
}
