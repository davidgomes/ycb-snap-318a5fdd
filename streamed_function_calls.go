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
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

const maxStreamedArrayIndex = 10000

// streamedNull is an explicit JSON null stored while arguments are accumulated.
// Missing array slots use streamedMissing so a later fragment can fill them.
type streamedNull struct{}

type streamedMissing struct{}

type streamedCallState struct {
	args       map[string]any
	continuing map[string]bool
}

type streamedFunctionCallAccumulator struct {
	calls map[string]*streamedCallState
}

func newStreamedFunctionCallAccumulator() *streamedFunctionCallAccumulator {
	return &streamedFunctionCallAccumulator{calls: map[string]*streamedCallState{}}
}

func (a *streamedFunctionCallAccumulator) applyGenerateContentResponse(response *GenerateContentResponse) error {
	if response == nil {
		return nil
	}
	for _, candidate := range response.Candidates {
		if candidate == nil || candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part == nil || part.FunctionCall == nil {
				continue
			}
			if err := a.applyFunctionCall(part.FunctionCall); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *streamedFunctionCallAccumulator) applyLiveServerMessage(message *LiveServerMessage) error {
	if message == nil || message.ToolCall == nil {
		return nil
	}
	for _, call := range message.ToolCall.FunctionCalls {
		if err := a.applyFunctionCall(call); err != nil {
			return err
		}
	}
	return nil
}

func (a *streamedFunctionCallAccumulator) applyFunctionCall(call *FunctionCall) error {
	if call == nil {
		return nil
	}
	key := streamedFunctionCallKey(call)
	_, hasState := a.calls[key]
	hasPartials := false
	for _, partial := range call.PartialArgs {
		if partial != nil {
			hasPartials = true
			break
		}
	}
	if !hasState && !hasPartials {
		if functionCallWillContinue(call) && len(call.Args) > 0 {
			a.calls[key] = &streamedCallState{
				args:       importJSONMap(call.Args),
				continuing: map[string]bool{},
			}
		}
		return nil
	}

	state := &streamedCallState{
		args:       map[string]any{},
		continuing: map[string]bool{},
	}
	if prev := a.calls[key]; prev != nil {
		state.args = cloneInternalMap(prev.args)
		state.continuing = cloneContinuing(prev.continuing)
	}
	if len(call.Args) > 0 {
		merged, err := mergeExternalArgs(state.args, call.Args)
		if err != nil {
			return err
		}
		state.args = merged
	}
	for _, partial := range call.PartialArgs {
		if partial == nil {
			continue
		}
		if err := state.applyPartial(partial); err != nil {
			return err
		}
	}
	call.Args = exportJSONMap(state.args)
	if functionCallWillContinue(call) {
		a.calls[key] = state
	} else {
		delete(a.calls, key)
	}
	return nil
}

func (s *streamedCallState) applyPartial(partial *PartialArg) error {
	if partial.JsonPath == "" {
		return fmt.Errorf("streamed function call json path is required")
	}
	tokens, err := parseStreamedJSONPath(partial.JsonPath)
	if err != nil {
		return err
	}
	value, err := streamedPartialArgValue(partial)
	if err != nil {
		return err
	}
	canonical := canonicalStreamedPath(tokens)
	updated, err := setStreamedJSONPathValue(s.args, tokens, value, s.continuing[canonical])
	if err != nil {
		return fmt.Errorf("streamed function call arguments conflict at %s: incompatible shapes", partial.JsonPath)
	}
	args, ok := updated.(map[string]any)
	if !ok {
		return fmt.Errorf("streamed function call arguments conflict at %s: incompatible shapes", partial.JsonPath)
	}
	s.args = args
	if _, isString := value.(string); isString && partialWillContinue(partial) {
		s.continuing[canonical] = true
		return nil
	}
	delete(s.continuing, canonical)
	return nil
}

func streamedPartialArgValue(partial *PartialArg) (any, error) {
	var values []any
	if partial.BoolValue != nil {
		values = append(values, *partial.BoolValue)
	}
	if partial.NumberValue != nil {
		values = append(values, *partial.NumberValue)
	}
	if partial.StringValue != "" {
		values = append(values, partial.StringValue)
	}
	if partial.NULLValue != "" {
		values = append(values, streamedNull{})
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("streamed function call partial arg must contain exactly one value")
	}
	return values[0], nil
}

func functionCallWillContinue(call *FunctionCall) bool {
	return call != nil && call.WillContinue != nil && *call.WillContinue
}

func partialWillContinue(partial *PartialArg) bool {
	return partial != nil && partial.WillContinue != nil && *partial.WillContinue
}

func streamedFunctionCallKey(call *FunctionCall) string {
	if call.ID != "" {
		return "id:" + call.ID
	}
	if call.Name != "" {
		return "name:" + call.Name
	}
	return "anonymous"
}

// UnmarshalJSON accepts protobuf JSON nullValue, which is either null or the
// NULL_VALUE enum string, and stores a non-empty sentinel so accumulation can
// tell an explicit null apart from an omitted field.
func (p *PartialArg) UnmarshalJSON(data []byte) error {
	type wire struct {
		BoolValue    *bool           `json:"boolValue,omitempty"`
		JsonPath     string          `json:"jsonPath,omitempty"`
		NULLValue    json.RawMessage `json:"nullValue,omitempty"`
		NumberValue  *float64        `json:"numberValue,omitempty"`
		StringValue  string          `json:"stringValue,omitempty"`
		WillContinue *bool           `json:"willContinue,omitempty"`
	}
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	p.BoolValue = decoded.BoolValue
	p.JsonPath = decoded.JsonPath
	p.NumberValue = decoded.NumberValue
	p.StringValue = decoded.StringValue
	p.WillContinue = decoded.WillContinue
	p.NULLValue = ""
	if len(decoded.NULLValue) == 0 || string(decoded.NULLValue) == "null" {
		if string(decoded.NULLValue) == "null" {
			p.NULLValue = "NULL_VALUE"
		}
		return nil
	}
	var asString string
	if err := json.Unmarshal(decoded.NULLValue, &asString); err != nil {
		return fmt.Errorf("invalid nullValue: %w", err)
	}
	p.NULLValue = asString
	return nil
}

func mergeStreamedFunctionCallContents(contents []*Content) []*Content {
	if !turnIsEntirelyFunctionCalls(contents) {
		return contents
	}
	type openSlot struct {
		index  int
		first  *Part
		latest *Part
	}
	var slots []*Part
	open := map[string]*openSlot{}
	role := ""
	for _, content := range contents {
		if content == nil {
			continue
		}
		if role == "" && content.Role != "" {
			role = content.Role
		}
		for _, part := range content.Parts {
			if part == nil || part.FunctionCall == nil {
				continue
			}
			key := streamedFunctionCallKey(part.FunctionCall)
			slot := open[key]
			if slot == nil {
				slot = &openSlot{index: len(slots), first: part, latest: part}
				slots = append(slots, nil)
				open[key] = slot
			} else {
				slot.latest = part
			}
			if !functionCallWillContinue(part.FunctionCall) {
				slots[slot.index] = finalizeStreamedHistoryPart(slot.first, slot.latest)
				delete(open, key)
			}
		}
	}
	var parts []*Part
	for _, slot := range slots {
		if slot != nil {
			parts = append(parts, slot)
		}
	}
	if len(parts) == 0 {
		return contents
	}
	if role == "" {
		role = RoleModel
	}
	return []*Content{{Role: role, Parts: parts}}
}

func turnIsEntirelyFunctionCalls(contents []*Content) bool {
	sawCall := false
	for _, content := range contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if !partIsOnlyFunctionCall(part) {
				return false
			}
			sawCall = true
		}
	}
	return sawCall
}

