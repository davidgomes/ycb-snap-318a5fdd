package genai

import (
	"fmt"
	"strconv"
	"strings"
)

type partialArgsState struct {
	args          map[string]any
	continuedPath map[string]bool
}

func (s *partialArgsState) addArgs(args map[string]any) error {
	for key, value := range args {
		old, ok := s.args[key]
		if !ok {
			s.args[key] = cloneJSONValue(value)
			continue
		}
		merged, err := mergeJSONValues(old, value)
		if err != nil {
			return fmt.Errorf("incompatible shapes at JSON path $.%s: %w", key, err)
		}
		s.args[key] = merged
	}
	return nil
}

func mergeJSONValues(old, value any) (any, error) {
	oldMap, oldIsMap := old.(map[string]any)
	valueMap, valueIsMap := value.(map[string]any)
	if oldIsMap != valueIsMap {
		return nil, fmt.Errorf("cannot merge an object with a non-object")
	}
	if oldIsMap {
		for key, item := range valueMap {
			if existing, ok := oldMap[key]; ok {
				merged, err := mergeJSONValues(existing, item)
				if err != nil {
					return nil, err
				}
				oldMap[key] = merged
			} else {
				oldMap[key] = cloneJSONValue(item)
			}
		}
		return oldMap, nil
	}
	return cloneJSONValue(value), nil
}

type jsonPathPart struct {
	name  string
	index *int
}

func parsePartialJSONPath(path string) ([]jsonPathPart, error) {
	if path == "$" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "$") {
		return nil, fmt.Errorf("JSON path must start with $")
	}
	var parts []jsonPathPart
	for i := 1; i < len(path); {
		switch path[i] {
		case '.':
			start := i + 1
			i = start
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("empty field name")
			}
			parts = append(parts, jsonPathPart{name: path[start:i]})
		case '[':
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated bracket")
			}
			end += i
			expression := path[i+1 : end]
			if len(expression) >= 2 && (expression[0] == '"' || expression[0] == '\'') && expression[len(expression)-1] == expression[0] {
				name, err := strconv.Unquote(expression)
				if expression[0] == '\'' {
					name, err = strconv.Unquote(`"` + strings.ReplaceAll(expression[1:len(expression)-1], `"`, `\"`) + `"`)
				}
				if err != nil {
					return nil, fmt.Errorf("invalid quoted field name: %w", err)
				}
				parts = append(parts, jsonPathPart{name: name})
			} else {
				index, err := strconv.Atoi(expression)
				if err != nil || index < 0 {
					return nil, fmt.Errorf("array index must be a non-negative integer")
				}
				parts = append(parts, jsonPathPart{index: &index})
			}
			i = end + 1
		default:
			return nil, fmt.Errorf("expected . or [")
		}
	}
	return parts, nil
}

func (s *partialArgsState) addPartialArg(partial *PartialArg) error {
	if partial == nil {
		return nil
	}
	parts, err := parsePartialJSONPath(partial.JsonPath)
	if err != nil {
		return fmt.Errorf("invalid partial argument path %q: %w", partial.JsonPath, err)
	}
	value := partialArgValue(partial)
	if len(parts) == 0 {
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("root partial argument must be a JSON object")
		}
		if err := s.addArgs(object); err != nil {
			return fmt.Errorf("incompatible shapes at JSON path %s: %w", partial.JsonPath, err)
		}
	} else if err := setPartialJSONValue(s.args, parts, value, partial.JsonPath, s.continuedPath[partial.JsonPath]); err != nil {
		return err
	}
	s.continuedPath[partial.JsonPath] = partial.WillContinue != nil && *partial.WillContinue
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

