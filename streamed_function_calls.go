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
	"fmt"
	"strconv"
	"strings"
)

type streamedJSONPathToken struct {
	field   string
	index   int
	isIndex bool
}

type streamedFunctionCallState struct {
	args             map[string]any
	pathContinuation map[string]bool
}

type streamedFunctionCallAccumulator struct {
	calls map[string]*streamedFunctionCallState
}

func (a *streamedFunctionCallAccumulator) addFunctionCall(call *FunctionCall) error {
	if call == nil {
		return nil
	}
	if a.calls == nil {
		a.calls = make(map[string]*streamedFunctionCallState)
	}

	key := streamedFunctionCallKey(call)
	state, ok := a.calls[key]
	if !ok {
		state = &streamedFunctionCallState{
			args:             cloneJSONMap(call.Args),
			pathContinuation: make(map[string]bool),
		}
		if state.args == nil {
			state.args = make(map[string]any)
		}
		a.calls[key] = state
	} else if call.Args != nil {
		if err := mergeStreamedJSONMap(state.args, call.Args, "$"); err != nil {
			return fmt.Errorf("accumulate function call %q arguments: %w", key, err)
		}
	}

	for _, partial := range call.PartialArgs {
		if err := state.addPartialArg(partial); err != nil {
			return fmt.Errorf("accumulate function call %q arguments: %w", key, err)
		}
	}

	call.Args = cloneJSONMap(state.args)
	if !streamedWillContinue(call.WillContinue) {
		delete(a.calls, key)
	}
	return nil
}

func (s *streamedFunctionCallState) addPartialArg(partial *PartialArg) error {
	if partial == nil {
		return nil
	}
	tokens, err := parseStreamedJSONPath(partial.JsonPath)
	if err != nil {
		return err
	}
	value := streamedPartialArgValue(partial)
	pathKey := streamedJSONPathKey(tokens)
	if s.pathContinuation[pathKey] {
		previous, exists := streamedJSONValueAtPath(s.args, tokens)
		if !exists {
			return fmt.Errorf("continued path %q has no previous value", partial.JsonPath)
		}
		previousString, previousOK := previous.(string)
		valueString, valueOK := value.(string)
		if !previousOK || !valueOK {
			if value == nil {
				// A null fragment replaces the value rather than appending to it.
			} else {
				return fmt.Errorf("cannot append non-string value at path %q", partial.JsonPath)
			}
		} else {
			value = previousString + valueString
		}
	}

	if len(tokens) == 0 {
		root, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("path %q must address a JSON object", partial.JsonPath)
		}
		if err := mergeStreamedJSONMap(s.args, root, "$"); err != nil {
			return err
		}
	} else if err := setStreamedJSONValue(s.args, tokens, value); err != nil {
		return err
	}
	s.pathContinuation[pathKey] = streamedWillContinue(partial.WillContinue)
	return nil
}

func streamedPartialArgValue(partial *PartialArg) any {
	switch {
	case partial.BoolValue != nil:
		return *partial.BoolValue
	case partial.NumberValue != nil:
		return *partial.NumberValue
	case partial.NULLValue != "":
		return nil
	default:
		return partial.StringValue
	}
}

