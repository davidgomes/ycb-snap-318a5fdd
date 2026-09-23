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
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// streamedFunctionCallState is the accumulated JSON arguments for one in-progress
// streamed function call. State is dropped once that call's willContinue is false
// or omitted, so a later call that reuses the same id starts fresh.
type streamedFunctionCallState struct {
	id         string
	name       string
	args       map[string]any
	set        map[string]bool
	continuing map[string]bool
}

type streamedFunctionCallAccumulator struct {
	open []*streamedFunctionCallState
}

func newStreamedFunctionCallAccumulator() *streamedFunctionCallAccumulator {
	return &streamedFunctionCallAccumulator{}
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
	state := a.matchOpen(call)
	// A non-streaming function call, and an empty end marker with nothing open,
	// already has its public Args.
	if state == nil && len(call.PartialArgs) == 0 && call.WillContinue == nil {
		return nil
	}

	working := state.clone()
	if call.ID != "" {
		working.id = call.ID
	}
	if call.Name != "" {
		working.name = call.Name
	}
	if err := mergeStreamedArgs(working, call.Args); err != nil {
		return err
	}
	for _, partial := range call.PartialArgs {
		if partial == nil {
			continue
		}
		if err := applyStreamedPartialArg(working, partial); err != nil {
			return err
		}
	}

	call.Args = cloneArgsForCaller(working.args)
	if functionCallWillContinue(call) {
		if state == nil {
			a.open = append(a.open, working)
		} else {
			*state = *working
			a.touch(state)
		}
		return nil
	}
	if state != nil {
		a.removeOpen(state)
	}
	return nil
}

func (a *streamedFunctionCallAccumulator) matchOpen(call *FunctionCall) *streamedFunctionCallState {
	if call.ID != "" {
		for i := len(a.open) - 1; i >= 0; i-- {
			if a.open[i].id == call.ID {
				return a.open[i]
			}
		}
	}
	if call.Name != "" {
		for i := len(a.open) - 1; i >= 0; i-- {
			st := a.open[i]
			if st.name == call.Name && (st.id == "" || call.ID == "" || st.id == call.ID) {
				return st
			}
		}
	}
	if call.ID == "" && call.Name == "" && len(a.open) > 0 {
		return a.open[len(a.open)-1]
	}
	return nil
}

func (a *streamedFunctionCallAccumulator) touch(state *streamedFunctionCallState) {
	a.removeOpen(state)
	a.open = append(a.open, state)
}

func (a *streamedFunctionCallAccumulator) removeOpen(state *streamedFunctionCallState) {
	filtered := a.open[:0]
	for _, st := range a.open {
		if st != state {
			filtered = append(filtered, st)
		}
	}
	a.open = filtered
}

func (s *streamedFunctionCallState) clone() *streamedFunctionCallState {
	if s == nil {
		return &streamedFunctionCallState{
			args:       map[string]any{},
			set:        map[string]bool{},
			continuing: map[string]bool{},
		}
	}
	return &streamedFunctionCallState{
		id:         s.id,
		name:       s.name,
		args:       cloneJSONMap(s.args),
		set:        cloneBoolMap(s.set),
		continuing: cloneBoolMap(s.continuing),
	}
}

func functionCallWillContinue(call *FunctionCall) bool {
	return call != nil && call.WillContinue != nil && *call.WillContinue
}

func partialArgWillContinue(partial *PartialArg) bool {
	return partial != nil && partial.WillContinue != nil && *partial.WillContinue
}

// mergeStreamedFunctionCallContents collapses a model turn that is made entirely
// of streamed function calls into one completed function-call turn. Each distinct
// call is stored once, in the order it first appeared, with its final Args and
// without partial fragments.
func mergeStreamedFunctionCallContents(contents []*Content) []*Content {
	if !turnIsEntirelyStreamedFunctionCalls(contents) {
		return contents
	}
	parts := completedStreamedFunctionCallParts(contents)
	if len(parts) == 0 {
		return contents
	}
	role := RoleModel
	for _, content := range contents {
		if content != nil && content.Role != "" {
			role = content.Role
			break
		}
	}
	return []*Content{{Role: role, Parts: parts}}
}

