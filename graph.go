package task

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/taskfile/ast"
)

const (
	GraphFormatJSON = "json"
	GraphFormatDOT  = "dot"
	GraphFormatText = "text"

	GraphEdgeDep = "dep"
	GraphEdgeCmd = "cmd"
)

type (
	// GraphOutput is the JSON representation of a task dependency graph.
	GraphOutput struct {
		Roots       []string              `json:"roots"`
		Nodes       map[string]*GraphNode `json:"nodes"`
		Edges       []*GraphEdge          `json:"edges"`
		DepthGroups [][]string            `json:"depth_groups"`
		LongestPath []string              `json:"longest_path"`
	}
	// GraphNode describes a single task in the dependency graph.
	GraphNode struct {
		Name     string         `json:"name"`
		Desc     string         `json:"desc"`
		Location *GraphLocation `json:"location"`
		UpToDate *bool          `json:"up_to_date,omitempty"`
		Deps     []string       `json:"deps"`
		Method   string         `json:"method"`
	}
	// GraphLocation describes where a task is defined.
	GraphLocation struct {
		Taskfile string `json:"taskfile"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
	}
	// GraphEdge is a single "from depends on to" relationship. In reverse mode
	// the direction is inverted.
	GraphEdge struct {
		From string         `json:"from"`
		To   string         `json:"to"`
		Type string         `json:"type"`
		Vars map[string]any `json:"vars"`
	}
)

// WithGraphFormat sets the output format used by [Executor.Graph]. Valid
// values are "json" (default), "dot" and "text".
func WithGraphFormat(format string) ExecutorOption {
	return &graphFormatOption{format}
}

type graphFormatOption struct {
	format string
}

func (o *graphFormatOption) ApplyToExecutor(e *Executor) {
	e.GraphFormat = o.format
}

// WithGraphReverse tells [Executor.Graph] to show the tasks that depend on the
// given tasks instead of the tasks they depend on.
func WithGraphReverse(reverse bool) ExecutorOption {
	return &graphReverseOption{reverse}
}

type graphReverseOption struct {
	reverse bool
}

func (o *graphReverseOption) ApplyToExecutor(e *Executor) {
	e.GraphReverse = o.reverse
}

// WithGraphNoStatus tells [Executor.Graph] not to compute whether tasks are up
// to date.
func WithGraphNoStatus(noStatus bool) ExecutorOption {
	return &graphNoStatusOption{noStatus}
}

type graphNoStatusOption struct {
	noStatus bool
}

func (o *graphNoStatusOption) ApplyToExecutor(e *Executor) {
	e.GraphNoStatus = o.noStatus
}

type taskGraph struct {
	tasks map[string]*ast.Task
	edges []*GraphEdge
	out   map[string][]string
}

// Graph prints the dependency graph of the given calls to stdout using the
// configured graph format. If no calls are given, the default task is used.
func (e *Executor) Graph(calls ...*Call) error {
	format := e.GraphFormat
	if format == "" {
		format = GraphFormatJSON
	}
	if format != GraphFormatJSON && format != GraphFormatDOT && format != GraphFormatText {
		return fmt.Errorf("task: unknown graph format %q, must be one of: json, dot, text", format)
	}

	if len(calls) == 0 {
		calls = []*Call{{Task: "default"}}
	}

	g := &taskGraph{
		tasks: map[string]*ast.Task{},
		out:   map[string][]string{},
	}

	roots := make([]string, 0, len(calls))
	for _, call := range calls {
		name, err := e.graphVisit(g, call)
		if err != nil {
			return err
		}
		if !slices.Contains(roots, name) {
			roots = append(roots, name)
		}
	}

	if e.GraphReverse {
		for task := range e.Taskfile.Tasks.Values(nil) {
			if _, err := e.graphVisit(g, &Call{Task: task.Task}); err != nil {
				return err
			}
		}
		g = g.reversed()
	}

	reachable := g.reachable(roots)
	if err := g.checkCycles(roots); err != nil {
		return err
	}

	output := &GraphOutput{
		Roots:       roots,
		Nodes:       make(map[string]*GraphNode, len(reachable)),
		Edges:       []*GraphEdge{},
		DepthGroups: g.depthGroups(reachable),
		LongestPath: g.longestPath(roots),
	}
	for _, edge := range g.edges {
		if slices.Contains(reachable, edge.From) {
			output.Edges = append(output.Edges, edge)
		}
	}
	for _, name := range reachable {
		node, err := e.graphNode(g, name)
		if err != nil {
			return err
		}
		output.Nodes[name] = node
	}

	switch format {
	case GraphFormatDOT:
		return writeGraphDOT(e.Stdout, output, reachable)
	case GraphFormatText:
		return writeGraphText(e.Stdout, output)
	default:
		encoder := json.NewEncoder(e.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}
}

// graphVisit resolves and compiles the task for the given call, records its
// outgoing edges and recursively visits its dependencies. It returns the
// canonical name of the task.
func (e *Executor) graphVisit(g *taskGraph, call *Call) (string, error) {
	call = &Call{Task: call.Task, Vars: call.Vars.DeepCopy()}
	origTask, err := e.GetTask(call)
	if err != nil {
		return "", err
	}
	name := origTask.Task
	if _, ok := g.tasks[name]; ok {
		return name, nil
	}

	t, err := e.FastCompiledTask(call)
	if err != nil {
		return "", err
	}
	g.tasks[name] = t
	g.out[name] = []string{}

	type target struct {
		task     string
		vars     *ast.Vars
		edgeType string
	}
	var targets []target
	for _, dep := range t.Deps {
		if dep.Task != "" {
			targets = append(targets, target{dep.Task, dep.Vars, GraphEdgeDep})
		}
	}
	for _, cmd := range t.Cmds {
		if cmd.Task != "" {
			targets = append(targets, target{cmd.Task, cmd.Vars, GraphEdgeCmd})
		}
	}

	depNames := make([]string, len(targets))
	for i, tgt := range targets {
		depTask, err := e.GetTask(&Call{Task: tgt.task, Vars: tgt.vars.DeepCopy()})
		if err != nil {
			return "", err
		}
		depName := depTask.Task
		depNames[i] = depName
		g.edges = append(g.edges, &GraphEdge{
			From: name,
			To:   depName,
			Type: tgt.edgeType,
			Vars: graphVars(tgt.vars),
		})
		if !slices.Contains(g.out[name], depName) {
			g.out[name] = append(g.out[name], depName)
		}
	}
	for i, tgt := range targets {
		if _, ok := g.tasks[depNames[i]]; ok {
			continue
		}
		if _, err := e.graphVisit(g, &Call{Task: tgt.task, Vars: tgt.vars}); err != nil {
			return "", err
		}
	}
	return name, nil
}

func (g *taskGraph) reversed() *taskGraph {
	r := &taskGraph{
		tasks: g.tasks,
		edges: make([]*GraphEdge, 0, len(g.edges)),
		out:   make(map[string][]string, len(g.out)),
	}
	for name := range g.tasks {
		r.out[name] = []string{}
	}
	for _, edge := range g.edges {
		r.edges = append(r.edges, &GraphEdge{From: edge.To, To: edge.From, Type: edge.Type, Vars: edge.Vars})
		if !slices.Contains(r.out[edge.To], edge.From) {
			r.out[edge.To] = append(r.out[edge.To], edge.From)
		}
	}
	return r
}

// reachable returns the sorted names of all tasks reachable from the roots.
func (g *taskGraph) reachable(roots []string) []string {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		for _, next := range g.out[name] {
			visit(next)
		}
	}
	for _, root := range roots {
		visit(root)
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (g *taskGraph) checkCycles(roots []string) error {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case visiting:
			start := slices.Index(stack, name)
			cycle := append(slices.Clone(stack[start:]), name)
			return fmt.Errorf("task: dependency cycle detected: %s", strings.Join(cycle, " -> "))
		case done:
			return nil
		}
		state[name] = visiting
		stack = append(stack, name)
		for _, next := range sortedCopy(g.out[name]) {
			if err := visit(next); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = done
		return nil
	}
	for _, root := range roots {
		if err := visit(root); err != nil {
			return err
		}
	}
	return nil
}

// depthGroups groups tasks by their height: tasks without outgoing edges are
// at level 0, and every other task is one level above its highest child.
// Must only be called on an acyclic graph.
func (g *taskGraph) depthGroups(names []string) [][]string {
	levels := map[string]int{}
	var level func(string) int
	level = func(name string) int {
		if l, ok := levels[name]; ok {
			return l
		}
		l := 0
		for _, next := range g.out[name] {
			l = max(l, level(next)+1)
		}
		levels[name] = l
		return l
	}
	groups := [][]string{}
	for _, name := range names {
		l := level(name)
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

// longestPath returns the longest chain from any root to a leaf. Ties are
// broken by root order and then alphabetically. Must only be called on an
// acyclic graph.
func (g *taskGraph) longestPath(roots []string) []string {
	memo := map[string][]string{}
	var longest func(string) []string
	longest = func(name string) []string {
		if p, ok := memo[name]; ok {
			return p
		}
		var best []string
		for _, next := range sortedCopy(g.out[name]) {
			if p := longest(next); len(p) > len(best) {
				best = p
			}
		}
		p := append([]string{name}, best...)
		memo[name] = p
		return p
	}
	best := []string{}
	for _, root := range roots {
		if p := longest(root); len(p) > len(best) {
			best = p
		}
	}
	return best
}

func (e *Executor) graphNode(g *taskGraph, name string) (*GraphNode, error) {
	t := g.tasks[name]
	method := e.Taskfile.Method
	if t.Method != "" {
		method = t.Method
	}
	node := &GraphNode{
		Name:     name,
		Desc:     t.Desc,
		Location: &GraphLocation{},
		Deps:     sortedCopy(g.out[name]),
		Method:   method,
	}
	if t.Location != nil {
		node.Location = &GraphLocation{
			Taskfile: t.Location.Taskfile,
			Line:     t.Location.Line,
			Column:   t.Location.Column,
		}
	}
	if !e.GraphNoStatus {
		upToDate, err := fingerprint.IsTaskUpToDate(context.Background(), t,
			fingerprint.WithMethod(method),
			fingerprint.WithTempDir(e.TempDir.Fingerprint),
			fingerprint.WithDry(true),
			fingerprint.WithLogger(e.Logger),
		)
		if err != nil {
			return nil, err
		}
		node.UpToDate = &upToDate
	}
	return node, nil
}

func graphVars(vars *ast.Vars) map[string]any {
	m := map[string]any{}
	if vars == nil {
		return m
	}
	for k, v := range vars.All() {
		switch {
		case v.Value != nil:
			m[k] = v.Value
		case v.Sh != nil:
			m[k] = map[string]any{"sh": *v.Sh}
		case v.Ref != "":
			m[k] = map[string]any{"ref": v.Ref}
		default:
			m[k] = nil
		}
	}
	return m
}

func sortedCopy(s []string) []string {
	c := slices.Clone(s)
	if c == nil {
		c = []string{}
	}
	slices.Sort(c)
	return c
}

func dotQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func writeGraphDOT(w io.Writer, g *GraphOutput, names []string) error {
	var b strings.Builder
	b.WriteString("digraph tasks {\n")
	for _, name := range names {
		node := g.Nodes[name]
		if node.UpToDate != nil && *node.UpToDate {
			fmt.Fprintf(&b, "  %s [style=dashed];\n", dotQuote(name))
		} else {
			fmt.Fprintf(&b, "  %s;\n", dotQuote(name))
		}
	}
	for _, edge := range g.Edges {
		fmt.Fprintf(&b, "  %s -> %s;\n", dotQuote(edge.From), dotQuote(edge.To))
	}
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeGraphText(w io.Writer, g *GraphOutput) error {
	var b strings.Builder
	expanded := map[string]bool{}
	var visit func(name string, depth int)
	visit = func(name string, depth int) {
		indent := strings.Repeat("  ", depth)
		if expanded[name] {
			fmt.Fprintf(&b, "%s%s (repeated)\n", indent, name)
			return
		}
		expanded[name] = true
		fmt.Fprintf(&b, "%s%s\n", indent, name)
		for _, dep := range g.Nodes[name].Deps {
			visit(dep, depth+1)
		}
	}
	for _, root := range g.Roots {
		visit(root, 0)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