func streamedWillContinue(value *bool) bool {
	return value != nil && *value
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

func parseStreamedJSONPath(path string) ([]streamedJSONPathToken, error) {
	if path == "$" {
		return nil, nil
	}
	if path == "" || path[0] != '$' {
		return nil, fmt.Errorf("invalid JSON path %q", path)
	}

	var tokens []streamedJSONPathToken
	for i := 1; i < len(path); {
		switch path[i] {
		case '.':
			start := i + 1
			i = start
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			if start == i || strings.ContainsRune(path[start:i], ']') {
				return nil, fmt.Errorf("invalid JSON path %q", path)
			}
			tokens = append(tokens, streamedJSONPathToken{field: path[start:i]})
		case '[':
			i++
			if i >= len(path) {
				return nil, fmt.Errorf("invalid JSON path %q", path)
			}
			if path[i] == '\'' || path[i] == '"' {
				quote := path[i]
				i++
				var name strings.Builder
				closed := false
				for i < len(path) {
					if path[i] == '\\' {
						if i+1 >= len(path) {
							return nil, fmt.Errorf("invalid JSON path %q", path)
						}
						name.WriteByte(path[i+1])
						i += 2
						continue
					}
					if path[i] == quote {
						closed = true
						i++
						break
					}
					name.WriteByte(path[i])
					i++
				}
				if !closed || i >= len(path) || path[i] != ']' {
					return nil, fmt.Errorf("invalid JSON path %q", path)
				}
				i++
				tokens = append(tokens, streamedJSONPathToken{field: name.String()})
				continue
			}

			start := i
			for i < len(path) && path[i] >= '0' && path[i] <= '9' {
				i++
			}
			if start == i || i >= len(path) || path[i] != ']' {
				return nil, fmt.Errorf("invalid JSON path %q", path)
			}
			index, err := strconv.Atoi(path[start:i])
			if err != nil {
				return nil, fmt.Errorf("invalid JSON path %q: %w", path, err)
			}
			i++
			tokens = append(tokens, streamedJSONPathToken{index: index, isIndex: true})
		default:
			return nil, fmt.Errorf("invalid JSON path %q", path)
		}
	}
	return tokens, nil
}

func streamedJSONPathKey(tokens []streamedJSONPathToken) string {
	var result strings.Builder
	for _, token := range tokens {
		if token.isIndex {
			result.WriteString("[")
			result.WriteString(strconv.Itoa(token.index))
			result.WriteString("]")
		} else {
			result.WriteString("[")
			result.WriteString(strconv.Quote(token.field))
			result.WriteString("]")
		}
	}
	return result.String()
}

func streamedJSONValueAtPath(root map[string]any, tokens []streamedJSONPathToken) (any, bool) {
	var current any = root
	for _, token := range tokens {
		if token.isIndex {
			values, ok := current.([]any)
			if !ok || token.index >= len(values) {
				return nil, false
			}
			current = values[token.index]
		} else {
			values, ok := current.(map[string]any)
			if !ok {
				return nil, false
			}
			var exists bool
			current, exists = values[token.field]
			if !exists {
				return nil, false
			}
		}
	}
	return current, true
}

func setStreamedJSONValue(root map[string]any, tokens []streamedJSONPathToken, value any) error {
	_, err := setStreamedJSONValueAt(root, tokens, value)
	return err
}

func setStreamedJSONValueAt(current any, tokens []streamedJSONPathToken, value any) (any, error) {
	if len(tokens) == 0 {
		return cloneJSONValue(value), nil
	}

	token := tokens[0]
	if token.isIndex {
		values, ok := current.([]any)
		if !ok {
			return nil, fmt.Errorf("path has incompatible array shape at index %d", token.index)
		}
		for len(values) <= token.index {
			values = append(values, nil)
		}
		if len(tokens) == 1 {
			if values[token.index] != nil && !streamedJSONValuesCompatible(values[token.index], value) {
				return nil, fmt.Errorf("path has incompatible shape at index %d", token.index)
			}
			values[token.index] = cloneJSONValue(value)
			return values, nil
		}

		child := values[token.index]
		next := tokens[1]
		if child == nil {
			if next.isIndex {
				child = []any{}
			} else {
				child = map[string]any{}
			}
		} else if !streamedJSONContainerMatches(child, next) {
			return nil, fmt.Errorf("path has incompatible shape at index %d", token.index)
		}
		updated, err := setStreamedJSONValueAt(child, tokens[1:], value)
		if err != nil {
			return nil, err
		}
		values[token.index] = updated
		return values, nil
	}

	values, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("path has incompatible object shape at field %q", token.field)
	}
	if len(tokens) == 1 {
		if existing, exists := values[token.field]; exists && !streamedJSONValuesCompatible(existing, value) {
			return nil, fmt.Errorf("path has incompatible shape at field %q", token.field)
		}
		values[token.field] = cloneJSONValue(value)
		return values, nil
	}

	child, exists := values[token.field]
	next := tokens[1]
	if !exists {
		if next.isIndex {
			child = []any{}
		} else {
			child = map[string]any{}
		}
	} else if child == nil || !streamedJSONContainerMatches(child, next) {
		return nil, fmt.Errorf("path has incompatible shape at field %q", token.field)
	}
	updated, err := setStreamedJSONValueAt(child, tokens[1:], value)
	if err != nil {
		return nil, err
	}
	values[token.field] = updated
	return values, nil
}

