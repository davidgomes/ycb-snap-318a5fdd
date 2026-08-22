package genai

import (
	"fmt"
	"strconv"
	"strings"
)

type jsonPathPart struct {
	key   string
	index int
	isIdx bool
}

type streamedCallArgs struct {
	args       map[string]any
	continuing map[string]bool
}

func newStreamedCallArgs(args map[string]any) *streamedCallArgs {
	result := &streamedCallArgs{args: map[string]any{}, continuing: map[string]bool{}}
	for key, value := range args {
		result.args[key] = cloneStreamValue(value)
	}
	return result
}

func cloneStreamValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, item := range value {
			result[key] = cloneStreamValue(item)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = cloneStreamValue(item)
		}
		return result
	default:
		return value
	}
}

func (a *streamedCallArgs) apply(call *FunctionCall) error {
	if len(call.PartialArgs) == 0 {
		call.Args = a.args
		return nil
	}
	for _, partial := range call.PartialArgs {
		if partial == nil {
			continue
		}
		parts, err := parseJSONPath(partial.JsonPath)
		if err != nil {
			return err
		}
		value := partialArgValue(partial)
		appendValue := a.continuing[partial.JsonPath]
		if err := setStreamValue(a.args, parts, value, appendValue); err != nil {
			return fmt.Errorf("apply partial argument %q: %w", partial.JsonPath, err)
		}
		if partial.WillContinue != nil && *partial.WillContinue {
			a.continuing[partial.JsonPath] = true
		} else {
			delete(a.continuing, partial.JsonPath)
		}
	}
	call.Args = a.args
	return nil
}

func partialArgValue(partial *PartialArg) any {
	if partial.NULLValue != "" {
		return nil
	}
	if partial.BoolValue != nil {
		return *partial.BoolValue
	}
	if partial.NumberValue != nil {
		return *partial.NumberValue
	}
	return partial.StringValue
}

func parseJSONPath(path string) ([]jsonPathPart, error) {
	if path == "$" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "$") {
		return nil, fmt.Errorf("invalid JSON path %q", path)
	}
	var result []jsonPathPart
	for i := 1; i < len(path); {
		switch path[i] {
		case '.':
			i++
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("invalid JSON path %q", path)
			}
			result = append(result, jsonPathPart{key: path[start:i]})
		case '[':
			i++
			if i < len(path) && (path[i] == '\'' || path[i] == '"') {
				quote := path[i]
				start := i
				i++
				for i < len(path) {
					if path[i] == '\\' {
						i += 2
						continue
					}
					if path[i] == quote {
						break
					}
					i++
				}
				if i >= len(path) || path[i] != quote || i+1 >= len(path) || path[i+1] != ']' {
					return nil, fmt.Errorf("invalid JSON path %q", path)
				}
				quoted := path[start : i+1]
				if quote == '\'' {
					quoted = `"` + strings.ReplaceAll(strings.ReplaceAll(quoted[1:len(quoted)-1], `"`, `\"`), `\'`, `'`) + `"`
				}
				key, err := strconv.Unquote(quoted)
				if err != nil {
					return nil, fmt.Errorf("invalid JSON path %q: %w", path, err)
				}
				result = append(result, jsonPathPart{key: key})
				i += 2
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
			result = append(result, jsonPathPart{index: index, isIdx: true})
			i++
		default:
			return nil, fmt.Errorf("invalid JSON path %q", path)
		}
	}
	return result, nil
}

func setStreamValue(root map[string]any, path []jsonPathPart, value any, appendValue bool) error {
	if len(path) == 0 {
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("root value must be an object")
		}
		for key, item := range object {
			if existing, exists := root[key]; exists {
				if isContainer(existing) != isContainer(item) {
					return fmt.Errorf("incompatible shapes at root field %q", key)
				}
			}
			root[key] = item
		}
		return nil
	}
	_, err := setStreamValueAny(root, path, value, appendValue)
	return err
}

