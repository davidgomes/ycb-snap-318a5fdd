package task_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-task/task/v3"
	"github.com/go-task/task/v3/errors"
)

const graphDir = "testdata/graph"

func runGraph(t *testing.T, opts []task.ExecutorOption, calls ...*task.Call) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	e := task.NewExecutor(append([]task.ExecutorOption{
		task.WithDir(graphDir),
		task.WithStdout(&buf),
		task.WithStderr(&buf),
	}, opts...)...)
	require.NoError(t, e.Setup())
	err := e.Graph(calls...)
	return buf.String(), err
}

func graphJSON(t *testing.T, opts []task.ExecutorOption, calls ...*task.Call) map[string]any {
	t.Helper()
	out, err := runGraph(t, opts, calls...)
	require.NoError(t, err)
	var g map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &g))
	return g
}

func TestGraphJSON(t *testing.T) {
	t.Parallel()

	g := graphJSON(t, nil, &task.Call{Task: "build"})

	assert.ElementsMatch(t, []string{"roots", "nodes", "edges", "depth_groups", "longest_path"}, mapKeys(g))
	assert.Equal(t, []any{"build"}, g["roots"])

	nodes := g["nodes"].(map[string]any)
	assert.Len(t, nodes, 5)
	build := nodes["build"].(map[string]any)
	assert.ElementsMatch(t, []string{"name", "desc", "location", "up_to_date", "deps", "method"}, mapKeys(build))
	assert.Equal(t, "build", build["name"])
	assert.Equal(t, "Build everything", build["desc"])
	assert.Equal(t, false, build["up_to_date"])
	assert.Equal(t, "checksum", build["method"])
	assert.Equal(t, []any{"compile", "generate", "lib:package"}, build["deps"])
	location := build["location"].(map[string]any)
	assert.Equal(t, float64(11), location["line"])
	assert.Equal(t, float64(3), location["column"])
	assert.Equal(t, "Taskfile.yml", filepath.Base(location["taskfile"].(string)))
	assert.Equal(t, "timestamp", nodes["generate"].(map[string]any)["method"])

	assert.Equal(t, []any{
		map[string]any{"from": "build", "to": "compile", "type": "dep", "vars": map[string]any{}},
		map[string]any{"from": "build", "to": "generate", "type": "dep", "vars": map[string]any{}},
		map[string]any{"from": "build", "to": "lib:package", "type": "cmd", "vars": map[string]any{"MODE": "release"}},
		map[string]any{"from": "compile", "to": "generate", "type": "dep", "vars": map[string]any{}},
		map[string]any{"from": "lib:package", "to": "lib:prepare", "type": "dep", "vars": map[string]any{}},
	}, g["edges"])

	assert.Equal(t, []any{
		[]any{"generate", "lib:prepare"},
		[]any{"compile", "lib:package"},
		[]any{"build"},
	}, g["depth_groups"])
	assert.Equal(t, []any{"build", "compile", "generate"}, g["longest_path"])
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestGraphDefaultTask(t *testing.T) {
	t.Parallel()

	g := graphJSON(t, nil)
	assert.Equal(t, []any{"default"}, g["roots"])
	assert.Equal(t, []any{"default", "build", "compile", "generate"}, g["longest_path"])
}

func TestGraphResolvesAliasesAndWildcards(t *testing.T) {
	t.Parallel()

	g := graphJSON(t, nil, &task.Call{Task: "al"}, &task.Call{Task: "wild-foo"})
	assert.Equal(t, []any{"aliased", "wild-*"}, g["roots"])
	assert.Contains(t, g["nodes"], "aliased")
	assert.Contains(t, g["nodes"], "wild-*")
}

func TestGraphForLoopEdges(t *testing.T) {
	t.Parallel()

	g := graphJSON(t, nil, &task.Call{Task: "fanout"})
	assert.Equal(t, []any{
		map[string]any{"from": "fanout", "to": "generate", "type": "dep", "vars": map[string]any{"TARGET": "a"}},
		map[string]any{"from": "fanout", "to": "generate", "type": "dep", "vars": map[string]any{"TARGET": "b"}},
		map[string]any{"from": "fanout", "to": "generate", "type": "dep", "vars": map[string]any{"TARGET": "c"}},
	}, g["edges"])
	assert.Equal(t, []any{"generate"}, g["nodes"].(map[string]any)["fanout"].(map[string]any)["deps"])
}

func TestGraphUpToDate(t *testing.T) {
	t.Parallel()

	g := graphJSON(t, nil, &task.Call{Task: "uses-fresh"})
	nodes := g["nodes"].(map[string]any)
	assert.Equal(t, true, nodes["fresh"].(map[string]any)["up_to_date"])
	assert.Equal(t, false, nodes["uses-fresh"].(map[string]any)["up_to_date"])

	g = graphJSON(t, []task.ExecutorOption{task.WithGraphNoStatus(true)}, &task.Call{Task: "uses-fresh"})
	for _, node := range g["nodes"].(map[string]any) {
		assert.NotContains(t, node, "up_to_date")
	}
}

func TestGraphDOT(t *testing.T) {
	t.Parallel()

	out, err := runGraph(t, []task.ExecutorOption{task.WithGraphFormat("dot")}, &task.Call{Task: "uses-fresh"})
	require.NoError(t, err)
	assert.Equal(t, `digraph tasks {
  "fresh" [style=dashed];
  "uses-fresh";
  "uses-fresh" -> "fresh";
}
`, out)

	out, err = runGraph(t, []task.ExecutorOption{
		task.WithGraphFormat("dot"),
		task.WithGraphNoStatus(true),
	}, &task.Call{Task: "uses-fresh"})
	require.NoError(t, err)
	assert.NotContains(t, out, "dashed")
}

func TestGraphText(t *testing.T) {
	t.Parallel()

	out, err := runGraph(t, []task.ExecutorOption{task.WithGraphFormat("text")}, &task.Call{Task: "default"})
	require.NoError(t, err)
	assert.Equal(t, `default
  build
    compile
      generate
    generate (repeated)
    lib:package
      lib:prepare
`, out)
}

func TestGraphReverse(t *testing.T) {
	t.Parallel()

	opts := []task.ExecutorOption{task.WithGraphReverse(true)}
	g := graphJSON(t, opts, &task.Call{Task: "generate"})

	assert.Equal(t, []any{"generate"}, g["roots"])
	nodes := g["nodes"].(map[string]any)
	assert.Len(t, nodes, 7)
	assert.Equal(t,
		[]any{"aliased", "build", "compile", "fanout", "wild-*"},
		nodes["generate"].(map[string]any)["deps"],
	)
	assert.Equal(t, []any{
		[]any{"aliased", "default", "fanout", "wild-*"},
		[]any{"build"},
		[]any{"compile"},
		[]any{"generate"},
	}, g["depth_groups"])
	assert.Equal(t, []any{"generate", "compile", "build", "default"}, g["longest_path"])

	for _, edge := range g["edges"].([]any) {
		edge := edge.(map[string]any)
		if edge["from"] == "build" {
			assert.Equal(t, "default", edge["to"])
		}
	}

	out, err := runGraph(t, append(opts, task.WithGraphFormat("text")), &task.Call{Task: "lib:prepare"})
	require.NoError(t, err)
	assert.Equal(t, `lib:prepare
  lib:package
    build
      default
`, out)
}

func TestGraphTaskNotFound(t *testing.T) {
	t.Parallel()

	_, err := runGraph(t, nil, &task.Call{Task: "does-not-exist-at-all"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist-at-all")

	_, err = runGraph(t, nil, &task.Call{Task: "broken"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist")

	_, err = runGraph(t, []task.ExecutorOption{task.WithGraphReverse(true)}, &task.Call{Task: "does-not-exist-at-all"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist-at-all")
}

func TestGraphCycle(t *testing.T) {
	t.Parallel()

	for _, reverse := range []bool{false, true} {
		_, err := runGraph(t, []task.ExecutorOption{task.WithGraphReverse(reverse)}, &task.Call{Task: "cycle-a"})
		require.Error(t, err)
		var cycleErr *errors.TaskDependencyCycleError
		require.ErrorAs(t, err, &cycleErr)
		assert.Contains(t, err.Error(), "cycle")
		for _, name := range []string{"cycle-a", "cycle-b", "cycle-c"} {
			assert.Contains(t, err.Error(), name)
		}
	}
}

func TestGraphInvalidFormat(t *testing.T) {
	t.Parallel()

	_, err := runGraph(t, []task.ExecutorOption{task.WithGraphFormat("yaml")}, &task.Call{Task: "build"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "yaml")
}
