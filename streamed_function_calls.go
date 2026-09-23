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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// streamedJSONNull is an explicit JSON null stored while a function call is
// still being accumulated. Exported Args use a plain nil instead.
type streamedJSONNull struct{}

type streamedPathSegment struct {
	key   string
	index int
	array bool
}

type streamedCallIdentity struct {
	id   string
	name string
}

type streamedCallState struct {
	id             string
	name           string
	args           map[string]any
	continuing     map[string]bool
	rootBuf        string
	rootContinuing bool
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
	key, migrateFrom, err := resolveStreamedFunctionCall(a.identities(), call)
	if err != nil {
		return err
	}
	state := a.calls[key]
	if migrateFrom != "" {
		state = a.calls[migrateFrom]
	}
	if state == nil && len(call.PartialArgs) == 0 && !functionCallWillContinue(call) {
		return nil
	}

	working := state.clone()
	if call.ID != "" {
		working.id = call.ID
	}
	if call.Name != "" {
		working.name = call.Name
	}
	if call.Args != nil {
		imported, ok := importStreamedJSON(call.Args).(map[string]any)
		if !ok {
			return fmt.Errorf("streamed function call args must be a JSON object")
		}
		merged, err := mergeStreamedArgs(working.args, imported)
		if err != nil {
			return err
		}
		working.args = merged
	}
	for _, partial := range call.PartialArgs {
		if err := working.applyPartial(partial); err != nil {
			return err
		}
	}
	if exported := exportStreamedArgs(working.args); exported != nil {
		call.Args = exported
	}
	if migrateFrom != "" && migrateFrom != key {
		delete(a.calls, migrateFrom)
	}
	if functionCallWillContinue(call) {
		a.calls[key] = working
	} else {
		delete(a.calls, key)
	}
	return nil
}

func (a *streamedFunctionCallAccumulator) identities() map[string]streamedCallIdentity {
	out := make(map[string]streamedCallIdentity, len(a.calls))
	for key, state := range a.calls {
		if state == nil {
			continue
		}
		out[key] = streamedCallIdentity{id: state.id, name: state.name}
	}
	return out
}

func (s *streamedCallState) clone() *streamedCallState {
	if s == nil {
		return &streamedCallState{
			args:       map[string]any{},
			continuing: map[string]bool{},
		}
	}
	continuing := make(map[string]bool, len(s.continuing))
	for key, value := range s.continuing {
		continuing[key] = value
	}
	args := cloneStreamedMap(s.args)
	if args == nil {
		args = map[string]any{}
	}
	return &streamedCallState{
		id:             s.id,
		name:           s.name,
		args:           args,
		continuing:     continuing,
		rootBuf:        s.rootBuf,
		rootContinuing: s.rootContinuing,
	}
}

func (s *streamedCallState) applyPartial(partial *PartialArg) error {
	if partial == nil {
		return nil
	}
	value, hasValue, err := streamedPartialArgValue(partial)
	if err != nil {
		return err
	}
	if partial.JsonPath == "" {
		if !hasValue {
			return nil
		}
		return fmt.Errorf("streamed function call partial arg missing json path")
	}
	if !hasValue {
		return nil
	}
	segments, err := parseStreamedJSONPath(partial.JsonPath)
	if err != nil {
		return err
	}
	willContinue := partial.WillContinue != nil && *partial.WillContinue
	if len(segments) == 0 {
		return s.applyRoot(value, willContinue)
	}
	pathKey := canonicalStreamedPath(segments)
	updated, err := setStreamedNode(s.args, segments, value, s.continuing[pathKey])
	if err != nil {
		return err
	}
	object, ok := updated.(map[string]any)
	if !ok {
		return fmt.Errorf("streamed function call path conflict: root must remain an object")
	}
	s.args = object
	if willContinue {
		if _, ok := value.(string); ok {
			s.continuing[pathKey] = true
			return nil
		}
	}
	delete(s.continuing, pathKey)
	return nil
}

