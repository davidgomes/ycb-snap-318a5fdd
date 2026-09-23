package task

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/taskfile/ast"
)

// Graph writes the dependency graph of the given calls to Stdout.
// The format is selected with [WithGraphFormat] ("json", "dot", or "text";
// JSON is the default). [WithGraphReverse] inverts the graph so each edge
// points at tasks that depend on a task. [WithGraphNoStatus] omits fingerprint
// status from the output.
func (e *Executor) Graph(calls ...*Call) error {
	if len(calls) == 0 {
		calls = []*Call{{Task: "default"}}
	}

	format := e.GraphFormat
	if format == "" {
		format = "json"
	}
	switch format {
	case "json", "dot", "text":
	default:
		return fmt.Errorf("task: unsupported graph format %q", format)
	}

	model, err := e.buildTaskGraph(calls)
	if err != nil {
		return err
	}

	switch format {
	case "dot":
		up := map[string]bool{}
		if !e.GraphNoStatus {
			for name, t := range model.nodes {
				ok, err := e.taskUpToDate(t)
				if err != nil {
					return err
				}
				up[name] = ok
			}
		}
		return writeDOT(e.Stdout, model, !e.GraphNoStatus, up)
	case "text":
		return writeTextTree(e.Stdout, model)
	default:
		return e.writeGraphJSON(e.Stdout, model)
	}
}

type graphEdge struct {
	From string
	To   string
	Type string
	Vars map[string]any
}

type graphModel struct {
	roots    []string
	order    []string
	nodes    map[string]*ast.Task
	edges    []graphEdge
	children map[string][]string
}

type graphJSON struct {
	Roots       []string             `json:"roots"`
	Nodes       map[string]graphNode `json:"nodes"`
	Edges       []graphEdgeJSON      `json:"edges"`
	DepthGroups [][]string           `json:"depth_groups"`
	LongestPath []string             `json:"longest_path"`
}

