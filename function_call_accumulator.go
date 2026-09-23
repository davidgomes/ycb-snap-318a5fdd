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
	"strconv"
	"strings"
)

// streamedFunctionCall is the accumulated state of one streamed function call.
type streamedFunctionCall struct {
	id        string
	name      string
	args      map[string]any
	completed bool
	// Parts in which this call appeared, in arrival order.
	parts []*Part
	// JSON paths whose last fragment had willContinue=true.
	continuing map[string]bool
}

// functionCallAccumulator rebuilds function call arguments from streamed
// partialArgs fragments.
type functionCallAccumulator struct {
	// All calls seen, in order of first appearance.
	calls []*streamedFunctionCall
	// Calls that have not yet received a fragment with willContinue unset or false.
	inProgress []*streamedFunctionCall
}

func newFunctionCallAccumulator() *functionCallAccumulator {
	return &functionCallAccumulator{}
}

func (a *functionCallAccumulator) find(fc *FunctionCall) *streamedFunctionCall {
	if fc.ID != "" {
		for i := len(a.inProgress) - 1; i >= 0; i-- {
			if a.inProgress[i].id == fc.ID {
				return a.inProgress[i]
			}
		}
	}
	for i := len(a.inProgress) - 1; i >= 0; i-- {
		s := a.inProgress[i]
		if (fc.ID == "" || s.id == "") && (fc.Name == "" || s.name == "" || s.name == fc.Name) {
			return s
		}
	}
	return nil
}

func (a *functionCallAccumulator) removeInProgress(s *streamedFunctionCall) {
	for i, c := range a.inProgress {
		if c == s {
			a.inProgress = append(a.inProgress[:i], a.inProgress[i+1:]...)
			return
		}
	}
}

// accumulate merges fc into the state of its in-progress call and replaces
// fc.Args with a snapshot of the accumulated arguments.
func (a *functionCallAccumulator) accumulate(fc *FunctionCall, part *Part) error {
	if fc == nil {
		return nil
	}
	s := a.find(fc)
	if s == nil {
		s = &streamedFunctionCall{args: map[string]any{}, continuing: map[string]bool{}}
		a.calls = append(a.calls, s)
		a.inProgress = append(a.inProgress, s)
	}
	if s.id == "" {
		s.id = fc.ID
	}
	if s.name == "" {
		s.name = fc.Name
	}
	if part != nil {
		s.parts = append(s.parts, part)
	}

	if fc.Args != nil {
		merged, err := mergeJSONValue(s.args, fc.Args, "$")
		if err != nil {
			return fmt.Errorf("function call %q: %w", s.name, err)
		}
		s.args = merged.(map[string]any)
	}
	for _, pa := range fc.PartialArgs {
		if pa == nil {
			continue
		}
		if err := s.applyPartialArg(pa); err != nil {
			return fmt.Errorf("function call %q: %w", s.name, err)
		}
	}

	if fc.ID == "" {
		fc.ID = s.id
	}
	if fc.Name == "" {
		fc.Name = s.name
	}
	fc.Args = copyJSONValue(s.args).(map[string]any)

	if fc.WillContinue == nil || !*fc.WillContinue {
		s.completed = true
		a.removeInProgress(s)
	}
	return nil
}

func (a *functionCallAccumulator) accumulateResponse(resp *GenerateContentResponse) error {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil
	}
	for _, part := range resp.Candidates[0].Content.Parts {
		if part == nil || part.FunctionCall == nil {
			continue
		}
		if err := a.accumulate(part.FunctionCall, part); err != nil {
			return err
		}
	}
	return nil
}

func (a *functionCallAccumulator) accumulateCalls(calls []*FunctionCall) error {
	for _, fc := range calls {
		if err := a.accumulate(fc, nil); err != nil {
			return err
		}
	}
	return nil
}

