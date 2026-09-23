package task

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/go-task/task/v3/errors"
	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/taskfile/ast"
)

const (
	graphFormatJSON = "json"
	graphFormatDot  = "dot"
	graphFormatText = "text"

	graphEdgeDep = "dep"
	graphEdgeCmd = "cmd"

	visitGray  = 1
	visitBlack = 2
)

type (
	graphLocation struct {
		Taskfile string `json:"taskfile"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
	}
	graphNode struct {
		Name     string        `json:"name"`
		Desc     string        `json:"desc"`
		Location graphLocation `json:"location"`
		UpToDate *bool         `json:"up_to_date,omitempty"`
		Deps     []string      `json:"deps"`
		Method   string        `json:"method"`
	}
	graphEdge struct {
		From string         `json:"from"`
		To   string         `json:"to"`
		Type string         `json:"type"`
		Vars map[string]any `json:"vars"`
	}
	graphOutput struct {
		Roots       []string             `json:"roots"`
		Nodes       map[string]graphNode `json:"nodes"`
		Edges       []graphEdge          `json:"edges"`
		DepthGroups [][]string           `json:"depth_groups"`
		LongestPath []string             `json:"longest_path"`
		tree        map[string][]string  `json:"-"`
	}
	graphLink struct {
		from     string
		to       string
		rawTo    string
		typ      string
		vars     *ast.Vars
		varMap   map[string]any
		resolved bool
		err      error
	}
	taskSnapshot struct {
		name  string
		task  *ast.Task
		links []graphLink
	}
)

// Graph prints the dependency graph for the given calls.
// Output format is selected with [WithGraphFormat] ("json", "dot", or "text").
// JSON is used when no format is set. [WithGraphReverse] inverts the graph so
// it shows tasks that depend on the given tasks. [WithGraphNoStatus] skips
// up-to-date checks.
func (e *Executor) Graph(calls ...*Call) error {
	if e.Taskfile == nil || e.Compiler == nil {
		return errors.New("task: executor is not set up")
	}
	format, err := e.normalizedGraphFormat()
	if err != nil {
		return err
	}
	calls = normalizeGraphCalls(calls)

	roots, rootCalls, err := e.resolveGraphRoots(calls)
	if err != nil {
		return err
	}

	var (
		snaps map[string]*taskSnapshot
		links []graphLink
	)
	if e.GraphReverse {
		snaps, links, err = e.reverseGraph(roots)
	} else {
		snaps, links, err = e.forwardGraph(rootCalls)
	}
	if err != nil {
		return err
	}

	output, err := e.graphOutput(roots, snaps, links)
	if err != nil {
		return err
	}
	return e.writeGraph(e.graphWriter(), format, output)
}

func (e *Executor) normalizedGraphFormat() (string, error) {
	format := strings.ToLower(strings.TrimSpace(e.GraphFormat))
	if format == "" {
		format = graphFormatJSON
	}
	switch format {
	case graphFormatJSON, graphFormatDot, graphFormatText:
		return format, nil
	default:
		return "", fmt.Errorf("task: unknown graph format %q", e.GraphFormat)
	}
}

func normalizeGraphCalls(calls []*Call) []*Call {
	out := make([]*Call, 0, len(calls))
	for _, call := range calls {
		if call == nil || call.Task == "" {
			continue
		}
		out = append(out, call)
	}
	if len(out) == 0 {
		out = []*Call{{Task: "default"}}
	}
	return out
}

func (e *Executor) resolveGraphRoots(calls []*Call) ([]string, []*Call, error) {
	roots := make([]string, 0, len(calls))
	rootCalls := make([]*Call, 0, len(calls))
	seen := make(map[string]bool, len(calls))
	for _, call := range calls {
		name, err := e.resolveTaskName(call.Task)
		if err != nil {
			return nil, nil, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		roots = append(roots, name)
		rootCalls = append(rootCalls, &Call{
			Task:   name,
			Vars:   call.Vars.DeepCopy(),
			Silent: call.Silent,
		})
	}
	return roots, rootCalls, nil
}

func (e *Executor) resolveTaskName(name string) (string, error) {
	if name == "" {
		return "", &errors.TaskNotFoundError{TaskName: name}
	}
	t, err := e.GetTask(&Call{Task: name})
	if err != nil {
		return "", err
	}
	if t == nil || t.Task == "" {
		return name, nil
	}
	return t.Task, nil
}

func (e *Executor) forwardGraph(rootCalls []*Call) (map[string]*taskSnapshot, []graphLink, error) {
	snaps := make(map[string]*taskSnapshot)
	colors := make(map[string]int)
	var stack []string
	var links []graphLink
	for _, call := range rootCalls {
		if err := e.visitForward(call, colors, &stack, snaps, &links); err != nil {
			return nil, nil, err
		}
	}
	return snaps, links, nil
}

func (e *Executor) visitForward(
	call *Call,
	colors map[string]int,
	stack *[]string,
	snaps map[string]*taskSnapshot,
	links *[]graphLink,
) error {
	name, err := e.resolveTaskName(call.Task)
	if err != nil {
		return err
	}
	switch colors[name] {
	case visitBlack:
		return nil
	case visitGray:
		return dependencyCycleError(*stack, name)
	}

	colors[name] = visitGray
	*stack = append(*stack, name)

	snap, err := e.snapshotTask(name, call)
	if err != nil {
		return err
	}
	snaps[name] = snap
	for _, link := range snap.links {
		if link.err != nil {
			return link.err
		}
		*links = append(*links, link)
		if err := e.visitForward(&Call{
			Task:     link.to,
			Vars:     link.vars.DeepCopy(),
			Indirect: true,
		}, colors, stack, snaps, links); err != nil {
			return err
		}
	}

	*stack = (*stack)[:len(*stack)-1]
	colors[name] = visitBlack
	return nil
}

func (e *Executor) reverseGraph(roots []string) (map[string]*taskSnapshot, []graphLink, error) {
	all := make(map[string]*taskSnapshot)
	for name, task := range e.Taskfile.Tasks.All(nil) {
		if task == nil {
			continue
		}
		snap, err := e.snapshotTask(name, &Call{Task: name})
		if err != nil {
			return nil, nil, err
		}
		all[name] = snap
	}

	adj := make(map[string][]graphLink)
	for name := range e.Taskfile.Tasks.Keys(nil) {
		snap, ok := all[name]
		if !ok {
			continue
		}
		for _, link := range snap.links {
			if !link.resolved {
				continue
			}
			rev := link
			rev.from, rev.to = link.to, link.from
			adj[rev.from] = append(adj[rev.from], rev)
		}
	}

	colors := make(map[string]int)
	reachable := make(map[string]bool)
	var stack []string
	var links []graphLink
	for _, root := range roots {
		if _, ok := all[root]; !ok {
			return nil, nil, &errors.TaskNotFoundError{TaskName: root}
		}
		if err := visitReverse(root, adj, colors, reachable, &stack, &links); err != nil {
			return nil, nil, err
		}
	}

	for name := range reachable {
		snap := all[name]
		if snap == nil {
			return nil, nil, &errors.TaskNotFoundError{TaskName: name}
		}
		for _, link := range snap.links {
			if link.err != nil {
				return nil, nil, link.err
			}
		}
	}

	snaps := make(map[string]*taskSnapshot, len(reachable))
	for name := range reachable {
		snaps[name] = all[name]
	}
	return snaps, links, nil
}

func visitReverse(
	name string,
	adj map[string][]graphLink,
	colors map[string]int,
	reachable map[string]bool,
	stack *[]string,
	links *[]graphLink,
) error {
	switch colors[name] {
	case visitBlack:
		return nil
	case visitGray:
		return dependencyCycleError(*stack, name)
	}

	colors[name] = visitGray
	reachable[name] = true
	*stack = append(*stack, name)
	for _, link := range adj[name] {
		*links = append(*links, link)
		if err := visitReverse(link.to, adj, colors, reachable, stack, links); err != nil {
			return err
		}
	}
	*stack = (*stack)[:len(*stack)-1]
	colors[name] = visitBlack
	return nil
}

func (e *Executor) snapshotTask(name string, call *Call) (*taskSnapshot, error) {
	vars := call.Vars.DeepCopy()
	compiled, err := e.FastCompiledTask(&Call{
		Task:     name,
		Vars:     vars,
		Silent:   call.Silent,
		Indirect: call.Indirect,
	})
	if err != nil {
		return nil, err
	}
	snap := &taskSnapshot{
		name: name,
		task: compiled,
	}
	for _, dep := range compiled.Deps {
		if dep == nil || dep.Task == "" {
			continue
		}
		snap.links = append(snap.links, e.graphLink(name, dep.Task, graphEdgeDep, dep.Vars))
	}
	for _, cmd := range compiled.Cmds {
		if cmd == nil || cmd.Task == "" {
			continue
		}
		snap.links = append(snap.links, e.graphLink(name, cmd.Task, graphEdgeCmd, cmd.Vars))
	}
	return snap, nil
}

func (e *Executor) graphLink(from, to, typ string, vars *ast.Vars) graphLink {
	link := graphLink{
		from:   from,
		rawTo:  to,
		typ:    typ,
		vars:   vars,
		varMap: varsToMap(vars),
	}
	resolved, err := e.resolveTaskName(to)
	if err != nil {
		link.err = err
		return link
	}
	link.to = resolved
	link.resolved = true
	return link
}

func (e *Executor) graphOutput(roots []string, snaps map[string]*taskSnapshot, links []graphLink) (*graphOutput, error) {
	outgoing := make(map[string][]string, len(snaps))
	for name := range snaps {
		outgoing[name] = nil
	}
	edges := make([]graphEdge, 0, len(links))
	for _, link := range links {
		if _, ok := snaps[link.from]; !ok {
			continue
		}
		if _, ok := snaps[link.to]; !ok {
			continue
		}
		outgoing[link.from] = append(outgoing[link.from], link.to)
		vars := link.varMap
		if vars == nil {
			vars = map[string]any{}
		}
		edges = append(edges, graphEdge{
			From: link.from,
			To:   link.to,
			Type: link.typ,
			Vars: vars,
		})
	}
	slices.SortFunc(edges, func(a, b graphEdge) int {
		if c := cmp.Compare(a.From, b.From); c != 0 {
			return c
		}
		if c := cmp.Compare(a.To, b.To); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Type, b.Type); c != 0 {
			return c
		}
		aj, _ := json.Marshal(a.Vars)
		bj, _ := json.Marshal(b.Vars)
		return cmp.Compare(string(aj), string(bj))
	})

	nodes := make(map[string]graphNode, len(snaps))
	for name, snap := range snaps {
		node, err := e.graphNode(name, snap, uniqueSorted(outgoing[name]))
		if err != nil {
			return nil, err
		}
		nodes[name] = node
	}

	tree := make(map[string][]string, len(snaps))
	for _, link := range links {
		if _, ok := snaps[link.from]; !ok {
			continue
		}
		if _, ok := snaps[link.to]; !ok {
			continue
		}
		tree[link.from] = append(tree[link.from], link.to)
	}

	return &graphOutput{
		Roots:       roots,
		Nodes:       nodes,
		Edges:       edges,
		DepthGroups: depthGroups(outgoing),
		LongestPath: longestPath(roots, outgoing),
		tree:        tree,
	}, nil
}

func (e *Executor) graphNode(name string, snap *taskSnapshot, deps []string) (graphNode, error) {
	if deps == nil {
		deps = []string{}
	}
	method := ""
	desc := ""
	var location graphLocation
	if snap != nil && snap.task != nil {
		method = snap.task.Method
		desc = snap.task.Desc
		if snap.task.Location != nil {
			location = graphLocation{
				Taskfile: snap.task.Location.Taskfile,
				Line:     snap.task.Location.Line,
				Column:   snap.task.Location.Column,
			}
		}
	}
	if method == "" && e.Taskfile != nil {
		method = e.Taskfile.Method
	}

	node := graphNode{
		Name:     name,
		Desc:     desc,
		Location: location,
		Deps:     deps,
		Method:   method,
	}
	if e.GraphNoStatus {
		return node, nil
	}

	upToDate, err := fingerprint.IsTaskUpToDate(context.Background(), snap.task,
		fingerprint.WithMethod(method),
		fingerprint.WithTempDir(e.TempDir.Fingerprint),
		fingerprint.WithDry(e.Dry),
		fingerprint.WithLogger(e.Logger),
	)
	if err != nil {
		return graphNode{}, err
	}
	node.UpToDate = &upToDate
	return node, nil
}

func (e *Executor) writeGraph(w io.Writer, format string, output *graphOutput) error {
	switch format {
	case graphFormatJSON:
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	case graphFormatDot:
		return writeDOT(w, output, e.GraphNoStatus)
	case graphFormatText:
		return writeText(w, output)
	default:
		return fmt.Errorf("task: unknown graph format %q", format)
	}
}

func (e *Executor) graphWriter() io.Writer {
	if e.Stdout != nil {
		return e.Stdout
	}
	return os.Stdout
}

func writeDOT(w io.Writer, output *graphOutput, noStatus bool) error {
	names := make([]string, 0, len(output.Nodes))
	for name := range output.Nodes {
		names = append(names, name)
	}
	slices.Sort(names)

	if _, err := fmt.Fprintln(w, "digraph tasks {"); err != nil {
		return err
	}
	for _, name := range names {
		node := output.Nodes[name]
		id := strconv.Quote(name)
		if !noStatus && node.UpToDate != nil && *node.UpToDate {
			if _, err := fmt.Fprintf(w, "  %s [style=dashed];\n", id); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "  %s;\n", id); err != nil {
			return err
		}
	}
	for _, edge := range output.Edges {
		if _, err := fmt.Fprintf(w, "  %s -> %s;\n", strconv.Quote(edge.From), strconv.Quote(edge.To)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "}")
	return err
}

func writeText(w io.Writer, output *graphOutput) error {
	children := output.tree
	seen := map[string]bool{}
	var walk func(name string, depth int) error
	walk = func(name string, depth int) error {
		indent := strings.Repeat("  ", depth)
		if seen[name] {
			_, err := fmt.Fprintf(w, "%s%s (repeated)\n", indent, name)
			return err
		}
		seen[name] = true
		if _, err := fmt.Fprintf(w, "%s%s\n", indent, name); err != nil {
			return err
		}
		for _, child := range children[name] {
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range output.Roots {
		if err := walk(root, 0); err != nil {
			return err
		}
	}
	return nil
}

func depthGroups(outgoing map[string][]string) [][]string {
	if len(outgoing) == 0 {
		return [][]string{}
	}
	memo := make(map[string]int, len(outgoing))
	var depth func(string) int
	depth = func(name string) int {
		if d, ok := memo[name]; ok {
			return d
		}
		// Mark before recursing so a cycle cannot overflow the stack.
		memo[name] = 0
		maxChild := -1
		for _, child := range uniqueSorted(outgoing[name]) {
			d := depth(child)
			if d > maxChild {
				maxChild = d
			}
		}
		d := 0
		if maxChild >= 0 {
			d = maxChild + 1
		}
		memo[name] = d
		return d
	}

	maxDepth := 0
	for name := range outgoing {
		if d := depth(name); d > maxDepth {
			maxDepth = d
		}
	}
	groups := make([][]string, maxDepth+1)
	for i := range groups {
		groups[i] = []string{}
	}
	for name := range outgoing {
		groups[memo[name]] = append(groups[memo[name]], name)
	}
	for i := range groups {
		slices.Sort(groups[i])
	}
	return groups
}

func longestPath(roots []string, outgoing map[string][]string) []string {
	memo := make(map[string][]string, len(outgoing))
	var from func(string) []string
	from = func(name string) []string {
		if path, ok := memo[name]; ok {
			return path
		}
		best := []string{name}
		for _, child := range uniqueSorted(outgoing[name]) {
			tail := from(child)
			candidate := make([]string, 0, 1+len(tail))
			candidate = append(candidate, name)
			candidate = append(candidate, tail...)
			if betterPath(candidate, best) {
				best = candidate
			}
		}
		// Copy so later appends cannot alias the memoized path.
		memo[name] = append([]string(nil), best...)
		return memo[name]
	}

	var best []string
	for _, root := range roots {
		path := from(root)
		if best == nil || betterPath(path, best) {
			best = append([]string(nil), path...)
		}
	}
	if best == nil {
		return []string{}
	}
	return best
}

func betterPath(candidate, best []string) bool {
	if len(candidate) != len(best) {
		return len(candidate) > len(best)
	}
	return slices.Compare(candidate, best) < 0
}

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, name := range in {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func varsToMap(vars *ast.Vars) map[string]any {
	m := map[string]any{}
	if vars == nil {
		return m
	}
	for key, value := range vars.All() {
		switch {
		case value.Live != nil:
			m[key] = value.Live
		case value.Sh != nil && value.Value == nil:
			m[key] = map[string]any{"sh": *value.Sh}
		case value.Ref != "" && value.Value == nil:
			m[key] = map[string]any{"ref": value.Ref}
		default:
			m[key] = value.Value
		}
	}
	return m
}

func dependencyCycleError(stack []string, repeated string) error {
	start := 0
	for i, name := range stack {
		if name == repeated {
			start = i
			break
		}
	}
	cycle := make([]string, 0, len(stack[start:])+1)
	cycle = append(cycle, stack[start:]...)
	cycle = append(cycle, repeated)
	return &errors.TaskDependencyCycleError{Cycle: cycle}
}