func partIsOnlyFunctionCall(part *Part) bool {
	if part == nil || part.FunctionCall == nil {
		return false
	}
	return part.Text == "" &&
		part.InlineData == nil &&
		part.FileData == nil &&
		part.FunctionResponse == nil &&
		part.ExecutableCode == nil &&
		part.CodeExecutionResult == nil &&
		part.ToolCall == nil &&
		part.ToolResponse == nil
}

func finalizeStreamedHistoryPart(first, latest *Part) *Part {
	src := latest
	if src == nil || src.FunctionCall == nil {
		src = first
	}
	call := src.FunctionCall
	outCall := &FunctionCall{
		ID:   call.ID,
		Name: call.Name,
		Args: cloneExportedArgs(call.Args),
	}
	if first != nil && first.FunctionCall != nil {
		if outCall.ID == "" {
			outCall.ID = first.FunctionCall.ID
		}
		if outCall.Name == "" {
			outCall.Name = first.FunctionCall.Name
		}
	}
	out := &Part{FunctionCall: outCall}
	signature := src.ThoughtSignature
	if len(signature) == 0 && first != nil {
		signature = first.ThoughtSignature
	}
	if len(signature) > 0 {
		out.ThoughtSignature = append([]byte(nil), signature...)
	}
	if src.Thought || (first != nil && first.Thought) {
		out.Thought = true
	}
	return out
}