func streamedJSONContainerMatches(value any, next streamedJSONPathToken) bool {
	if next.isIndex {
		_, ok := value.([]any)
		return ok
	}
	_, ok := value.(map[string]any)
	return ok
}

func streamedJSONValuesCompatible(existing, incoming any) bool {
	return streamedJSONShape(existing) == streamedJSONShape(incoming)
}

func streamedJSONShape(value any) byte {
	switch value.(type) {
	case map[string]any:
		return 'o'
	case []any:
		return 'a'
	default:
		return 's'
	}
}

func mergeStreamedJSONMap(destination, source map[string]any, path string) error {
	for key, incoming := range source {
		existing, exists := destination[key]
		if !exists {
			destination[key] = cloneJSONValue(incoming)
			continue
		}
		if !streamedJSONValuesCompatible(existing, incoming) {
			return fmt.Errorf("incompatible shapes at %s.%s", path, key)
		}
		switch existingValue := existing.(type) {
		case map[string]any:
			if err := mergeStreamedJSONMap(existingValue, incoming.(map[string]any), path+"."+key); err != nil {
				return err
			}
		default:
			destination[key] = cloneJSONValue(incoming)
		}
	}
	return nil
}

func cloneJSONMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneJSONValue(value)
	}
	return result
}

func cloneJSONValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneJSONMap(value)
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = cloneJSONValue(item)
		}
		return result
	default:
		return value
	}
}

func (a *streamedFunctionCallAccumulator) addFunctionCalls(calls []*FunctionCall) error {
	for _, call := range calls {
		if err := a.addFunctionCall(call); err != nil {
			return err
		}
	}
	return nil
}

func (a *streamedFunctionCallAccumulator) addResponse(response *GenerateContentResponse) error {
	if response == nil {
		return nil
	}
	for _, candidate := range response.Candidates {
		if candidate == nil || candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part != nil && part.FunctionCall != nil {
				if err := a.addFunctionCall(part.FunctionCall); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (a *streamedFunctionCallAccumulator) addLiveServerMessage(message *LiveServerMessage) error {
	if message == nil || message.ToolCall == nil {
		return nil
	}
	return a.addFunctionCalls(message.ToolCall.FunctionCalls)
}

func cloneCompletedFunctionCall(call *FunctionCall) *FunctionCall {
	if call == nil {
		return nil
	}
	return &FunctionCall{
		ID:   call.ID,
		Args: cloneJSONMap(call.Args),
		Name: call.Name,
	}
}

func mergeStreamedFunctionCallContents(contents []*Content) ([]*Content, bool) {
	if len(contents) == 0 {
		return contents, false
	}
	var calls []*FunctionCall
	active := make(map[string]int)
	for _, content := range contents {
		if content == nil {
			continue
		}
		if len(content.Parts) == 0 {
			continue
		}
		for _, part := range content.Parts {
			if part == nil || part.FunctionCall == nil {
				return contents, false
			}
			call := cloneCompletedFunctionCall(part.FunctionCall)
			key := streamedFunctionCallKey(call)
			if index, ok := active[key]; ok {
				calls[index] = call
			} else {
				active[key] = len(calls)
				calls = append(calls, call)
			}
			if !streamedWillContinue(part.FunctionCall.WillContinue) {
				delete(active, key)
			}
		}
	}
	if len(calls) == 0 {
		return contents, false
	}
	parts := make([]*Part, len(calls))
	for i, call := range calls {
		parts[i] = &Part{FunctionCall: call}
	}
	return []*Content{{Role: RoleModel, Parts: parts}}, true
}