func turnIsEntirelyStreamedFunctionCalls(contents []*Content) bool {
	sawCall := false
	sawStream := false
	for _, content := range contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall == nil || partHasNonFunctionPayload(part) {
				return false
			}
			sawCall = true
			if len(part.FunctionCall.PartialArgs) > 0 || part.FunctionCall.WillContinue != nil {
				sawStream = true
			}
		}
	}
	return sawCall && sawStream
}

func partHasNonFunctionPayload(part *Part) bool {
	return part.Text != "" ||
		part.InlineData != nil ||
		part.FileData != nil ||
		part.FunctionResponse != nil ||
		part.ExecutableCode != nil ||
		part.CodeExecutionResult != nil ||
		part.ToolCall != nil ||
		part.ToolResponse != nil ||
		part.VideoMetadata != nil ||
		part.MediaResolution != nil
}

type streamedHistoryCall struct {
	id               string
	name             string
	args             map[string]any
	thoughtSignature []byte
	done             bool
}

func completedStreamedFunctionCallParts(contents []*Content) []*Part {
	var seq []*streamedHistoryCall
	var open []*streamedHistoryCall
	for _, content := range contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part == nil || part.FunctionCall == nil {
				continue
			}
			fc := part.FunctionCall
			target := matchHistoryCall(open, fc)
			if target == nil {
				if fc.ID == "" && fc.Name == "" && len(fc.PartialArgs) == 0 && fc.Args == nil && !functionCallWillContinue(fc) {
					continue
				}
				target = &streamedHistoryCall{id: fc.ID, name: fc.Name}
				seq = append(seq, target)
				if functionCallWillContinue(fc) {
					open = append(open, target)
				} else {
					target.done = true
				}
			} else {
				if fc.ID != "" {
					target.id = fc.ID
				}
				if fc.Name != "" {
					target.name = fc.Name
				}
				if functionCallWillContinue(fc) {
					touchHistoryCall(&open, target)
				} else {
					target.done = true
					removeHistoryCall(&open, target)
				}
			}
			if fc.Args != nil {
				target.args = cloneJSONMap(fc.Args)
			}
			if len(target.thoughtSignature) == 0 && len(part.ThoughtSignature) > 0 {
				target.thoughtSignature = append([]byte(nil), part.ThoughtSignature...)
			}
		}
	}

	var parts []*Part
	for _, call := range seq {
		if !call.done {
			continue
		}
		part := &Part{
			FunctionCall: &FunctionCall{
				ID:   call.id,
				Name: call.name,
				Args: cloneJSONMap(call.args),
			},
		}
		if len(call.thoughtSignature) > 0 {
			part.ThoughtSignature = call.thoughtSignature
		}
		parts = append(parts, part)
	}
	return parts
}

func matchHistoryCall(open []*streamedHistoryCall, call *FunctionCall) *streamedHistoryCall {
	if call.ID != "" {
		for i := len(open) - 1; i >= 0; i-- {
			if open[i].id == call.ID {
				return open[i]
			}
		}
	}
	if call.Name != "" {
		for i := len(open) - 1; i >= 0; i-- {
			st := open[i]
			if st.name == call.Name && (st.id == "" || call.ID == "" || st.id == call.ID) {
				return st
			}
		}
	}
	if call.ID == "" && call.Name == "" && len(open) > 0 {
		return open[len(open)-1]
	}
	return nil
}

func touchHistoryCall(open *[]*streamedHistoryCall, target *streamedHistoryCall) {
	removeHistoryCall(open, target)
	*open = append(*open, target)
}

func removeHistoryCall(open *[]*streamedHistoryCall, target *streamedHistoryCall) {
	filtered := (*open)[:0]
	for _, call := range *open {
		if call != target {
			filtered = append(filtered, call)
		}
	}
	*open = filtered
}