func cloneExportedArgs(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	cloned, _ := cloneExportedJSON(in).(map[string]any)
	return cloned
}

func cloneExportedJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = cloneExportedJSON(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneExportedJSON(item)
		}
		return out
	default:
		return value
	}
}

func importJSONMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = importJSONValue(value)
	}
	return out
}

func importJSONValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return streamedNull{}
	case map[string]any:
		return importJSONMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = importJSONValue(item)
		}
		return out
	default:
		return value
	}
}

func exportJSONMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = exportJSONValue(value)
	}
	return out
}

func exportJSONValue(value any) any {
	switch typed := value.(type) {
	case streamedNull, streamedMissing:
		return nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = exportJSONValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = exportJSONValue(item)
		}
		return out
	default:
		return value
	}
}

func cloneInternalMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneInternalValue(value)
	}
	return out
}

func cloneInternalValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneInternalMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneInternalValue(item)
		}
		return out
	default:
		return value
	}
}

func cloneContinuing(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mergeExternalArgs(dst map[string]any, src map[string]any) (map[string]any, error) {
	if dst == nil {
		dst = map[string]any{}
	}
	if len(src) == 0 {
		return dst, nil
	}
	merged, err := mergeJSONValues(dst, importJSONMap(src))
	if err != nil {
		return nil, err
	}
	args, ok := merged.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("streamed function call arguments conflict: incompatible shapes")
	}
	return args, nil
}

func mergeJSONValues(dst, src any) (any, error) {
	if isStreamedMissing(dst) {
		return src, nil
	}
	dstMap, dstIsMap := dst.(map[string]any)
	srcMap, srcIsMap := src.(map[string]any)
	if dstIsMap && srcIsMap {
		for key, srcValue := range srcMap {
			dstValue, ok := dstMap[key]
			if !ok {
				dstValue = streamedMissing{}
			}
			merged, err := mergeJSONValues(dstValue, srcValue)
			if err != nil {
				return nil, err
			}
			dstMap[key] = merged
		}
		return dstMap, nil
	}
	dstSlice, dstIsSlice := dst.([]any)
	srcSlice, srcIsSlice := src.([]any)
	if dstIsSlice && srcIsSlice {
		if len(srcSlice) > len(dstSlice) {
			dstSlice = growStreamedArray(dstSlice, len(srcSlice))
		}
		for i := range srcSlice {
			merged, err := mergeJSONValues(dstSlice[i], srcSlice[i])
			if err != nil {
				return nil, err
			}
			dstSlice[i] = merged
		}
		return dstSlice, nil
	}
	if reflect.DeepEqual(dst, src) {
		return dst, nil
	}
	return nil, fmt.Errorf("streamed function call arguments conflict: incompatible shapes")
}

func isStreamedMissing(value any) bool {
	_, ok := value.(streamedMissing)
	return ok || value == nil
}

func setStreamedJSONPathValue(current any, tokens []any, value any, appendString bool) (any, error) {
	if len(tokens) == 0 {
		merged, err := mergeStreamedJSONLeaf(current, value, appendString)
		if err != nil {
			return nil, err
		}
		return merged, nil
	}
	token := tokens[0]
	last := len(tokens) == 1
	switch typed := token.(type) {
	case string:
		container, ok := current.(map[string]any)
		if isStreamedMissing(current) {
			container = map[string]any{}
		} else if !ok {
			return nil, fmt.Errorf("incompatible shapes")
		}
		existing, found := container[typed]
		if !found {
			existing = streamedMissing{}
		}
		if last {
			merged, err := mergeStreamedJSONLeaf(existing, value, appendString)
			if err != nil {
				return nil, err
			}
			container[typed] = merged
			return container, nil
		}
		if isStreamedMissing(existing) {
			existing = newStreamedContainer(tokens[1])
		}
		updated, err := setStreamedJSONPathValue(existing, tokens[1:], value, appendString)
		if err != nil {
			return nil, err
		}
		container[typed] = updated
		return container, nil
	case int:
		if typed < 0 || typed > maxStreamedArrayIndex {
			return nil, fmt.Errorf("streamed function call json path has invalid array index %d", typed)
		}
		container, ok := current.([]any)
		if isStreamedMissing(current) {
			container = []any{}
		} else if !ok {
			return nil, fmt.Errorf("incompatible shapes")
		}
		container = growStreamedArray(container, typed+1)
		if last {
			merged, err := mergeStreamedJSONLeaf(container[typed], value, appendString)
			if err != nil {
				return nil, err
			}
			container[typed] = merged
			return container, nil
		}
		existing := container[typed]
		if isStreamedMissing(existing) {
			existing = newStreamedContainer(tokens[1])
		}
		updated, err := setStreamedJSONPathValue(existing, tokens[1:], value, appendString)
		if err != nil {
			return nil, err
		}
		container[typed] = updated
		return container, nil
	default:
		return nil, fmt.Errorf("streamed function call json path has unsupported token")
	}
}