// completedParts returns one part per completed call, in order of first
// appearance, carrying the final arguments and no streaming fields.
func (a *functionCallAccumulator) completedParts() []*Part {
	var parts []*Part
	for _, s := range a.calls {
		if !s.completed {
			continue
		}
		p := &Part{}
		for _, sp := range s.parts {
			if p.ThoughtSignature == nil && sp.ThoughtSignature != nil {
				p.ThoughtSignature = sp.ThoughtSignature
			}
			if !p.Thought && sp.Thought {
				p.Thought = sp.Thought
			}
		}
		p.FunctionCall = &FunctionCall{
			ID:   s.id,
			Name: s.name,
			Args: copyJSONValue(s.args).(map[string]any),
		}
		parts = append(parts, p)
	}
	return parts
}

func (s *streamedFunctionCall) applyPartialArg(pa *PartialArg) error {
	segments, err := parseJSONPath(pa.JsonPath)
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		return fmt.Errorf("partial argument at json path %q cannot replace the arguments object", pa.JsonPath)
	}
	key := canonicalJSONPath(segments)

	var value any
	isString := false
	switch {
	case pa.NULLValue != "":
		value = nil
	case pa.BoolValue != nil:
		value = *pa.BoolValue
	case pa.NumberValue != nil:
		value = *pa.NumberValue
	default:
		value = pa.StringValue
		isString = true
	}

	appendString := false
	if s.continuing[key] {
		existing, found := getJSONPath(s.args, segments)
		existingStr, existingIsString := existing.(string)
		if !found || !existingIsString || !isString {
			return fmt.Errorf("partial argument at json path %q continues a value of an incompatible type", pa.JsonPath)
		}
		value = existingStr + pa.StringValue
		appendString = true
	}

	updated, err := setJSONPath(s.args, segments, value, pa.JsonPath, appendString)
	if err != nil {
		return err
	}
	s.args = updated.(map[string]any)

	if pa.WillContinue != nil && *pa.WillContinue {
		s.continuing[key] = true
	} else {
		delete(s.continuing, key)
	}
	return nil
}

// jsonPathSegment is either a field name or an array index.
type jsonPathSegment struct {
	field   string
	index   int
	isIndex bool
}

// parseJSONPath parses the subset of RFC 9535 used for streamed arguments:
// the root "$", dot-separated field names, bracket-quoted field names and
// zero-based array indexes.
func parseJSONPath(path string) ([]jsonPathSegment, error) {
	if !strings.HasPrefix(path, "$") {
		return nil, fmt.Errorf("unsupported json path %q: must start with $", path)
	}
	var segments []jsonPathSegment
	i := 1
	for i < len(path) {
		switch path[i] {
		case '.':
			i++
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("unsupported json path %q: empty field name", path)
			}
			segments = append(segments, jsonPathSegment{field: path[start:i]})
		case '[':
			i++
			if i >= len(path) {
				return nil, fmt.Errorf("unsupported json path %q: unterminated bracket", path)
			}
			if quote := path[i]; quote == '\'' || quote == '"' {
				i++
				var sb strings.Builder
				closed := false
				for i < len(path) {
					c := path[i]
					if c == '\\' && i+1 < len(path) {
						sb.WriteByte(path[i+1])
						i += 2
						continue
					}
					if c == quote {
						closed = true
						i++
						break
					}
					sb.WriteByte(c)
					i++
				}
				if !closed || i >= len(path) || path[i] != ']' {
					return nil, fmt.Errorf("unsupported json path %q: unterminated quoted field name", path)
				}
				i++
				segments = append(segments, jsonPathSegment{field: sb.String()})
			} else {
				start := i
				for i < len(path) && path[i] >= '0' && path[i] <= '9' {
					i++
				}
				if start == i || i >= len(path) || path[i] != ']' {
					return nil, fmt.Errorf("unsupported json path %q: invalid array index", path)
				}
				idx, err := strconv.Atoi(path[start:i])
				if err != nil {
					return nil, fmt.Errorf("unsupported json path %q: %w", path, err)
				}
				i++
				segments = append(segments, jsonPathSegment{index: idx, isIndex: true})
			}
		default:
			return nil, fmt.Errorf("unsupported json path %q: unexpected character %q", path, path[i])
		}
	}
	return segments, nil
}