func mergeStreamedArgs(state *streamedFunctionCallState, incoming map[string]any) error {
	if len(incoming) == 0 {
		return nil
	}
	if state.args == nil {
		state.args = map[string]any{}
	}
	return mergeStreamedObject(state, state.args, incoming, nil)
}

func mergeStreamedObject(state *streamedFunctionCallState, dst map[string]any, src map[string]any, prefix []any) error {
	for key, srcValue := range src {
		path := appendPath(prefix, key)
		if err := mergeStreamedValue(state, dst, key, srcValue, path); err != nil {
			return err
		}
	}
	return nil
}

func mergeStreamedValue(state *streamedFunctionCallState, dst map[string]any, key string, srcValue any, path []any) error {
	canon := canonicalPath(path)
	dstValue, exists := dst[key]
	switch typed := srcValue.(type) {
	case map[string]any:
		if state.set[canon] || (exists && dstValue != nil && !isStreamedContainer(dstValue)) {
			return incompatibleStreamedShape(path)
		}
		if existing, ok := dstValue.(map[string]any); ok {
			return mergeStreamedObject(state, existing, typed, path)
		}
		cloned := cloneJSONMap(typed)
		if cloned == nil {
			cloned = map[string]any{}
		}
		dst[key] = cloned
		indexExistingStreamedValue(state, cloned, path)
		return nil
	case []any:
		if state.set[canon] || (exists && dstValue != nil && !isStreamedContainer(dstValue)) {
			return incompatibleStreamedShape(path)
		}
		if existing, ok := dstValue.([]any); ok {
			merged, err := mergeStreamedArray(state, existing, typed, path)
			if err != nil {
				return err
			}
			dst[key] = merged
			return nil
		}
		cloned, _ := cloneJSONValue(typed).([]any)
		dst[key] = cloned
		indexExistingStreamedValue(state, cloned, path)
		return nil
	default:
		if exists && isStreamedContainer(dstValue) {
			return incompatibleStreamedShape(path)
		}
		if state.set[canon] {
			if !streamedLeafEqual(dstValue, srcValue, srcValue == nil) {
				return incompatibleStreamedShape(path)
			}
			return nil
		}
		dst[key] = cloneJSONValue(srcValue)
		state.set[canon] = true
		return nil
	}
}

func mergeStreamedArray(state *streamedFunctionCallState, dst []any, src []any, prefix []any) ([]any, error) {
	if len(src) > len(dst) {
		grown := make([]any, len(src))
		copy(grown, dst)
		dst = grown
	}
	for i, srcValue := range src {
		path := appendPath(prefix, i)
		canon := canonicalPath(path)
		switch typed := srcValue.(type) {
		case map[string]any:
			if state.set[canon] || (dst[i] != nil && !isStreamedContainer(dst[i])) {
				return nil, incompatibleStreamedShape(path)
			}
			if existing, ok := dst[i].(map[string]any); ok {
				if err := mergeStreamedObject(state, existing, typed, path); err != nil {
					return nil, err
				}
				continue
			}
			cloned := cloneJSONMap(typed)
			if cloned == nil {
				cloned = map[string]any{}
			}
			dst[i] = cloned
			indexExistingStreamedValue(state, cloned, path)
		case []any:
			if state.set[canon] || (dst[i] != nil && !isStreamedContainer(dst[i])) {
				return nil, incompatibleStreamedShape(path)
			}
			if existing, ok := dst[i].([]any); ok {
				merged, err := mergeStreamedArray(state, existing, typed, path)
				if err != nil {
					return nil, err
				}
				dst[i] = merged
				continue
			}
			cloned, _ := cloneJSONValue(typed).([]any)
			dst[i] = cloned
			indexExistingStreamedValue(state, cloned, path)
		default:
			if isStreamedContainer(dst[i]) {
				return nil, incompatibleStreamedShape(path)
			}
			if state.set[canon] {
				if !streamedLeafEqual(dst[i], srcValue, srcValue == nil) {
					return nil, incompatibleStreamedShape(path)
				}
				continue
			}
			dst[i] = cloneJSONValue(srcValue)
			state.set[canon] = true
		}
	}
	return dst, nil
}