func (s *streamedCallState) applyRoot(value any, willContinue bool) error {
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("streamed function call path conflict: root path requires a JSON object")
	}
	switch {
	case s.rootContinuing:
		s.rootBuf += text
	case s.rootBuf == "":
		s.rootBuf = text
	case s.rootBuf == text:
		// The same completed root fragment was repeated.
	default:
		return fmt.Errorf("streamed function call path conflict: refusing to overwrite root")
	}
	s.rootContinuing = willContinue
	if willContinue {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(s.rootBuf), &parsed); err != nil {
		return fmt.Errorf("streamed function call root json path: %w", err)
	}
	imported, ok := importStreamedJSON(parsed).(map[string]any)
	if !ok {
		return fmt.Errorf("streamed function call path conflict: root path requires a JSON object")
	}
	merged, err := mergeStreamedArgs(s.args, imported)
	if err != nil {
		return err
	}
	s.args = merged
	s.rootBuf = ""
	s.rootContinuing = false
	return nil
}

func resolveStreamedFunctionCall(active map[string]streamedCallIdentity, call *FunctionCall) (key string, migrateFrom string, err error) {
	if call.ID != "" {
		key = "id:" + call.ID
		if _, ok := active[key]; ok {
			return key, "", nil
		}
		if call.Name != "" {
			nameKey := "name:" + call.Name
			if identity, ok := active[nameKey]; ok && identity.id == "" {
				return key, nameKey, nil
			}
		}
		return key, "", nil
	}
	if call.Name != "" {
		var foundKey string
		count := 0
		for activeKey, identity := range active {
			if identity.name == call.Name || activeKey == "name:"+call.Name {
				count++
				foundKey = activeKey
			}
		}
		if count > 1 {
			return "", "", fmt.Errorf("streamed function call %q matches multiple in-progress calls", call.Name)
		}
		if count == 1 {
			return foundKey, "", nil
		}
		return "name:" + call.Name, "", nil
	}
	switch len(active) {
	case 0:
		return "anonymous", "", nil
	case 1:
		for activeKey := range active {
			return activeKey, "", nil
		}
	default:
		return "", "", fmt.Errorf("streamed function call fragment is missing an id")
	}
	return "", "", fmt.Errorf("streamed function call fragment is missing an id")
}

func streamedPartialArgValue(partial *PartialArg) (any, bool, error) {
	count := 0
	var value any
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
		value = nil
	}
	if count > 1 {
		return nil, false, fmt.Errorf("streamed function call partial arg must contain one value at %q", partial.JsonPath)
	}
	if count == 0 {
		return nil, false, nil
	}
	return value, true, nil
}

func parseStreamedJSONPath(path string) ([]streamedPathSegment, error) {
	if path == "" || path[0] != '$' {
		return nil, fmt.Errorf("unsupported streamed function call json path %q: must start with $", path)
	}
	var segments []streamedPathSegment
	for i := 1; i < len(path); {
		switch path[i] {
		case '.':
			i++
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("unsupported streamed function call json path %q: empty field", path)
			}
			segments = append(segments, streamedPathSegment{key: path[start:i]})
		case '[':
			segment, next, err := parseStreamedJSONPathBracket(path, i)
			if err != nil {
				return nil, err
			}
			segments = append(segments, segment)
			i = next
		default:
			return nil, fmt.Errorf("unsupported streamed function call json path %q", path)
		}
	}
	return segments, nil
}