func setPartialJSONValue(root map[string]any, parts []jsonPathPart, value any, path string, appendString bool) error {
	var current any = root
	for i, part := range parts {
		last := i == len(parts)-1
		if part.index == nil {
			object, ok := current.(map[string]any)
			if !ok {
				return fmt.Errorf("incompatible shapes at JSON path %s", path)
			}
			if last {
				if old, exists := object[part.name]; exists && appendString {
					oldString, oldOK := old.(string)
					newString, newOK := value.(string)
					if !oldOK || !newOK {
						return fmt.Errorf("incompatible shapes at JSON path %s", path)
					}
					object[part.name] = oldString + newString
				} else if old, exists := object[part.name]; exists && incompatibleJSONShape(old, value) {
					return fmt.Errorf("incompatible shapes at JSON path %s", path)
				} else {
					object[part.name] = cloneJSONValue(value)
				}
				return nil
			}
			next := parts[i+1]
			child, exists := object[part.name]
			if !exists {
				if next.index == nil {
					child = map[string]any{}
				} else {
					child = []any{}
				}
				object[part.name] = child
			}
			if incompatibleContainer(child, next.index == nil) {
				return fmt.Errorf("incompatible shapes at JSON path %s", path)
			}
			current = child
		} else {
			array, ok := current.([]any)
			if !ok {
				return fmt.Errorf("incompatible shapes at JSON path %s", path)
			}
			index := *part.index
			for len(array) <= index {
				array = append(array, nil)
			}
			if last {
				if old := array[index]; old != nil && appendString {
					oldString, oldOK := old.(string)
					newString, newOK := value.(string)
					if !oldOK || !newOK {
						return fmt.Errorf("incompatible shapes at JSON path %s", path)
					}
					array[index] = oldString + newString
				} else if old := array[index]; old != nil && incompatibleJSONShape(old, value) {
					return fmt.Errorf("incompatible shapes at JSON path %s", path)
				} else {
					array[index] = cloneJSONValue(value)
				}
				replaceSliceAtParent(root, parts[:i], array)
				return nil
			}
			if array[index] == nil {
				if parts[i+1].index == nil {
					array[index] = map[string]any{}
				} else {
					array[index] = []any{}
				}
			}
			if incompatibleContainer(array[index], parts[i+1].index == nil) {
				return fmt.Errorf("incompatible shapes at JSON path %s", path)
			}
			replaceSliceAtParent(root, parts[:i], array)
			current = array[index]
		}
	}
	return nil
}

// replaceSliceAtParent updates the root map after growing a nested slice.
func replaceSliceAtParent(root map[string]any, parts []jsonPathPart, value []any) {
	if len(parts) == 0 {
		return
	}
	var current any = root
	for i, part := range parts {
		last := i == len(parts)-1
		if part.index == nil {
			object, ok := current.(map[string]any)
			if !ok {
				return
			}
			if last {
				object[part.name] = value
				return
			}
			current = object[part.name]
		} else {
			array, ok := current.([]any)
			if !ok || *part.index >= len(array) {
				return
			}
			if last {
				array[*part.index] = value
				return
			}
			current = array[*part.index]
		}
	}
}

func incompatibleContainer(value any, wantObject bool) bool {
	if wantObject {
		_, ok := value.(map[string]any)
		return !ok
	}
	_, ok := value.([]any)
	return !ok
}

func incompatibleJSONShape(old, value any) bool {
	_, oldMap := old.(map[string]any)
	_, valueMap := value.(map[string]any)
	_, oldSlice := old.([]any)
	_, valueSlice := value.([]any)
	return oldMap != valueMap || oldSlice != valueSlice || (oldMap && valueSlice) || (oldSlice && valueMap)
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = cloneJSONValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = cloneJSONValue(item)
		}
		return result
	default:
		return value
	}
}

func accumulateFunctionCall(states map[string]*partialArgsState, call *FunctionCall) (*FunctionCall, error) {
	if call == nil {
		return nil, nil
	}
	state, ok := states[call.ID]
	if !ok {
		state = &partialArgsState{args: map[string]any{}, continuedPath: map[string]bool{}}
		states[call.ID] = state
	}
	if err := state.addArgs(call.Args); err != nil {
		return nil, err
	}
	for _, partial := range call.PartialArgs {
		if err := state.addPartialArg(partial); err != nil {
			return nil, err
		}
	}
	result := *call
	result.Args = state.args
	if call.WillContinue == nil || !*call.WillContinue {
		delete(states, call.ID)
	}
	return &result, nil
}
