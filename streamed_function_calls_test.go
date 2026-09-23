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

func newStreamedFunctionCallTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	cc := &ClientConfig{
		HTTPOptions: HTTPOptions{BaseURL: ts.URL},
		HTTPClient:  ts.Client(),
		Credentials: &auth.Credentials{},
	}
	ac := &apiClient{clientConfig: cc}
	return &Client{clientConfig: *cc, Models: &Models{apiClient: ac}, Chats: &Chats{apiClient: ac}}
}

// sseResponse serves chunks as a server-sent event stream. Each chunk lists the
// JSON parts of the first candidate; the last chunk finishes the turn.
func sseResponse(chunks ...[]string) string {
	var b strings.Builder
	for i, parts := range chunks {
		finishReason := ""
		if i == len(chunks)-1 {
			finishReason = `,"finishReason":"STOP"`
		}
		fmt.Fprintf(&b, `data:{"candidates":[{"content":{"role":"model","parts":[%s]}%s}]}`+"\n\n", strings.Join(parts, ","), finishReason)
	}
	return b.String()
}

func functionCallPart(functionCall string) string {
	return `{"functionCall":` + functionCall + `}`
}

func functionCallChunks(functionCalls ...string) [][]string {
	chunks := make([][]string, len(functionCalls))
	for i, fc := range functionCalls {
		chunks[i] = []string{functionCallPart(fc)}
	}
	return chunks
}

func streamFunctionCalls(t *testing.T, chunks [][]string) ([]*GenerateContentResponse, error) {
	t.Helper()
	client := newStreamedFunctionCallTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, sseResponse(chunks...))
	})
	var responses []*GenerateContentResponse
	for resp, err := range client.Models.GenerateContentStream(context.Background(), "gemini-3-pro", Text("hi"), nil) {
		if err != nil {
			return responses, err
		}
		responses = append(responses, resp)
	}
	return responses, nil
}

// argsOfFirstCall returns the Args of the first function call of every response
// through both public access paths, failing if they disagree.
func argsOfFirstCall(t *testing.T, responses []*GenerateContentResponse) []map[string]any {
	t.Helper()
	var got []map[string]any
	for i, resp := range responses {
		calls := resp.FunctionCalls()
		if len(calls) == 0 {
			t.Fatalf("response %d has no function calls", i)
		}
		fromParts := resp.Candidates[0].Content.Parts[0].FunctionCall.Args
		if diff := cmp.Diff(fromParts, calls[0].Args); diff != "" {
			t.Errorf("response %d: FunctionCalls() and Parts disagree (-parts +FunctionCalls()):\n%s", i, diff)
		}
		got = append(got, calls[0].Args)
	}
	return got
}

func TestGenerateContentStreamAccumulatesPartialArgs(t *testing.T) {
	responses, err := streamFunctionCalls(t, functionCallChunks(
		`{"name":"controlLight","partialArgs":[{"jsonPath":"$.brightness","numberValue":50}],"willContinue":true}`,
		`{"partialArgs":[{"jsonPath":"$.colorTemperature","stringValue":"wa","willContinue":true}],"willContinue":true}`,
		`{"partialArgs":[{"jsonPath":"$.colorTemperature","stringValue":"rm"}],"willContinue":true}`,
		`{}`,
	))
	if err != nil {
		t.Fatalf("GenerateContentStream failed: %v", err)
	}

	want := []map[string]any{
		{"brightness": 50.0},
		{"brightness": 50.0, "colorTemperature": "wa"},
		{"brightness": 50.0, "colorTemperature": "warm"},
		{"brightness": 50.0, "colorTemperature": "warm"},
	}
	if diff := cmp.Diff(want, argsOfFirstCall(t, responses)); diff != "" {
		t.Errorf("accumulated Args mismatch (-want +got):\n%s", diff)
	}
	fragment := responses[1].FunctionCalls()[0].PartialArgs
	if len(fragment) != 1 || fragment[0].StringValue != "wa" {
		t.Errorf("PartialArgs of the second response = %+v, want the original fragment", fragment)
	}
}

