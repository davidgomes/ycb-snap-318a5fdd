package task

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/go-task/task/v3/errors"
	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/internal/logger"
	"github.com/go-task/task/v3/taskfile/ast"
)

const (
	graphFormatJSON = "json"
	graphFormatDOT  = "dot"
	graphFormatText = "text"

	graphEdgeDep = "dep"
	graphEdgeCmd = "cmd"
)

type graphEdge struct {
	From string
	To   string
	Type string
	Vars map[string]any
}

type graphOutput struct {
	Roots       []string                   `json:"roots"`
	Nodes       map[string]graphOutputNode `json:"nodes"`
	Edges       []graphOutputEdge          `json:"edges"`
	DepthGroups [][]string                 `json:"depth_groups"`
	LongestPath []string                   `json:"longest_path"`
}

type graphOutputNode struct {
	Name     string         `json:"name"`
	Desc     string         `json:"desc"`
	Location graphOutputLoc `json:"location"`
	UpToDate *bool          `json:"up_to_date,omitempty"`
	Deps     []string       `json:"deps"`
	Method   string         `json:"method"`
}

type graphOutputLoc struct {
	Taskfile string `json:"taskfile"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

type graphOutputEdge struct {
	From string         `json:"from"`
	To   string         `json:"to"`
	Type string         `json:"type"`
	Vars map[string]any `json:"vars"`
}

type graphBuilder struct {
	e        *Executor
	nodes    map[string]*ast.Task
	edges    []graphEdge
	expanded map[string]bool
}

// Graph writes the dependency graph of the given tasks.
// The format is selected with [WithGraphFormat] and defaults to json.
// [WithGraphReverse] inverts the graph so it lists tasks that depend on the
// requested tasks. [WithGraphNoStatus] skips fingerprint checks.
// When calls is empty, the default task is used.
func (e *Executor) Graph(calls ...*Call) error {
	if e.Taskfile == nil || e.Taskfile.Tasks == nil {
		return errors.New("task: no Taskfile loaded")
	}

	format := e.GraphFormat
	if format == "" {
		format = graphFormatJSON
	}
	switch format {
	case graphFormatJSON, graphFormatDOT, graphFormatText:
	default:
		return fmt.Errorf("task: unsupported graph format %q", format)
	}

	filtered := make([]*Call, 0, len(calls))
	for _, call := range calls {
		if call != nil && call.Task != "" {
			filtered = append(filtered, call)
		}
	}
	if len(filtered) == 0 {
		filtered = []*Call{{Task: "default"}}
	}

	output, err := e.buildGraph(filtered)
	if err != nil {
		return err
	}

	w := e.Stdout
	if w == nil {
		w = os.Stdout
	}
	switch format {
	case graphFormatDOT:
		return writeGraphDOT(w, output)
	case graphFormatText:
		return writeGraphText(w, output)
	default:
		return writeGraphJSON(w, output)
	}
}

func (e *Executor) buildGraph(calls []*Call) (*graphOutput, error) {
	b := &graphBuilder{
		e:        e,
		nodes:    map[string]*ast.Task{},
		expanded: map[string]bool{},
	}

	// Reverse mode has to see every task, because a dependent can live
	// anywhere in the Taskfile, not only under the requested roots.
	if e.GraphReverse {
		for task := range e.Taskfile.Tasks.Values(nil) {
			if _, err := b.expand(&Call{Task: task.Task}); err != nil {
				return nil, err
			}
		}
	}

	roots := make([]string, 0, len(calls))
	seenRoots := map[string]bool{}
	for _, call := range calls {
		name, err := b.expand(call)
		if err != nil {
			return nil, err
		}
		if seenRoots[name] {
			continue
		}
		seenRoots[name] = true
		roots = append(roots, name)
	}

	edges := b.edges
	if e.GraphReverse {
		edges = reverseGraphEdges(edges)
	}

	keep, err := reachableGraphNodes(roots, edges)
	if err != nil {
		return nil, err
	}
	edges = filterGraphEdges(edges, keep)

	nodes := make(map[string]*ast.Task, len(keep))
	for name := range keep {
		nodes[name] = b.nodes[name]
	}

	out, err := e.graphOutput(roots, nodes, edges)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (b *graphBuilder) expand(call *Call) (string, error) {
	t, err := b.compile(call)
	if err != nil {
		return "", err
	}
	name := taskGraphName(t)
	if b.expanded[name] {
		return name, nil
	}
	b.expanded[name] = true
	b.nodes[name] = t

	for _, dep := range t.Deps {
		if dep == nil || dep.Task == "" {
			continue
		}
		vars := varsToMap(dep.Vars)
		childName, err := b.expand(&Call{Task: dep.Task, Vars: dep.Vars, Silent: dep.Silent, Indirect: true})
		if err != nil {
			if skipUnresolvedWildcardDep(name, err) {
				continue
			}
			return "", err
		}
		b.edges = append(b.edges, graphEdge{From: name, To: childName, Type: graphEdgeDep, Vars: vars})
	}
	for _, cmd := range t.Cmds {
		if cmd == nil || cmd.Task == "" {
			continue
		}
		vars := varsToMap(cmd.Vars)
		childName, err := b.expand(&Call{Task: cmd.Task, Vars: cmd.Vars, Silent: cmd.Silent, Indirect: true})
		if err != nil {
			if skipUnresolvedWildcardDep(name, err) {
				continue
			}
			return "", err
		}
		b.edges = append(b.edges, graphEdge{From: name, To: childName, Type: graphEdgeCmd, Vars: vars})
	}
	return name, nil
}

func (b *graphBuilder) compile(call *Call) (*ast.Task, error) {
	t, err := b.e.CompiledTask(call)
	if err == nil {
		return t, nil
	}
	// Wildcard task keys cannot be compiled until a concrete call supplies
	// MATCH. Fall back so the rest of the Taskfile can still be scanned.
	var notFound *errors.TaskNotFoundError
	if errors.As(err, &notFound) || call == nil || !strings.Contains(call.Task, "*") {
		return nil, err
	}
	t, fastErr := b.e.FastCompiledTask(call)
	if fastErr != nil {
		return nil, err
	}
	return t, nil
}

func skipUnresolvedWildcardDep(parent string, err error) bool {
	if !strings.Contains(parent, "*") {
		return false
	}
	var notFound *errors.TaskNotFoundError
	return errors.As(err, &notFound)
}

func taskGraphName(t *ast.Task) string {
	if t.FullName != "" {
		return t.FullName
	}
	return t.Task
}

func varsToMap(vars *ast.Vars) map[string]any {
	out := map[string]any{}
	if vars == nil {
		return out
	}
	for k, v := range vars.All() {
		switch {
		case v.Live != nil:
			out[k] = v.Live
		case v.Value != nil:
			out[k] = v.Value
		case v.Sh != nil:
			out[k] = *v.Sh
		}
	}
	return out
}

func reverseGraphEdges(edges []graphEdge) []graphEdge {
	out := make([]graphEdge, len(edges))
	for i, edge := range edges {
		out[i] = graphEdge{
			From: edge.To,
			To:   edge.From,
			Type: edge.Type,
			Vars: edge.Vars,
		}
	}
	return out
}

func filterGraphEdges(edges []graphEdge, keep map[string]bool) []graphEdge {
	out := make([]graphEdge, 0, len(edges))
	for _, edge := range edges {
		if keep[edge.From] && keep[edge.To] {
			out = append(out, edge)
		}
	}
	return out
}

// reachableGraphNodes returns the tasks reachable from roots along outgoing
// edges. A back-edge to a task already on the stack is a dependency cycle.
func reachableGraphNodes(roots []string, edges []graphEdge) (map[string]bool, error) {
	adj := map[string][]string{}
	for _, edge := range edges {
		adj[edge.From] = append(adj[edge.From], edge.To)
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	keep := map[string]bool{}
	var stack []string

	var visit func(string) error
	visit = func(name string) error {
		switch color[name] {
		case gray:
			i := slices.Index(stack, name)
			if i < 0 {
				i = 0
			}
			cycle := append(slices.Clone(stack[i:]), name)
			return &errors.TaskDependencyCycleError{Cycle: cycle}
		case black:
			return nil
		}
		color[name] = gray
		stack = append(stack, name)
		for _, next := range adj[name] {
			if err := visit(next); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = black
		keep[name] = true
		return nil
	}

	for _, root := range roots {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	return keep, nil
}

func (e *Executor) graphOutput(roots []string, nodes map[string]*ast.Task, edges []graphEdge) (*graphOutput, error) {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	slices.Sort(names)

	outNodes := make(map[string]graphOutputNode, len(nodes))
	for _, name := range names {
		node, err := e.graphNode(name, nodes[name], edges)
		if err != nil {
			return nil, err
		}
		outNodes[name] = node
	}

	outEdges := make([]graphOutputEdge, 0, len(edges))
	for _, edge := range edges {
		vars := edge.Vars
		if vars == nil {
			vars = map[string]any{}
		}
		outEdges = append(outEdges, graphOutputEdge{
			From: edge.From,
			To:   edge.To,
			Type: edge.Type,
			Vars: vars,
		})
	}

	if roots == nil {
		roots = []string{}
	}
	return &graphOutput{
		Roots:       roots,
		Nodes:       outNodes,
		Edges:       outEdges,
		DepthGroups: graphDepthGroups(names, edges),
		LongestPath: graphLongestPath(roots, edges),
	}, nil
}

func (e *Executor) graphNode(name string, t *ast.Task, edges []graphEdge) (graphOutputNode, error) {
	if t == nil {
		return graphOutputNode{}, fmt.Errorf("task: graph node %q is missing", name)
	}
	method := e.Taskfile.Method
	if t.Method != "" {
		method = t.Method
	}
	if method == "" {
		method = "checksum"
	}

	node := graphOutputNode{
		Name:     name,
		Desc:     t.Desc,
		Location: graphLocation(t.Location),
		Deps:     outgoingNames(name, edges),
		Method:   method,
	}
	if e.GraphNoStatus {
		return node, nil
	}

	l := e.Logger
	if l == nil {
		l = &logger.Logger{Stdout: io.Discard, Stderr: io.Discard}
	}
	upToDate, err := fingerprint.IsTaskUpToDate(context.Background(), t,
		fingerprint.WithMethod(method),
		fingerprint.WithTempDir(e.TempDir.Fingerprint),
		fingerprint.WithDry(true),
		fingerprint.WithLogger(l),
	)
	if err != nil {
		return graphOutputNode{}, err
	}
	node.UpToDate = &upToDate
	return node, nil
}

func graphLocation(loc *ast.Location) graphOutputLoc {
	if loc == nil {
		return graphOutputLoc{}
	}
	return graphOutputLoc{
		Taskfile: loc.Taskfile,
		Line:     loc.Line,
		Column:   loc.Column,
	}
}

func outgoingNames(name string, edges []graphEdge) []string {
	seen := map[string]struct{}{}
	names := make([]string, 0)
	for _, edge := range edges {
		if edge.From != name {
			continue
		}
		if _, ok := seen[edge.To]; ok {
			continue
		}
		seen[edge.To] = struct{}{}
		names = append(names, edge.To)
	}
	slices.Sort(names)
	return names
}

func childNames(name string, edges []graphEdge) []string {
	names := make([]string, 0)
	for _, edge := range edges {
		if edge.From == name {
			names = append(names, edge.To)
		}
	}
	return names
}

func graphDepthGroups(names []string, edges []graphEdge) [][]string {
	depsOf := func(name string) []string {
		return outgoingNames(name, edges)
	}
	level := map[string]int{}
	var compute func(string) int
	compute = func(name string) int {
		if l, ok := level[name]; ok {
			return l
		}
		// Mark in progress so a cycle that escaped detection cannot recurse forever.
		level[name] = 0
		maxDep := -1
		for _, dep := range depsOf(name) {
			if dl := compute(dep); dl > maxDep {
				maxDep = dl
			}
		}
		level[name] = maxDep + 1
		return level[name]
	}

	maxLevel := 0
	for _, name := range names {
		if l := compute(name); l > maxLevel {
			maxLevel = l
		}
	}
	groups := make([][]string, maxLevel+1)
	for i := range groups {
		groups[i] = []string{}
	}
	for _, name := range names {
		l := level[name]
		groups[l] = append(groups[l], name)
	}
	for i := range groups {
		slices.Sort(groups[i])
	}
	return groups
}

func graphLongestPath(roots []string, edges []graphEdge) []string {
	depsOf := func(name string) []string {
		return outgoingNames(name, edges)
	}
	memo := map[string][]string{}
	var bestFrom func(string) []string
	bestFrom = func(name string) []string {
		if path, ok := memo[name]; ok {
			return path
		}
		best := []string{name}
		for _, dep := range depsOf(name) {
			candidate := append([]string{name}, bestFrom(dep)...)
			best = preferPath(best, candidate)
		}
		memo[name] = append([]string{}, best...)
		return memo[name]
	}

	var best []string
	for _, root := range roots {
		best = preferPath(best, bestFrom(root))
	}
	if best == nil {
		return []string{}
	}
	return best
}

// preferPath keeps the longer chain. Equal lengths use lexicographical order
// so the result does not depend on map iteration.
func preferPath(current, candidate []string) []string {
	if len(candidate) == 0 {
		return current
	}
	if len(current) == 0 || len(candidate) > len(current) {
		return candidate
	}
	if len(candidate) < len(current) {
		return current
	}
	for i := range current {
		if current[i] < candidate[i] {
			return current
		}
		if current[i] > candidate[i] {
			return candidate
		}
	}
	return current
}

func writeGraphJSON(w io.Writer, output *graphOutput) error {
	if output.Edges == nil {
		output.Edges = []graphOutputEdge{}
	}
	if output.Roots == nil {
		output.Roots = []string{}
	}
	if output.LongestPath == nil {
		output.LongestPath = []string{}
	}
	if output.DepthGroups == nil {
		output.DepthGroups = [][]string{}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func writeGraphDOT(w io.Writer, output *graphOutput) error {
	names := make([]string, 0, len(output.Nodes))
	for name := range output.Nodes {
		names = append(names, name)
	}
	slices.Sort(names)

	var b strings.Builder
	b.WriteString("digraph tasks {\n")
	for _, name := range names {
		b.WriteString("  ")
		b.WriteString(dotID(name))
		node := output.Nodes[name]
		if node.UpToDate != nil && *node.UpToDate {
			b.WriteString(" [style=dashed]")
		}
		b.WriteString(";\n")
	}
	for _, edge := range output.Edges {
		fmt.Fprintf(&b, "  %s -> %s;\n", dotID(edge.From), dotID(edge.To))
	}
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func dotID(name string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range name {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func writeGraphText(w io.Writer, output *graphOutput) error {
	edges := make([]graphEdge, len(output.Edges))
	for i, edge := range output.Edges {
		edges[i] = graphEdge{From: edge.From, To: edge.To, Type: edge.Type, Vars: edge.Vars}
	}
	var b strings.Builder
	expanded := map[string]bool{}
	for _, root := range output.Roots {
		writeGraphTree(&b, root, 0, expanded, edges)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeGraphTree(b *strings.Builder, name string, depth int, expanded map[string]bool, edges []graphEdge) {
	b.WriteString(strings.Repeat("  ", depth))
	if expanded[name] {
		b.WriteString(name)
		b.WriteString(" (repeated)\n")
		return
	}
	expanded[name] = true
	b.WriteString(name)
	b.WriteByte('\n')
	for _, child := range childNames(name, edges) {
		writeGraphTree(b, child, depth+1, expanded, edges)
	}
}
