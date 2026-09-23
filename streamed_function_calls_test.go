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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/auth"
	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"
)

func TestStreamedFunctionCallAccumulator(t *testing.T) {
	willContinue := true
	done := false
	acc := newStreamedFunctionCallAccumulator()

	first := &FunctionCall{
		ID:   "call-1",
		Name: "controlLight",
		Args: map[string]any{"brightness": 50},
		PartialArgs: []*PartialArg{
			{JsonPath: "$.colorTemperature", StringValue: "wa", WillContinue: &willContinue},
			{JsonPath: "$['room']", StringValue: "liv", WillContinue: &willContinue},
		},
		WillContinue: &willContinue,
	}
	if err := acc.applyFunctionCall(first); err != nil {
		t.Fatalf("apply first call: %v", err)
	}
	if diff := cmp.Diff(map[string]any{
		"brightness":       50,
		"colorTemperature": "wa",
		"room":             "liv",
	}, first.Args); diff != "" {
		t.Fatalf("first args mismatch (-want +got):\n%s", diff)
	}

	second := &FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.colorTemperature", StringValue: "rm"},
			{JsonPath: "$.room", StringValue: "ing", WillContinue: &willContinue},
			{JsonPath: "$.tags[0]", StringValue: "warm"},
			{JsonPath: "$.tags[2].name", StringValue: "late"},
			{JsonPath: "$.meta['shade'].level", NumberValue: Ptr(2.0)},
			{JsonPath: "$.enabled", BoolValue: Ptr(false)},
			{JsonPath: "$.optional", NULLValue: "NULL_VALUE"},
		},
		WillContinue: &done,
	}
	if err := acc.applyFunctionCall(second); err != nil {
		t.Fatalf("apply second call: %v", err)
	}
	if diff := cmp.Diff(map[string]any{
		"brightness":       50,
		"colorTemperature": "warm",
		"room":             "living",
		"tags":             []any{"warm", nil, map[string]any{"name": "late"}},
		"meta":             map[string]any{"shade": map[string]any{"level": 2.0}},
		"enabled":          false,
		"optional":         nil,
	}, second.Args); diff != "" {
		t.Fatalf("second args mismatch (-want +got):\n%s", diff)
	}
	if len(second.PartialArgs) == 0 || second.WillContinue == nil {
		t.Fatalf("streamed response should keep partial fragments, got %#v", second)
	}

	reused := &FunctionCall{
		ID:          "call-1",
		Name:        "controlLight",
		PartialArgs: []*PartialArg{{JsonPath: "$.zone", StringValue: "north"}},
	}
	if err := acc.applyFunctionCall(reused); err != nil {
		t.Fatalf("apply reused id: %v", err)
	}
	if diff := cmp.Diff(map[string]any{"zone": "north"}, reused.Args); diff != "" {
		t.Fatalf("reused id args mismatch (-want +got):\n%s", diff)
	}
}