func TestGenerateContentStreamPartialArgsPathsAndValues(t *testing.T) {
	responses, err := streamFunctionCalls(t, functionCallChunks(
		`{"id":"call-1","name":"configure","args":{"existing":{"kept":true}},"partialArgs":[
			{"jsonPath":"$['display name']","stringValue":"Lamp"},
			{"jsonPath":"$.existing.added","numberValue":1}
		],"willContinue":true}`,
		`{"partialArgs":[
			{"jsonPath":"$.items[1].label","stringValue":"se","willContinue":true},
			{"jsonPath":"$[\"items\"][1]['label']","stringValue":"cond"}
		],"willContinue":true}`,
		`{"partialArgs":[
			{"jsonPath":"$.items[0]","nullValue":"NULL_VALUE"},
			{"jsonPath":"$.enabled","boolValue":false},
			{"jsonPath":"$.nothing","nullValue":null},
			{"jsonPath":"$['it\\'s']","stringValue":"quoted"}
		]}`,
	))
	if err != nil {
		t.Fatalf("GenerateContentStream failed: %v", err)
	}

	want := []map[string]any{
		{
			"existing":     map[string]any{"kept": true, "added": 1.0},
			"display name": "Lamp",
		},
		{
			"existing":     map[string]any{"kept": true, "added": 1.0},
			"display name": "Lamp",
			"items":        []any{nil, map[string]any{"label": "second"}},
		},
		{
			"existing":     map[string]any{"kept": true, "added": 1.0},
			"display name": "Lamp",
			"items":        []any{nil, map[string]any{"label": "second"}},
			"enabled":      false,
			"nothing":      nil,
			"it's":         "quoted",
		},
	}
	if diff := cmp.Diff(want, argsOfFirstCall(t, responses)); diff != "" {
		t.Errorf("accumulated Args mismatch (-want +got):\n%s", diff)
	}
}

func TestGenerateContentStreamPartialArgsStateIsScopedToOneCall(t *testing.T) {
	responses, err := streamFunctionCalls(t, functionCallChunks(
		`{"id":"a","name":"f","partialArgs":[{"jsonPath":"$.x","stringValue":"1","willContinue":true}],"willContinue":true}`,
		`{"id":"b","name":"g","partialArgs":[{"jsonPath":"$.y","stringValue":"2"}],"willContinue":true}`,
		`{"id":"a","partialArgs":[{"jsonPath":"$.x","stringValue":"1"}]}`,
		`{"partialArgs":[{"jsonPath":"$.z","numberValue":3}]}`,
		`{"id":"a","name":"f","partialArgs":[{"jsonPath":"$.x","stringValue":"new"}],"willContinue":true}`,
		`{}`,
	))
	if err != nil {
		t.Fatalf("GenerateContentStream failed: %v", err)
	}

	want := []map[string]any{
		{"x": "1"},
		{"y": "2"},
		{"x": "11"},
		{"y": "2", "z": 3.0},
		{"x": "new"},
		{"x": "new"},
	}
	if diff := cmp.Diff(want, argsOfFirstCall(t, responses)); diff != "" {
		t.Errorf("accumulated Args mismatch (-want +got):\n%s", diff)
	}
}

