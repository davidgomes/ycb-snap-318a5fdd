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
	"reflect"
	"strings"
	"testing"

	"cloud.google.com/go/auth"
	"github.com/google/go-cmp/cmp"
)

func TestStreamedFunctionCallAccumulator(t *testing.T) {
	continueTrue := true
	continueFalse := false

	t.Run("paths args null and continuation", func(t *testing.T) {
		acc := newStreamedFunctionCallAccumulator()
		first := &FunctionCall{
			ID:   "1",
			Name: "controlLight",
			Args: map[string]any{"brightness": float64(50)},
			PartialArgs: []*PartialArg{
				{JsonPath: "$.color", StringValue: "wa", WillContinue: &continueTrue},
				{JsonPath: `$['room']`, StringValue: "lab"},
				{JsonPath: "$.items[0].name", StringValue: "la", WillContinue: &continueTrue},
			},
			WillContinue: &continueTrue,
		}
		if err := acc.applyFunctionCall(first); err != nil {
			t.Fatal(err)
		}
		second := &FunctionCall{
			PartialArgs: []*PartialArg{
				{JsonPath: `$["color"]`, StringValue: "rm"},
				{JsonPath: "$.items[0]['name']", StringValue: "mp"},
				{JsonPath: "$.note", NULLValue: "NULL_VALUE"},
				{JsonPath: "$.on", BoolValue: &continueTrue},
			},
		}
		if err := acc.applyFunctionCall(second); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"brightness": float64(50),
			"color":      "warm",
			"room":       "lab",
			"items":      []any{map[string]any{"name": "lamp"}},
			"note":       nil,
			"on":         true,
		}
		if diff := cmp.Diff(want, second.Args); diff != "" {
			t.Fatalf("accumulated args mismatch (-want +got):\n%s", diff)
		}
		if _, ok := second.Args["note"]; !ok {
			t.Fatal("nullValue was missing from accumulated args")
		}

		reused := &FunctionCall{
			ID:          "1",
			Name:        "controlLight",
			PartialArgs: []*PartialArg{{JsonPath: "$.only", StringValue: "fresh"}},
		}
		if err := acc.applyFunctionCall(reused); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(map[string]any{"only": "fresh"}, reused.Args); diff != "" {
			t.Fatalf("reused id args mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("root json merges with existing args", func(t *testing.T) {
		acc := newStreamedFunctionCallAccumulator()
		call := &FunctionCall{
			Name: "sum",
			Args: map[string]any{"keep": true},
			PartialArgs: []*PartialArg{
				{JsonPath: "$", StringValue: `{"k":`, WillContinue: &continueTrue},
				{JsonPath: "$", StringValue: `1}`},
			},
		}
		if err := acc.applyFunctionCall(call); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"keep": true, "k": float64(1)}
		if diff := cmp.Diff(want, call.Args); diff != "" {
			t.Fatalf("root args mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("incompatible shape", func(t *testing.T) {
		acc := newStreamedFunctionCallAccumulator()
		call := &FunctionCall{
			ID: "shape",
			PartialArgs: []*PartialArg{
				{JsonPath: "$.foo", StringValue: "a", WillContinue: &continueFalse},
				{JsonPath: "$.foo.bar", NumberValue: Ptr(1.0)},
			},
			WillContinue: &continueTrue,
		}
		if err := acc.applyFunctionCall(call); err == nil {
			t.Fatal("expected path conflict")
		}
		next := &FunctionCall{
			ID:          "shape",
			PartialArgs: []*PartialArg{{JsonPath: "$.other", StringValue: "ok"}},
		}
		if err := acc.applyFunctionCall(next); err != nil {
			t.Fatal(err)
		}
		if _, ok := next.Args["foo"]; ok {
			t.Fatalf("failed fragment was committed: %#v", next.Args)
		}
	})

	t.Run("completed string is not overwritten", func(t *testing.T) {
		acc := newStreamedFunctionCallAccumulator()
		err := acc.applyFunctionCall(&FunctionCall{
			Name: "once",
			PartialArgs: []*PartialArg{
				{JsonPath: "$.color", StringValue: "warm", WillContinue: &continueFalse},
				{JsonPath: "$.color", StringValue: "white"},
			},
			WillContinue: &continueTrue,
		})
		if err == nil {
			t.Fatal("expected overwrite conflict")
		}
	})
}

func TestMergeStreamedFunctionCallContents(t *testing.T) {
	continueTrue := true
	firstSig := []byte("sig-a")
	contents := []*Content{
		{
			Role: RoleModel,
			Parts: []*Part{{
				ThoughtSignature: firstSig,
				FunctionCall: &FunctionCall{
					ID:           "a",
					Name:         "alpha",
					Args:         map[string]any{"n": float64(1)},
					PartialArgs:  []*PartialArg{{JsonPath: "$.n", NumberValue: Ptr(1.0)}},
					WillContinue: &continueTrue,
				},
			}},
		},
		{
			Role: RoleModel,
			Parts: []*Part{{
				FunctionCall: &FunctionCall{
					ID:           "b",
					Name:         "beta",
					Args:         map[string]any{"city": "P"},
					PartialArgs:  []*PartialArg{{JsonPath: "$.city", StringValue: "P", WillContinue: &continueTrue}},
					WillContinue: &continueTrue,
				},
			}},
		},
		{
			Role: RoleModel,
			Parts: []*Part{
				{
					FunctionCall: &FunctionCall{
						ID:   "b",
						Args: map[string]any{"city": "Paris"},
					},
				},
				{
					FunctionCall: &FunctionCall{
						Args: map[string]any{"n": float64(2)},
					},
				},
			},
		},
	}
	merged := mergeStreamedFunctionCallContents(contents)
	if len(merged) != 1 {
		t.Fatalf("expected one model turn, got %#v", merged)
	}
	if merged[0].Role != RoleModel || len(merged[0].Parts) != 2 {
		t.Fatalf("unexpected merged content: %#v", merged[0])
	}
	got := []FunctionCall{*merged[0].Parts[0].FunctionCall, *merged[0].Parts[1].FunctionCall}
	want := []FunctionCall{
		{ID: "a", Name: "alpha", Args: map[string]any{"n": float64(2)}},
		{ID: "b", Name: "beta", Args: map[string]any{"city": "Paris"}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("merged calls mismatch (-want +got):\n%s", diff)
	}
	if merged[0].Parts[0].FunctionCall.PartialArgs != nil || merged[0].Parts[0].FunctionCall.WillContinue != nil {
		t.Fatal("stored call still carries partial fragments")
	}
	if !reflect.DeepEqual(merged[0].Parts[0].ThoughtSignature, firstSig) {
		t.Fatalf("thought signature = %v", merged[0].Parts[0].ThoughtSignature)
	}
	merged[0].Parts[1].FunctionCall.Args["city"] = "mutated"
	if contents[2].Parts[0].FunctionCall.Args["city"] != "Paris" {
		t.Fatal("history aliases the streamed response args")
	}
}

func TestGenerateContentStreamAccumulatesFunctionCallArgs(t *testing.T) {
	ctx := context.Background()
	var saw []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		saw = append(saw, string(body))
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"1","name":"controlLight","args":{"brightness":50},"partialArgs":[{"jsonPath":"$.color","stringValue":"wa","willContinue":true},{"jsonPath":"$.items[0].name","stringValue":"la","willContinue":true}],"willContinue":true}}]}}]}