func parseStreamedJSONPathBracket(path string, i int) (streamedPathSegment, int, error) {
	i++
	for i < len(path) && path[i] == ' ' {
		i++
	}
	if i >= len(path) {
		return streamedPathSegment{}, 0, fmt.Errorf("unsupported streamed function call json path %q: unclosed bracket", path)
	}
	if path[i] == '\'' || path[i] == '"' {
		quote := path[i]
		i++
		var key strings.Builder
		for i < len(path) {
			if path[i] == '\\' {
				if i+1 >= len(path) {
					return streamedPathSegment{}, 0, fmt.Errorf("unsupported streamed function call json path %q: truncated escape", path)
				}
				key.WriteByte(path[i+1])
				i += 2
				continue
			}
			if path[i] == quote {
				break
			}
			key.WriteByte(path[i])
			i++
		}
		if i >= len(path) || path[i] != quote {
			return streamedPathSegment{}, 0, fmt.Errorf("unsupported streamed function call json path %q: unclosed quoted key", path)
		}
		i++
		for i < len(path) && path[i] == ' ' {
			i++
		}
		if i >= len(path) || path[i] != ']' {
			return streamedPathSegment{}, 0, fmt.Errorf("unsupported streamed function call json path %q: unclosed bracket", path)
		}
		return streamedPathSegment{key: key.String()}, i + 1, nil
	}
	start := i
	for i < len(path) && path[i] != ']' && path[i] != ' ' {
		i++
	}
	indexText := path[start:i]
	for i < len(path) && path[i] == ' ' {
		i++
	}
	if i >= len(path) || path[i] != ']' {
		return streamedPathSegment{}, 0, fmt.Errorf("unsupported streamed function call json path %q: unclosed index", path)
	}
	if indexText == "" || strings.Trim(indexText, "0123456789") != "" {
		return streamedPathSegment{}, 0, fmt.Errorf("unsupported streamed function call json path %q: array index must be a non-negative integer", path)
	}
	index, err := strconv.Atoi(indexText)
	if err != nil {
		return streamedPathSegment{}, 0, fmt.Errorf("unsupported streamed function call json path %q: array index must be a non-negative integer", path)
	}
	return streamedPathSegment{array: true, index: index}, i + 1, nil
}

func canonicalStreamedPath(segments []streamedPathSegment) string {
	var b strings.Builder
	b.WriteByte('$')
	for _, segment := range segments {
		if segment.array {
			fmt.Fprintf(&b, "[%d]", segment.index)
			continue
		}
		b.WriteString("['")
		b.WriteString(strings.ReplaceAll(segment.key, "'", "\\'"))
		b.WriteString("']")
	}
	return b.String()
}

func setStreamedNode(current any, segments []streamedPathSegment, value any, appendString bool) (any, error) {
	if len(segments) == 0 {
		return mergeStreamedLeaf(current, value, appendString)
	}
	segment := segments[0]
	if current == nil {
		if segment.array {
			current = []any{}
		} else {
			current = map[string]any{}
		}
	}
	if _, ok := current.(streamedJSONNull); ok {
		return nil, fmt.Errorf("streamed function call path conflict: cannot descend into null")
	}
	if segment.array {
		array, ok := current.([]any)
		if !ok {
			return nil, fmt.Errorf("streamed function call path conflict: expected array")
		}
		array = cloneStreamedSlice(array)
		for len(array) <= segment.index {
			array = append(array, nil)
		}
		if len(segments) == 1 {
			updated, err := mergeStreamedLeaf(array[segment.index], value, appendString)
			if err != nil {
				return nil, err
			}
			array[segment.index] = updated
			return array, nil
		}
		child := array[segment.index]
		if child == nil {
			child = emptyStreamedContainer(segments[1])
		}
		updated, err := setStreamedNode(child, segments[1:], value, appendString)
		if err != nil {
			return nil, err
		}
		array[segment.index] = updated
		return array, nil
	}

	object, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("streamed function call path conflict: expected object")
	}
	object = cloneStreamedMap(object)
	if len(segments) == 1 {
		var child any
		if existing, exists := object[segment.key]; exists {
			child = existing
		}
		updated, err := mergeStreamedLeaf(child, value, appendString)
		if err != nil {
			return nil, err
		}
		object[segment.key] = updated
		return object, nil
	}
	child, exists := object[segment.key]
	if !exists || child == nil {
		child = emptyStreamedContainer(segments[1])
	}
	updated, err := setStreamedNode(child, segments[1:], value, appendString)
	if err != nil {
		return nil, err
	}
	object[segment.key] = updated
	return object, nil
}