func TestGenerateContentStreamPartialArgsErrors(t *testing.T) {
	tests := []struct {
		name          string
		functionCalls []string
	}{
		{
			name: "object below a string",
			functionCalls: []string{
				`{"name":"f","partialArgs":[{"jsonPath":"$.a","stringValue":"text"}],"willContinue":true}`,
				`{"partialArgs":[{"jsonPath":"$.a.b","numberValue":1}]}`,
			},
		},
		{
			name: "scalar over an object",
			functionCalls: []string{
				`{"name":"f","partialArgs":[{"jsonPath":"$.a.b","numberValue":1}],"willContinue":true}`,
				`{"partialArgs":[{"jsonPath":"$.a","numberValue":2}]}`,
			},
		},
		{
			name: "array over an object",
			functionCalls: []string{
				`{"name":"f","partialArgs":[{"jsonPath":"$.a.b","numberValue":1}],"willContinue":true}`,
				`{"partialArgs":[{"jsonPath":"$.a[0]","numberValue":2}]}`,
			},
		},
		{
			name: "fragment conflicting with args",
			functionCalls: []string{
				`{"name":"f","args":{"a":"text"},"willContinue":true}`,
				`{"partialArgs":[{"jsonPath":"$.a.b","numberValue":1}]}`,
			},
		},
		{
			name: "continued string followed by a number",
			functionCalls: []string{
				`{"name":"f","partialArgs":[{"jsonPath":"$.a","stringValue":"te","willContinue":true}],"willContinue":true}`,
				`{"partialArgs":[{"jsonPath":"$.a","numberValue":1}]}`,
			},
		},
		{
			name: "scalar at the root",
			functionCalls: []string{
				`{"name":"f","partialArgs":[{"jsonPath":"$.a","stringValue":"ok"}],"willContinue":true}`,
				`{"partialArgs":[{"jsonPath":"$","stringValue":"text"}]}`,
			},
		},
		{
			name: "unsupported path",
			functionCalls: []string{
				`{"name":"f","partialArgs":[{"jsonPath":"$.a","stringValue":"ok"}],"willContinue":true}`,
				`{"partialArgs":[{"jsonPath":"$..a","stringValue":"text"}]}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responses, err := streamFunctionCalls(t, functionCallChunks(append(tt.functionCalls, `{}`)...))
			if err == nil {
				t.Fatalf("GenerateContentStream succeeded, want an error")
			}
			if len(responses) != 1 {
				t.Errorf("got %d responses before the error, want 1", len(responses))
			}
		})
	}
}

func TestParseJSONPath(t *testing.T) {
	valid := []struct {
		path string
		want string
	}{
		{"$", "$"},
		{"$.a", "$.a"},
		{"$['a']", "$.a"},
		{`$["a"]`, "$.a"},
		{"$.a.b[0][12].c", "$.a.b[0][12].c"},
		{"$['a b'].c", `$["a b"].c`},
		{`$['it\'s']`, `$["it's"]`},
		{`$["say \"hi\""]`, `$["say \"hi\""]`},
		{`$['\u0041']`, "$.A"},
		{"$['0']", `$["0"]`},
		{"$['a.b']", `$["a.b"]`},
	}
	for _, tt := range valid {
		got, err := parseJSONPath(tt.path)
		if err != nil {
			t.Errorf("parseJSONPath(%q) failed: %v", tt.path, err)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("parseJSONPath(%q) = %s, want %s", tt.path, got, tt.want)
		}
	}

	invalid := []string{"", "a", "$a", "$.", "$..a", "$.*", "$[*]", "$[-1]", "$[1", "$[]", "$['a'", "$['a'b]", "$['a','b']", "$[?(@.a)]"}
	for _, path := range invalid {
		if got, err := parseJSONPath(path); err == nil {
			t.Errorf("parseJSONPath(%q) = %s, want an error", path, got)
		}
	}
}

type streamedChatServer struct {
	mu       sync.Mutex
	stream   string
	requests []map[string]any
}

func (s *streamedChatServer) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var request map[string]any
	_ = json.Unmarshal(body, &request)
	s.mu.Lock()
	s.requests = append(s.requests, request)
	s.mu.Unlock()
	if strings.Contains(r.URL.Path, "streamGenerateContent") {
		fmt.Fprint(w, s.stream)
		return
	}
	fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`)
}

func sendStreamedChatTurn(t *testing.T, stream string) (*Chat, *streamedChatServer) {
	t.Helper()
	server := &streamedChatServer{stream: stream}
	client := newStreamedFunctionCallTestClient(t, server.handle)
	chat, err := client.Chats.Create(context.Background(), "gemini-3-pro", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range chat.SendMessageStream(context.Background(), Part{Text: "Compare the weather"}) {
		if err != nil {
			t.Fatalf("SendMessageStream failed: %v", err)
		}
	}
	return chat, server
}

func TestChatStoresStreamedFunctionCallTurn(t *testing.T) {
	chat, server := sendStreamedChatTurn(t, sseResponse(
		[]string{`{"functionCall":{"name":"get_weather","willContinue":true},"thoughtSignature":"c2ln"}`},
		[]string{functionCallPart(`{"partialArgs":[{"jsonPath":"$.location","stringValue":"New ","willContinue":true}],"willContinue":true}`)},
		[]string{functionCallPart(`{"partialArgs":[{"jsonPath":"$.location","stringValue":"Delhi"}],"willContinue":true}`)},
		[]string{functionCallPart(`{}`)},
		[]string{functionCallPart(`{"name":"get_weather","willContinue":true}`)},
		[]string{functionCallPart(`{"partialArgs":[{"jsonPath":"$.location","stringValue":"San Francisco"}],"willContinue":true}`)},
		[]string{functionCallPart(`{}`)},
	))

	wantTurn := &Content{
		Role: RoleModel,
		Parts: []*Part{
			{FunctionCall: &FunctionCall{Name: "get_weather", Args: map[string]any{"location": "New Delhi"}}, ThoughtSignature: []byte("sig")},
			{FunctionCall: &FunctionCall{Name: "get_weather", Args: map[string]any{"location": "San Francisco"}}},
		},
	}
	want := []*Content{{Role: RoleUser, Parts: []*Part{{Text: "Compare the weather"}}}, wantTurn}
	if diff := cmp.Diff(want, chat.History(true)); diff != "" {
		t.Errorf("curated history mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(want, chat.History(false)); diff != "" {
		t.Errorf("comprehensive history mismatch (-want +got):\n%s", diff)
	}

	_, err := chat.SendMessage(context.Background(),
		Part{FunctionResponse: &FunctionResponse{Name: "get_weather", Response: map[string]any{"temperature": 35}}},
		Part{FunctionResponse: &FunctionResponse{Name: "get_weather", Response: map[string]any{"temperature": 18}}},
	)
	if err != nil {
		t.Fatalf("SendMessage replaying the streamed turn failed: %v", err)
	}
	if len(server.requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(server.requests))
	}
	contents, _ := server.requests[1]["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("replayed request has %d contents, want 3: %v", len(contents), contents)
	}
	wantReplayed := map[string]any{
		"role": "model",
		"parts": []any{
			map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"location": "New Delhi"}}, "thoughtSignature": "c2ln"},
			map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"location": "San Francisco"}}},
		},
	}
	if diff := cmp.Diff(wantReplayed, contents[1]); diff != "" {
		t.Errorf("replayed model turn mismatch (-want +got):\n%s", diff)
	}
}

