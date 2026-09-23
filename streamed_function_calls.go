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
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"
)

// functionCallAccumulator rebuilds the arguments of function calls streamed as
// [PartialArg] fragments over a single streamed response. Each candidate is
// tracked independently.
type functionCallAccumulator struct {
	candidates map[int]*functionCallStream
}

func newFunctionCallAccumulator() *functionCallAccumulator {
	return &functionCallAccumulator{candidates: map[int]*functionCallStream{}}
}

// accumulateStream yields the responses of seq with the Args of every streamed
// function call set to the arguments accumulated so far for that call. It ends
// the stream with an error if fragments of a call conflict.
func (a *functionCallAccumulator) accumulateStream(seq iter.Seq2[*GenerateContentResponse, error]) iter.Seq2[*GenerateContentResponse, error] {
	return func(yield func(*GenerateContentResponse, error) bool) {
		for resp, err := range seq {
			if err == nil && resp != nil {
				if err := a.accumulateResponse(resp); err != nil {
					yield(nil, err)
					return
				}
			}
			if !yield(resp, err) {
				return
			}
		}
	}
}

func (a *functionCallAccumulator) accumulateResponse(resp *GenerateContentResponse) error {
	for i, candidate := range resp.Candidates {
		if candidate == nil || candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part == nil || part.FunctionCall == nil {
				continue
			}
			calls := a.candidates[i]
			if calls == nil {
				calls = &functionCallStream{recordCalls: true}
				a.candidates[i] = calls
			}
			if err := calls.accumulate(part.FunctionCall, part.ThoughtSignature); err != nil {
				return err
			}
		}
	}
	return nil
}

// completedTurn returns a model turn holding every distinct function call seen
// for the candidate, once each, in order of first appearance and with its final
// arguments. It returns nil if none of those calls had streamed arguments.
func (a *functionCallAccumulator) completedTurn(candidate int) *Content {
	calls := a.candidates[candidate]
	if calls == nil {
		return nil
	}
	streamed := false
	for _, call := range calls.calls {
		streamed = streamed || call.streamed
	}
	if !streamed {
		return nil
	}
	parts := make([]*Part, 0, len(calls.calls))
	for _, call := range calls.calls {
		parts = append(parts, &Part{
			FunctionCall:     &FunctionCall{ID: call.id, Name: call.name, Args: snapshotJSONObject(call.args)},
			ThoughtSignature: call.thoughtSignature,
		})
	}
	return &Content{Role: RoleModel, Parts: parts}
}

// functionCallStream tracks the function calls of one ordered sequence of
// function call messages, such as one response candidate or the tool calls of a
// live session.
type functionCallStream struct {
	// inProgress holds the calls whose latest message had willContinue=true,
	// least recently updated first.
	inProgress []*streamedFunctionCall
	// recordCalls keeps every distinct call in calls, in order of first
	// appearance. Live sessions leave it unset so long sessions stay bounded.
	recordCalls bool
	calls       []*streamedFunctionCall
}

type streamedFunctionCall struct {
	id       string
	name     string
	args     map[string]any
	streamed bool
	// continuedStrings holds the canonical paths whose latest fragment was a
	// string with willContinue=true.
	continuedStrings map[string]bool
	thoughtSignature []byte
}

func (s *functionCallStream) accumulateToolCall(toolCall *LiveServerToolCall) error {
	if toolCall == nil {
		return nil
	}
	for _, fc := range toolCall.FunctionCalls {
		if fc == nil {
			continue
		}
		if err := s.accumulate(fc, nil); err != nil {
			return err
		}
	}
	return nil
}

// accumulate folds fc into the call it belongs to and sets fc.Args to that
// call's accumulated arguments. Complete calls that are not part of a stream are
// left untouched.
func (s *functionCallStream) accumulate(fc *FunctionCall, thoughtSignature []byte) error {
	call := s.lookup(fc)
	if call == nil {
		if len(fc.PartialArgs) == 0 && fc.WillContinue == nil {
			return s.recordCompleteCall(fc, thoughtSignature)
		}
		call = &streamedFunctionCall{id: fc.ID, name: fc.Name, args: map[string]any{}, streamed: true}
		if s.recordCalls {
			s.calls = append(s.calls, call)
		}
	} else {
		s.removeInProgress(call)
	}
	if call.id == "" {
		call.id = fc.ID
	}
	if call.name == "" {
		call.name = fc.Name
	}
	if len(call.thoughtSignature) == 0 {
		call.thoughtSignature = thoughtSignature
	}
	if err := call.apply(fc); err != nil {
		return err
	}
	fc.Args = snapshotJSONObject(call.args)
	if fc.WillContinue != nil && *fc.WillContinue {
		s.inProgress = append(s.inProgress, call)
	}
	return nil
}