func indexExistingStreamedValue(state *streamedFunctionCallState, value any, prefix []any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			indexExistingStreamedValue(state, child, appendPath(prefix, key))
		}
	case []any:
		for i, child := range typed {
			indexExistingStreamedValue(state, child, appendPath(prefix, i))
		}
	default:
		if len(prefix) > 0 {
			state.set[canonicalPath(prefix)] = true
		}
	}
}

func applyStreamedPartialArg(state *streamedFunctionCallState, partial *PartialArg) error {
	value, isNull, present, err := streamedPartialArgValue(partial)
	if err != nil {
		return err
	}
	tokens, err := parseStreamedJSONPath(partial.JsonPath)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		if !present {
			return nil
		}
		return incompatibleStreamedShape(nil)
	}
	if !present {
		canon := canonicalPath(tokens)
		if partialArgWillContinue(partial) {
			state.continuing[canon] = true
		} else {
			delete(state.continuing, canon)
		}
		return nil
	}
	if state.args == nil {
		state.args = map[string]any{}
	}
	updated, err := assignStreamedValue(state.args, tokens, nil, value, isNull, partialArgWillContinue(partial), state)
	if err != nil {
		return err
	}
	object, ok := updated.(map[string]any)
	if !ok {
		return incompatibleStreamedShape(nil)
	}
	state.args = object
	return nil
}

func streamedPartialArgValue(partial *PartialArg) (value any, isNull bool, present bool, err error) {
	count := 0
	if partial.BoolValue != nil {
		count++
		value = *partial.BoolValue
	}
	if partial.NumberValue != nil {
		count++
		value = *partial.NumberValue
	}
	if partial.StringValue != "" {
		count++
		value = partial.StringValue
	}
	if partial.NULLValue != "" {
		count++
		isNull = true
		value = nil
	}
	if count > 1 {
		return nil, false, false, fmt.Errorf("streamed function call partial arg must contain exactly one value")
	}
	return value, isNull, count == 1, nil
}

func assignStreamedValue(current any, tokens []any, prefix []any, value any, isNull bool, willContinue bool, state *streamedFunctionCallState) (any, error) {
	if len(tokens) == 0 {
		return nil, incompatibleStreamedShape(prefix)
	}
	token := tokens[0]
	path := appendPath(prefix, token)
	last := len(tokens) == 1
	switch key := token.(type) {
	case string:
		object, err := streamedObject(current)
		if err != nil {
			return nil, incompatibleStreamedShape(prefix)
		}
		if last {
			if err := assignStreamedObjectLeaf(object, key, path, value, isNull, willContinue, state); err != nil {
				return nil, err
			}
			return object, nil
		}
		next, err := descendStreamedObject(object, key, path, tokens[1], state)
		if err != nil {
			return nil, err
		}
		updated, err := assignStreamedValue(next, tokens[1:], path, value, isNull, willContinue, state)
		if err != nil {
			return nil, err
		}
		object[key] = updated
		return object, nil
	case int:
		array, err := streamedArray(current, key)
		if err != nil {
			return nil, incompatibleStreamedShape(prefix)
		}
		if last {
			if err := assignStreamedArrayLeaf(array, key, path, value, isNull, willContinue, state); err != nil {
				return nil, err
			}
			return array, nil
		}
		next, err := descendStreamedArray(array, key, path, tokens[1], state)
		if err != nil {
			return nil, err
		}
		updated, err := assignStreamedValue(next, tokens[1:], path, value, isNull, willContinue, state)
		if err != nil {
			return nil, err
		}
		array[key] = updated
		return array, nil
	default:
		return nil, fmt.Errorf("streamed function call json path has unsupported token")
	}
}

func streamedObject(current any) (map[string]any, error) {
	if current == nil {
		return map[string]any{}, nil
	}
	object, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object")
	}
	return object, nil
}