data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"partialArgs":[{"jsonPath":"$[\"color\"]","stringValue":"rm"},{"jsonPath":"$.items[0]['name']","stringValue":"mp"},{"jsonPath":"$.note","nullValue":"NULL_VALUE"}]}}]},"finishReason":"STOP"}]}

`)
	}))
	defer ts.Close()

	client := testStreamClient(t, ts)
	var responses []*GenerateContentResponse
	for response, err := range client.Models.GenerateContentStream(ctx, "gemini-2.5-flash", Text("dim the light"), nil) {
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != 2 {
		t.Fatalf("got %d responses", len(responses))
	}
	wantFirst := map[string]any{
		"brightness": float64(50),
		"color":      "wa",
		"items":      []any{map[string]any{"name": "la"}},
	}
	if diff := cmp.Diff(wantFirst, responses[0].FunctionCalls()[0].Args); diff != "" {
		t.Fatalf("FunctionCalls args mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantFirst, responses[0].Candidates[0].Content.Parts[0].FunctionCall.Args); diff != "" {
		t.Fatalf("part args mismatch (-want +got):\n%s", diff)
	}
	wantFinal := map[string]any{
		"brightness": float64(50),
		"color":      "warm",
		"items":      []any{map[string]any{"name": "lamp"}},
		"note":       nil,
	}
	finalCall := responses[1].Candidates[0].Content.Parts[0].FunctionCall
	if diff := cmp.Diff(wantFinal, responses[1].FunctionCalls()[0].Args); diff != "" {
		t.Fatalf("final FunctionCalls args mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantFinal, finalCall.Args); diff != "" {
		t.Fatalf("final part args mismatch (-want +got):\n%s", diff)
	}
	if len(saw) != 1 {
		t.Fatalf("unexpected request count %d", len(saw))
	}
}

func TestGenerateContentStreamFunctionCallShapeError(t *testing.T) {
	ctx := context.Background()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"1","name":"controlLight","partialArgs":[{"jsonPath":"$.color","stringValue":"warm"}],"willContinue":true}}]}}]}

data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"partialArgs":[{"jsonPath":"$.color.shade","stringValue":"soft"}]}}]}}]}

`)
	}))
	defer ts.Close()

	client := testStreamClient(t, ts)
	var gotErr error
	var count int
	for _, err := range client.Models.GenerateContentStream(ctx, "gemini-2.5-flash", Text("dim the light"), nil) {
		count++
		if err != nil {
			gotErr = err
			break
		}
	}
	if gotErr == nil {
		t.Fatal("expected streamed path conflict")
	}
	if !strings.Contains(gotErr.Error(), "path conflict") {
		t.Fatalf("error = %v", gotErr)
	}
	if count != 2 {
		t.Fatalf("yielded %d results before conflict", count)
	}
}

