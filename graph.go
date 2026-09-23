package task

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/taskfile/ast"
)

const (
	graphFormatJSON = "json"
	graphFormatDOT  = "dot"
	graphFormatText = "text"

	edgeTypeDep = "dep"
	edgeTypeCmd = "cmd"
)

// templateHole matches a single Go template expression so empty substitutions
// can be recognized when a call-scoped variable was not set.
var templateHole = regexp.MustCompile(`\{\{.*?\}\}`)

// WithGraphFormat selects how [Executor.Graph] prints the dependency graph.
// Accepted values are "json" (the default), "dot", and "text".
func WithGraphFormat(format string) ExecutorOption {
	return &graphFormatOption{format: format}
}

type graphFormatOption struct {
	format string
}

func (o *graphFormatOption) ApplyToExecutor(e *Executor) {
	e.GraphFormat = o.format
}

// WithGraphReverse inverts the graph so it lists tasks that depend on the
// given tasks instead of the tasks they depend on.
func WithGraphReverse(reverse bool) ExecutorOption {
	return &graphReverseOption{reverse: reverse}
}

type graphReverseOption struct {
	reverse bool
}

func (o *graphReverseOption) ApplyToExecutor(e *Executor) {
	e.GraphReverse = o.reverse
}

// WithGraphNoStatus skips fingerprint checks. JSON nodes then omit up_to_date
// and DOT output does not mark up-to-date tasks as dashed.
func WithGraphNoStatus(noStatus bool) ExecutorOption {
	return &graphNoStatusOption{noStatus: noStatus}
}

type graphNoStatusOption struct {
	noStatus bool
}

func (o *graphNoStatusOption) ApplyToExecutor(e *Executor) {
	e.GraphNoStatus = o.noStatus
}

// Graph writes the dependency graph of the given calls to the executor stdout.
// With no calls, the default task is used. Task names are resolved through
// aliases and wildcards, and namespaced tasks keep their fully qualified names.
func (e *Executor) Graph(calls ...*Call) error {
	format, err := normalizeGraphFormat(e.GraphFormat)
	if err != nil {
		return err
	}
	if e.Taskfile == nil || e.Compiler == nil {
		return fmt.Errorf("task: executor is not set up")
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

	b := newGraphBuilder(e)
	roots, err := b.rootNames(filtered)
	if err != nil {
		return err
	}

	seeds := filtered
	if e.GraphReverse {
		seeds = make([]*Call, 0, e.Taskfile.Tasks.Len()+len(filtered))
		for task := range e.Taskfile.Tasks.Values(nil) {
			seeds = append(seeds, &Call{Task: task.Task})
		}
		seeds = append(seeds, filtered...)
	}
	if err := b.expand(seeds); err != nil {
		return err
	}

	next := b.forwardFrom
	if e.GraphReverse {
		b.buildReverse()
		next = b.reverseFrom
	}

	included, edges, err := b.closure(roots, next)
	if err != nil {
		return err
	}

	out := e.Stdout
	if out == nil {
		out = os.Stdout
	}
	switch format {
	case graphFormatJSON:
		return b.writeJSON(out, roots, included, edges, next)
	case graphFormatDOT:
		return b.writeDOT(out, included, edges)
	case graphFormatText:
		return writeGraphText(out, roots, next)
	default:
		return fmt.Errorf("task: unknown graph format %q", format)
	}
}

func normalizeGraphFormat(format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", graphFormatJSON:
		return graphFormatJSON, nil
	case graphFormatDOT:
		return graphFormatDOT, nil
	case graphFormatText:
		return graphFormatText, nil
	default:
		return "", fmt.Errorf("task: unknown graph format %q", format)
	}
}

type graphEdge struct {
	From    string
	To      string
	Type    string
	Vars    map[string]any
	rawVars *ast.Vars
}

func (e graphEdge) identity() string {
	return e.From + "\x00" + e.To + "\x00" + e.Type + "\x00" + varsKey(e.rawVars)
}

type inspectedTask struct {
	name  string
	task  *ast.Task
	edges []graphEdge
}

