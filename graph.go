package task

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/go-task/task/v3/errors"
	"github.com/go-task/task/v3/internal/editors"
	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/internal/templater"
	"github.com/go-task/task/v3/taskfile/ast"
)

const (
	GraphFormatJSON = "json"
	GraphFormatDOT  = "dot"
	GraphFormatText = "text"

	GraphEdgeTypeDep = "dep"
	GraphEdgeTypeCmd = "cmd"
)

type (
	// TaskGraph is the dependency graph of a set of root tasks.
	TaskGraph struct {
		Roots       []string              `json:"roots"`
		Nodes       map[string]*GraphNode `json:"nodes"`
		Edges       []*GraphEdge          `json:"edges"`
		DepthGroups [][]string            `json:"depth_groups"`
		LongestPath []string              `json:"longest_path"`
	}
	// GraphNode describes a single task in a [TaskGraph].
	GraphNode struct {
		Name     string            `json:"name"`
		Desc     string            `json:"desc"`
		Location *editors.Location `json:"location"`
		UpToDate *bool             `json:"up_to_date,omitempty"`
		Deps     []string          `json:"deps"`
		Method   string            `json:"method"`
	}
	// GraphEdge describes a call from one task to another, either through a
	// "deps" entry or a task-calling command in "cmds".
	GraphEdge struct {
		From string         `json:"from"`
		To   string         `json:"to"`
		Type string         `json:"type"`
		Vars map[string]any `json:"vars"`
	}
)