// lookup returns the in-progress call that fc continues, or nil if fc starts a
// new call. Continuation messages usually omit the id and name, in which case
// they belong to the most recently updated call.
func (s *functionCallStream) lookup(fc *FunctionCall) *streamedFunctionCall {
	if len(s.inProgress) == 0 {
		return nil
	}
	if fc.ID != "" {
		for i := len(s.inProgress) - 1; i >= 0; i-- {
			if s.inProgress[i].id == fc.ID {
				return s.inProgress[i]
			}
		}
	}
	latest := s.inProgress[len(s.inProgress)-1]
	if fc.ID != "" && latest.id != "" {
		return nil
	}
	if fc.Name != "" && latest.name != "" && fc.Name != latest.name {
		return nil
	}
	return latest
}

func (s *functionCallStream) removeInProgress(call *streamedFunctionCall) {
	for i, c := range s.inProgress {
		if c == call {
			s.inProgress = append(s.inProgress[:i], s.inProgress[i+1:]...)
			return
		}
	}
}

func (s *functionCallStream) recordCompleteCall(fc *FunctionCall, thoughtSignature []byte) error {
	if !s.recordCalls || (fc.ID == "" && fc.Name == "" && len(fc.Args) == 0) {
		return nil
	}
	call := &streamedFunctionCall{id: fc.ID, name: fc.Name, args: map[string]any{}, thoughtSignature: thoughtSignature}
	if err := call.apply(fc); err != nil {
		return err
	}
	s.calls = append(s.calls, call)
	return nil
}

// apply merges the args of fc into the call, then its partial arguments in
// order.
func (c *streamedFunctionCall) apply(fc *FunctionCall) error {
	if len(fc.Args) > 0 {
		if _, err := mergeJSON(c.args, fc.Args, nil); err != nil {
			return fmt.Errorf("streamed function call %s: args conflict with previously streamed arguments: %w", c.label(), err)
		}
	}
	for _, arg := range fc.PartialArgs {
		if arg == nil {
			continue
		}
		if err := c.applyPartialArg(arg); err != nil {
			return fmt.Errorf("streamed function call %s: partial argument %q: %w", c.label(), arg.JsonPath, err)
		}
	}
	return nil
}

func (c *streamedFunctionCall) applyPartialArg(arg *PartialArg) error {
	path, err := parseJSONPath(arg.JsonPath)
	if err != nil {
		return err
	}
	key := path.String()
	value := partialArgValue(arg)
	continued := c.continuedStrings[key]
	if _, isString := value.(string); continued && !isString {
		return fmt.Errorf("%s continues a streamed string, which cannot be extended with %s", key, jsonKind(value))
	}
	if _, err := assignJSONPath(c.args, path, 0, value, continued); err != nil {
		return err
	}
	if _, isString := value.(string); isString && arg.WillContinue != nil && *arg.WillContinue {
		if c.continuedStrings == nil {
			c.continuedStrings = map[string]bool{}
		}
		c.continuedStrings[key] = true
	} else {
		delete(c.continuedStrings, key)
	}
	return nil
}

func (c *streamedFunctionCall) label() string {
	if c.id != "" {
		return fmt.Sprintf("%q (id %q)", c.name, c.id)
	}
	return strconv.Quote(c.name)
}

func partialArgValue(arg *PartialArg) any {
	switch {
	case arg.StringValue != "":
		return arg.StringValue
	case arg.BoolValue != nil:
		return *arg.BoolValue
	case arg.NumberValue != nil:
		return *arg.NumberValue
	case arg.NULLValue != "":
		return nil
	default:
		return ""
	}
}