// merge keeps every edge from a single compilation, including repeated for-loop
// iterations, while a later compilation of the same task only adds edges that
// were not already recorded.
func (t *inspectedTask) merge(batch []graphEdge) {
	counts := make(map[string]int, len(t.edges))
	for _, edge := range t.edges {
		counts[edge.identity()]++
	}
	seen := map[string]int{}
	for _, edge := range batch {
		id := edge.identity()
		seen[id]++
		if seen[id] <= counts[id] {
			continue
		}
		t.edges = append(t.edges, edge)
		counts[id] = seen[id]
	}
}

type graphBuilder struct {
	e       *Executor
	tasks   map[string]*inspectedTask
	queued  map[string]struct{}
	reverse map[string][]graphEdge
}

func newGraphBuilder(e *Executor) *graphBuilder {
	return &graphBuilder{
		e:      e,
		tasks:  map[string]*inspectedTask{},
		queued: map[string]struct{}{},
	}
}

func (b *graphBuilder) rootNames(calls []*Call) ([]string, error) {
	roots := make([]string, 0, len(calls))
	seen := map[string]struct{}{}
	for _, call := range calls {
		name, err := b.e.resolveGraphName(call)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		roots = append(roots, name)
	}
	return roots, nil
}

func (b *graphBuilder) expand(seeds []*Call) error {
	queue := make([]*Call, 0, len(seeds))
	for _, seed := range seeds {
		if err := b.enqueue(&queue, seed); err != nil {
			return err
		}
	}
	for i := 0; i < len(queue); i++ {
		info, err := b.inspect(queue[i])
		if err != nil {
			return err
		}
		for _, edge := range info.edges {
			if err := b.enqueue(&queue, &Call{Task: edge.To, Vars: edge.rawVars, Indirect: true}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *graphBuilder) enqueue(queue *[]*Call, call *Call) error {
	if call == nil || call.Task == "" {
		return nil
	}
	name, err := b.e.resolveGraphName(call)
	if err != nil {
		return err
	}
	key := name + "\x00" + varsKey(call.Vars)
	if _, ok := b.queued[key]; ok {
		return nil
	}
	b.queued[key] = struct{}{}
	*queue = append(*queue, &Call{
		Task:     name,
		Vars:     call.Vars,
		Silent:   call.Silent,
		Indirect: call.Indirect,
	})
	return nil
}

func (b *graphBuilder) inspect(call *Call) (*inspectedTask, error) {
	compiled, err := b.e.CompiledTask(cloneCall(call))
	if err != nil {
		return nil, err
	}
	name := graphTaskName(compiled)
	info := b.tasks[name]
	if info == nil {
		info = &inspectedTask{name: name, task: compiled}
		b.tasks[name] = info
	}

	origin, ok := b.e.Taskfile.Tasks.Get(compiled.Task)
	if !ok {
		origin = compiled
	}
	batch := make([]graphEdge, 0, len(compiled.Deps)+len(compiled.Cmds))
	for _, dep := range compiled.Deps {
		if dep == nil || dep.Task == "" {
			continue
		}
		to, skip, err := b.e.resolveDep(origin, dep.Task)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		batch = append(batch, graphEdge{
			From:    name,
			To:      to,
			Type:    edgeTypeDep,
			Vars:    varsToMap(dep.Vars),
			rawVars: dep.Vars,
		})
	}
	for _, cmd := range compiled.Cmds {
		if cmd == nil || cmd.Task == "" {
			continue
		}
		to, skip, err := b.e.resolveDep(origin, cmd.Task)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		batch = append(batch, graphEdge{
			From:    name,
			To:      to,
			Type:    edgeTypeCmd,
			Vars:    varsToMap(cmd.Vars),
			rawVars: cmd.Vars,
		})
	}
	info.merge(batch)
	return info, nil
}

func (b *graphBuilder) forwardFrom(name string) []graphEdge {
	info := b.tasks[name]
	if info == nil {
		return nil
	}
	return info.edges
}

func (b *graphBuilder) buildReverse() {
	// Preserve taskfile order, then the order edges were discovered on each task.
	names := make([]string, 0, len(b.tasks))
	seen := map[string]struct{}{}
	if b.e.Taskfile != nil {
		for task := range b.e.Taskfile.Tasks.Values(nil) {
			name := task.Task
			if info := b.tasks[name]; info != nil {
				names = append(names, name)
				seen[name] = struct{}{}
			}
		}
	}
	extra := make([]string, 0)
	for name := range b.tasks {
		if _, ok := seen[name]; !ok {
			extra = append(extra, name)
		}
	}
	slices.Sort(extra)
	names = append(names, extra...)

	b.reverse = map[string][]graphEdge{}
	for _, name := range names {
		for _, edge := range b.tasks[name].edges {
			b.reverse[edge.To] = append(b.reverse[edge.To], graphEdge{
				From:    edge.To,
				To:      edge.From,
				Type:    edge.Type,
				Vars:    edge.Vars,
				rawVars: edge.rawVars,
			})
		}
	}
}

func (b *graphBuilder) reverseFrom(name string) []graphEdge {
	return b.reverse[name]
}

// closure walks next from roots. Level computation uses the same adjacency.
// A cycle in the walked subgraph returns an error naming the tasks involved.
func (b *graphBuilder) closure(roots []string, next func(string) []graphEdge) (map[string]struct{}, []graphEdge, error) {
	included := map[string]struct{}{}
	color := map[string]int{}
	var stack []string
	var ordered []graphEdge

	var walk func(string) error
	walk = func(name string) error {
		switch color[name] {
		case 1:
			return cycleError(stack, name)
		case 2:
			return nil
		}
		if _, ok := b.tasks[name]; !ok {
			if _, err := b.inspect(&Call{Task: name}); err != nil {
				return err
			}
		}
		included[name] = struct{}{}
		color[name] = 1
		stack = append(stack, name)
		edges := next(name)
		ordered = append(ordered, edges...)
		for _, edge := range edges {
			if err := walk(edge.To); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = 2
		return nil
	}

	for _, root := range roots {
		if err := walk(root); err != nil {
			return nil, nil, err
		}
	}
	if ordered == nil {
		ordered = []graphEdge{}
	}
	return included, ordered, nil
}

func (e *Executor) resolveGraphName(call *Call) (string, error) {
	if call == nil || call.Task == "" {
		return "", fmt.Errorf("task: Task %q does not exist", "")
	}
	probe := cloneCall(call)
	t, err := e.GetTask(probe)
	if err != nil {
		return "", err
	}
	name := t.Task
	if probe.Vars != nil {
		if matches, ok := probe.Vars.Get("MATCH"); ok {
			if list, ok := matches.Value.([]string); ok {
				for _, match := range list {
					name = strings.Replace(name, "*", match, 1)
				}
			}
		}
	}
	return name, nil
}

// resolveDep resolves a dependency or task-call name. skip is true when the
// name is the empty rendering of a call-scoped template, which happens while
// scanning tasks that are only meaningful with variables supplied by a caller.
func (e *Executor) resolveDep(origin *ast.Task, compiledName string) (string, bool, error) {
	if compiledName == "" || strings.Contains(compiledName, "{{") {
		return "", true, nil
	}
	name, err := e.resolveGraphName(&Call{Task: compiledName})
	if err == nil {
		return name, false, nil
	}
	if renderedFromMissingVars(origin, compiledName) {
		return "", true, nil
	}
	return "", false, err
}

func renderedFromMissingVars(origin *ast.Task, compiledName string) bool {
	if origin == nil {
		return false
	}
	for _, pattern := range callPatterns(origin) {
		if pattern == compiledName || !strings.Contains(pattern, "{{") {
			continue
		}
		if templateHole.ReplaceAllString(pattern, "") == compiledName {
			return true
		}
	}
	return false
}

func callPatterns(t *ast.Task) []string {
	var patterns []string
	for _, dep := range t.Deps {
		if dep != nil && dep.Task != "" {
			patterns = append(patterns, dep.Task)
		}
	}
	for _, cmd := range t.Cmds {
		if cmd != nil && cmd.Task != "" {
			patterns = append(patterns, cmd.Task)
		}
	}
	return patterns
}

func graphTaskName(t *ast.Task) string {
	if t == nil {
		return ""
	}
	if t.FullName != "" {
		return t.FullName
	}
	return t.Task
}

func cloneCall(call *Call) *Call {
	if call == nil {
		return &Call{}
	}
	cloned := *call
	if call.Vars != nil {
		cloned.Vars = call.Vars.DeepCopy()
	}
	return &cloned
}

func varsToMap(vars *ast.Vars) map[string]any {
	if vars == nil || vars.Len() == 0 {
		return nil
	}
	out := make(map[string]any, vars.Len())
	for key, value := range vars.All() {
		out[key] = varValue(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func varValue(v ast.Var) any {
	switch {
	case v.Live != nil:
		return v.Live
	case v.Value != nil:
		return v.Value
	case v.Sh != nil:
		return *v.Sh
	default:
		return nil
	}
}

func varsKey(vars *ast.Vars) string {
	m := varsToMap(vars)
	if len(m) == 0 {
		return ""
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return fmt.Sprint(m)
	}
	return string(encoded)
}

func cycleError(stack []string, repeated string) error {
	start := 0
	for i, name := range stack {
		if name == repeated {
			start = i
			break
		}
	}
	path := append(append([]string{}, stack[start:]...), repeated)
	return fmt.Errorf("task: cycle detected: %s", strings.Join(path, " -> "))
}

func outgoingNames(edges []graphEdge) []string {
	if len(edges) == 0 {
		return []string{}
	}
	seen := map[string]struct{}{}
	names := make([]string, 0, len(edges))
	for _, edge := range edges {
		if _, ok := seen[edge.To]; ok {
			continue
		}
		seen[edge.To] = struct{}{}
		names = append(names, edge.To)
	}
	slices.Sort(names)
	return names
}

func depthGroups(nodes []string, depsOf map[string][]string) [][]string {
	if len(nodes) == 0 {
		return [][]string{}
	}
	memo := map[string]int{}
	var level func(string) int
	level = func(name string) int {
		if lv, ok := memo[name]; ok {
			return lv
		}
		// Temporary value stops a cycle from recursing forever. Cycles are
		// rejected before this runs; this keeps the walk total if one is missed.
		memo[name] = 0
		maxParent := -1
		for _, dep := range depsOf[name] {
			if lv := level(dep); lv > maxParent {
				maxParent = lv
			}
		}
		if maxParent >= 0 {
			memo[name] = maxParent + 1
		}
		return memo[name]
	}

	grouped := map[int][]string{}
	maxLevel := 0
	for _, name := range nodes {
		lv := level(name)
		grouped[lv] = append(grouped[lv], name)
		if lv > maxLevel {
			maxLevel = lv
		}
	}
	out := make([][]string, maxLevel+1)
	for i := 0; i <= maxLevel; i++ {
		group := grouped[i]
		slices.Sort(group)
		if group == nil {
			group = []string{}
		}
		out[i] = group
	}
	return out
}

func longestPath(roots []string, depsOf map[string][]string) []string {
	memo := map[string][]string{}
	var bestFrom func(string) []string
	bestFrom = func(name string) []string {
		if path, ok := memo[name]; ok {
			return path
		}
		best := []string{name}
		children := append([]string{}, depsOf[name]...)
		slices.Sort(children)
		for _, child := range children {
			candidate := append([]string{name}, bestFrom(child)...)
			if betterPath(candidate, best) {
				best = candidate
			}
		}
		memo[name] = append([]string{}, best...)
		return memo[name]
	}

	var best []string
	for _, root := range roots {
		candidate := bestFrom(root)
		if betterPath(candidate, best) {
			best = append([]string{}, candidate...)
		}
	}
	if best == nil {
		return []string{}
	}
	return best
}

func betterPath(candidate, best []string) bool {
	if len(best) == 0 {
		return len(candidate) > 0
	}
	if len(candidate) != len(best) {
		return len(candidate) > len(best)
	}
	for i := range candidate {
		if candidate[i] == best[i] {
			continue
		}
		return candidate[i] < best[i]
	}
	return false
}

type (
	graphJSON struct {
		Roots       []string            `json:"roots"`
		Nodes       map[string]nodeJSON `json:"nodes"`
		Edges       []edgeJSON          `json:"edges"`
		DepthGroups [][]string          `json:"depth_groups"`
		LongestPath []string            `json:"longest_path"`
	}
	nodeJSON struct {
		Name     string   `json:"name"`
		Desc     string   `json:"desc"`
		Location locJSON  `json:"location"`
		UpToDate *bool    `json:"up_to_date,omitempty"`
		Deps     []string `json:"deps"`
		Method   string   `json:"method"`
	}
	locJSON struct {
		Taskfile string `json:"taskfile"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
	}
	edgeJSON struct {
		From string         `json:"from"`
		To   string         `json:"to"`
		Type string         `json:"type"`
		Vars map[string]any `json:"vars"`
	}
)

func (b *graphBuilder) writeJSON(w io.Writer, roots []string, included map[string]struct{}, edges []graphEdge, next func(string) []graphEdge) error {
	depsOf := map[string][]string{}
	nodes := make(map[string]nodeJSON, len(included))
	names := make([]string, 0, len(included))
	for name := range included {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		info := b.tasks[name]
		if info == nil || info.task == nil {
			continue
		}
		deps := outgoingNames(next(name))
		depsOf[name] = deps
		upToDate, err := b.upToDate(info.task)
		if err != nil {
			return err
		}
		node := nodeJSON{
			Name:     name,
			Desc:     info.task.Desc,
			Location: locationJSON(info.task.Location),
			Deps:     deps,
			Method:   b.e.graphMethod(info.task),
		}
		if !b.e.GraphNoStatus {
			node.UpToDate = upToDate
		}
		nodes[name] = node
	}

	outEdges := make([]edgeJSON, 0, len(edges))
	for _, edge := range edges {
		outEdges = append(outEdges, edgeJSON{
			From: edge.From,
			To:   edge.To,
			Type: edge.Type,
			Vars: edge.Vars,
		})
	}
	if roots == nil {
		roots = []string{}
	}

	payload := graphJSON{
		Roots:       roots,
		Nodes:       nodes,
		Edges:       outEdges,
		DepthGroups: depthGroups(names, depsOf),
		LongestPath: longestPath(roots, depsOf),
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func (b *graphBuilder) upToDate(t *ast.Task) (*bool, error) {
	if b.e.GraphNoStatus {
		return nil, nil
	}
	upToDate, err := fingerprint.IsTaskUpToDate(context.Background(), t,
		fingerprint.WithMethod(b.e.graphMethod(t)),
		fingerprint.WithTempDir(b.e.TempDir.Fingerprint),
		fingerprint.WithDry(true),
		fingerprint.WithLogger(b.e.Logger),
	)
	if err != nil {
		return nil, err
	}
	return &upToDate, nil
}

func (b *graphBuilder) writeDOT(w io.Writer, included map[string]struct{}, edges []graphEdge) error {
	if _, err := fmt.Fprintln(w, "digraph tasks {"); err != nil {
		return err
	}
	names := make([]string, 0, len(included))
	for name := range included {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		info := b.tasks[name]
		dashed := false
		if info != nil && !b.e.GraphNoStatus {
			upToDate, err := b.upToDate(info.task)
			if err != nil {
				return err
			}
			dashed = upToDate != nil && *upToDate
		}
		var line string
		if dashed {
			line = fmt.Sprintf("  %s [style=dashed];\n", strconv.Quote(name))
		} else {
			line = fmt.Sprintf("  %s;\n", strconv.Quote(name))
		}
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
	}
	for _, edge := range edges {
		if _, err := fmt.Fprintf(w, "  %s -> %s;\n", strconv.Quote(edge.From), strconv.Quote(edge.To)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "}")
	return err
}

func writeGraphText(w io.Writer, roots []string, next func(string) []graphEdge) error {
	seen := map[string]struct{}{}
	var walk func(string, int) error
	walk = func(name string, depth int) error {
		indent := strings.Repeat("  ", depth)
		if _, ok := seen[name]; ok {
			_, err := fmt.Fprintf(w, "%s%s (repeated)\n", indent, name)
			return err
		}
		seen[name] = struct{}{}
		if _, err := fmt.Fprintf(w, "%s%s\n", indent, name); err != nil {
			return err
		}
		for _, edge := range next(name) {
			if err := walk(edge.To, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if err := walk(root, 0); err != nil {
			return err
		}
	}
	return nil
}

func locationJSON(loc *ast.Location) locJSON {
	if loc == nil {
		return locJSON{}
	}
	return locJSON{
		Taskfile: loc.Taskfile,
		Line:     loc.Line,
		Column:   loc.Column,
	}
}

func (e *Executor) graphMethod(t *ast.Task) string {
	if t != nil && t.Method != "" {
		return t.Method
	}
	if e.Taskfile != nil {
		return e.Taskfile.Method
	}
	return ""
}
