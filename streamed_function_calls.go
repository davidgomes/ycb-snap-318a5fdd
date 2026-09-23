package genai

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

type streamedFunctionCallState struct {
	args      map[string]any
	openPaths map[string]bool
}

type streamedFunctionCallAccumulator struct {
	calls map[string]*streamedFunctionCallState
}

type streamedFunctionCallPathSegment struct {
	key   string
	index int
	array bool
}

func newStreamedFunctionCallAccumulator() *streamedFunctionCallAccumulator {
	return &streamedFunctionCallAccumulator{calls: make(map[string]*streamedFunctionCallState)}
}

func (a *streamedFunctionCallAccumulator) accumulateResponse(response *GenerateContentResponse) error {
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
			if err := a.accumulateFunctionCall(part.FunctionCall); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *streamedFunctionCallAccumulator) accumulateFunctionCalls(functionCalls []*FunctionCall) error {
	for _, functionCall := range functionCalls {
		if functionCall == nil {
			continue
		}
		if err := a.accumulateFunctionCall(functionCall); err != nil {
			return err
		}
	}
	return nil
}

func (a *streamedFunctionCallAccumulator) accumulateFunctionCall(functionCall *FunctionCall) error {
	if functionCall == nil {
		return nil
	}
	key := streamedFunctionCallKey(functionCall)
	var args map[string]any
	var openPaths map[string]bool
	if state := a.calls[key]; state == nil {
		args = cloneStringAnyMap(functionCall.Args)
		openPaths = map[string]bool{}
	} else {
		args = cloneStringAnyMap(state.args)
		openPaths = cloneBoolMap(state.openPaths)
		if functionCall.Args != nil {
			merged, err := mergeStreamedFunctionCallValue(args, functionCall.Args, false)
			if err != nil {
				return err
			}
			object, ok := merged.(map[string]any)
			if !ok {
				return fmt.Errorf("streamed function call root must remain an object")
			}
			args = object
		}
	}

	for _, partialArg := range functionCall.PartialArgs {
		if partialArg == nil {
			continue
		}
		segments, err := parseStreamedFunctionCallJSONPath(partialArg.JsonPath)
		if err != nil {
			return err
		}
		value, present, err := streamedFunctionCallPartialArgValue(partialArg)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		pathKey := streamedFunctionCallPathKey(segments)
		next, err := streamedFunctionCallSet(args, segments, value, openPaths[pathKey])
		if err != nil {
			return err
		}
		args = next
		if _, isString := value.(string); isString {
			openPaths[pathKey] = partialArg.WillContinue != nil && *partialArg.WillContinue
		} else {
			delete(openPaths, pathKey)
		}
	}

	if len(args) > 0 {
		functionCall.Args = cloneStringAnyMap(args)
	}
	if functionCall.WillContinue == nil || !*functionCall.WillContinue {
		delete(a.calls, key)
	} else {
		a.calls[key] = &streamedFunctionCallState{args: args, openPaths: openPaths}
	}
	return nil
}

func streamedFunctionCallKey(functionCall *FunctionCall) string {
	if functionCall.ID != "" {
		return "id:" + functionCall.ID
	}
	if functionCall.Name != "" {
		return "name:" + functionCall.Name
	}
	return "anonymous"
}

func streamedFunctionCallPartialArgValue(partialArg *PartialArg) (any, bool, error) {
	var value any
	seen := false
	set := func(next any) error {
		if seen {
			return fmt.Errorf("streamed function call partial arg has multiple value fields at %q", partialArg.JsonPath)
		}
		value = next
		seen = true
		return nil
	}
	if partialArg.BoolValue != nil {
		if err := set(*partialArg.BoolValue); err != nil {
			return nil, false, err
		}
	}
	if partialArg.NumberValue != nil {
		if err := set(*partialArg.NumberValue); err != nil {
			return nil, false, err
		}
	}
	if partialArg.StringValue != "" {
		if err := set(partialArg.StringValue); err != nil {
			return nil, false, err
		}
	}
	if partialArg.NULLValue != "" {
		if err := set(nil); err != nil {
			return nil, false, err
		}
	}
	return value, seen, nil
}