// UnmarshalJSON keeps a streamed JSON null, which the API encodes as
// "nullValue": null, distinguishable from an empty string fragment.
func (p *PartialArg) UnmarshalJSON(data []byte) error {
	type alias PartialArg
	aux := struct {
		NULLValue json.RawMessage `json:"nullValue,omitempty"`
		*alias
	}{alias: (*alias)(p)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	switch {
	case aux.NULLValue == nil:
	case string(aux.NULLValue) == "null":
		p.NULLValue = "NULL_VALUE"
	default:
		return json.Unmarshal(aux.NULLValue, &p.NULLValue)
	}
	return nil
}

// absentJSONValue marks a missing object member or an array element that has
// not been streamed yet. It never escapes a snapshot.
type absentJSONValue struct{}

// assignJSONPath stores value at path[i:] below node and returns the updated
// node. Missing containers are created; existing values of another shape are
// reported as errors rather than overwritten.
func assignJSONPath(node any, path jsonPath, i int, value any, appendString bool) (any, error) {
	if i == len(path) {
		if appendString {
			existing, ok := node.(string)
			if !ok {
				return nil, fmt.Errorf("%s holds %s, which cannot be extended with a continued string", path, jsonKind(node))
			}
			return existing + value.(string), nil
		}
		switch node.(type) {
		case map[string]any, []any:
			return nil, fmt.Errorf("%s holds %s, which cannot be replaced by %s", path, jsonKind(node), jsonKind(value))
		}
		return value, nil
	}
	segment := path[i]
	if segment.isIndex {
		arr, ok := node.([]any)
		if _, absent := node.(absentJSONValue); !ok && !absent {
			return nil, jsonShapeConflict(path[:i], "an array", node)
		}
		for len(arr) <= segment.index {
			arr = append(arr, absentJSONValue{})
		}
		child, err := assignJSONPath(arr[segment.index], path, i+1, value, appendString)
		if err != nil {
			return nil, err
		}
		arr[segment.index] = child
		return arr, nil
	}
	obj, ok := node.(map[string]any)
	if _, absent := node.(absentJSONValue); !ok && !absent {
		return nil, jsonShapeConflict(path[:i], "an object", node)
	}
	if obj == nil {
		obj = map[string]any{}
	}
	existing, exists := obj[segment.key]
	if !exists {
		existing = absentJSONValue{}
	}
	child, err := assignJSONPath(existing, path, i+1, value, appendString)
	if err != nil {
		return nil, err
	}
	obj[segment.key] = child
	return obj, nil
}

// mergeJSON deep-merges incoming into existing and returns the result.
func mergeJSON(existing, incoming any, path jsonPath) (any, error) {
	switch incoming := incoming.(type) {
	case map[string]any:
		obj, ok := existing.(map[string]any)
		if _, absent := existing.(absentJSONValue); !ok && !absent {
			return nil, jsonShapeConflict(path, "an object", existing)
		}
		if obj == nil {
			obj = map[string]any{}
		}
		for key, value := range incoming {
			current, exists := obj[key]
			if !exists {
				current = absentJSONValue{}
			}
			merged, err := mergeJSON(current, value, append(path, jsonPathSegment{key: key}))
			if err != nil {
				return nil, err
			}
			obj[key] = merged
		}
		return obj, nil
	case []any:
		arr, ok := existing.([]any)
		if _, absent := existing.(absentJSONValue); !ok && !absent {
			return nil, jsonShapeConflict(path, "an array", existing)
		}
		for i, value := range incoming {
			for len(arr) <= i {
				arr = append(arr, absentJSONValue{})
			}
			merged, err := mergeJSON(arr[i], value, append(path, jsonPathSegment{index: i, isIndex: true}))
			if err != nil {
				return nil, err
			}
			arr[i] = merged
		}
		return arr, nil
	default:
		switch existing.(type) {
		case map[string]any, []any:
			return nil, fmt.Errorf("%s holds %s, which cannot be replaced by %s", path, jsonKind(existing), jsonKind(incoming))
		}
		return incoming, nil
	}
}

func jsonShapeConflict(at jsonPath, want string, found any) error {
	return fmt.Errorf("%s holds %s where %s is required", at, jsonKind(found), want)
}

func jsonKind(v any) string {
	switch v.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	case absentJSONValue:
		return "no value"
	case float64, float32, int, int32, int64, json.Number:
		return "a number"
	default:
		return fmt.Sprintf("a %T", v)
	}
}

// snapshotJSONObject deep-copies accumulated arguments so that values handed to
// callers do not change as later fragments arrive.
func snapshotJSONObject(obj map[string]any) map[string]any {
	return snapshotJSON(obj).(map[string]any)
}