// Graph prints the dependency graph of the given tasks in the format set by
// [WithGraphFormat]. If no calls are given, the default task is used.
func (e *Executor) Graph(calls ...*Call) error {
	format := e.GraphFormat
	if format == "" {
		format = GraphFormatJSON
	}
	if format != GraphFormatJSON && format != GraphFormatDOT && format != GraphFormatText {
		return fmt.Errorf(`task: Invalid graph format %q. Valid formats are "json", "dot" and "text"`, format)
	}

	g, err := e.TaskGraph(calls...)
	if err != nil {
		return err
	}

	switch format {
	case GraphFormatDOT:
		return g.writeDOT(e.Stdout, !e.GraphNoStatus)
	case GraphFormatText:
		return g.writeText(e.Stdout)
	default:
		encoder := json.NewEncoder(e.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(g)
	}
}

// TaskGraph builds the dependency graph of the given tasks. If no calls are
// given, the default task is used. When [WithGraphReverse] is set, the graph is
// inverted and contains every task that depends on the given tasks.
func (e *Executor) TaskGraph(calls ...*Call) (*TaskGraph, error) {
	if len(calls) == 0 {
		calls = []*Call{{Task: "default"}}
	}

	b := &graphBuilder{
		e:     e,
		tasks: map[string]*ast.Task{},
		edges: map[string][]*GraphEdge{},
	}

	roots := make([]string, 0, len(calls))
	for _, call := range calls {
		name, err := b.resolve(call)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(roots, name) {
			roots = append(roots, name)
		}
	}

	next := func(name string) ([]*GraphEdge, error) {
		return b.outgoing(name, false)
	}
	if e.GraphReverse {
		reversed, err := b.reversedEdges()
		if err != nil {
			return nil, err
		}
		next = func(name string) ([]*GraphEdge, error) {
			return reversed[name], nil
		}
	}

	adjacency, err := walkGraph(roots, next)
	if err != nil {
		return nil, err
	}

	g := &TaskGraph{
		Roots: roots,
		Nodes: make(map[string]*GraphNode, len(adjacency)),
		Edges: []*GraphEdge{},
	}
	names := make([]string, 0, len(adjacency))
	for name := range adjacency {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		node, err := b.node(name, adjacency[name])
		if err != nil {
			return nil, err
		}
		g.Nodes[name] = node
		g.Edges = append(g.Edges, adjacency[name]...)
	}

	levels := g.levels()
	g.DepthGroups = depthGroups(levels)
	g.LongestPath = g.longestPath(levels)

	return g, nil
}

type graphBuilder struct {
	e *Executor
	// tasks holds the compiled tasks indexed by their fully qualified name.
	tasks map[string]*ast.Task
	// edges holds the outgoing (non-reversed) edges of each compiled task.
	edges map[string][]*GraphEdge
}

// resolve returns the name of the task matching the given call, compiling and
// caching it the first time it is seen. Aliases and wildcards are resolved to
// the name of the task that defines them.
func (b *graphBuilder) resolve(call *Call) (string, error) {
	t, err := b.e.GetTask(call)
	if err != nil {
		return "", err
	}
	if _, ok := b.tasks[t.Task]; ok {
		return t.Task, nil
	}
	compiled, err := b.e.CompiledTask(call)
	if err != nil {
		return "", err
	}
	b.tasks[t.Task] = compiled
	return t.Task, nil
}

// outgoing returns the edges from the given (already resolved) task to the
// tasks it calls. Calls to tasks that do not exist cause an error unless
// skipMissing is set, in which case they are left out.
func (b *graphBuilder) outgoing(name string, skipMissing bool) ([]*GraphEdge, error) {
	if edges, ok := b.edges[name]; ok {
		return edges, nil
	}

	t := b.tasks[name]
	edges := []*GraphEdge{}
	addEdge := func(taskName string, vars *ast.Vars, edgeType string) error {
		to, err := b.resolve(&Call{Task: taskName, Vars: vars.DeepCopy(), Indirect: true})
		if err != nil {
			if _, ok := err.(*errors.TaskNotFoundError); ok && skipMissing {
				return nil
			}
			return err
		}
		edges = append(edges, &GraphEdge{
			From: name,
			To:   to,
			Type: edgeType,
			Vars: graphVars(vars),
		})
		return nil
	}

	for _, dep := range t.Deps {
		if dep == nil || dep.Task == "" {
			continue
		}
		if err := addEdge(dep.Task, dep.Vars, GraphEdgeTypeDep); err != nil {
			return nil, err
		}
	}
	for _, cmd := range t.Cmds {
		if cmd == nil || cmd.Task == "" {
			continue
		}
		taskName, vars := cmd.Task, cmd.Vars
		// Deferred commands are not templated when the task is compiled
		if cmd.Defer {
			cache := &templater.Cache{Vars: t.Vars}
			taskName = templater.Replace(taskName, cache)
			vars = templater.ReplaceVars(vars, cache)
		}
		if err := addEdge(taskName, vars, GraphEdgeTypeCmd); err != nil {
			return nil, err
		}
	}

	b.edges[name] = edges
	return edges, nil
}

// reversedEdges compiles every task in the Taskfile and returns their edges
// inverted, indexed by the task they now start from.
func (b *graphBuilder) reversedEdges() (map[string][]*GraphEdge, error) {
	reversed := map[string][]*GraphEdge{}
	for name := range b.e.Taskfile.Tasks.Keys(nil) {
		resolved, err := b.resolve(&Call{Task: name})
		if err != nil {
			return nil, err
		}
		edges, err := b.outgoing(resolved, true)
		if err != nil {
			return nil, err
		}
		for _, edge := range edges {
			reversed[edge.To] = append(reversed[edge.To], &GraphEdge{
				From: edge.To,
				To:   edge.From,
				Type: edge.Type,
				Vars: edge.Vars,
			})
		}
	}
	for _, edges := range reversed {
		slices.SortStableFunc(edges, func(a, b *GraphEdge) int {
			return strings.Compare(a.To, b.To)
		})
	}
	return reversed, nil
}

func (b *graphBuilder) node(name string, edges []*GraphEdge) (*GraphNode, error) {
	t := b.tasks[name]

	method := b.e.Taskfile.Method
	if t.Method != "" {
		method = t.Method
	}

	node := &GraphNode{
		Name:   name,
		Desc:   t.Desc,
		Deps:   edgeTargets(edges),
		Method: method,
	}
	slices.Sort(node.Deps)
	if t.Location != nil {
		node.Location = &editors.Location{
			Line:     t.Location.Line,
			Column:   t.Location.Column,
			Taskfile: t.Location.Taskfile,
		}
	}

	if !b.e.GraphNoStatus {
		// Always check in dry mode so that inspecting the graph never updates
		// the stored fingerprints.
		upToDate, err := fingerprint.IsTaskUpToDate(context.Background(), t,
			fingerprint.WithMethod(method),
			fingerprint.WithTempDir(b.e.TempDir.Fingerprint),
			fingerprint.WithDry(true),
			fingerprint.WithLogger(b.e.Logger),
		)
		if err != nil {
			return nil, err
		}
		node.UpToDate = &upToDate
	}

	return node, nil
}

// walkGraph does a depth-first traversal from the given roots and returns the
// outgoing edges of every reachable task. It returns a
// [errors.TaskDependencyCycleError] if a cycle is found.
func walkGraph(roots []string, next func(string) ([]*GraphEdge, error)) (map[string][]*GraphEdge, error) {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := map[string]int{}
	adjacency := map[string][]*GraphEdge{}
	var stack []string

	var visit func(name string) error
	visit = func(name string) error {
		state[name] = visiting
		stack = append(stack, name)

		edges, err := next(name)
		if err != nil {
			return err
		}
		adjacency[name] = edges

		for _, child := range edgeTargets(edges) {
			switch state[child] {
			case visiting:
				start := slices.Index(stack, child)
				cycle := append(slices.Clone(stack[start:]), child)
				return &errors.TaskDependencyCycleError{Tasks: cycle}
			case unvisited:
				if err := visit(child); err != nil {
					return err
				}
			}
		}

		stack = stack[:len(stack)-1]
		state[name] = visited
		return nil
	}

	for _, root := range roots {
		if state[root] == unvisited {
			if err := visit(root); err != nil {
				return nil, err
			}
		}
	}
	return adjacency, nil
}

// levels returns the depth level of every node: 0 for nodes without
// dependencies, otherwise one more than the highest level of its dependencies.
func (g *TaskGraph) levels() map[string]int {
	levels := make(map[string]int, len(g.Nodes))
	var level func(name string) int
	level = func(name string) int {
		if l, ok := levels[name]; ok {
			return l
		}
		l := 0
		for _, dep := range g.Nodes[name].Deps {
			l = max(l, level(dep)+1)
		}
		levels[name] = l
		return l
	}
	for name := range g.Nodes {
		level(name)
	}
	return levels
}

func depthGroups(levels map[string]int) [][]string {
	groups := [][]string{}
	for name, l := range levels {
		for len(groups) <= l {
			groups = append(groups, []string{})
		}
		groups[l] = append(groups[l], name)
	}
	for _, group := range groups {
		slices.Sort(group)
	}
	return groups
}

// longestPath returns the longest chain of tasks from a root to a leaf,
// root-first. Ties are broken by root order and then alphabetically.
func (g *TaskGraph) longestPath(levels map[string]int) []string {
	path := []string{}
	if len(g.Roots) == 0 {
		return path
	}
	current := g.Roots[0]
	for _, root := range g.Roots[1:] {
		if levels[root] > levels[current] {
			current = root
		}
	}
	for {
		path = append(path, current)
		if levels[current] == 0 {
			return path
		}
		// Deps are sorted, so the first match is the alphabetically first one
		for _, dep := range g.Nodes[current].Deps {
			if levels[dep] == levels[current]-1 {
				current = dep
				break
			}
		}
	}
}

func (g *TaskGraph) writeDOT(w io.Writer, showStatus bool) error {
	var sb strings.Builder
	sb.WriteString("digraph tasks {\n")
	names := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		node := g.Nodes[name]
		if showStatus && node.UpToDate != nil && *node.UpToDate {
			fmt.Fprintf(&sb, "  %s [style=dashed];\n", dotQuote(name))
		} else {
			fmt.Fprintf(&sb, "  %s;\n", dotQuote(name))
		}
	}
	for _, edge := range g.Edges {
		fmt.Fprintf(&sb, "  %s -> %s;\n", dotQuote(edge.From), dotQuote(edge.To))
	}
	sb.WriteString("}\n")
	_, err := io.WriteString(w, sb.String())
	return err
}