func parseStreamedFunctionCallJSONPath(path string) ([]streamedFunctionCallPathSegment, error) {
	if path == "" {
		return nil, fmt.Errorf("streamed function call partial arg missing json path")
	}
	if path[0] != '$' {
		return nil, fmt.Errorf("unsupported streamed function call json path %q: must start with $", path)
	}
	var segments []streamedFunctionCallPathSegment
	for i := 1; i < len(path); {
		switch path[i] {
		case '.':
			i++
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("unsupported streamed function call json path %q: empty dot field", path)
			}
			segments = append(segments, streamedFunctionCallPathSegment{key: path[start:i]})
		case '[':
			i++
			if i >= len(path) {
				return nil, fmt.Errorf("unsupported streamed function call json path %q: unclosed bracket", path)
			}
			if path[i] == '\'' || path[i] == '"' {
				quote := path[i]
				i++
				start := i
				for i < len(path) && path[i] != quote {
					i++
				}
				if i >= len(path) {
					return nil, fmt.Errorf("unsupported streamed function call json path %q: unclosed quoted key", path)
				}
				key := path[start:i]
				i++
				if i >= len(path) || path[i] != ']' {
					return nil, fmt.Errorf("unsupported streamed function call json path %q: missing closing bracket", path)
				}
				i++
				segments = append(segments, streamedFunctionCallPathSegment{key: key})
				continue
			}
			start := i
			for i < len(path) && path[i] != ']' {
				i++
			}
			if i >= len(path) {
				return nil, fmt.Errorf("unsupported streamed function call json path %q: unclosed index", path)
			}
			indexText := strings.TrimSpace(path[start:i])
			i++
			index, err := strconv.Atoi(indexText)
			if err != nil || index < 0 {
				return nil, fmt.Errorf("unsupported streamed function call json path %q: array index must be a non-negative integer", path)
			}
			segments = append(segments, streamedFunctionCallPathSegment{array: true, index: index})
		default:
			return nil, fmt.Errorf("unsupported streamed function call json path %q: expected dot field or bracket segment", path)
		}
	}
	return segments, nil
}

func streamedFunctionCallPathKey(segments []streamedFunctionCallPathSegment) string {
	var b strings.Builder
	b.WriteByte('$')
	for _, segment := range segments {
		if segment.array {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(segment.index))
			b.WriteByte(']')
			continue
		}
		b.WriteByte('.')
		b.WriteString(segment.key)
	}
	return b.String()
}

func streamedFunctionCallSet(root map[string]any, segments []streamedFunctionCallPathSegment, value any, appendString bool) (map[string]any, error) {
	next := cloneStringAnyMap(root)
	if len(segments) == 0 {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("streamed function call root path requires object value")
		}
		for key, objectValue := range object {
			merged, err := mergeStreamedFunctionCallValue(next[key], objectValue, false)
			if err != nil {
				return nil, err
			}
			next[key] = merged
		}
		return next, nil
	}
	updated, err := streamedFunctionCallSetAt(next, segments, value, appendString)
	if err != nil {
		return nil, err
	}
	object, ok := updated.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("streamed function call root must remain an object")
	}
	return object, nil
}

func streamedFunctionCallSetAt(current any, segments []streamedFunctionCallPathSegment, value any, appendString bool) (any, error) {
	if len(segments) == 0 {
		return mergeStreamedFunctionCallValue(current, value, appendString)
	}
	segment := segments[0]
	if segment.array {
		var array []any
		if current != nil {
			var ok bool
			array, ok = current.([]any)
			if !ok {
				return nil, fmt.Errorf("streamed function call path conflict: expected array at index %d", segment.index)
			}
		}
		next := cloneAnySlice(array)
		for len(next) <= segment.index {
			next = append(next, nil)
		}
		child := next[segment.index]
		if child == nil && len(segments) > 1 {
			child = emptyContainerForSegment(segments[1])
		}
		updated, err := streamedFunctionCallSetAt(child, segments[1:], value, appendString)
		if err != nil {
			return nil, err
		}
		next[segment.index] = updated
		return next, nil
	}

	var object map[string]any
	if current != nil {
		var ok bool
		object, ok = current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("streamed function call path conflict: expected object at key %q", segment.key)
		}
	}
	next := cloneStringAnyMap(object)
	child := next[segment.key]
	if child == nil && len(segments) > 1 {
		child = emptyContainerForSegment(segments[1])
	}
	updated, err := streamedFunctionCallSetAt(child, segments[1:], value, appendString)
	if err != nil {
		return nil, err
	}
	next[segment.key] = updated
	return next, nil
}