func emptyStreamedContainer(segment streamedPathSegment) any {
	if segment.array {
		return []any{}
	}
	return map[string]any{}
}

func mergeStreamedLeaf(current any, value any, appendString bool) (any, error) {
	if _, ok := current.(map[string]any); ok {
		return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite object with %T", value)
	}
	if _, ok := current.([]any); ok {
		return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite array with %T", value)
	}
	if current == nil {
		if value == nil {
			return streamedJSONNull{}, nil
		}
		return value, nil
	}
	if _, ok := current.(streamedJSONNull); ok {
		if value == nil {
			return streamedJSONNull{}, nil
		}
		return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite null with %T", value)
	}
	if text, ok := current.(string); ok {
		next, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite string with %T", value)
		}
		if appendString {
			return text + next, nil
		}
		if text == next {
			return text, nil
		}
		return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite string")
	}
	if value == nil {
		return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite %T with null", current)
	}
	if streamedValuesEqual(current, value) {
		return current, nil
	}
	return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite %T with %T", current, value)
}

func mergeStreamedArgs(dst map[string]any, src map[string]any) (map[string]any, error) {
	out := cloneStreamedMap(dst)
	if out == nil {
		out = map[string]any{}
	}
	for key, value := range src {
		var current any
		if existing, ok := out[key]; ok {
			current = existing
		}
		merged, err := mergeStreamedTrees(current, value)
		if err != nil {
			return nil, err
		}
		out[key] = merged
	}
	return out, nil
}

func mergeStreamedTrees(dst any, src any) (any, error) {
	if dst == nil {
		return cloneStreamedValue(src), nil
	}
	if _, ok := dst.(streamedJSONNull); ok {
		if _, ok := src.(streamedJSONNull); ok {
			return streamedJSONNull{}, nil
		}
		return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite null with %T", src)
	}
	if _, ok := src.(streamedJSONNull); ok {
		return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite %T with null", dst)
	}
	dstMap, dstIsMap := dst.(map[string]any)
	srcMap, srcIsMap := src.(map[string]any)
	if dstIsMap || srcIsMap {
		if !dstIsMap || !srcIsMap {
			return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite %T with %T", dst, src)
		}
		return mergeStreamedArgs(dstMap, srcMap)
	}
	dstSlice, dstIsSlice := dst.([]any)
	srcSlice, srcIsSlice := src.([]any)
	if dstIsSlice || srcIsSlice {
		if !dstIsSlice || !srcIsSlice {
			return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite %T with %T", dst, src)
		}
		n := len(dstSlice)
		if len(srcSlice) > n {
			n = len(srcSlice)
		}
		out := make([]any, n)
		for i := 0; i < n; i++ {
			if i >= len(srcSlice) {
				out[i] = cloneStreamedValue(dstSlice[i])
				continue
			}
			if i >= len(dstSlice) || dstSlice[i] == nil {
				out[i] = cloneStreamedValue(srcSlice[i])
				continue
			}
			merged, err := mergeStreamedTrees(dstSlice[i], srcSlice[i])
			if err != nil {
				return nil, err
			}
			out[i] = merged
		}
		return out, nil
	}
	if text, ok := dst.(string); ok {
		next, ok := src.(string)
		if ok && text == next {
			return text, nil
		}
		return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite string")
	}
	if streamedValuesEqual(dst, src) {
		return dst, nil
	}
	return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite %T with %T", dst, src)
}

func streamedValuesEqual(a any, b any) bool {
	af, aok := streamedNumber(a)
	bf, bok := streamedNumber(b)
	if aok || bok {
		return aok && bok && af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b) && fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b)
}

func streamedNumber(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func importStreamedJSON(value any) any {
	switch typed := value.(type) {
	case nil:
		return streamedJSONNull{}
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = importStreamedJSON(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = importStreamedJSON(child)
		}
		return out
	default:
		return typed
	}
}

func exportStreamedArgs(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	return exportStreamedMap(value)
}

func exportStreamedMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, child := range value {
		out[key] = exportStreamedValue(child)
	}
	return out
}