func setStreamValueAny(current any, path []jsonPathPart, value any, appendValue bool) (any, error) {
	part := path[0]
	if len(path) == 1 {
		if part.isIdx {
			array, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("expected array at final path component")
			}
			for len(array) <= part.index {
				array = append(array, nil)
			}
			if existing := array[part.index]; existing != nil && isContainer(existing) != isContainer(value) {
				return nil, fmt.Errorf("incompatible shapes at array index %d", part.index)
			}
			if appendValue {
				old, ok := array[part.index].(string)
				next, nextOK := value.(string)
				if !ok || !nextOK {
					return nil, fmt.Errorf("cannot append non-string values")
				}
				value = old + next
			}
			array[part.index] = value
			return array, nil
		}
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected object at field %q", part.key)
		}
		if existing, exists := object[part.key]; exists && isContainer(existing) != isContainer(value) {
			return nil, fmt.Errorf("incompatible shapes at field %q", part.key)
		}
		if appendValue {
			old, ok := object[part.key].(string)
			next, nextOK := value.(string)
			if !ok || !nextOK {
				return nil, fmt.Errorf("cannot append non-string values")
			}
			value = old + next
		}
		object[part.key] = value
		return object, nil
	}

	if part.isIdx {
		array, ok := current.([]any)
		if !ok {
			return nil, fmt.Errorf("expected array at path component")
		}
		for len(array) <= part.index {
			array = append(array, nil)
		}
		child := array[part.index]
		if child == nil {
			child = newStreamContainer(path[1])
		} else if !isExpectedStreamContainer(child, path[1]) {
			return nil, fmt.Errorf("incompatible shape at array index %d", part.index)
		}
		updated, err := setStreamValueAny(child, path[1:], value, appendValue)
		if err != nil {
			return nil, err
		}
		array[part.index] = updated
		return array, nil
	}

	object, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object at field %q", part.key)
	}
	child, exists := object[part.key]
	if !exists {
		child = newStreamContainer(path[1])
	} else if !isExpectedStreamContainer(child, path[1]) {
		return nil, fmt.Errorf("incompatible shape at field %q", part.key)
	}
	updated, err := setStreamValueAny(child, path[1:], value, appendValue)
	if err != nil {
		return nil, err
	}
	object[part.key] = updated
	return object, nil
}

func newStreamContainer(next jsonPathPart) any {
	if next.isIdx {
		return make([]any, next.index+1)
	}
	return map[string]any{}
}

func isExpectedStreamContainer(value any, next jsonPathPart) bool {
	if value == nil {
		return true
	}
	if next.isIdx {
		_, ok := value.([]any)
		return ok
	}
	_, ok := value.(map[string]any)
	return ok
}

func isContainer(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

func applyStreamedFunctionCalls(response *GenerateContentResponse, states map[string]*streamedCallArgs) error {
	if len(response.Candidates) == 0 || response.Candidates[0].Content == nil {
		return nil
	}
	for _, part := range response.Candidates[0].Content.Parts {
		if part == nil || part.FunctionCall == nil {
			continue
		}
		call := part.FunctionCall
		state, exists := states[call.ID]
		if !exists {
			state = newStreamedCallArgs(call.Args)
			states[call.ID] = state
		}
		if err := state.apply(call); err != nil {
			return err
		}
		if call.WillContinue == nil || !*call.WillContinue {
			delete(states, call.ID)
		}
	}
	return nil
}

func applyStreamedLiveToolCalls(message *LiveServerMessage, states map[string]*streamedCallArgs) error {
	if message.ToolCall == nil {
		return nil
	}
	for _, call := range message.ToolCall.FunctionCalls {
		if call == nil {
			continue
		}
		state, exists := states[call.ID]
		if !exists {
			state = newStreamedCallArgs(call.Args)
			states[call.ID] = state
		}
		if err := state.apply(call); err != nil {
			return err
		}
		if call.WillContinue == nil || !*call.WillContinue {
			delete(states, call.ID)
		}
	}
	return nil
}

func consolidateStreamedFunctionCallContents(contents []*Content) []*Content {
	if len(contents) == 0 {
		return contents
	}
	var calls []*FunctionCall
	active := map[string]*FunctionCall{}
	for _, content := range contents {
		if content == nil || len(content.Parts) == 0 {
			return contents
		}
		for _, part := range content.Parts {
			if part == nil || part.FunctionCall == nil {
				return contents
			}
			call := part.FunctionCall
			current, exists := active[call.ID]
			if !exists {
				current = &FunctionCall{ID: call.ID, Name: call.Name, Args: cloneStreamArgs(call.Args)}
				calls = append(calls, current)
			} else {
				current.Name = call.Name
				current.Args = cloneStreamArgs(call.Args)
			}
			if call.WillContinue != nil && *call.WillContinue {
				active[call.ID] = current
			} else {
				delete(active, call.ID)
			}
		}
	}
	if len(calls) == 0 {
		return contents
	}
	parts := make([]*Part, 0, len(calls))
	for _, call := range calls {
		parts = append(parts, &Part{FunctionCall: call})
	}
	return []*Content{{Role: RoleModel, Parts: parts}}
}

func cloneStreamArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	return cloneStreamValue(args).(map[string]any)
}