func TestStreamedFunctionCallNamelessReuse(t *testing.T) {
	willContinue := true
	acc := newStreamedFunctionCallAccumulator()
	var chunks []*Content

	record := func(call *FunctionCall) {
		t.Helper()
		if err := acc.applyFunctionCall(call); err != nil {
			t.Fatalf("apply %#v: %v", call, err)
		}
		chunks = append(chunks, &Content{Role: RoleModel, Parts: []*Part{{FunctionCall: call}}})
	}

	record(&FunctionCall{
		Name:         "get_current_weather",
		WillContinue: &willContinue,
	})
	record(&FunctionCall{
		PartialArgs:  []*PartialArg{{JsonPath: "$.location", StringValue: "New Delhi", WillContinue: &willContinue}},
		WillContinue: &willContinue,
	})
	record(&FunctionCall{
		PartialArgs:  []*PartialArg{{JsonPath: "$.location", StringValue: ""}},
		WillContinue: &willContinue,
	})
	record(&FunctionCall{})
	record(&FunctionCall{
		Name:         "get_current_weather",
		WillContinue: &willContinue,
	})
	record(&FunctionCall{
		PartialArgs:  []*PartialArg{{JsonPath: "$.location", StringValue: "San Francisco"}},
		WillContinue: &willContinue,
	})
	record(&FunctionCall{})

	if diff := cmp.Diff(map[string]any{"location": "New Delhi"}, chunks[3].Parts[0].FunctionCall.Args); diff != "" {
		t.Fatalf("first closer args (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]any{"location": "San Francisco"}, chunks[6].Parts[0].FunctionCall.Args); diff != "" {
		t.Fatalf("second closer args (-want +got):\n%s", diff)
	}

	merged := mergeStreamedFunctionCallContents(chunks)
	want := []*Part{
		{FunctionCall: &FunctionCall{Name: "get_current_weather", Args: map[string]any{"location": "New Delhi"}}},
		{FunctionCall: &FunctionCall{Name: "get_current_weather", Args: map[string]any{"location": "San Francisco"}}},
	}
	if len(merged) != 1 {
		t.Fatalf("merged = %#v", merged)
	}
	if diff := cmp.Diff(want, merged[0].Parts); diff != "" {
		t.Fatalf("stored turn mismatch (-want +got):\n%s", diff)
	}
}

func TestStreamedFunctionCallStringContinuationAndConflicts(t *testing.T) {
	willContinue := true
	acc := newStreamedFunctionCallAccumulator()
	open := &FunctionCall{
		ID:           "call-1",
		PartialArgs:  []*PartialArg{{JsonPath: "$.note", StringValue: "hel", WillContinue: &willContinue}},
		WillContinue: &willContinue,
	}
	if err := acc.applyFunctionCall(open); err != nil {
		t.Fatalf("open call: %v", err)
	}

	conflict := &FunctionCall{
		ID: "call-1",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.note", StringValue: "lo"},
			{JsonPath: "$.note.extra", StringValue: "nope"},
		},
		WillContinue: &willContinue,
	}
	if err := acc.applyFunctionCall(conflict); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("expected incompatible shape error, got %v", err)
	}
	if diff := cmp.Diff(map[string]any{"note": "hel"}, open.Args); diff != "" {
		t.Fatalf("conflict mutated earlier args (-want +got):\n%s", diff)
	}
	if conflict.Args != nil {
		t.Fatalf("failed call should not publish partial args, got %#v", conflict.Args)
	}

	continued := &FunctionCall{
		ID:          "call-1",
		PartialArgs: []*PartialArg{{JsonPath: "$.note", StringValue: "lo"}},
	}
	if err := acc.applyFunctionCall(continued); err != nil {
		t.Fatalf("append after rolled back conflict: %v", err)
	}
	if got := continued.Args["note"]; got != "hello" {
		t.Fatalf("note = %#v, want hello", got)
	}

	overwrite := &FunctionCall{
		Name:        "other",
		PartialArgs: []*PartialArg{{JsonPath: "$.note", StringValue: "ab"}, {JsonPath: "$.note", StringValue: "cd"}},
	}
	if err := acc.applyFunctionCall(overwrite); err == nil {
		t.Fatal("expected overwrite without willContinue to fail")
	}
}

func TestStreamedFunctionCallPaths(t *testing.T) {
	acc := newStreamedFunctionCallAccumulator()
	call := &FunctionCall{
		Name: "lookup",
		PartialArgs: []*PartialArg{
			{JsonPath: "$", StringValue: "root"},
		},
	}
	if err := acc.applyFunctionCall(call); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("root scalar should be an incompatible shape, got %v", err)
	}

	call = &FunctionCall{
		Name: "lookup",
		PartialArgs: []*PartialArg{
			{JsonPath: "$.items[0].label", StringValue: "a"},
			{JsonPath: `$["items"][1]["label"]`, StringValue: "b"},
			{JsonPath: "$.items[0]", NumberValue: Ptr(1.0)},
		},
	}
	if err := acc.applyFunctionCall(call); err == nil {
		t.Fatal("expected object/scalar conflict at $.items[0]")
	}

	if _, err := parseStreamedJSONPath("$.ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := parseStreamedJSONPath("items[0]"); err == nil {
		t.Fatal("expected path without root $ to fail")
	}
}