func exportStreamedValue(value any) any {
	switch typed := value.(type) {
	case streamedJSONNull:
		return nil
	case map[string]any:
		return exportStreamedMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = exportStreamedValue(child)
		}
		return out
	default:
		return typed
	}
}

func cloneStreamedMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, child := range value {
		out[key] = cloneStreamedValue(child)
	}
	return out
}

func cloneStreamedSlice(value []any) []any {
	if value == nil {
		return nil
	}
	out := make([]any, len(value))
	for i, child := range value {
		out[i] = cloneStreamedValue(child)
	}
	return out
}

func cloneStreamedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneStreamedMap(typed)
	case []any:
		return cloneStreamedSlice(typed)
	case streamedJSONNull:
		return streamedJSONNull{}
	default:
		return typed
	}
}

func functionCallWillContinue(call *FunctionCall) bool {
	return call != nil && call.WillContinue != nil && *call.WillContinue
}

type streamedHistorySlot struct {
	id        string
	name      string
	args      map[string]any
	signature []byte
	done      bool
}

// mergeStreamedFunctionCallContents collapses a model turn made only of streamed
// function calls into one content. Completed calls are kept once, in the order
// they first appeared, with final Args and no partial fragments.
func mergeStreamedFunctionCallContents(contents []*Content) []*Content {
	if !streamedTurnIsFunctionCalls(contents) {
		return contents
	}
	var order []*streamedHistorySlot
	active := map[string]*streamedHistorySlot{}
	role := ""
	for _, content := range contents {
		if content == nil || len(content.Parts) == 0 {
			continue
		}
		if role == "" && content.Role != "" {
			role = content.Role
		}
		for _, part := range content.Parts {
			if part == nil || part.FunctionCall == nil {
				continue
			}
			call := part.FunctionCall
			identities := make(map[string]streamedCallIdentity, len(active))
			for key, slot := range active {
				identities[key] = streamedCallIdentity{id: slot.id, name: slot.name}
			}
			key, migrateFrom, err := resolveStreamedFunctionCall(identities, call)
			if err != nil {
				return contents
			}
			slot := active[key]
			if migrateFrom != "" {
				slot = active[migrateFrom]
				if slot != nil && migrateFrom != key {
					delete(active, migrateFrom)
				}
			}
			if slot == nil {
				slot = &streamedHistorySlot{}
				order = append(order, slot)
			}
			active[key] = slot
			if call.ID != "" {
				slot.id = call.ID
			}
			if call.Name != "" {
				slot.name = call.Name
			}
			if call.Args != nil {
				slot.args = call.Args
			}
			if len(part.ThoughtSignature) > 0 && len(slot.signature) == 0 {
				slot.signature = part.ThoughtSignature
			}
			if !functionCallWillContinue(call) {
				slot.done = true
				delete(active, key)
			}
		}
	}
	var parts []*Part
	for _, slot := range order {
		if !slot.done {
			continue
		}
		parts = append(parts, &Part{
			ThoughtSignature: append([]byte(nil), slot.signature...),
			FunctionCall: &FunctionCall{
				ID:   slot.id,
				Name: slot.name,
				Args: cloneExportedFunctionArgs(slot.args),
			},
		})
	}
	if len(parts) == 0 {
		return contents
	}
	if role == "" {
		role = RoleModel
	}
	return []*Content{{Role: role, Parts: parts}}
}

func streamedTurnIsFunctionCalls(contents []*Content) bool {
	sawCall := false
	for _, content := range contents {
		if content == nil || len(content.Parts) == 0 {
			continue
		}
		for _, part := range content.Parts {
			if part == nil || part.FunctionCall == nil || partHasNonFunctionPayload(part) {
				return false
			}
			sawCall = true
		}
	}
	return sawCall
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

func cloneExportedFunctionArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	imported, ok := importStreamedJSON(args).(map[string]any)
	if !ok {
		return nil
	}
	return exportStreamedArgs(imported)
}
