// Copyright 2024 The Carvel Authors.
// SPDX-License-Identifier: Apache-2.0

package orderedmap

import (
	"fmt"
	"sort"
	"unicode/utf8"
)

// SyntaxError describes a malformed JSONPath expression.
type SyntaxError struct {
	// Message explains what is wrong with the expression.
	Message string
	// Position is the byte offset within the expression where the problem was found.
	Position int
}

// Error formats the syntax error including its position.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("syntax error at position %d: %s", e.Position, e.Message)
}

// Query evaluates the JSONPath expression path against doc and returns every
// matching value. doc is expected to be composed of *Map, []interface{} and
// scalar values.
//
// An empty (non-nil) slice is returned when nothing matches. A malformed path
// yields a *SyntaxError.
func Query(doc interface{}, path string) ([]interface{}, error) {
	parsed, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	return parsed.evaluate(doc, doc), nil
}

// QueryOne evaluates the JSONPath expression path against doc and returns the
// first matching value. The boolean result reports whether anything matched.
func QueryOne(doc interface{}, path string) (interface{}, bool, error) {
	results, err := Query(doc, path)
	if err != nil {
		return nil, false, err
	}
	if len(results) == 0 {
		return nil, false, nil
	}
	return results[0], true, nil
}

type jsonPath []pathSegment

func (p jsonPath) evaluate(start, root interface{}) []interface{} {
	nodes := []interface{}{start}
	for _, segment := range p {
		nodes = segment.apply(nodes, root)
	}
	return nodes
}

type pathSegment struct {
	descendant bool
	selectors  []selector
}

func (s pathSegment) apply(nodes []interface{}, root interface{}) []interface{} {
	matches := []interface{}{}
	for _, node := range nodes {
		if !s.descendant {
			matches = s.applySelectors(matches, node, root)
			continue
		}
		for _, descendant := range appendSelfAndDescendants(nil, node) {
			matches = s.applySelectors(matches, descendant, root)
		}
	}
	return matches
}

func (s pathSegment) applySelectors(matches []interface{}, node, root interface{}) []interface{} {
	for _, sel := range s.selectors {
		matches = sel.appendMatches(matches, node, root)
	}
	return matches
}

type selector interface {
	appendMatches(matches []interface{}, node, root interface{}) []interface{}
}

type nameSelector string

func (s nameSelector) appendMatches(matches []interface{}, node, _ interface{}) []interface{} {
	if value, found := lookupKey(node, string(s)); found {
		return append(matches, value)
	}
	return matches
}

type indexSelector int

func (s indexSelector) appendMatches(matches []interface{}, node, _ interface{}) []interface{} {
	items, isArray := node.([]interface{})
	if !isArray {
		return matches
	}
	idx := int(s)
	if idx < 0 {
		idx += len(items)
	}
	return appendItemAt(matches, items, idx)
}

// fromEndSelector selects the item at index len(array)+offset, as written in
// script expressions such as `(@.length-1)`.
type fromEndSelector int

func (s fromEndSelector) appendMatches(matches []interface{}, node, _ interface{}) []interface{} {
	items, isArray := node.([]interface{})
	if !isArray || s >= 0 {
		return matches
	}
	return appendItemAt(matches, items, len(items)+int(s))
}

type wildcardSelector struct{}

func (wildcardSelector) appendMatches(matches []interface{}, node, _ interface{}) []interface{} {
	forEachChild(node, func(child interface{}) {
		matches = append(matches, child)
	})
	return matches
}

// selfSelector selects the node it is applied to. Combined with a descendant
// segment it implements `..*`, which yields a node followed by all of its
// descendants.
type selfSelector struct{}

func (selfSelector) appendMatches(matches []interface{}, node, _ interface{}) []interface{} {
	return append(matches, node)
}

type lengthSelector struct{}

func (lengthSelector) appendMatches(matches []interface{}, node, _ interface{}) []interface{} {
	if length, hasLength := lengthOf(node); hasLength {
		return append(matches, length)
	}
	return matches
}

type filterSelector struct {
	expr filterExpr
}

func (s filterSelector) appendMatches(matches []interface{}, node, root interface{}) []interface{} {
	forEachChild(node, func(child interface{}) {
		if isTruthy(s.expr.evaluate(child, root)) {
			matches = append(matches, child)
		}
	})
	return matches
}

func appendItemAt(matches []interface{}, items []interface{}, idx int) []interface{} {
	if idx < 0 || idx >= len(items) {
		return matches
	}
	return append(matches, items[idx])
}

// appendSelfAndDescendants appends node and all nodes nested within it, in
// depth-first pre-order.
func appendSelfAndDescendants(nodes []interface{}, node interface{}) []interface{} {
	nodes = append(nodes, node)
	forEachChild(node, func(child interface{}) {
		nodes = appendSelfAndDescendants(nodes, child)
	})
	return nodes
}

// forEachChild calls fn with every array item or map value held by node.
// Scalars have no children.
func forEachChild(node interface{}, fn func(child interface{})) {
	if items, isArray := node.([]interface{}); isArray {
		for _, item := range items {
			fn(item)
		}
		return
	}
	entries, _ := mapEntries(node)
	for _, entry := range entries {
		fn(entry.Value)
	}
}

func lookupKey(node interface{}, key string) (interface{}, bool) {
	switch typedNode := node.(type) {
	case *Map:
		return typedNode.Get(key)
	case map[string]interface{}:
		value, found := typedNode[key]
		return value, found
	case map[interface{}]interface{}:
		value, found := typedNode[key]
		return value, found
	}
	return nil, false
}

func lengthOf(node interface{}) (int, bool) {
	switch typedNode := node.(type) {
	case []interface{}:
		return len(typedNode), true
	case string:
		return utf8.RuneCountInString(typedNode), true
	}
	if entries, isMap := mapEntries(node); isMap {
		return len(entries), true
	}
	return 0, false
}

// mapEntries returns the entries of any supported map type. Go maps are
// unordered, so their entries are sorted by key to keep results stable.
func mapEntries(node interface{}) ([]MapItem, bool) {
	switch typedNode := node.(type) {
	case *Map:
		return typedNode.items, true
	case map[string]interface{}:
		entries := make([]MapItem, 0, len(typedNode))
		for key, value := range typedNode {
			entries = append(entries, MapItem{key, value})
		}
		return sortEntries(entries), true
	case map[interface{}]interface{}:
		entries := make([]MapItem, 0, len(typedNode))
		for key, value := range typedNode {
			entries = append(entries, MapItem{key, value})
		}
		return sortEntries(entries), true
	}
	return nil, false
}

func sortEntries(entries []MapItem) []MapItem {
	sort.Slice(entries, func(i, j int) bool {
		return fmt.Sprint(entries[i].Key) < fmt.Sprint(entries[j].Key)
	})
	return entries
}
