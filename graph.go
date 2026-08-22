package task

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/taskfile/ast"
)

// Graph prints the dependency graph for calls in the configured format.
func (e *Executor) Graph(calls ...*Call) error {
	if len(calls) == 0 {
		calls = []*Call{{Task: "default"}}
	}
	if e.GraphFormat == "" {
		e.GraphFormat = "json"
	}
	if e.GraphFormat != "json" && e.GraphFormat != "dot" && e.GraphFormat != "text" {
		return fmt.Errorf("task: unsupported graph format %q", e.GraphFormat)
	}

	roots, err := e.graphCalls(calls)
	if err != nil {
		return err
	}
	g := &taskGraph{
		nodes: map[string]*graphNode{},
		adj:   map[string][]string{},
		edges: map[string][]graphEdge{},
	}

	if e.GraphReverse {
		if err := e.buildReverseGraph(g); err != nil {
			return err
		}
		var selected []string
		for _, root := range roots {
			selected = append(selected, root.name)
		}
		g.restrictToReverseReachable(selected)
		g.roots = uniqueSorted(selected)
	} else {
		for _, root := range roots {
			if err := e.addGraphNode(g, root.call); err != nil {
				return err
			}
			g.roots = append(g.roots, root.name)
		}
		g.roots = uniqueSorted(g.roots)
	}
	if err := g.checkCycle(); err != nil {
		return err
	}
	g.depthGroups = g.depths()
	g.longestPath = g.longest()

	switch e.GraphFormat {
	case "dot":
		return g.writeDOT(e.Stdout, e.GraphNoStatus)
	case "text":
		return g.writeText(e.Stdout)
	default:
		return g.writeJSON(e.Stdout, e.GraphNoStatus)
	}
}

type graphCall struct {
	call *Call
	name string
}

type graphNode struct {
	name, desc, method string
	task               *ast.Task
	upToDate           bool
}

type graphEdge struct {
	from, to, typ string
	vars          map[string]any
}

type taskGraph struct {
	roots, longestPath []string
	nodes              map[string]*graphNode
	adj                map[string][]string
	edges              map[string][]graphEdge
	depthGroups        [][]string
}

func (e *Executor) graphCalls(calls []*Call) ([]graphCall, error) {
	var result []graphCall
	for _, call := range calls {
		matches, err := e.FindMatchingTasks(call)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("task: graph task %q does not exist", call.Task)
		}
		for _, match := range matches {
			c := &Call{Task: call.Task, Vars: call.Vars, Silent: call.Silent, Indirect: call.Indirect}
			if len(match.Wildcards) > 0 {
				if c.Vars == nil {
					c.Vars = ast.NewVars()
				}
				c.Vars.Set("MATCH", ast.Var{Value: match.Wildcards})
				c.Task = match.Task.Task
			}
			compiled, err := e.CompiledTask(c)
			if err != nil {
				return nil, err
			}
			result = append(result, graphCall{call: c, name: graphTaskName(compiled)})
		}
	}
	return result, nil
}

