// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package genai

import (
	"fmt"
	"iter"
	"strconv"
	"strings"
)

type streamedCallState struct {
	args         map[string]any
	continuePath string
}

// functionCallAccumulator rebuilds Args for function calls streamed through partialArgs.
type functionCallAccumulator struct {
	inProgress map[string]*streamedCallState
}

func streamedCallKey(fc *FunctionCall) string {
	if fc.ID != "" {
		return "id:" + fc.ID
	}
	return "name:" + fc.Name
}

func (a *functionCallAccumulator) apply(fc *FunctionCall) error {
	if fc == nil {
		return nil
	}
	key := streamedCallKey(fc)
	state, ok := a.inProgress[key]
	if !ok {
		if len(fc.PartialArgs) == 0 && (fc.WillContinue == nil || !*fc.WillContinue) {
			return nil
		}
		state = &streamedCallState{args: map[string]any{}}
	}
	if fc.Args != nil {
		merged, err := mergeJSONValue(state.args, deepCopyJSON(fc.Args), "$")
		if err != nil {
			return err
		}
		state.args = merged.(map[string]any)
	}
	for _, pa := range fc.PartialArgs {
		if pa == nil {
			continue
		}
		if err := state.applyPartial(pa); err != nil {
			return fmt.Errorf("function call %q: %w", fc.Name, err)
		}
	}
	fc.Args = deepCopyJSON(state.args).(map[string]any)
	if fc.WillContinue != nil && *fc.WillContinue {
		if a.inProgress == nil {
			a.inProgress = map[string]*streamedCallState{}
		}
		a.inProgress[key] = state
	} else {
		delete(a.inProgress, key)
	}
	return nil
}

func (a *functionCallAccumulator) applyResponse(r *GenerateContentResponse) error {
	if r == nil {
		return nil
	}
	for _, c := range r.Candidates {
		if c == nil || c.Content == nil {
			continue
		}
		for _, p := range c.Content.Parts {
			if p != nil {
				if err := a.apply(p.FunctionCall); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *streamedCallState) applyPartial(pa *PartialArg) error {
	path, err := parseJSONPath(pa.JsonPath)
	if err != nil {
		return err
	}
	appendMode := s.continuePath != "" && s.continuePath == pa.JsonPath
	var value any
	switch {
	case pa.NULLValue != "":
		value = nil
	case pa.NumberValue != nil:
		value = *pa.NumberValue
	case pa.BoolValue != nil:
		value = *pa.BoolValue
	default:
		value = pa.StringValue
	}
	if len(path) == 0 {
		m, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("cannot set non-object value at %q", pa.JsonPath)
		}
		s.args = m
	} else {
		root, err := setJSONPath(s.args, path, value, appendMode, pa.JsonPath)
		if err != nil {
			return err
		}
		s.args = root.(map[string]any)
	}
	if pa.WillContinue != nil && *pa.WillContinue {
		s.continuePath = pa.JsonPath
	} else {
		s.continuePath = ""
	}
	return nil
}

// parseJSONPath returns path segments; strings are field names, ints are array indexes.
func parseJSONPath(p string) ([]any, error) {
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("invalid JSON path %q: must start with $", p)
	}
	var segs []any
	i := 1
	for i < len(p) {
		switch p[i] {
		case '.':
			j := i + 1
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				j++
			}
			if j == i+1 {
				return nil, fmt.Errorf("invalid JSON path %q: empty field name", p)
			}
			segs = append(segs, p[i+1:j])
			i = j
		case '[':
			end := strings.IndexByte(p[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("invalid JSON path %q: unterminated bracket", p)
			}
			inner := p[i+1 : i+end]
			if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') && inner[len(inner)-1] == inner[0] {
				segs = append(segs, inner[1:len(inner)-1])
			} else {
				n, err := strconv.Atoi(inner)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("invalid JSON path %q: bad index %q", p, inner)
				}
				segs = append(segs, n)
			}
			i += end + 1
		default:
			return nil, fmt.Errorf("invalid JSON path %q: unexpected character %q", p, p[i])
		}
	}
	return segs, nil
}

