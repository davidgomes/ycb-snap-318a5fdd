package task

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/go-task/task/v3/errors"
	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/internal/sort"
	"github.com/go-task/task/v3/internal/templater"
	"github.com/go-task/task/v3/taskfile/ast"
)

// Output formats supported by [Executor.Graph].
const (
	GraphFormatJSON = "json"
	GraphFormatDOT  = "dot"
	GraphFormatText = "text"
)

const (
	graphEdgeDep = "dep"
	graphEdgeCmd = "cmd"
)

type (
	taskGraph struct {
		Roots       []string              `json:"roots"`
		Nodes       map[string]*graphNode `json:"nodes"`
		Edges       []*graphEdge          `json:"edges"`
		DepthGroups [][]string            `json:"depth_groups"`
		LongestPath []string              `json:"longest_path"`
	}
	graphNode struct {
		Name     string        `json:"name"`
		Desc     string        `json:"desc"`
		Location graphLocation `json:"location"`
		UpToDate *bool         `json:"up_to_date,omitempty"`
		Deps     []string      `json:"deps"`
		Method   string        `json:"method"`

		task *ast.Task
	}
	graphLocation struct {
		Taskfile string `json:"taskfile"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
	}
	graphEdge struct {
		From string         `json:"from"`
		To   string         `json:"to"`
		Type string         `json:"type"`
		Vars map[string]any `json:"vars"`
	}
	// graphCall is an outgoing task call found in the deps or cmds of a task.
	graphCall struct {
		edgeType string
		task     string
		vars     *ast.Vars
	}
	graphBuilder struct {
		e *Executor
		// skipMissing drops calls to tasks that do not exist instead of
		// returning an error.
		skipMissing bool
		// resolved maps a call (task name and vars) to the name of its node.
		resolved   map[string]string
		expansions map[string]int
		tasks      map[string]*ast.Task
		edges      []*graphEdge
		edgeCounts map[string]int
	}
)

// Graph prints the dependency graph of the given calls to the [Executor]'s
// stdout, using the format set by [WithGraphFormat]. If no calls are given, the
// default task is used. When [WithGraphReverse] is set, the graph shows every
// task in the Taskfile that depends on the given tasks instead.
func (e *Executor) Graph(calls ...*Call) error {
	format := cmp.Or(e.GraphFormat, GraphFormatJSON)
	if !slices.Contains([]string{GraphFormatJSON, GraphFormatDOT, GraphFormatText}, format) {
		return fmt.Errorf("task: Invalid graph format %q. Valid formats are: json, dot, text", format)
	}
	if len(calls) == 0 {
		calls = []*Call{{Task: "default"}}
	}

	g, err := e.buildTaskGraph(calls)
	if err != nil {
		return err
	}
	if cycle := g.findCycle(); cycle != nil {
		if e.GraphReverse {
			slices.Reverse(cycle)
		}
		return &errors.TaskCycleError{TaskNames: cycle}
	}
	g.computeDepths()

	if format != GraphFormatText && !e.GraphNoStatus {
		if err := e.setGraphStatus(g); err != nil {
			return err
		}
	}

	switch format {
	case GraphFormatDOT:
		return g.writeDOT(e.Stdout)
	case GraphFormatText:
		return g.writeText(e.Stdout)
	default:
		encoder := json.NewEncoder(e.Stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(g)
	}
}

func (e *Executor) buildTaskGraph(calls []*Call) (*taskGraph, error) {
	b := &graphBuilder{
		e:           e,
		skipMissing: e.GraphReverse,
		resolved:    map[string]string{},
		expansions:  map[string]int{},
		tasks:       map[string]*ast.Task{},
		edgeCounts:  map[string]int{},
	}

	roots := make([]string, 0, len(calls))
	for _, call := range calls {
		name, err := b.visit(call)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(roots, name) {
			roots = append(roots, name)
		}
	}

	edges := b.edges
	if e.GraphReverse {
		// Finding every dependent requires the outgoing calls of every task.
		// Tasks are compiled without the vars their callers would pass, so
		// calls that only resolve with those vars are skipped.
		for name := range e.Taskfile.Tasks.Keys(sort.AlphaNumeric) {
			if _, err := b.visit(&Call{Task: name}); err != nil {
				return nil, err
			}
		}
		edges = make([]*graphEdge, len(b.edges))
		for i, edge := range b.edges {
			edges[i] = &graphEdge{From: edge.To, To: edge.From, Type: edge.Type, Vars: edge.Vars}
		}
	}

	outgoing := map[string][]*graphEdge{}
	for _, edge := range edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}

	g := &taskGraph{
		Roots: roots,
		Nodes: map[string]*graphNode{},
		Edges: []*graphEdge{},
	}
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, ok := g.Nodes[name]; ok {
			continue
		}
		t := b.tasks[name]
		node := &graphNode{
			Name:   name,
			Desc:   t.Desc,
			Deps:   []string{},
			Method: cmp.Or(t.Method, e.Taskfile.Method),
			task:   t,
		}
		if t.Location != nil {
			node.Location = graphLocation{
				Taskfile: t.Location.Taskfile,
				Line:     t.Location.Line,
				Column:   t.Location.Column,
			}
		}
		for _, edge := range outgoing[name] {
			g.Edges = append(g.Edges, edge)
			if !slices.Contains(node.Deps, edge.To) {
				node.Deps = append(node.Deps, edge.To)
			}
			queue = append(queue, edge.To)
		}
		slices.Sort(node.Deps)
		g.Nodes[name] = node
	}
	slices.SortStableFunc(g.Edges, func(a, b *graphEdge) int {
		return strings.Compare(a.From, b.From)
	})

	return g, nil
}

// visit compiles the task for the given call, records the calls it makes as
// edges and recursively visits them. It returns the name of the call's node.
func (b *graphBuilder) visit(call *Call) (string, error) {
	// The key must be computed before compiling, as it adds MATCH to the vars.
	key := call.Task + "\x00" + graphVarsKey(graphVars(call.Vars))
	if name, ok := b.resolved[key]; ok {
		return name, nil
	}
	t, err := b.e.FastCompiledTask(call)
	if err != nil {
		return "", err
	}
	name := t.FullName
	b.resolved[key] = name
	if _, ok := b.tasks[name]; !ok {
		b.tasks[name] = t
	}

	// A task is expanded once for each distinct set of vars it is called with,
	// since its calls may depend on them. The limit stops recursive tasks whose
	// vars change on every call from being expanded forever.
	if b.expansions[name] >= MaximumTaskCall {
		return name, nil
	}
	b.expansions[name]++

	// Merge edges with the ones found by previous expansions of this task, but
	// keep duplicates within an expansion so each for-loop iteration has one.
	counts := map[string]int{}
	for _, c := range graphCalls(t) {
		to, err := b.visit(&Call{Task: c.task, Vars: c.vars.DeepCopy(), Indirect: true})
		var notFound *errors.TaskNotFoundError
		if b.skipMissing && errors.As(err, &notFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		edge := &graphEdge{From: name, To: to, Type: c.edgeType, Vars: graphVars(c.vars)}
		edgeKey := strings.Join([]string{edge.From, edge.To, edge.Type, graphVarsKey(edge.Vars)}, "\x00")
		counts[edgeKey]++
		if counts[edgeKey] > b.edgeCounts[edgeKey] {
			b.edgeCounts[edgeKey] = counts[edgeKey]
			b.edges = append(b.edges, edge)
		}
	}
	return name, nil
}

// graphCalls returns the task calls made by a compiled task, in the order they
// would run: deps first, then task-calling commands.
func graphCalls(t *ast.Task) []graphCall {
	var calls []graphCall
	for _, dep := range t.Deps {
		if dep != nil && dep.Task != "" {
			calls = append(calls, graphCall{edgeType: graphEdgeDep, task: dep.Task, vars: dep.Vars})
		}
	}
	for _, cmd := range t.Cmds {
		if cmd == nil || cmd.Task == "" {
			continue
		}
		taskName, vars := cmd.Task, cmd.Vars
		// Deferred commands are not templated when the task is compiled.
		if cmd.Defer {
			cache := &templater.Cache{Vars: t.Vars}
			taskName = templater.Replace(cmd.Task, cache)
			vars = templater.ReplaceVars(cmd.Vars, cache)
		}
		if taskName != "" {
			calls = append(calls, graphCall{edgeType: graphEdgeCmd, task: taskName, vars: vars})
		}
	}
	return calls
}

func graphVars(vars *ast.Vars) map[string]any {
	m := make(map[string]any, vars.Len())
	for k, v := range vars.All() {
		if v.Value == nil && v.Sh != nil {
			m[k] = map[string]any{"sh": *v.Sh}
			continue
		}
		m[k] = v.Value
	}
	return m
}

func graphVarsKey(vars map[string]any) string {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		fmt.Fprintf(&b, "%s=%#v\x00", k, vars[k])
	}
	return b.String()
}

// findCycle returns the first cycle reachable from the roots, starting and
// ending with the same task, or nil if the graph is acyclic.
func (g *taskGraph) findCycle() []string {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(g.Nodes))
	var stack, cycle []string
	var visit func(name string) bool
	visit = func(name string) bool {
		state[name] = visiting
		stack = append(stack, name)
		for _, dep := range g.Nodes[name].Deps {
			switch state[dep] {
			case visiting:
				cycle = append(slices.Clone(stack[slices.Index(stack, dep):]), dep)
				return true
			case unvisited:
				if visit(dep) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = visited
		return false
	}
	for _, root := range g.Roots {
		if state[root] == unvisited && visit(root) {
			return cycle
		}
	}
	return nil
}

// computeDepths sets the depth groups and longest path of an acyclic graph.
// A task's depth is 0 if it has no dependencies, or one more than the deepest
// of its dependencies otherwise.
func (g *taskGraph) computeDepths() {
	depths := make(map[string]int, len(g.Nodes))
	var depth func(name string) int
	depth = func(name string) int {
		if d, ok := depths[name]; ok {
			return d
		}
		d := 0
		for _, dep := range g.Nodes[name].Deps {
			d = max(d, depth(dep)+1)
		}
		depths[name] = d
		return d
	}

	g.DepthGroups = [][]string{}
	for _, name := range slices.Sorted(maps.Keys(g.Nodes)) {
		d := depth(name)
		for len(g.DepthGroups) <= d {
			g.DepthGroups = append(g.DepthGroups, []string{})
		}
		g.DepthGroups[d] = append(g.DepthGroups[d], name)
	}

	// Ties are broken by root order, then alphabetically.
	current := g.Roots[0]
	for _, root := range g.Roots[1:] {
		if depths[root] > depths[current] {
			current = root
		}
	}
	g.LongestPath = []string{current}
	for deps := g.Nodes[current].Deps; len(deps) > 0; deps = g.Nodes[current].Deps {
		current = deps[0]
		for _, dep := range deps[1:] {
			if depths[dep] > depths[current] {
				current = dep
			}
		}
		g.LongestPath = append(g.LongestPath, current)
	}
}

func (e *Executor) setGraphStatus(g *taskGraph) error {
	var eg errgroup.Group
	for _, node := range g.Nodes {
		eg.Go(func() error {
			upToDate, err := fingerprint.IsTaskUpToDate(context.Background(), node.task,
				fingerprint.WithMethod(node.Method),
				fingerprint.WithTempDir(e.TempDir.Fingerprint),
				// A non-dry check stores the new checksum of the sources, which
				// would make the next run skip the task.
				fingerprint.WithDry(true),
				fingerprint.WithLogger(e.Logger),
			)
			if err != nil {
				return err
			}
			node.UpToDate = &upToDate
			return nil
		})
	}
	return eg.Wait()
}

func (g *taskGraph) writeDOT(w io.Writer) error {
	var b strings.Builder
	b.WriteString("digraph tasks {\n")
	for _, name := range slices.Sorted(maps.Keys(g.Nodes)) {
		fmt.Fprintf(&b, "  %s", dotID(name))
		if upToDate := g.Nodes[name].UpToDate; upToDate != nil && *upToDate {
			b.WriteString(" [style=dashed]")
		}
		b.WriteString(";\n")
	}
	for _, edge := range g.Edges {
		fmt.Fprintf(&b, "  %s -> %s;\n", dotID(edge.From), dotID(edge.To))
	}
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func dotID(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// writeText prints each root as an indented tree. A task that has already been
// printed is marked as repeated and its dependencies are not printed again.
func (g *taskGraph) writeText(w io.Writer) error {
	var b strings.Builder
	printed := make(map[string]bool, len(g.Nodes))
	var write func(name string, depth int)
	write = func(name string, depth int) {
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString(name)
		if printed[name] {
			b.WriteString(" (repeated)\n")
			return
		}
		b.WriteString("\n")
		printed[name] = true
		for _, dep := range g.Nodes[name].Deps {
			write(dep, depth+1)
		}
	}
	for _, root := range g.Roots {
		write(root, 0)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