func streamedArray(current any, index int) ([]any, error) {
	if index < 0 {
		return nil, fmt.Errorf("invalid index")
	}
	if current == nil {
		return make([]any, index+1), nil
	}
	array, ok := current.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array")
	}
	if index >= len(array) {
		grown := make([]any, index+1)
		copy(grown, array)
		return grown, nil
	}
	return array, nil
}

func descendStreamedObject(object map[string]any, key string, path []any, nextToken any, state *streamedFunctionCallState) (any, error) {
	canon := canonicalPath(path)
	next, exists := object[key]
	if state.set[canon] || (exists && next != nil && !isStreamedContainer(next)) {
		return nil, incompatibleStreamedShape(path)
	}
	if !exists || next == nil {
		next = newStreamedContainer(nextToken)
		object[key] = next
		return next, nil
	}
	if !isStreamedContainer(next) {
		return nil, incompatibleStreamedShape(path)
	}
	return next, nil
}

func descendStreamedArray(array []any, index int, path []any, nextToken any, state *streamedFunctionCallState) (any, error) {
	canon := canonicalPath(path)
	next := array[index]
	if state.set[canon] || (next != nil && !isStreamedContainer(next)) {
		return nil, incompatibleStreamedShape(path)
	}
	if next == nil {
		next = newStreamedContainer(nextToken)
		array[index] = next
		return next, nil
	}
	if !isStreamedContainer(next) {
		return nil, incompatibleStreamedShape(path)
	}
	return next, nil
}

func assignStreamedObjectLeaf(object map[string]any, key string, path []any, value any, isNull bool, willContinue bool, state *streamedFunctionCallState) error {
	canon := canonicalPath(path)
	current, exists := object[key]
	if exists && isStreamedContainer(current) {
		return incompatibleStreamedShape(path)
	}
	merged, err := mergeStreamedLeaf(current, state.set[canon], state.continuing[canon], value, isNull)
	if err != nil {
		return incompatibleStreamedShape(path)
	}
	object[key] = merged
	state.set[canon] = true
	noteStreamedContinuation(state, canon, merged, willContinue && !isNull)
	return nil
}

func assignStreamedArrayLeaf(array []any, index int, path []any, value any, isNull bool, willContinue bool, state *streamedFunctionCallState) error {
	canon := canonicalPath(path)
	current := array[index]
	if isStreamedContainer(current) {
		return incompatibleStreamedShape(path)
	}
	merged, err := mergeStreamedLeaf(current, state.set[canon], state.continuing[canon], value, isNull)
	if err != nil {
		return incompatibleStreamedShape(path)
	}
	array[index] = merged
	state.set[canon] = true
	noteStreamedContinuation(state, canon, merged, willContinue && !isNull)
	return nil
}

func mergeStreamedLeaf(current any, exists bool, continuing bool, value any, isNull bool) (any, error) {
	if isStreamedContainer(current) {
		return nil, fmt.Errorf("container")
	}
	if !exists {
		return value, nil
	}
	if continuing && !isNull {
		currentString, currentIsString := current.(string)
		valueString, valueIsString := value.(string)
		if currentIsString && valueIsString {
			return currentString + valueString, nil
		}
		return nil, fmt.Errorf("append")
	}
	if streamedLeafEqual(current, value, isNull) {
		return current, nil
	}
	return nil, fmt.Errorf("overwrite")
}

func noteStreamedContinuation(state *streamedFunctionCallState, canon string, stored any, willContinue bool) {
	if willContinue {
		if _, ok := stored.(string); ok {
			state.continuing[canon] = true
			return
		}
	}
	delete(state.continuing, canon)
}

func newStreamedContainer(next any) any {
	if index, ok := next.(int); ok {
		if index < 0 {
			index = 0
		}
		return make([]any, index+1)
	}
	return map[string]any{}
}