func snapshotJSON(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = snapshotJSON(value)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = snapshotJSON(value)
		}
		return out
	case absentJSONValue:
		return nil
	default:
		return v
	}
}

type jsonPathSegment struct {
	key     string
	index   int
	isIndex bool
}

// jsonPath is a parsed streamed argument path. The root `$` is the arguments
// object itself.
type jsonPath []jsonPathSegment

// String returns the canonical form of p, so equivalent spellings such as
// `$.a` and `$['a']` compare equal.
func (p jsonPath) String() string {
	var b strings.Builder
	b.WriteByte('$')
	for _, segment := range p {
		switch {
		case segment.isIndex:
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(segment.index))
			b.WriteByte(']')
		case isJSONPathShorthandName(segment.key):
			b.WriteByte('.')
			b.WriteString(segment.key)
		default:
			b.WriteByte('[')
			b.WriteString(strconv.Quote(segment.key))
			b.WriteByte(']')
		}
	}
	return b.String()
}

func isJSONPathShorthandName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_', 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z':
		case '0' <= r && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// parseJSONPath parses the subset of RFC 9535 used for streamed arguments: the
// root `$`, dot-separated member names, bracket-quoted member names and
// zero-based array indexes.
func parseJSONPath(path string) (jsonPath, error) {
	if !strings.HasPrefix(path, "$") {
		return nil, fmt.Errorf("unsupported JSON path %q: it must start with $", path)
	}
	var segments jsonPath
	for rest := path[1:]; rest != ""; {
		var segment jsonPathSegment
		var n int
		var err error
		switch rest[0] {
		case '.':
			segment, n, err = parseJSONPathDotName(rest)
		case '[':
			segment, n, err = parseJSONPathBracket(rest)
		default:
			err = fmt.Errorf("unexpected %q", rest[0])
		}
		if err != nil {
			return nil, fmt.Errorf("unsupported JSON path %q: %w", path, err)
		}
		segments = append(segments, segment)
		rest = rest[n:]
	}
	return segments, nil
}

func parseJSONPathDotName(rest string) (jsonPathSegment, int, error) {
	end := strings.IndexAny(rest[1:], ".[")
	if end < 0 {
		end = len(rest) - 1
	}
	name := rest[1 : end+1]
	if name == "" || name == "*" {
		return jsonPathSegment{}, 0, errors.New("expected a member name after .")
	}
	return jsonPathSegment{key: name}, end + 1, nil
}

func parseJSONPathBracket(rest string) (jsonPathSegment, int, error) {
	if len(rest) > 1 && (rest[1] == '\'' || rest[1] == '"') {
		return parseJSONPathQuotedName(rest)
	}
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return jsonPathSegment{}, 0, errors.New("missing ]")
	}
	digits := rest[1:end]
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return jsonPathSegment{}, 0, fmt.Errorf("[%s] is not a zero-based array index", digits)
	}
	index, err := strconv.Atoi(digits)
	if err != nil {
		return jsonPathSegment{}, 0, fmt.Errorf("[%s] is not a zero-based array index", digits)
	}
	return jsonPathSegment{index: index, isIndex: true}, end + 1, nil
}

// parseJSONPathQuotedName parses a bracket-quoted member name. RFC 9535 quoted
// names use JSON string escapes plus \', so the name is decoded as a JSON
// string.
func parseJSONPathQuotedName(rest string) (jsonPathSegment, int, error) {
	quote := rest[1]
	var literal strings.Builder
	literal.WriteByte('"')
	for i := 2; i < len(rest); i++ {
		switch c := rest[i]; {
		case c == quote:
			if i+1 >= len(rest) || rest[i+1] != ']' {
				return jsonPathSegment{}, 0, errors.New("expected ] after quoted member name")
			}
			literal.WriteByte('"')
			var name string
			if err := json.Unmarshal([]byte(literal.String()), &name); err != nil {
				return jsonPathSegment{}, 0, fmt.Errorf("invalid quoted member name: %w", err)
			}
			return jsonPathSegment{key: name}, i + 2, nil
		case c == '\\' && i+1 < len(rest):
			i++
			if rest[i] == '\'' {
				literal.WriteByte('\'')
			} else {
				literal.WriteByte('\\')
				literal.WriteByte(rest[i])
			}
		case c == '"':
			literal.WriteString(`\"`)
		default:
			literal.WriteByte(c)
		}
	}
	return jsonPathSegment{}, 0, errors.New("unterminated quoted member name")
}
