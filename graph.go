package task

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/taskfile/ast"
)

type GraphFormat string

const (
	GraphFormatJSON GraphFormat = "json"
	GraphFormatDOT  GraphFormat = "dot"
	GraphFormatText GraphFormat = "text"
)

// WithGraphFormat selects the output format used by Graph.
func WithGraphFormat(format string) ExecutorOption {
	return graphFormatOption(format)
}

type graphFormatOption string

func (o graphFormatOption) ApplyToExecutor(e *Executor) { e.graphFormat = GraphFormat(o) }

type graphReverseOption bool

func (o graphReverseOption) ApplyToExecutor(e *Executor) { e.graphReverse = bool(o) }
func WithGraphReverse(reverse bool) ExecutorOption       { return graphReverseOption(reverse) }

type graphNoStatusOption bool

func (o graphNoStatusOption) ApplyToExecutor(e *Executor) { e.graphNoStatus = bool(o) }
func WithGraphNoStatus(noStatus bool) ExecutorOption      { return graphNoStatusOption(noStatus) }

type GraphNode struct {
	Name     string        `json:"name"`
	Desc     string        `json:"desc"`
	Location *ast.Location `json:"location"`
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
	Roots       []string             `json:"roots"`
	Nodes       map[string]GraphNode `json:"nodes"`
	Edges       []GraphEdge          `json:"edges"`
	DepthGroups [][]string           `json:"depth_groups"`
	LongestPath []string             `json:"longest_path"`
}

type graphTask struct {
	task  *ast.Task
	deps  map[string]struct{}
	edges []GraphEdge
}
type graphBuilder struct {
	e     *Executor
	tasks map[string]*graphTask
}

// Graph writes the dependency graph for calls to Stdout.
func (e *Executor) Graph(calls ...*Call) error {
	if len(calls) == 0 {
		calls = []*Call{{Task: "default"}}
	}
	g := &graphBuilder{e: e, tasks: map[string]*graphTask{}}
	var roots []string
	for _, call := range calls {
		matches, err := e.FindMatchingTasks(call)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			return &taskNotFound{call.Task}
		}
		for _, match := range matches {
			c := &Call{Task: call.Task, Vars: ast.NewVars()}
			c.Vars.Set("MATCH", ast.Var{Value: match.Wildcards})
			t, err := e.CompiledTask(c)
			if err != nil {
				return err
			}
			name := t.FullName
			roots = append(roots, name)
			if err := g.expand(name, t); err != nil {
				return err
			}
		}
	}
	roots = uniqueSorted(roots)
	if e.graphReverse {
		if err := g.expandAll(); err != nil {
			return err
		}
		g.invert(roots)
	}
	out, err := g.output(roots)
	if err != nil {
		return err
	}
	switch e.graphFormat {
	case "", GraphFormatJSON:
		enc := json.NewEncoder(e.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case GraphFormatDOT:
		return writeDOT(e.Stdout, out, e.graphNoStatus)
	case GraphFormatText:
		return writeText(e.Stdout, out)
	default:
		return fmt.Errorf("task: invalid graph format %q", e.graphFormat)
	}
}

type taskNotFound struct{ name string }

func (e *taskNotFound) Error() string { return fmt.Sprintf("task: Task %q does not exist", e.name) }