func (e *Executor) addGraphNode(g *taskGraph, call *Call) error {
	t, err := e.CompiledTask(call)
	if err != nil {
		return err
	}
	name := graphTaskName(t)
	if _, ok := g.nodes[name]; ok {
		return nil
	}
	node := &graphNode{name: name, desc: t.Desc, method: graphMethod(e, t), task: t}
	if !e.GraphNoStatus {
		node.upToDate, err = fingerprint.IsTaskUpToDate(context.Background(), t,
			fingerprint.WithMethod(node.method),
			fingerprint.WithTempDir(e.TempDir.Fingerprint),
			fingerprint.WithDry(e.Dry),
			fingerprint.WithLogger(e.Logger))
		if err != nil {
			return err
		}
	}
	g.nodes[name] = node
	for _, dep := range t.Deps {
		if err := e.addGraphEdge(g, name, dep.Task, dep.Vars, "dep"); err != nil {
			return err
		}
	}
	for _, cmd := range t.Cmds {
		if cmd.Task != "" {
			if err := e.addGraphEdge(g, name, cmd.Task, cmd.Vars, "cmd"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Executor) addGraphEdge(g *taskGraph, from, target string, vars *ast.Vars, typ string) error {
	c := &Call{Task: target, Vars: vars, Indirect: true}
	matches, err := e.FindMatchingTasks(c)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("task: graph task %q does not exist", target)
	}
	for _, match := range matches {
		if len(match.Wildcards) > 0 {
			if c.Vars == nil {
				c.Vars = ast.NewVars()
			}
			c.Vars.Set("MATCH", ast.Var{Value: match.Wildcards})
			c.Task = match.Task.Task
		}
		if err := e.addGraphNode(g, c); err != nil {
			return err
		}
		compiled, err := e.CompiledTask(c)
		if err != nil {
			return err
		}
		to := graphTaskName(compiled)
		g.adj[from] = append(g.adj[from], to)
		g.edges[from] = append(g.edges[from], graphEdge{from: from, to: to, typ: typ, vars: varsMap(vars)})
	}
	return nil
}

func (e *Executor) buildReverseGraph(g *taskGraph) error {
	var all []graphCall
	for name := range e.Taskfile.Tasks.Keys(nil) {
		calls, err := e.graphCalls([]*Call{{Task: name}})
		if err != nil {
			return err
		}
		all = append(all, calls...)
	}
	for _, item := range all {
		if err := e.addGraphNode(g, item.call); err != nil {
			return err
		}
	}
	reverseAdj := map[string][]string{}
	reverseEdges := map[string][]graphEdge{}
	for from, edges := range g.edges {
		for _, edge := range edges {
			reverseAdj[edge.to] = append(reverseAdj[edge.to], from)
			reverseEdges[edge.to] = append(reverseEdges[edge.to], graphEdge{from: edge.to, to: from, typ: edge.typ, vars: edge.vars})
		}
	}
	g.adj, g.edges = reverseAdj, reverseEdges
	return nil
}

func (g *taskGraph) restrictToReverseReachable(roots []string) {
	keep := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if keep[name] {
			return
		}
		keep[name] = true
		for _, next := range g.adj[name] {
			visit(next)
		}
	}
	for _, root := range roots {
		visit(root)
	}
	for name := range g.nodes {
		if !keep[name] {
			delete(g.nodes, name)
			delete(g.adj, name)
			delete(g.edges, name)
		}
	}
}

func (g *taskGraph) checkCycle() error {
	state := map[string]int{}
	stack := []string{}
	var visit func(string) error
	visit = func(name string) error {
		state[name] = 1
		stack = append(stack, name)
		for _, next := range uniqueSorted(g.adj[name]) {
			if state[next] == 1 {
				start := 0
				for i, n := range stack {
					if n == next {
						start = i
						break
					}
				}
				return fmt.Errorf("task: cycle detected: %s", strings.Join(append(stack[start:], next), " -> "))
			}
			if state[next] == 0 {
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = 2
		return nil
	}
	for name := range g.nodes {
		if state[name] == 0 {
			if err := visit(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *taskGraph) depths() [][]string {
	memo := map[string]int{}
	var depth func(string) int
	depth = func(name string) int {
		if d, ok := memo[name]; ok {
			return d
		}
		d := 0
		for _, next := range uniqueSorted(g.adj[name]) {
			if candidate := depth(next) + 1; candidate > d {
				d = candidate
			}
		}
		memo[name] = d
		return d
	}
	max := 0
	for name := range g.nodes {
		if d := depth(name); d > max {
			max = d
		}
	}
	groups := make([][]string, max+1)
	for name := range g.nodes {
		d := depth(name)
		groups[d] = append(groups[d], name)
	}
	for _, group := range groups {
		sort.Strings(group)
	}
	return groups
}

func (g *taskGraph) longest() []string {
	memo := map[string][]string{}
	var path func(string) []string
	path = func(name string) []string {
		if p, ok := memo[name]; ok {
			return p
		}
		best := []string{name}
		for _, next := range uniqueSorted(g.adj[name]) {
			candidate := append([]string{name}, path(next)...)
			if len(candidate) > len(best) {
				best = candidate
			}
		}
		memo[name] = best
		return best
	}
	var best []string
	for _, root := range g.roots {
		if candidate := path(root); len(candidate) > len(best) {
			best = candidate
		}
	}
	return best
}

type graphJSON struct {
	Roots       []string                 `json:"roots"`
	Nodes       map[string]graphJSONNode `json:"nodes"`
	Edges       []graphJSONEdge          `json:"edges"`
	DepthGroups [][]string               `json:"depth_groups"`
	LongestPath []string                 `json:"longest_path"`
}

type graphJSONNode struct {
	Name     string        `json:"name"`
	Desc     string        `json:"desc"`
	Location graphLocation `json:"location"`
	UpToDate *bool         `json:"up_to_date,omitempty"`
	Deps     []string      `json:"deps"`
	Method   string        `json:"method"`
}

type graphLocation struct {
	Taskfile string `json:"taskfile"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

type graphJSONEdge struct {
	From string         `json:"from"`
	To   string         `json:"to"`
	Type string         `json:"type"`
	Vars map[string]any `json:"vars"`
}

func (g *taskGraph) writeJSON(w interface{ Write([]byte) (int, error) }, noStatus bool) error {
	out := graphJSON{Roots: g.roots, Nodes: map[string]graphJSONNode{}, DepthGroups: g.depthGroups, LongestPath: g.longestPath}
	for name, node := range g.nodes {
		deps := uniqueSorted(g.adj[name])
		location := graphLocation{}
		if node.task.Location != nil {
			location = graphLocation{
				Taskfile: node.task.Location.Taskfile,
				Line:     node.task.Location.Line,
				Column:   node.task.Location.Column,
			}
		}
		item := graphJSONNode{Name: name, Desc: node.desc, Location: location, Deps: deps, Method: node.method}
		if !noStatus {
			value := node.upToDate
			item.UpToDate = &value
		}
		out.Nodes[name] = item
	}
	for from, edges := range g.edges {
		for _, edge := range edges {
			out.Edges = append(out.Edges, graphJSONEdge{From: from, To: edge.to, Type: edge.typ, Vars: edge.vars})
		}
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].From != out.Edges[j].From {
			return out.Edges[i].From < out.Edges[j].From
		}
		return out.Edges[i].To < out.Edges[j].To
	})
	return json.NewEncoder(w).Encode(out)
}

func (g *taskGraph) writeDOT(w interface{ Write([]byte) (int, error) }, noStatus bool) error {
	var b strings.Builder
	b.WriteString("digraph tasks {\n")
	names := make([]string, 0, len(g.nodes))
	for name := range g.nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !noStatus && g.nodes[name].upToDate {
			fmt.Fprintf(&b, "  %q [style=dashed];\n", name)
		} else {
			fmt.Fprintf(&b, "  %q;\n", name)
		}
	}
	for _, from := range names {
		for _, edge := range g.edges[from] {
			fmt.Fprintf(&b, "  %q -> %q;\n", from, edge.to)
		}
	}
	b.WriteString("}\n")
	_, err := w.Write([]byte(b.String()))
	return err
}

func (g *taskGraph) writeText(w interface{ Write([]byte) (int, error) }) error {
	var b strings.Builder
	seen := map[string]bool{}
	var print func(string, int)
	print = func(name string, depth int) {
		repeated := seen[name]
		seen[name] = true
		fmt.Fprintf(&b, "%s%s", strings.Repeat("  ", depth), name)
		if repeated {
			b.WriteString(" (repeated)\n")
			return
		}
		b.WriteByte('\n')
		nextTasks := append([]string(nil), g.adj[name]...)
		sort.Strings(nextTasks)
		for _, next := range nextTasks {
			print(next, depth+1)
		}
	}
	for _, root := range g.roots {
		print(root, 0)
	}
	_, err := w.Write([]byte(b.String()))
	return err
}

func graphTaskName(t *ast.Task) string {
	if t.FullName != "" {
		return t.FullName
	}
	return t.Task
}

func graphMethod(e *Executor, t *ast.Task) string {
	if t.Method != "" {
		return t.Method
	}
	return e.Taskfile.Method
}

func varsMap(vars *ast.Vars) map[string]any {
	if vars == nil {
		return map[string]any{}
	}
	return vars.ToCacheMap()
}

func uniqueSorted(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