func TestChatStoresStreamedFunctionCallsInOrderOfFirstAppearance(t *testing.T) {
	chat, _ := sendStreamedChatTurn(t, sseResponse(functionCallChunks(
		`{"id":"a","name":"lookup","willContinue":true}`,
		`{"id":"b","name":"search","partialArgs":[{"jsonPath":"$.q","stringValue":"b"}],"willContinue":true}`,
		`{"id":"b","willContinue":false}`,
		`{"id":"a","partialArgs":[{"jsonPath":"$.q","stringValue":"a"}]}`,
		`{"id":"a","name":"lookup","partialArgs":[{"jsonPath":"$.q","stringValue":"again"}]}`,
	)...))

	want := &Content{
		Role: RoleModel,
		Parts: []*Part{
			{FunctionCall: &FunctionCall{ID: "a", Name: "lookup", Args: map[string]any{"q": "a"}}},
			{FunctionCall: &FunctionCall{ID: "b", Name: "search", Args: map[string]any{"q": "b"}}},
			{FunctionCall: &FunctionCall{ID: "a", Name: "lookup", Args: map[string]any{"q": "again"}}},
		},
	}
	history := chat.History(true)
	if len(history) != 2 {
		t.Fatalf("got %d curated history entries, want 2: %v", len(history), history)
	}
	if diff := cmp.Diff(want, history[1]); diff != "" {
		t.Errorf("stored model turn mismatch (-want +got):\n%s", diff)
	}
}

func TestLiveReceiveAccumulatesToolCallPartialArgs(t *testing.T) {
	ctx := context.Background()
	messages := []string{
		`{"toolCall":{"functionCalls":[{"id":"t1","name":"search","partialArgs":[{"jsonPath":"$.query","stringValue":"go ","willContinue":true}],"willContinue":true}]}}`,
		`{"toolCall":{"functionCalls":[{"id":"t1","partialArgs":[{"jsonPath":"$.query","stringValue":"genai"},{"jsonPath":"$.limit","numberValue":5}],"willContinue":true}]}}`,
		`{"toolCall":{"functionCalls":[{"id":"t1"}]}}`,
		`{"toolCall":{"functionCalls":[{"id":"t1","name":"search","partialArgs":[{"jsonPath":"$.query","stringValue":"fresh"}]}]}}`,
		`{"toolCall":{"functionCalls":[{"name":"search","partialArgs":[{"jsonPath":"$.query","stringValue":"x"},{"jsonPath":"$.query.deep","stringValue":"y"}]}]}}`,
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for _, message := range messages {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer ts.Close()

	client, err := NewClient(ctx, &ClientConfig{
		Backend:     BackendGeminiAPI,
		APIKey:      "test-api-key",
		HTTPOptions: HTTPOptions{BaseURL: strings.Replace(ts.URL, "http", "ws", 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Live.Connect(ctx, "test-model", &LiveConnectConfig{})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer session.Close()

	want := []map[string]any{
		{"query": "go "},
		{"query": "go genai", "limit": 5.0},
		{"query": "go genai", "limit": 5.0},
		{"query": "fresh"},
	}
	for i, wantArgs := range want {
		message, err := session.Receive()
		if err != nil {
			t.Fatalf("Receive %d failed: %v", i, err)
		}
		if diff := cmp.Diff(wantArgs, message.ToolCall.FunctionCalls[0].Args); diff != "" {
			t.Errorf("Receive %d: accumulated Args mismatch (-want +got):\n%s", i, diff)
		}
	}
	if _, err := session.Receive(); err == nil {
		t.Errorf("Receive of conflicting fragments succeeded, want an error")
	}
}
