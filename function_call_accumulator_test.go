// Copyright 2025 Google LLC
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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/auth"
	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"
)

func TestFunctionCallAccumulatorPaths(t *testing.T) {
	acc := newFunctionCallAccumulator()
	fc := &FunctionCall{
		Name: "f",
		Args: map[string]any{"existing": "v"},
		PartialArgs: []*PartialArg{
			{JsonPath: "$.a.b", NumberValue: Ptr(1.0)},
			{JsonPath: "$['c d'][1]", BoolValue: Ptr(true)},
			{JsonPath: `$["e"][0].f`, NULLValue: "NULL_VALUE"},
			{JsonPath: "$.s", StringValue: "hel", WillContinue: Ptr(true)},
		},
		WillContinue: Ptr(true),
	}
	if err := acc.accumulate(fc, nil); err != nil {
		t.Fatal(err)
	}
	fc2 := &FunctionCall{
		PartialArgs:  []*PartialArg{{JsonPath: "$.s", StringValue: "lo"}},
		WillContinue: Ptr(true),
	}
	if err := acc.accumulate(fc2, nil); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"existing": "v",
		"a":        map[string]any{"b": 1.0},
		"c d":      []any{nil, true},
		"e":        []any{map[string]any{"f": nil}},
		"s":        "hello",
	}
	if diff := cmp.Diff(want, fc2.Args); diff != "" {
		t.Errorf("args mismatch (-want +got):\n%s", diff)
	}
	if fc2.Name != "f" {
		t.Errorf("name = %q, want f", fc2.Name)
	}
	// Earlier snapshot must not be mutated by later fragments.
	if fc.Args["s"] != "hel" {
		t.Errorf("earlier snapshot mutated: %v", fc.Args["s"])
	}

	// A path that did not have willContinue replaces instead of appending.
	fc3 := &FunctionCall{PartialArgs: []*PartialArg{{JsonPath: "$.s", StringValue: "x"}}}
	if err := acc.accumulate(fc3, nil); err != nil {
		t.Fatal(err)
	}
	if fc3.Args["s"] != "x" {
		t.Errorf("s = %v, want x", fc3.Args["s"])
	}

	// Reusing the id/name after completion starts fresh.
	fc4 := &FunctionCall{Name: "f", PartialArgs: []*PartialArg{{JsonPath: "$.z", StringValue: "1"}}}
	if err := acc.accumulate(fc4, nil); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]any{"z": "1"}, fc4.Args); diff != "" {
		t.Errorf("fresh state mismatch (-want +got):\n%s", diff)
	}
}

func TestFunctionCallAccumulatorFreshStateForReusedID(t *testing.T) {
	acc := newFunctionCallAccumulator()
	calls := []*FunctionCall{
		{ID: "1", Name: "f", PartialArgs: []*PartialArg{{JsonPath: "$.a", StringValue: "x"}}, WillContinue: Ptr(true)},
		{ID: "1", WillContinue: Ptr(false)},
		{ID: "1", Name: "f", PartialArgs: []*PartialArg{{JsonPath: "$.b", StringValue: "y"}}},
	}
	if err := acc.accumulateCalls(calls); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]any{"a": "x"}, calls[1].Args); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]any{"b": "y"}, calls[2].Args); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestFunctionCallAccumulatorErrors(t *testing.T) {
	tests := []struct {
		name  string
		calls []*FunctionCall
	}{
		{
			name: "object then array",
			calls: []*FunctionCall{
				{Name: "f", PartialArgs: []*PartialArg{{JsonPath: "$.a.b", StringValue: "x"}}, WillContinue: Ptr(true)},
				{PartialArgs: []*PartialArg{{JsonPath: "$.a[0]", StringValue: "y"}}},
			},
		},
		{
			name: "scalar then object",
			calls: []*FunctionCall{
				{Name: "f", PartialArgs: []*PartialArg{{JsonPath: "$.a", StringValue: "x"}, {JsonPath: "$.a.b", StringValue: "y"}}},
			},
		},
		{
			name: "container replaced by scalar",
			calls: []*FunctionCall{
				{Name: "f", Args: map[string]any{"a": map[string]any{"b": 1.0}}, PartialArgs: []*PartialArg{{JsonPath: "$.a", StringValue: "y"}}},
			},
		},
		{
			name: "continued string with number",
			calls: []*FunctionCall{
				{Name: "f", PartialArgs: []*PartialArg{{JsonPath: "$.a", StringValue: "x", WillContinue: Ptr(true)}, {JsonPath: "$.a", NumberValue: Ptr(1.0)}}},
			},
		},
		{
			name: "unsupported path",
			calls: []*FunctionCall{
				{Name: "f", PartialArgs: []*PartialArg{{JsonPath: "$..a", StringValue: "x"}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := newFunctionCallAccumulator().accumulateCalls(tt.calls); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

const streamedFunctionCallSSE = `data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"controlLight","args":{"room":"living"},"willContinue":true}}]}}]}

data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"partialArgs":[{"jsonPath":"$.brightness","numberValue":50},{"jsonPath":"$.color","stringValue":"warm","willContinue":true}],"willContinue":true}}]}}]}

data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"partialArgs":[{"jsonPath":"$.color","stringValue":" white"}],"willContinue":true}}]}}]}

data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{}},{"functionCall":{"name":"setThermostat","partialArgs":[{"jsonPath":"$.temp","numberValue":21}]}}]},"finishReason":"STOP"}]}

`

func newStreamTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	cc := &ClientConfig{
		HTTPOptions: HTTPOptions{BaseURL: ts.URL},
		HTTPClient:  ts.Client(),
		Credentials: &auth.Credentials{},
	}
	ac := &apiClient{clientConfig: cc}
	return &Client{
		clientConfig: *cc,
		Models:       &Models{apiClient: ac},
		Chats:        &Chats{apiClient: ac},
	}
}

func TestGenerateContentStreamAccumulatesPartialArgs(t *testing.T) {
	client := newStreamTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, streamedFunctionCallSSE)
	})
	var last *GenerateContentResponse
	var colors []any
	for resp, err := range client.Models.GenerateContentStream(context.Background(), "m", Text("hi"), nil) {
		if err != nil {
			t.Fatal(err)
		}
		fromHelper := resp.FunctionCalls()[0].Args
		fromParts := resp.Candidates[0].Content.Parts[0].FunctionCall.Args
		if diff := cmp.Diff(fromHelper, fromParts); diff != "" {
			t.Errorf("access paths disagree (-helper +parts):\n%s", diff)
		}
		colors = append(colors, fromParts["color"])
		last = resp
	}
	if diff := cmp.Diff([]any{nil, "warm", "warm white", "warm white"}, colors); diff != "" {
		t.Errorf("color progression (-want +got):\n%s", diff)
	}
	calls := last.FunctionCalls()
	want := map[string]any{"room": "living", "brightness": 50.0, "color": "warm white"}
	if diff := cmp.Diff(want, calls[0].Args); diff != "" {
		t.Errorf("final args (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]any{"temp": 21.0}, calls[1].Args); diff != "" {
		t.Errorf("second call args (-want +got):\n%s", diff)
	}
}

func TestGenerateContentStreamIncompatiblePartialArgs(t *testing.T) {
	client := newStreamTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"f","partialArgs":[{"jsonPath":"$.a","stringValue":"x"}],"willContinue":true}}]}}]}