func setJSONPath(node any, path []any, value any, appendMode bool, full string) (any, error) {
	if len(path) == 0 {
		if appendMode {
			if s, ok := value.(string); ok {
				if existing, ok := node.(string); ok {
					return existing + s, nil
				}
			}
		}
		if node != nil {
			if _, isMap := node.(map[string]any); isMap {
				return nil, fmt.Errorf("incompatible shape at %q: object vs scalar", full)
			}
			if _, isArr := node.([]any); isArr {
				return nil, fmt.Errorf("incompatible shape at %q: array vs scalar", full)
			}
		}
		return value, nil
	}
	switch seg := path[0].(type) {
	case string:
		var m map[string]any
		if node == nil {
			m = map[string]any{}
		} else if mm, ok := node.(map[string]any); ok {
			m = mm
		} else {
			return nil, fmt.Errorf("incompatible shape at %q: expected object", full)
		}
		child, err := setJSONPath(m[seg], path[1:], value, appendMode, full)
		if err != nil {
			return nil, err
		}
		m[seg] = child
		return m, nil
	case int:
		var arr []any
		if node == nil {
			arr = []any{}
		} else if aa, ok := node.([]any); ok {
			arr = aa
		} else {
			return nil, fmt.Errorf("incompatible shape at %q: expected array", full)
		}
		for len(arr) <= seg {
			arr = append(arr, nil)
		}
		child, err := setJSONPath(arr[seg], path[1:], value, appendMode, full)
		if err != nil {
			return nil, err
		}
		arr[seg] = child
		return arr, nil
	}
	return nil, fmt.Errorf("invalid JSON path %q", full)
}

func mergeJSONValue(dst, src any, path string) (any, error) {
	switch s := src.(type) {
	case map[string]any:
		if dst == nil {
			return s, nil
		}
		d, ok := dst.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("incompatible shape at %q: expected object", path)
		}
		for k, v := range s {
			merged, err := mergeJSONValue(d[k], v, path+"."+k)
			if err != nil {
				return nil, err
			}
			d[k] = merged
		}
		return d, nil
	case []any:
		if dst == nil {
			return s, nil
		}
		d, ok := dst.([]any)
		if !ok {
			return nil, fmt.Errorf("incompatible shape at %q: expected array", path)
		}
		for i, v := range s {
			if i >= len(d) {
				d = append(d, v)
				continue
			}
			merged, err := mergeJSONValue(d[i], v, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			d[i] = merged
		}
		return d, nil
	default:
		if dst != nil && src != nil {
			switch dst.(type) {
			case map[string]any, []any:
				return nil, fmt.Errorf("incompatible shape at %q: container vs scalar", path)
			}
		}
		return src, nil
	}
}

func deepCopyJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = deepCopyJSON(val)
		}
		return m
	case []any:
		a := make([]any, len(t))
		for i, val := range t {
			a[i] = deepCopyJSON(val)
		}
		return a
	default:
		return v
	}
}

func accumulateStreamedFunctionCalls(seq iter.Seq2[*GenerateContentResponse, error]) iter.Seq2[*GenerateContentResponse, error] {
	return func(yield func(*GenerateContentResponse, error) bool) {
		acc := &functionCallAccumulator{}
		for resp, err := range seq {
			if err == nil {
				if aerr := acc.applyResponse(resp); aerr != nil {
					yield(nil, aerr)
					return
				}
			}
			if !yield(resp, err) {
				return
			}
		}
	}
}

// mergeStreamedFunctionCallTurn collapses a model turn made entirely of streamed
// function calls into one content with each completed call exactly once.
func mergeStreamedFunctionCallTurn(contents []*Content) []*Content {
	streamed := false
	var parts []*Part
	for _, c := range contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p == nil || p.FunctionCall == nil {
				return contents
			}
			fc := p.FunctionCall
			if len(fc.PartialArgs) > 0 || (fc.WillContinue != nil && *fc.WillContinue) {
				streamed = true
			}
			parts = append(parts, p)
		}
	}
	if !streamed || len(parts) == 0 {
		return contents
	}
	var merged []*Part
	open := map[string]*Part{}
	for _, p := range parts {
		fc := p.FunctionCall
		key := streamedCallKey(fc)
		target, ok := open[key]
		if !ok {
			cp := *p
			cp.FunctionCall = &FunctionCall{ID: fc.ID, Name: fc.Name}
			target = &cp
			merged = append(merged, target)
		}
		if fc.Name != "" {
			target.FunctionCall.Name = fc.Name
		}
		if fc.Args != nil {
			target.FunctionCall.Args = fc.Args
		}
		if fc.WillContinue != nil && *fc.WillContinue {
			open[key] = target
		} else {
			delete(open, key)
		}
	}
	return []*Content{{Role: RoleModel, Parts: merged}}
}