func mergeStreamedJSONLeaf(current, value any, appendString bool) (any, error) {
	if isStreamedMissing(current) {
		return value, nil
	}
	if appendString {
		currentString, currentIsString := current.(string)
		valueString, valueIsString := value.(string)
		if currentIsString && valueIsString {
			return currentString + valueString, nil
		}
		return nil, fmt.Errorf("incompatible shapes")
	}
	if reflect.DeepEqual(current, value) {
		return current, nil
	}
	return nil, fmt.Errorf("incompatible shapes")
}

func newStreamedContainer(next any) any {
	if index, ok := next.(int); ok {
		if index < 0 || index > maxStreamedArrayIndex {
			return []any{}
		}
		return growStreamedArray(nil, index+1)
	}
	return map[string]any{}
}

func growStreamedArray(in []any, size int) []any {
	if size <= len(in) {
		return in
	}
	out := make([]any, size)
	copy(out, in)
	for i := len(in); i < size; i++ {
		out[i] = streamedMissing{}
	}
	return out
}

func parseStreamedJSONPath(path string) ([]any, error) {
	if path == "" || path[0] != '$' {
		return nil, fmt.Errorf("streamed function call json path %q must start with $", path)
	}
	var tokens []any
	for i := 1; i < len(path); {
		switch path[i] {
		case '.':
			i++
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("streamed function call json path %q has empty field", path)
			}
			tokens = append(tokens, path[start:i])
		case '[':
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("streamed function call json path %q has unterminated bracket", path)
			}
			body := strings.TrimSpace(path[i+1 : i+end])
			if body == "" {
				return nil, fmt.Errorf("streamed function call json path %q has empty bracket", path)
			}
			if body[0] == '\'' || body[0] == '"' {
				quote := body[0]
				if len(body) < 2 || body[len(body)-1] != quote {
					return nil, fmt.Errorf("streamed function call json path %q has unterminated quoted key", path)
				}
				unquoted, err := unescapeJSONPathString(body[1 : len(body)-1])
				if err != nil {
					return nil, fmt.Errorf("streamed function call json path %q has invalid quoted key: %w", path, err)
				}
				tokens = append(tokens, unquoted)
			} else {
				index, err := strconv.Atoi(body)
				if err != nil || index < 0 {
					return nil, fmt.Errorf("streamed function call json path %q has invalid array index %q", path, body)
				}
				tokens = append(tokens, index)
			}
			i += end + 1
		default:
			return nil, fmt.Errorf("streamed function call json path %q has unsupported token", path)
		}
	}
	return tokens, nil
}

func unescapeJSONPathString(value string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			b.WriteByte(value[i])
			continue
		}
		if i+1 >= len(value) {
			return "", fmt.Errorf("truncated escape")
		}
		i++
		switch value[i] {
		case '\\', '\'', '"', '/':
			b.WriteByte(value[i])
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			b.WriteByte(value[i])
		}
	}
	return b.String(), nil
}

func canonicalStreamedPath(tokens []any) string {
	var b strings.Builder
	b.WriteByte('$')
	for _, token := range tokens {
		switch typed := token.(type) {
		case string:
			if isStreamedIdent(typed) {
				b.WriteByte('.')
				b.WriteString(typed)
				continue
			}
			b.WriteString("['")
			b.WriteString(strings.ReplaceAll(typed, "'", "\\'"))
			b.WriteString("']")
		case int:
			fmt.Fprintf(&b, "[%d]", typed)
		}
	}
	return b.String()
}

func isStreamedIdent(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}