type graphNode struct {
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

type graphEdgeJSON struct {
	From string         `json:"from"`
	To   string         `json:"to"`
	Type string         `json:"type"`
	Vars map[string]any `json:"vars"`
}

func (e *Executor) buildTaskGraph(calls []*Call) (*graphModel, error) {
	m := &graphModel{
		nodes:    map[string]*ast.Task{},
		children: map[string][]string{},
	}

	var err error
	if e.GraphReverse {
		err = e.buildReverseGraph(m, calls)
	} else {
		err = e.buildForwardGraph(m, calls)
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (e *Executor) buildForwardGraph(m *graphModel, calls []*Call) error {
	onStack := map[string]bool{}
	var stack []string

	var visit func(call *Call) (string, error)
	visit = func(call *Call) (string, error) {
		name, t, err := e.compileGraphTask(call)
		if err != nil {
			return "", err
		}
		if onStack[name] {
			return "", cycleError(stack, name)
		}
		if _, ok := m.nodes[name]; ok {
			return name, nil
		}
		m.nodes[name] = t
		m.order = append(m.order, name)
		onStack[name] = true
		stack = append(stack, name)

		for _, edge := range outgoingCalls(t) {
			childName, err := visit(edge.call)
			if err != nil {
				return "", err
			}
			m.edges = append(m.edges, graphEdge{
				From: name,
				To:   childName,
				Type: edge.kind,
				Vars: varsToMap(edge.call.Vars),
			})
			m.children[name] = append(m.children[name], childName)
		}

		stack = stack[:len(stack)-1]
		onStack[name] = false
		return name, nil
	}

	for _, call := range calls {
		name, err := visit(call)
		if err != nil {
			return err
		}
		m.roots = append(m.roots, name)
	}
	return nil
}

func (e *Executor) buildReverseGraph(m *graphModel, calls []*Call) error {
	// Direct edges from every task in the Taskfile, then invert them.
	type direct struct {
		from string
		edge outgoing
	}
	var directs []direct

	for key := range e.Taskfile.Tasks.Keys(nil) {
		name, t, err := e.compileGraphTask(&Call{Task: key})
		if err != nil {
			return err
		}
		for _, edge := range outgoingCalls(t) {
			childName, _, err := e.compileGraphTask(edge.call)
			if err != nil {
				return err
			}
			directs = append(directs, direct{from: name, edge: outgoing{
				call: &Call{Task: childName, Vars: edge.call.Vars},
				kind: edge.kind,
			}})
			// Keep the child task available even when it is only reached as a dependency.
			if _, ok := m.nodes[childName]; !ok {
				_, child, err := e.compileGraphTask(&Call{Task: childName})
				if err != nil {
					return err
				}
				m.nodes[childName] = child
			}
			if _, ok := m.nodes[name]; !ok {
				m.nodes[name] = t
			}
		}
		if _, ok := m.nodes[name]; !ok {
			m.nodes[name] = t
		}
	}

	// Inverted adjacency: dependency -> tasks that depend on it.
	type revEdge struct {
		to   string
		kind string
		vars map[string]any
	}
	rev := map[string][]revEdge{}
	for _, d := range directs {
		childName := d.edge.call.Task
		rev[childName] = append(rev[childName], revEdge{
			to:   d.from,
			kind: d.edge.kind,
			vars: varsToMap(d.edge.call.Vars),
		})
	}

	// The reachable subgraph starts at the requested tasks.
	m.nodes = map[string]*ast.Task{}
	onStack := map[string]bool{}
	var stack []string

	var visit func(call *Call) (string, error)
	visit = func(call *Call) (string, error) {
		name, t, err := e.compileGraphTask(call)
		if err != nil {
			return "", err
		}
		if onStack[name] {
			return "", cycleError(stack, name)
		}
		if _, ok := m.nodes[name]; ok {
			return name, nil
		}
		m.nodes[name] = t
		m.order = append(m.order, name)
		onStack[name] = true
		stack = append(stack, name)

		for _, edge := range rev[name] {
			childName, err := visit(&Call{Task: edge.to})
			if err != nil {
				return "", err
			}
			m.edges = append(m.edges, graphEdge{
				From: name,
				To:   childName,
				Type: edge.kind,
				Vars: edge.vars,
			})
			m.children[name] = append(m.children[name], childName)
		}

		stack = stack[:len(stack)-1]
		onStack[name] = false
		return name, nil
	}

	for _, call := range calls {
		name, err := visit(call)
		if err != nil {
			return err
		}
		m.roots = append(m.roots, name)
	}
	return nil
}

type outgoing struct {
	call *Call
	kind string
}

func outgoingCalls(t *ast.Task) []outgoing {
	var out []outgoing
	for _, dep := range t.Deps {
		if dep == nil || dep.Task == "" {
			continue
		}
		out = append(out, outgoing{
			call: &Call{Task: dep.Task, Vars: dep.Vars, Silent: dep.Silent, Indirect: true},
			kind: "dep",
		})
	}
	for _, cmd := range t.Cmds {
		if cmd == nil || cmd.Task == "" {
			continue
		}
		out = append(out, outgoing{
			call: &Call{Task: cmd.Task, Vars: cmd.Vars, Silent: cmd.Silent, Indirect: true},
			kind: "cmd",
		})
	}
	return out
}

func (e *Executor) compileGraphTask(call *Call) (string, *ast.Task, error) {
	// Copy so GetTask can record wildcard matches without mutating the caller.
	c := &Call{Task: call.Task, Vars: call.Vars, Silent: call.Silent, Indirect: call.Indirect}
	t, err := e.FastCompiledTask(c)
	if err != nil {
		return "", nil, err
	}
	name := t.Task
	if t.FullName != "" {
		name = t.FullName
	}
	return name, t, nil
}

func cycleError(stack []string, name string) error {
	i := slices.Index(stack, name)
	cyc := append(append([]string{}, stack[i:]...), name)
	return fmt.Errorf("task: cycle detected: %s", strings.Join(cyc, " -> "))
}

func varsToMap(vars *ast.Vars) map[string]any {
	m := map[string]any{}
	if vars == nil {
		return m
	}
	for k, v := range vars.All() {
		switch {
		case v.Live != nil:
			m[k] = v.Live
		case v.Value != nil:
			m[k] = v.Value
		case v.Sh != nil:
			m[k] = *v.Sh
		case v.Ref != "":
			m[k] = v.Ref
		}
	}
	return m
}

func (m *graphModel) uniqueChildren(name string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, child := range m.children[name] {
		if _, ok := seen[child]; ok {
			continue
		}
		seen[child] = struct{}{}
		out = append(out, child)
	}
	return out
}

func (m *graphModel) sortedDeps(name string) []string {
	deps := m.uniqueChildren(name)
	slices.Sort(deps)
	if deps == nil {
		return []string{}
	}
	return deps
}

func (m *graphModel) depthGroups() [][]string {
	level := map[string]int{}
	var depth func(string) int
	depth = func(name string) int {
		if v, ok := level[name]; ok {
			return v
		}
		// Sentinel so a bug cannot recurse forever; cycles are rejected earlier.
		level[name] = 0
		maxChild := -1
		for _, child := range m.uniqueChildren(name) {
			if d := depth(child); d > maxChild {
				maxChild = d
			}
		}
		level[name] = maxChild + 1
		return level[name]
	}
	maxLevel := 0
	for name := range m.nodes {
		if d := depth(name); d > maxLevel {
			maxLevel = d
		}
	}
	groups := make([][]string, maxLevel+1)
	for name := range m.nodes {
		d := level[name]
		groups[d] = append(groups[d], name)
	}
	for i := range groups {
		slices.Sort(groups[i])
		if groups[i] == nil {
			groups[i] = []string{}
		}
	}
	return groups
}

func (m *graphModel) longestPath() []string {
	memo := map[string][]string{}
	var path func(string) []string
	path = func(name string) []string {
		if p, ok := memo[name]; ok {
			return p
		}
		best := []string{name}
		children := m.uniqueChildren(name)
		slices.Sort(children)
		for _, child := range children {
			p := path(child)
			if len(p)+1 > len(best) {
				best = append([]string{name}, p...)
			}
		}
		memo[name] = best
		return best
	}
	var best []string
	for _, root := range m.roots {
		p := path(root)
		if len(p) > len(best) {
			best = p
		}
	}
	if best == nil {
		return []string{}
	}
	return best
}

func (e *Executor) fingerprintMethod(t *ast.Task) string {
	if t != nil && t.Method != "" {
		return t.Method
	}
	if e.Taskfile != nil && e.Taskfile.Method != "" {
		return e.Taskfile.Method
	}
	return "checksum"
}

func (e *Executor) taskUpToDate(t *ast.Task) (bool, error) {
	method := e.fingerprintMethod(t)
	return fingerprint.IsTaskUpToDate(context.Background(), t,
		fingerprint.WithMethod(method),
		fingerprint.WithTempDir(e.TempDir.Fingerprint),
		fingerprint.WithDry(true),
		fingerprint.WithLogger(e.Logger),
	)
}

func (e *Executor) writeGraphJSON(w io.Writer, m *graphModel) error {
	out := graphJSON{
		Roots:       m.roots,
		Nodes:       map[string]graphNode{},
		Edges:       make([]graphEdgeJSON, 0, len(m.edges)),
		DepthGroups: m.depthGroups(),
		LongestPath: m.longestPath(),
	}
	if out.Roots == nil {
		out.Roots = []string{}
	}
	for name, t := range m.nodes {
		node := graphNode{
			Name:     name,
			Desc:     t.Desc,
			Deps:     m.sortedDeps(name),
			Method:   e.fingerprintMethod(t),
			Location: locationOf(t),
		}
		if !e.GraphNoStatus {
			up, err := e.taskUpToDate(t)
			if err != nil {
				return err
			}
			node.UpToDate = &up
		}
		out.Nodes[name] = node
	}
	for _, edge := range m.edges {
		vars := edge.Vars
		if vars == nil {
			vars = map[string]any{}
		}
		out.Edges = append(out.Edges, graphEdgeJSON{
			From: edge.From,
			To:   edge.To,
			Type: edge.Type,
			Vars: vars,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

func locationOf(t *ast.Task) graphLocation {
	if t == nil || t.Location == nil {
		return graphLocation{}
	}
	return graphLocation{
		Taskfile: t.Location.Taskfile,
		Line:     t.Location.Line,
		Column:   t.Location.Column,
	}
}

func writeDOT(w io.Writer, m *graphModel, withStatus bool, upToDate map[string]bool) error {
	var b strings.Builder
	b.WriteString("digraph tasks {\n")
	names := append([]string{}, m.order...)
	slices.Sort(names)
	for _, name := range names {
		if withStatus && upToDate[name] {
			fmt.Fprintf(&b, "  %s [style=dashed];\n", strconv.Quote(name))
		} else {
			fmt.Fprintf(&b, "  %s;\n", strconv.Quote(name))
		}
	}
	for _, edge := range m.edges {
		fmt.Fprintf(&b, "  %s -> %s;\n", strconv.Quote(edge.From), strconv.Quote(edge.To))
	}
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeTextTree(w io.Writer, m *graphModel) error {
	seen := map[string]bool{}
	var walk func(name string, depth int)
	walk = func(name string, depth int) {
		indent := strings.Repeat("  ", depth)
		if seen[name] {
			fmt.Fprintf(w, "%s%s (repeated)\n", indent, name)
			return
		}
		seen[name] = true
		fmt.Fprintf(w, "%s%s\n", indent, name)
		for _, child := range m.children[name] {
			walk(child, depth+1)
		}
	}
	for _, root := range m.roots {
		walk(root, 0)
	}
	return nil
}