func mergeStreamedFunctionCallValue(current any, value any, appendString bool) (any, error) {
	if current == nil && value == nil {
		return nil, nil
	}
	if currentMap, ok := current.(map[string]any); ok {
		valueMap, valueIsMap := value.(map[string]any)
		if !valueIsMap {
			if value == nil {
				return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite map with null")
			}
			return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite map with %T", value)
		}
		next := cloneStringAnyMap(currentMap)
		for key, child := range valueMap {
			merged, err := mergeStreamedFunctionCallValue(next[key], child, false)
			if err != nil {
				return nil, err
			}
			next[key] = merged
		}
		return next, nil
	}
	if valueMap, ok := value.(map[string]any); ok {
		if current != nil {
			return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite %T with map", current)
		}
		return cloneStringAnyMap(valueMap), nil
	}
	if currentSlice, ok := current.([]any); ok {
		valueSlice, valueIsSlice := value.([]any)
		if !valueIsSlice {
			return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite array with %T", value)
		}
		next := cloneAnySlice(currentSlice)
		if len(valueSlice) > len(next) {
			next = append(next, make([]any, len(valueSlice)-len(next))...)
		}
		for i, child := range valueSlice {
			merged, err := mergeStreamedFunctionCallValue(next[i], child, false)
			if err != nil {
				return nil, err
			}
			next[i] = merged
		}
		return next, nil
	}
	if valueSlice, ok := value.([]any); ok {
		if current != nil {
			return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite %T with array", current)
		}
		return cloneAnySlice(valueSlice), nil
	}
	if current == nil {
		return value, nil
	}
	if appendString {
		currentString, currentIsString := current.(string)
		valueString, valueIsString := value.(string)
		if currentIsString && valueIsString {
			return currentString + valueString, nil
		}
		return nil, fmt.Errorf("streamed function call path conflict: refusing to append %T to %T", value, current)
	}
	if reflect.DeepEqual(current, value) {
		return current, nil
	}
	return nil, fmt.Errorf("streamed function call path conflict: refusing to overwrite %T with %T", current, value)
}

func emptyContainerForSegment(segment streamedFunctionCallPathSegment) any {
	if segment.array {
		return []any{}
	}
	return map[string]any{}
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneAnySlice(in []any) []any {
	out := make([]any, len(in))
	for i, value := range in {
		out[i] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneStringAnyMap(typed)
	case []any:
		return cloneAnySlice(typed)
	default:
		return typed
	}
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// mergeStreamedFunctionCallTurn collapses a model turn made only of streamed
// function calls into one content. Completed calls appear once, in the order
// they first showed up, with final Args and no partial fragments.
func mergeStreamedFunctionCallTurn(contents []*Content) (*Content, bool) {
	if len(contents) == 0 {
		return nil, false
	}
	type generation struct {
		call *FunctionCall
	}
	var order []string
	slots := map[string]*generation{}
	active := map[string]string{}

	for _, content := range contents {
		if content == nil || len(content.Parts) == 0 {
			return nil, false
		}
		for _, part := range content.Parts {
			if part == nil || part.FunctionCall == nil || partHasNonFunctionCallPayload(part) {
				return nil, false
			}
			fc := part.FunctionCall
			identity := streamedFunctionCallKey(fc)
			genKey, ok := active[identity]
			if !ok {
				genKey = fmt.Sprintf("%s#%d", identity, len(order))
				active[identity] = genKey
				order = append(order, genKey)
				slots[genKey] = &generation{}
			}
			slots[genKey].call = fc
			if fc.WillContinue == nil || !*fc.WillContinue {
				delete(active, identity)
			}
		}
	}

	parts := make([]*Part, 0, len(order))
	for _, key := range order {
		fc := slots[key].call
		if fc.WillContinue != nil && *fc.WillContinue {
			continue
		}
		parts = append(parts, &Part{FunctionCall: &FunctionCall{
			ID:   fc.ID,
			Name: fc.Name,
			Args: cloneStringAnyMap(fc.Args),
		}})
	}
	return &Content{Role: RoleModel, Parts: parts}, true
}

func partHasNonFunctionCallPayload(part *Part) bool {
	return part.Text != "" ||
		part.InlineData != nil ||
		part.FileData != nil ||
		part.FunctionResponse != nil ||
		part.ExecutableCode != nil ||
		part.CodeExecutionResult != nil ||
		part.VideoMetadata != nil
}