func (g *graphBuilder) expand(name string, t *ast.Task) error {
	if _, ok := g.tasks[name]; ok {
		return nil
	}
	g.tasks[name] = &graphTask{task: t, deps: map[string]struct{}{}}
	add := func(target, typ string, vars *ast.Vars) error {
		matches, err := g.e.FindMatchingTasks(&Call{Task: target})
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			return &taskNotFound{target}
		}
		for _, m := range matches {
			c := &Call{Task: target, Vars: ast.NewVars()}
			c.Vars.Set("MATCH", ast.Var{Value: m.Wildcards})
			child, err := g.e.CompiledTask(c)
			if err != nil {
				return err
			}
			childName := child.FullName
			g.tasks[name].deps[childName] = struct{}{}
			var values map[string]any
			if vars != nil {
				values = maps.Clone(vars.ToCacheMap())
			} else {
				values = map[string]any{}
			}
			g.tasks[name].edges = append(g.tasks[name].edges, GraphEdge{From: name, To: childName, Type: typ, Vars: values})
			if err := g.expand(childName, child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, d := range t.Deps {
		if d != nil {
			if err := add(d.Task, "dep", d.Vars); err != nil {
				return err
			}
		}
	}
	for _, c := range t.Cmds {
		if c != nil && c.Task != "" {
			if err := add(c.Task, "cmd", c.Vars); err != nil {
				return err
			}
		}
	}
	return nil
}
func (g *graphBuilder) expandAll() error {
	for _, t := range g.e.Taskfile.Tasks.Values(nil) {
		c, err := g.e.CompiledTask(&Call{Task: t.Task})
		if err != nil {
			return err
		}
		if err := g.expand(c.FullName, c); err != nil {
			return err
		}
	}
	return nil
}
func (g *graphBuilder) invert(roots []string) {
	rev := map[string]*graphTask{}
	for n, t := range g.tasks {
		rev[n] = &graphTask{task: t.task, deps: map[string]struct{}{}}
	}
	for from, t := range g.tasks {
		for _, edge := range t.edges {
			rev[edge.To].deps[from] = struct{}{}
			rev[edge.To].edges = append(rev[edge.To].edges, GraphEdge{From: edge.To, To: from, Type: edge.Type, Vars: edge.Vars})
		}
	}
	keep := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if keep[n] {
			return
		}
		keep[n] = true
		for d := range rev[n].deps {
			visit(d)
		}
	}
	for _, r := range roots {
		visit(r)
	}
	for n := range g.tasks {
		if !keep[n] {
			delete(rev, n)
		}
	}
	g.tasks = rev
}
func (g *graphBuilder) output(roots []string) (GraphOutput, error) {
	if err := detectGraphCycle(g.tasks); err != nil {
		return GraphOutput{}, err
	}
	o := GraphOutput{Roots: roots, Nodes: map[string]GraphNode{}}
	for n, t := range g.tasks {
		method := t.task.Method
		if method == "" {
			method = g.e.Taskfile.Method
		}
		var status *bool
		if !g.e.graphNoStatus {
			v, err := fingerprint.IsTaskUpToDate(context.Background(), t.task, fingerprint.WithMethod(method), fingerprint.WithTempDir(g.e.TempDir.Fingerprint), fingerprint.WithDry(g.e.Dry), fingerprint.WithLogger(g.e.Logger))
			if err != nil {
				return GraphOutput{}, err
			}
			status = &v
		}
		deps := make([]string, 0, len(t.deps))
		for d := range t.deps {
			deps = append(deps, d)
		}
		sort.Strings(deps)
		o.Nodes[n] = GraphNode{Name: n, Desc: t.task.Desc, Location: t.task.Location, UpToDate: status, Deps: deps, Method: method}
		o.Edges = append(o.Edges, t.edges...)
	}
	sort.Slice(o.Edges, func(i, j int) bool {
		if o.Edges[i].From != o.Edges[j].From {
			return o.Edges[i].From < o.Edges[j].From
		}
		return o.Edges[i].To < o.Edges[j].To
	})
	o.DepthGroups = depthGroups(o.Nodes)
	o.LongestPath = longestPath(o.Nodes, roots)
	return o, nil
}

func detectGraphCycle(tasks map[string]*graphTask) error {
	state := map[string]uint8{}
	var path []string
	var visit func(string) error
	visit = func(n string) error {
		if state[n] == 1 {
			i := slices.Index(path, n)
			return fmt.Errorf("task: dependency cycle: %s", strings.Join(append(path[i:], n), " -> "))
		}
		if state[n] == 2 {
			return nil
		}
		state[n] = 1
		path = append(path, n)
		for d := range tasks[n].deps {
			if err := visit(d); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[n] = 2
		return nil
	}
	for n := range tasks {
		if err := visit(n); err != nil {
			return err
		}
	}
	return nil
}
func uniqueSorted(v []string) []string {
	sort.Strings(v)
	return slices.Compact(v)
}

func depthGroups(nodes map[string]GraphNode) [][]string {
	// Levels are based on dependency depth, so repeatedly remove leaves.
	levels := map[string]int{}
	var walk func(string) int
	walk = func(n string) int {
		if v, ok := levels[n]; ok {
			return v
		}
		max := 0
		for _, d := range nodes[n].Deps {
			if x := walk(d) + 1; x > max {
				max = x
			}
		}
		levels[n] = max
		return max
	}
	max := 0
	for n := range nodes {
		if x := walk(n); x > max {
			max = x
		}
	}
	out := make([][]string, max+1)
	for n, l := range levels {
		out[l] = append(out[l], n)
	}
	for i := range out {
		sort.Strings(out[i])
	}
	return out
}
func longestPath(nodes map[string]GraphNode, roots []string) []string {
	var best []string
	var visit func(string, []string)
	visit = func(n string, p []string) {
		p = append(p, n)
		if len(p) > len(best) {
			best = slices.Clone(p)
		}
		for _, d := range nodes[n].Deps {
			visit(d, p)
		}
	}
	for _, r := range roots {
		visit(r, nil)
	}
	return best
}

func writeDOT(w io.Writer, o GraphOutput, noStatus bool) error {
	fmt.Fprintln(w, "digraph tasks {")
	names := make([]string, 0, len(o.Nodes))
	for n := range o.Nodes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !noStatus && o.Nodes[n].UpToDate != nil && *o.Nodes[n].UpToDate {
			fmt.Fprintf(w, "  %q [style=dashed];\n", n)
		} else {
			fmt.Fprintf(w, "  %q;\n", n)
		}
	}
	for _, e := range o.Edges {
		fmt.Fprintf(w, "  %q -> %q;\n", e.From, e.To)
	}
	_, err := fmt.Fprintln(w, "}")
	return err
}
func writeText(w io.Writer, o GraphOutput) error {
	seen := map[string]bool{}
	var walk func(string, int) error
	walk = func(n string, depth int) error {
		suffix := ""
		if seen[n] {
			suffix = " (repeated)"
		}
		if _, err := fmt.Fprintf(w, "%s%s%s\n", strings.Repeat("  ", depth), n, suffix); err != nil {
			return err
		}
		if seen[n] {
			return nil
		}
		seen[n] = true
		for _, d := range o.Nodes[n].Deps {
			if err := walk(d, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range o.Roots {
		if err := walk(r, 0); err != nil {
			return err
		}
	}
	return nil
}