func TestStreamedFunctionCallHistoryOrder(t *testing.T) {
	willContinue := true
	contents := []*Content{
		{Role: RoleModel, Parts: []*Part{{
			ThoughtSignature: []byte("sig-a"),
			FunctionCall: &FunctionCall{
				ID:           "a",
				Name:         "alpha",
				Args:         map[string]any{"n": "a1"},
				PartialArgs:  []*PartialArg{{JsonPath: "$.n", StringValue: "a1"}},
				WillContinue: &willContinue,
			},
		}}},
		{Role: RoleModel, Parts: []*Part{{
			FunctionCall: &FunctionCall{
				ID:           "b",
				Name:         "beta",
				Args:         map[string]any{"n": "b"},
				PartialArgs:  []*PartialArg{{JsonPath: "$.n", StringValue: "b"}},
				WillContinue: &willContinue,
			},
		}}},
		{Role: RoleModel, Parts: []*Part{{
			FunctionCall: &FunctionCall{
				ID:          "a",
				Args:        map[string]any{"n": "a-final"},
				PartialArgs: []*PartialArg{{JsonPath: "$.n", StringValue: "-final"}},
			},
		}, {
			FunctionCall: &FunctionCall{ID: "b"},
		}}},
	}
	merged := mergeStreamedFunctionCallContents(contents)
	if len(merged) != 1 || len(merged[0].Parts) != 2 {
		t.Fatalf("merged contents = %#v", merged)
	}
	want := []*Part{
		{ThoughtSignature: []byte("sig-a"), FunctionCall: &FunctionCall{ID: "a", Name: "alpha", Args: map[string]any{"n": "a-final"}}},
		{FunctionCall: &FunctionCall{ID: "b", Name: "beta", Args: map[string]any{"n": "b"}}},
	}
	if diff := cmp.Diff(want, merged[0].Parts); diff != "" {
		t.Fatalf("history mismatch (-want +got):\n%s", diff)
	}
	if merged[0].Parts[0].FunctionCall.PartialArgs != nil || merged[0].Parts[0].FunctionCall.WillContinue != nil {
		t.Fatalf("stored call still has partial fragments: %#v", merged[0].Parts[0].FunctionCall)
	}
}

func TestStreamedFunctionCallTextHistoryUnchanged(t *testing.T) {
	contents := []*Content{
		{Role: RoleModel, Parts: []*Part{{Text: "hello"}}},
		{Role: RoleModel, Parts: []*Part{{Text: " world"}}},
	}
	if got := mergeStreamedFunctionCallContents(contents); len(got) != 2 || got[0] != contents[0] {
		t.Fatalf("text history should be left untouched")
	}
}

func TestGenerateContentStreamAccumulatesFunctionCallArgs(t *testing.T) {
	willContinue := true
	var requests []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, string(body))
		w.WriteHeader(http.StatusOK)
		if len(requests) > 1 {
			fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`)
			return
		}
		fmt.Fprint(w, strings.Join([]string{
			`data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"1","name":"controlLight","args":{"brightness":50},"partialArgs":[{"jsonPath":"$.colorTemperature","stringValue":"wa","willContinue":true},{"jsonPath":"$.tags[0]","stringValue":"warm"}],"willContinue":true}}]}}]}`,
			``,
			`data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"1","partialArgs":[{"jsonPath":"$.colorTemperature","stringValue":"rm"},{"jsonPath":"$['room']","stringValue":"living"},{"jsonPath":"$.optional","nullValue":"NULL_VALUE"}]}}]},"finishReason":"STOP"}]}`,
			``,
			`data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"2","name":"getWeather","partialArgs":[{"jsonPath":"$.location","stringValue":"Paris"}]}}]},"finishReason":"STOP"}]}`,
			``,
		}, "\n"))
	}))
	defer ts.Close()

	cc := &ClientConfig{
		HTTPOptions: HTTPOptions{BaseURL: ts.URL},
		HTTPClient:  ts.Client(),
		Credentials: &auth.Credentials{},
	}
	ac := &apiClient{clientConfig: cc}
	client := &Client{clientConfig: *cc, Models: &Models{apiClient: ac}, Chats: &Chats{apiClient: ac}}
	chat, err := client.Chats.Create(context.Background(), "gemini-2.5-flash", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var snapshots []map[string]any
	for result, err := range chat.SendMessageStream(context.Background(), Part{Text: "control the light"}) {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		calls := result.FunctionCalls()
		if len(calls) != 1 {
			t.Fatalf("FunctionCalls() = %#v", calls)
		}
		partCall := result.Candidates[0].Content.Parts[0].FunctionCall
		if diff := cmp.Diff(calls[0].Args, partCall.Args); diff != "" {
			t.Fatalf("public access paths differ (-FunctionCalls +part):\n%s", diff)
		}
		snapshots = append(snapshots, cloneJSONMap(calls[0].Args))
	}

	if diff := cmp.Diff([]map[string]any{
		{"brightness": float64(50), "colorTemperature": "wa", "tags": []any{"warm"}},
		{"brightness": float64(50), "colorTemperature": "warm", "tags": []any{"warm"}, "room": "living", "optional": nil},
		{"location": "Paris"},
	}, snapshots); diff != "" {
		t.Fatalf("stream snapshots mismatch (-want +got):\n%s", diff)
	}

	history := chat.History(true)
	if len(history) != 2 {
		t.Fatalf("curated history len = %d, %#v", len(history), history)
	}
	modelTurn := history[1]
	if modelTurn.Role != RoleModel || len(modelTurn.Parts) != 2 {
		t.Fatalf("stored model turn = %#v", modelTurn)
	}
	if modelTurn.Parts[0].FunctionCall.Name != "controlLight" || modelTurn.Parts[1].FunctionCall.Name != "getWeather" {
		t.Fatalf("stored call order = %#v %#v", modelTurn.Parts[0].FunctionCall, modelTurn.Parts[1].FunctionCall)
	}
	if modelTurn.Parts[0].FunctionCall.PartialArgs != nil || modelTurn.Parts[0].FunctionCall.WillContinue != nil {
		t.Fatalf("stored call kept partial fragments: %#v", modelTurn.Parts[0].FunctionCall)
	}
	if diff := cmp.Diff(map[string]any{
		"brightness":       float64(50),
		"colorTemperature": "warm",
		"tags":             []any{"warm"},
		"room":             "living",
		"optional":         nil,
	}, modelTurn.Parts[0].FunctionCall.Args); diff != "" {
		t.Fatalf("stored args mismatch (-want +got):\n%s", diff)
	}

	_, err = chat.Send(context.Background(), &Part{Text: "thanks"})
	if err != nil {
		t.Fatalf("replay send: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d", len(requests))
	}
	replay := requests[1]
	if strings.Contains(replay, "partialArgs") || strings.Contains(replay, "willContinue") {
		t.Fatalf("replayed turn still contains partial fragments: %s", replay)
	}
	for _, want := range []string{`"name":"controlLight"`, `"name":"getWeather"`, `"colorTemperature":"warm"`, `"location":"Paris"`} {
		if !strings.Contains(replay, want) {
			t.Fatalf("replayed request missing %s: %s", want, replay)
		}
	}
	_ = willContinue
}

func TestGenerateContentStreamFunctionCallConflict(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"1","name":"controlLight","partialArgs":[{"jsonPath":"$.mode","stringValue":"auto","willContinue":true}],"willContinue":true}}]}}]}

data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"1","partialArgs":[{"jsonPath":"$.mode.extra","stringValue":"no"}]}}]}}]}
`)
	}))
	defer ts.Close()

	models := Models{apiClient: &apiClient{clientConfig: &ClientConfig{
		HTTPOptions: HTTPOptions{BaseURL: ts.URL},
		HTTPClient:  ts.Client(),
		Credentials: &auth.Credentials{},
	}}}
	var saw map[string]any
	var streamErr error
	for result, err := range models.GenerateContentStream(context.Background(), "gemini-2.5-flash", Text("x"), nil) {
		if err != nil {
			streamErr = err
			break
		}
		saw = cloneJSONMap(result.FunctionCalls()[0].Args)
	}
	if streamErr == nil || !strings.Contains(streamErr.Error(), "incompatible") {
		t.Fatalf("stream error = %v", streamErr)
	}
	if diff := cmp.Diff(map[string]any{"mode": "auto"}, saw); diff != "" {
		t.Fatalf("earlier chunk was overwritten (-want +got):\n%s", diff)
	}
}