data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"partialArgs":[{"jsonPath":"$.a[0]","stringValue":"y"}]}}]}}]}

`)
	})
	var gotErr error
	for _, err := range client.Models.GenerateContentStream(context.Background(), "m", Text("hi"), nil) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr == nil {
		t.Fatal("expected error for incompatible partial args")
	}
}

func TestChatSendStreamRecordsCompletedFunctionCalls(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	client := newStreamTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		_ = json.Unmarshal(data, &body)
		mu.Lock()
		bodies = append(bodies, body)
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			fmt.Fprint(w, streamedFunctionCallSSE)
			return
		}
		fmt.Fprint(w, `data:{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}

`)
	})
	ctx := context.Background()
	chat, err := client.Chats.Create(ctx, "m", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range chat.SendMessageStream(ctx, Part{Text: "hi"}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	history := chat.History(true)
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	want := &Content{Role: RoleModel, Parts: []*Part{
		{FunctionCall: &FunctionCall{Name: "controlLight", Args: map[string]any{"room": "living", "brightness": 50.0, "color": "warm white"}}},
		{FunctionCall: &FunctionCall{Name: "setThermostat", Args: map[string]any{"temp": 21.0}}},
	}}
	if diff := cmp.Diff(want, history[1]); diff != "" {
		t.Errorf("model turn (-want +got):\n%s", diff)
	}

	for _, err := range chat.SendMessageStream(ctx, Part{Text: "next"}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	contents := bodies[1]["contents"].([]any)
	wantTurn := map[string]any{"role": "model", "parts": []any{
		map[string]any{"functionCall": map[string]any{"name": "controlLight", "args": map[string]any{"room": "living", "brightness": 50.0, "color": "warm white"}}},
		map[string]any{"functionCall": map[string]any{"name": "setThermostat", "args": map[string]any{"temp": 21.0}}},
	}}
	if diff := cmp.Diff(wantTurn, contents[1]); diff != "" {
		t.Errorf("replayed turn (-want +got):\n%s", diff)
	}
}

func TestLiveReceiveAccumulatesToolCallPartialArgs(t *testing.T) {
	messages := []string{
		`{"toolCall":{"functionCalls":[{"id":"1","name":"f","partialArgs":[{"jsonPath":"$.q","stringValue":"ab","willContinue":true}],"willContinue":true}]}}`,
		`{"toolCall":{"functionCalls":[{"id":"1","partialArgs":[{"jsonPath":"$.q","stringValue":"cd"}]}]}}`,
		`{"toolCall":{"functionCalls":[{"id":"1","name":"f","partialArgs":[{"jsonPath":"$.r","stringValue":"new"}]}]}}`,
	}
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for _, m := range messages {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(m))
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer ts.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{conn: conn, apiClient: &apiClient{clientConfig: &ClientConfig{Backend: BackendGeminiAPI}}, toolCalls: newFunctionCallAccumulator()}
	defer s.Close()

	wants := []map[string]any{{"q": "ab"}, {"q": "abcd"}, {"r": "new"}}
	for i, want := range wants {
		msg, err := s.Receive()
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, msg.ToolCall.FunctionCalls[0].Args); diff != "" {
			t.Errorf("message %d args (-want +got):\n%s", i, diff)
		}
	}
}