func canonicalJSONPath(segments []jsonPathSegment) string {
	var sb strings.Builder
	sb.WriteString("$")
	for _, seg := range segments {
		if seg.isIndex {
			sb.WriteString("[" + strconv.Itoa(seg.index) + "]")
		} else {
			sb.WriteString("[" + strconv.Quote(seg.field) + "]")
		}
	}
	return sb.String()
}

func getJSONPath(root any, segments []jsonPathSegment) (any, bool) {
	cur := root
	for _, seg := range segments {
		if seg.isIndex {
			arr, ok := cur.([]any)
			if !ok || seg.index >= len(arr) {
				return nil, false
			}
			cur = arr[seg.index]
		} else {
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			v, ok := obj[seg.field]
			if !ok {
				return nil, false
			}
			cur = v
		}
	}
	return cur, true
}

func isJSONContainer(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

// setJSONPath sets value at segments within cur and returns the updated cur.
// Containers are created as needed; an existing value of a different shape is
// reported as an error rather than overwritten.
func setJSONPath(cur any, segments []jsonPathSegment, value any, path string, replaceString bool) (any, error) {
	if len(segments) == 0 {
		if cur != nil && !replaceString && (isJSONContainer(cur) || isJSONContainer(value)) {
			return nil, fmt.Errorf("partial argument at json path %q conflicts with an existing value of a different shape", path)
		}
		return value, nil
	}
	seg := segments[0]
	if seg.isIndex {
		var arr []any
		switch v := cur.(type) {
		case nil:
		case []any:
			arr = v
		default:
			return nil, fmt.Errorf("partial argument at json path %q requires an array where a %T exists", path, cur)
		}
		for len(arr) <= seg.index {
			arr = append(arr, nil)
		}
		child, err := setJSONPath(arr[seg.index], segments[1:], value, path, replaceString)
		if err != nil {
			return nil, err
		}
		arr[seg.index] = child
		return arr, nil
	}
	var obj map[string]any
	switch v := cur.(type) {
	case nil:
		obj = map[string]any{}
	case map[string]any:
		obj = v
	default:
		return nil, fmt.Errorf("partial argument at json path %q requires an object where a %T exists", path, cur)
	}
	child, err := setJSONPath(obj[seg.field], segments[1:], value, path, replaceString)
	if err != nil {
		return nil, err
	}
	obj[seg.field] = child
	return obj, nil
}

// mergeJSONValue deep-merges src into dst. Objects are merged key by key and
// arrays element by element; mismatched container shapes are an error.
func mergeJSONValue(dst, src any, path string) (any, error) {
	switch s := src.(type) {
	case map[string]any:
		var d map[string]any
		switch v := dst.(type) {
		case nil:
			d = map[string]any{}
		case map[string]any:
			d = v
		default:
			return nil, fmt.Errorf("args at json path %q conflict with an existing value of a different shape", path)
		}
		for k, sv := range s {
			merged, err := mergeJSONValue(d[k], sv, path+"["+strconv.Quote(k)+"]")
			if err != nil {
				return nil, err
			}
			d[k] = merged
		}
		return d, nil
	case []any:
		var d []any
		switch v := dst.(type) {
		case nil:
		case []any:
			d = v
		default:
			return nil, fmt.Errorf("args at json path %q conflict with an existing value of a different shape", path)
		}
		for len(d) < len(s) {
			d = append(d, nil)
		}
		for i, sv := range s {
			merged, err := mergeJSONValue(d[i], sv, path+"["+strconv.Itoa(i)+"]")
			if err != nil {
				return nil, err
			}
			d[i] = merged
		}
		return d, nil
	default:
		if isJSONContainer(dst) {
			return nil, fmt.Errorf("args at json path %q conflict with an existing value of a different shape", path)
		}
		return copyJSONValue(src), nil
	}
}

func copyJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = copyJSONValue(val)
		}
		return m
	case []any:
		arr := make([]any, len(t))
		for i, val := range t {
			arr[i] = copyJSONValue(val)
		}
		return arr
	default:
		return v
	}
}