func TestChatReplaysCompletedStreamedFunctionCalls(t *testing.T) {
	ctx := context.Background()
	var bodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.WriteHeader(http.StatusOK)
		if len(bodies) == 1 {
			fmt.Fprint(w, `data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"a","name":"alpha","partialArgs":[{"jsonPath":"$.label","stringValue":"al","willContinue":true}],"willContinue":true}}]}}]}

data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"b","name":"beta","partialArgs":[{"jsonPath":"$['city']","stringValue":"Par","willContinue":true}],"willContinue":true}}]}}]}

data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"b","partialArgs":[{"jsonPath":"$.city","stringValue":"is"}]}},{"functionCall":{"partialArgs":[{"jsonPath":"$.label","stringValue":"pha"}]}}]},"finishReason":"STOP"}]}

`)
			return
		}
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`)
	}))
	defer ts.Close()

	client := testStreamClient(t, ts)
	chat, err := client.Chats.Create(ctx, "gemini-2.5-flash", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range chat.SendMessageStream(ctx, Part{Text: "run tools"}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	history := chat.History(true)
	if len(history) != 2 {
		t.Fatalf("curated history len = %d", len(history))
	}
	modelTurn := history[1]
	if modelTurn.Role != RoleModel || len(modelTurn.Parts) != 2 {
		t.Fatalf("stored model turn = %#v", modelTurn)
	}
	if modelTurn.Parts[0].FunctionCall.Name != "alpha" || modelTurn.Parts[1].FunctionCall.Name != "beta" {
		t.Fatalf("call order = %#v %#v", modelTurn.Parts[0].FunctionCall, modelTurn.Parts[1].FunctionCall)
	}
	if diff := cmp.Diff(map[string]any{"label": "alpha"}, modelTurn.Parts[0].FunctionCall.Args); diff != "" {
		t.Fatalf("alpha args mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]any{"city": "Paris"}, modelTurn.Parts[1].FunctionCall.Args); diff != "" {
		t.Fatalf("beta args mismatch (-want +got):\n%s", diff)
	}
	for _, part := range modelTurn.Parts {
		if part.FunctionCall.PartialArgs != nil || part.FunctionCall.WillContinue != nil {
			t.Fatal("stored turn kept partial fragments")
		}
	}

	if _, err := chat.SendMessage(ctx, Part{Text: "thanks"}); err != nil {
		t.Fatal(err)
	}
	replay := bodies[1]
	if strings.Contains(replay, "partialArgs") || strings.Contains(replay, "willContinue") {
		t.Fatalf("replayed turn still has partial fragments: %s", replay)
	}
	alpha := strings.Index(replay, `"name":"alpha"`)
	beta := strings.Index(replay, `"name":"beta"`)
	if alpha < 0 || beta < 0 || alpha > beta {
		t.Fatalf("replayed call order missing: %s", replay)
	}
	if strings.Count(replay, `"functionCall"`) != 2 {
		t.Fatalf("replayed function call count: %s", replay)
	}
	if !strings.Contains(replay, `"city":"Paris"`) || !strings.Contains(replay, `"label":"alpha"`) {
		t.Fatalf("replayed args missing: %s", replay)
	}
}

func TestLiveReceiveAccumulatesToolCallArgs(t *testing.T) {
	ctx := context.Background()
	client, err := NewClient(ctx, &ClientConfig{Backend: BackendGeminiAPI, APIKey: "test-api-key"})
	if err != nil {
		t.Fatal(err)
	}
	ts := setupTestWebsocketServer(t,
		[]string{
			`{"setup":{"model":"models/test-model"}}`,
			`{"clientContent":{"turnComplete":true,"turns":[{"parts":[{"text":"client test message"}],"role":"user"}]}}`,
		},
		[]string{
			`{"toolCall":{"functionCalls":[{"id":"1","name":"controlLight","args":{"brightness":50},"partialArgs":[{"jsonPath":"$.color","stringValue":"wa","willContinue":true}],"willContinue":true}]}}`,
			`{"toolCall":{"functionCalls":[{"partialArgs":[{"jsonPath":"$.color","stringValue":"rm"}]}]}}`,
		},
	)
	defer ts.Close()
	client.Live.apiClient.clientConfig.HTTPOptions.BaseURL = strings.Replace(ts.URL, "http", "ws", 1)

	session, err := client.Live.Connect(ctx, "test-model", &LiveConnectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	first, err := session.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]any{"brightness": float64(50), "color": "wa"}, first.ToolCall.FunctionCalls[0].Args); diff != "" {
		t.Fatalf("first tool call args mismatch (-want +got):\n%s", diff)
	}
	if err := session.SendClientContent(LiveClientContentInput{Turns: Text("client test message"), TurnComplete: Ptr(true)}); err != nil {
		t.Fatal(err)
	}
	second, err := session.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]any{"brightness": float64(50), "color": "warm"}, second.ToolCall.FunctionCalls[0].Args); diff != "" {
		t.Fatalf("second tool call args mismatch (-want +got):\n%s", diff)
	}
}

func testStreamClient(t *testing.T, ts *httptest.Server) *Client {
	t.Helper()
	cc := &ClientConfig{
		Backend:     BackendGeminiAPI,
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