func (g *TaskGraph) writeText(w io.Writer) error {
	var sb strings.Builder
	seen := map[string]bool{}
	var write func(name string, depth int)
	write = func(name string, depth int) {
		indent := strings.Repeat("  ", depth)
		if seen[name] {
			fmt.Fprintf(&sb, "%s%s (repeated)\n", indent, name)
			return
		}
		seen[name] = true
		fmt.Fprintf(&sb, "%s%s\n", indent, name)
		for _, dep := range g.Nodes[name].Deps {
			write(dep, depth+1)
		}
	}
	for _, root := range g.Roots {
		write(root, 0)
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

// edgeTargets returns the unique targets of the given edges in order.
func edgeTargets(edges []*GraphEdge) []string {
	targets := []string{}
	for _, edge := range edges {
		if !slices.Contains(targets, edge.To) {
			targets = append(targets, edge.To)
		}
	}
	return targets
}

func graphVars(vars *ast.Vars) map[string]any {
	m := map[string]any{}
	if vars == nil {
		return m
	}
	for name, v := range vars.All() {
		switch {
		case v.Value != nil:
			m[name] = v.Value
		case v.Sh != nil:
			m[name] = map[string]string{"sh": *v.Sh}
		case v.Ref != "":
			m[name] = map[string]string{"ref": v.Ref}
		default:
			m[name] = nil
		}
	}
	return m
}

func dotQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
