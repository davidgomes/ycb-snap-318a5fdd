package task

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/taskfile/ast"
)

const (
	GraphFormatJSON = "json"
	GraphFormatDOT  = "dot"
	GraphFormatText = "text"
)

type GraphLocation struct {
	Taskfile string `json:"taskfile"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

type GraphNode struct {
	Name     string        `json:"name"`
	Desc     string        `json:"desc"`
	Location GraphLocation `json:"location"`
	UpToDate *bool         `json:"up_to_date,omitempty"`
	Deps     []string      `json:"deps"`
	Method   string        `json:"method"`
}

type GraphEdge struct {
	From string         `json:"from"`
	To   string         `json:"to"`
	Type string         `json:"type"`
	Vars map[string]any `json:"vars"`
}

type GraphOutput struct {
	Roots       []string              `json:"roots"`
	Nodes       map[string]*GraphNode `json:"nodes"`
	Edges       []GraphEdge           `json:"edges"`
	DepthGroups [][]string            `json:"depth_groups"`
	LongestPath []string              `json:"longest_path"`
}

type graphBuilder struct {
	e     *Executor
	nodes map[string]*GraphNode
	edges []GraphEdge
	tasks map[string]*ast.Task
}

// Graph writes the dependency graph of the given calls to the executor's
// stdout using the configured graph format.
func (e *Executor) Graph(calls ...*Call) error {
	out, err := e.buildGraph(calls...)
	if err != nil {
		return err
	}
	var w io.Writer = os.Stdout
	if e.Stdout != nil {
		w = e.Stdout
	}
	switch e.GraphFormat {
	case "", GraphFormatJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case GraphFormatDOT:
		return writeGraphDOT(w, out, e.GraphNoStatus)
	case GraphFormatText:
		return writeGraphText(w, out)
	default:
		return fmt.Errorf("task: unknown graph format %q", e.GraphFormat)
	}
}

func (e *Executor) buildGraph(calls ...*Call) (*GraphOutput, error) {
	if len(calls) == 0 {
		calls = []*Call{{Task: "default"}}
	}
	b := &graphBuilder{e: e, nodes: map[string]*GraphNode{}, tasks: map[string]*ast.Task{}}

	roots := make([]string, 0, len(calls))
	for _, c := range calls {
		t, err := e.GetTask(&Call{Task: c.Task, Vars: c.Vars})
		if err != nil {
			return nil, err
		}
		if !slices.Contains(roots, t.Task) {
			roots = append(roots, t.Task)
		}
	}

	if e.GraphReverse {
		for t := range e.Taskfile.Tasks.Values(nil) {
			if strings.Contains(t.Task, "*") {
				continue
			}
			if err := b.visit(&Call{Task: t.Task}); err != nil {
				return nil, err
			}
		}
	} else {
		for _, c := range calls {
			if err := b.visit(c); err != nil {
				return nil, err
			}
		}
	}

	adj := map[string][]string{}
	for _, edge := range b.edges {
		from, to := edge.From, edge.To
		if e.GraphReverse {
			from, to = to, from
		}
		if !slices.Contains(adj[from], to) {
			adj[from] = append(adj[from], to)
		}
	}

	// Restrict to nodes reachable from the roots
	reachable := map[string]bool{}
	var walk func(string)
	walk = func(n string) {
		if reachable[n] {
			return
		}
		reachable[n] = true
		for _, m := range adj[n] {
			walk(m)
		}
	}
	for _, r := range roots {
		walk(r)
	}

	out := &GraphOutput{Roots: roots, Nodes: map[string]*GraphNode{}, Edges: []GraphEdge{}}
	for name, node := range b.nodes {
		if !reachable[name] {
			continue
		}
		deps := slices.Clone(adj[name])
		sort.Strings(deps)
		if deps == nil {
			deps = []string{}
		}
		node.Deps = deps
		if !e.GraphNoStatus {
			upToDate, err := b.upToDate(name)
			if err != nil {
				return nil, err
			}
			node.UpToDate = &upToDate
		}
		out.Nodes[name] = node
	}
	for _, edge := range b.edges {
		if e.GraphReverse {
			edge.From, edge.To = edge.To, edge.From
		}
		if reachable[edge.From] && reachable[edge.To] {
			out.Edges = append(out.Edges, edge)
		}
	}

	if cycle := findCycle(roots, adj); cycle != nil {
		return nil, fmt.Errorf("task: dependency cycle detected: %s", strings.Join(cycle, " -> "))
	}

	levels := map[string]int{}
	var level func(string) int
	level = func(n string) int {
		if l, ok := levels[n]; ok {
			return l
		}
		l := 0
		for _, m := range adj[n] {
			l = max(l, level(m)+1)
		}
		levels[n] = l
		return l
	}
	for n := range out.Nodes {
		lvl := level(n)
		for len(out.DepthGroups) <= lvl {
			out.DepthGroups = append(out.DepthGroups, []string{})
		}
		out.DepthGroups[lvl] = append(out.DepthGroups[lvl], n)
	}
	for _, g := range out.DepthGroups {
		sort.Strings(g)
	}
	if out.DepthGroups == nil {
		out.DepthGroups = [][]string{}
	}

	out.LongestPath = []string{}
	best := ""
	for _, r := range roots {
		if best == "" || levels[r] > levels[best] {
			best = r
		}
	}
	for best != "" {
		out.LongestPath = append(out.LongestPath, best)
		next := ""
		children := slices.Clone(adj[best])
		sort.Strings(children)
		for _, c := range children {
			if next == "" || levels[c] > levels[next] {
				next = c
			}
		}
		best = next
	}

	return out, nil
}

func (b *graphBuilder) visit(call *Call) error {
	t, err := b.e.GetTask(&Call{Task: call.Task})
	if err != nil {
		return err
	}
	name := t.Task
	if _, ok := b.nodes[name]; ok {
		return nil
	}
	compiled, err := b.e.FastCompiledTask(&Call{Task: call.Task, Vars: call.Vars})
	if err != nil {
		return err
	}
	method := b.e.Taskfile.Method
	if compiled.Method != "" {
		method = compiled.Method
	}
	node := &GraphNode{Name: name, Desc: compiled.Desc, Method: method}
	if compiled.Location != nil {
		node.Location = GraphLocation{
			Taskfile: compiled.Location.Taskfile,
			Line:     compiled.Location.Line,
			Column:   compiled.Location.Column,
		}
	}
	b.nodes[name] = node
	b.tasks[name] = compiled

	type target struct {
		task string
		vars *ast.Vars
		kind string
	}
	var targets []target
	for _, d := range compiled.Deps {
		if d != nil && d.Task != "" {
			targets = append(targets, target{d.Task, d.Vars, "dep"})
		}
	}
	for _, c := range compiled.Cmds {
		if c != nil && c.Task != "" {
			targets = append(targets, target{c.Task, c.Vars, "cmd"})
		}
	}
	for _, tg := range targets {
		dt, err := b.e.GetTask(&Call{Task: tg.task})
		if err != nil {
			return err
		}
		vars := map[string]any{}
		if tg.vars != nil {
			for k, v := range tg.vars.All() {
				vars[k] = v.Value
			}
		}
		b.edges = append(b.edges, GraphEdge{From: name, To: dt.Task, Type: tg.kind, Vars: vars})
		if err := b.visit(&Call{Task: tg.task, Vars: tg.vars}); err != nil {
			return err
		}
	}
	return nil
}

func (b *graphBuilder) upToDate(name string) (bool, error) {
	t := b.tasks[name]
	method := b.nodes[name].Method
	return fingerprint.IsTaskUpToDate(context.Background(), t,
		fingerprint.WithMethod(method),
		fingerprint.WithTempDir(b.e.TempDir.Fingerprint),
		fingerprint.WithDry(true),
		fingerprint.WithLogger(b.e.Logger),
	)
}

func findCycle(roots []string, adj map[string][]string) []string {
	const (
		white = iota
		gray
		black
	)
	color := map[string]int{}
	var stack []string
	var dfs func(string) []string
	dfs = func(n string) []string {
		color[n] = gray
		stack = append(stack, n)
		for _, m := range adj[n] {
			switch color[m] {
			case gray:
				i := slices.Index(stack, m)
				return append(slices.Clone(stack[i:]), m)
			case white:
				if c := dfs(m); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return nil
	}
	for _, r := range roots {
		if color[r] == white {
			if c := dfs(r); c != nil {
				return c
			}
		}
	}
	return nil
}

func writeGraphDOT(w io.Writer, g *GraphOutput, noStatus bool) error {
	var sb strings.Builder
	sb.WriteString("digraph tasks {\n")
	names := make([]string, 0, len(g.Nodes))
	for n := range g.Nodes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		node := g.Nodes[n]
		attrs := ""
		if !noStatus && node.UpToDate != nil && *node.UpToDate {
			attrs = " [style=dashed]"
		}
		fmt.Fprintf(&sb, "  %s%s;\n", strconv.Quote(n), attrs)
	}
	for _, e := range g.Edges {
		fmt.Fprintf(&sb, "  %s -> %s;\n", strconv.Quote(e.From), strconv.Quote(e.To))
	}
	sb.WriteString("}\n")
	_, err := io.WriteString(w, sb.String())
	return err
}

func writeGraphText(w io.Writer, g *GraphOutput) error {
	var sb strings.Builder
	seen := map[string]bool{}
	var print func(string, int)
	print = func(n string, depth int) {
		indent := strings.Repeat("  ", depth)
		if seen[n] {
			fmt.Fprintf(&sb, "%s%s (repeated)\n", indent, n)
			return
		}
		seen[n] = true
		fmt.Fprintf(&sb, "%s%s\n", indent, n)
		for _, d := range g.Nodes[n].Deps {
			print(d, depth+1)
		}
	}
	for _, r := range g.Roots {
		print(r, 0)
	}
	_, err := io.WriteString(w, sb.String())
	return err
}