func TestLiveToolCallAccumulatesFunctionCallArgs(t *testing.T) {
	willContinue := true
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		messages := []string{
			`{"toolCall":{"functionCalls":[{"id":"live-1","name":"controlLight","partialArgs":[{"jsonPath":"$.colorTemperature","stringValue":"wa","willContinue":true}],"willContinue":true}]}}`,
			`{"toolCall":{"functionCalls":[{"id":"live-1","partialArgs":[{"jsonPath":"$.colorTemperature","stringValue":"rm"},{"jsonPath":"$.brightness","numberValue":50}]}]}}`,
		}
		for _, message := range messages {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	ctx := context.Background()
	client, err := NewClient(ctx, &ClientConfig{
		Backend:     BackendGeminiAPI,
		APIKey:      "test-api-key",
		HTTPOptions: HTTPOptions{BaseURL: "ws" + strings.TrimPrefix(ts.URL, "http"), APIVersion: "v1alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Live.Connect(ctx, "test-model", &LiveConnectConfig{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	var got []map[string]any
	for i := 0; i < 2; i++ {
		message, err := session.Receive()
		if err != nil {
			t.Fatalf("receive %d: %v", i, err)
		}
		calls := message.ToolCall.FunctionCalls
		if len(calls) != 1 {
			t.Fatalf("tool calls = %#v", calls)
		}
		got = append(got, cloneJSONMap(calls[0].Args))
	}
	if diff := cmp.Diff([]map[string]any{
		{"colorTemperature": "wa"},
		{"colorTemperature": "warm", "brightness": float64(50)},
	}, got); diff != "" {
		t.Fatalf("live tool call args mismatch (-want +got):\n%s", diff)
	}
	_ = willContinue
}