func isStreamedContainer(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

func streamedLeafEqual(current any, value any, isNull bool) bool {
	if isNull {
		return current == nil
	}
	if currentFloat, currentOK := streamedFloat(current); currentOK {
		if valueFloat, valueOK := streamedFloat(value); valueOK {
			return currentFloat == valueFloat
		}
	}
	return reflect.DeepEqual(current, value)
}

func streamedFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func incompatibleStreamedShape(path []any) error {
	return fmt.Errorf("streamed function call has incompatible shapes at %s", formatStreamedJSONPath(path))
}

func parseStreamedJSONPath(path string) ([]any, error) {
	if path == "" || path[0] != '$' {
		return nil, fmt.Errorf("streamed function call json path must start with $")
	}
	var tokens []any
	for i := 1; i < len(path); {
		switch path[i] {
		case '.':
			i++
			start := i
			for i < len(path) && isStreamedPathIdent(path[i]) {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("streamed function call json path has empty field")
			}
			tokens = append(tokens, path[start:i])
		case '[':
			next, token, err := parseStreamedBracket(path, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token)
			i = next
		default:
			return nil, fmt.Errorf("streamed function call json path has unsupported token at byte %d", i)
		}
	}
	return tokens, nil
}

func parseStreamedBracket(path string, start int) (int, any, error) {
	if start+1 >= len(path) {
		return 0, nil, fmt.Errorf("streamed function call json path has unterminated bracket")
	}
	if path[start+1] == '\'' || path[start+1] == '"' {
		quote := path[start+1]
		var body strings.Builder
		i := start + 2
		for i < len(path) {
			if path[i] == '\\' && i+1 < len(path) {
				body.WriteByte(path[i+1])
				i += 2
				continue
			}
			if path[i] == quote {
				break
			}
			body.WriteByte(path[i])
			i++
		}
		if i >= len(path) || path[i] != quote {
			return 0, nil, fmt.Errorf("streamed function call json path has unterminated quoted key")
		}
		i++
		if i >= len(path) || path[i] != ']' {
			return 0, nil, fmt.Errorf("streamed function call json path has unterminated bracket")
		}
		return i + 1, body.String(), nil
	}
	end := strings.IndexByte(path[start:], ']')
	if end < 0 {
		return 0, nil, fmt.Errorf("streamed function call json path has unterminated bracket")
	}
	body := strings.TrimSpace(path[start+1 : start+end])
	if body == "" || body[0] == '+' || body[0] == '-' {
		return 0, nil, fmt.Errorf("streamed function call json path has invalid array index %q", body)
	}
	index, err := strconv.Atoi(body)
	if err != nil || index < 0 {
		return 0, nil, fmt.Errorf("streamed function call json path has invalid array index %q", body)
	}
	return start + end + 1, index, nil
}

func isStreamedPathIdent(char byte) bool {
	return char == '_' || char == '-' ||
		(char >= '0' && char <= '9') ||
		(char >= 'A' && char <= 'Z') ||
		(char >= 'a' && char <= 'z')
}

func appendPath(prefix []any, token any) []any {
	path := make([]any, len(prefix)+1)
	copy(path, prefix)
	path[len(prefix)] = token
	return path
}

func canonicalPath(tokens []any) string {
	var b strings.Builder
	for _, token := range tokens {
		b.WriteByte('\x1f')
		switch typed := token.(type) {
		case string:
			b.WriteByte('s')
			b.WriteString(typed)
		case int:
			b.WriteByte('i')
			b.WriteString(strconv.Itoa(typed))
		}
	}
	return b.String()
}

func formatStreamedJSONPath(tokens []any) string {
	var b strings.Builder
	b.WriteByte('$')
	for _, token := range tokens {
		switch typed := token.(type) {
		case string:
			if typed != "" && isStreamedDotName(typed) {
				b.WriteByte('.')
				b.WriteString(typed)
			} else {
				b.WriteString("['")
				b.WriteString(typed)
				b.WriteString("']")
			}
		case int:
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(typed))
			b.WriteByte(']')
		}
	}
	return b.String()
}

func isStreamedDotName(name string) bool {
	for i := 0; i < len(name); i++ {
		if !isStreamedPathIdent(name[i]) {
			return false
		}
	}
	return true
}

func cloneArgsForCaller(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	return cloneJSONMap(args)
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneJSONMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneJSONValue(item)
		}
		return out
	default:
		return typed
	}
}
