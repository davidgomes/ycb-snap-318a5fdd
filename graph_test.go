package task_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-task/task/v3"
)

func runGraph(t *testing.T, dir string, opts []task.ExecutorOption, calls ...*task.Call) (string, error) {
	t.Helper()
	var buff bytes.Buffer
	e := task.NewExecutor(append([]task.ExecutorOption{
		task.WithDir(dir),
		task.WithTempDir(task.TempDir{Remote: t.TempDir(), Fingerprint: t.TempDir()}),
		task.WithStdout(&buff),
		task.WithStderr(&buff),
	}, opts...)...)
	require.NoError(t, e.Setup())
	err := e.Graph(calls...)
	return buff.String(), err
}

func decodeGraph(t *testing.T, out string) *task.GraphOutput {
	t.Helper()
	var g task.GraphOutput
	require.NoError(t, json.Unmarshal([]byte(out), &g))
	return &g
}

func TestGraphJSON(t *testing.T) {
	t.Parallel()

	out, err := runGraph(t, "testdata/graph", nil)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	for _, key := range []string{"roots", "nodes", "edges", "depth_groups", "longest_path"} {
		assert.Contains(t, raw, key)
	}

	g := decodeGraph(t, out)
	assert.Equal(t, []string{"default"}, g.Roots)
	assert.Equal(t, [][]string{
		{"lib:arch", "lib:setup", "package"},
		{"generate"},
		{"compile"},
		{"build"},
		{"default"},
	}, g.DepthGroups)
	assert.Equal(t, []string{"default", "build", "compile", "generate", "lib:setup"}, g.LongestPath)

	build := g.Nodes["build"]
	require.NotNil(t, build)
	assert.Equal(t, "build", build.Name)
	assert.Equal(t, "Build everything", build.Desc)
	assert.Equal(t, []string{"compile", "lib:setup", "package"}, build.Deps)
	assert.Equal(t, "checksum", build.Method)
	assert.True(t, strings.HasSuffix(build.Location.Taskfile, "Taskfile.yml"))
	assert.Positive(t, build.Location.Line)
	require.NotNil(t, build.UpToDate)
	assert.False(t, *build.UpToDate)

	var archEdges []*task.GraphEdge
	for _, edge := range g.Edges {
		if edge.From == "compile" && edge.To == "lib:arch" {
			archEdges = append(archEdges, edge)
		}
	}
	require.Len(t, archEdges, 2)
	assert.Equal(t, "cmd", archEdges[0].Type)
	assert.Equal(t, map[string]any{"OS": "linux"}, archEdges[0].Vars)
	assert.Equal(t, map[string]any{"OS": "darwin"}, archEdges[1].Vars)
}

func TestGraphAliasAndWildcardRoots(t *testing.T) {
	t.Parallel()

	out, err := runGraph(t, "testdata/graph", nil, &task.Call{Task: "b"}, &task.Call{Task: "gen-x"})
	require.NoError(t, err)
	g := decodeGraph(t, out)
	assert.Equal(t, []string{"build", "gen-*"}, g.Roots)
}

func TestGraphNoStatus(t *testing.T) {
	t.Parallel()

	out, err := runGraph(t, "testdata/graph", []task.ExecutorOption{task.WithGraphNoStatus(true)}, &task.Call{Task: "uses-fresh"})
	require.NoError(t, err)
	assert.NotContains(t, out, "up_to_date")

	out, err = runGraph(t, "testdata/graph", []task.ExecutorOption{
		task.WithGraphFormat("dot"),
		task.WithGraphNoStatus(true),
	}, &task.Call{Task: "uses-fresh"})
	require.NoError(t, err)
	assert.NotContains(t, out, "dashed")
}

func TestGraphDOT(t *testing.T) {
	t.Parallel()

	out, err := runGraph(t, "testdata/graph", []task.ExecutorOption{task.WithGraphFormat("dot")}, &task.Call{Task: "uses-fresh"})
	require.NoError(t, err)
	assert.Equal(t, "digraph tasks {\n  \"fresh\" [style=dashed];\n  \"uses-fresh\";\n  \"uses-fresh\" -> \"fresh\";\n}\n", out)
}

func TestGraphText(t *testing.T) {
	t.Parallel()

	out, err := runGraph(t, "testdata/graph", []task.ExecutorOption{task.WithGraphFormat("text")}, &task.Call{Task: "build"})
	require.NoError(t, err)
	assert.Equal(t, strings.Join([]string{
		"build",
		"  compile",
		"    generate",
		"      lib:setup",
		"    lib:arch",
		"  lib:setup (repeated)",
		"  package",
		"",
	}, "\n"), out)
}

func TestGraphReverse(t *testing.T) {
	t.Parallel()

	out, err := runGraph(t, "testdata/graph", []task.ExecutorOption{task.WithGraphReverse(true)}, &task.Call{Task: "lib:setup"})
	require.NoError(t, err)
	g := decodeGraph(t, out)
	assert.Equal(t, []string{"lib:setup"}, g.Roots)
	assert.Equal(t, [][]string{
		{"default", "gen-*"},
		{"build"},
		{"compile"},
		{"generate"},
		{"lib:setup"},
	}, g.DepthGroups)
	assert.Equal(t, []string{"lib:setup", "generate", "compile", "build", "default"}, g.LongestPath)
	assert.Equal(t, []string{"build", "generate"}, g.Nodes["lib:setup"].Deps)
	assert.Equal(t, []string{"compile", "gen-*"}, g.Nodes["generate"].Deps)
	for _, edge := range g.Edges {
		if edge.From == "lib:setup" {
			assert.Contains(t, []string{"build", "generate"}, edge.To)
		}
	}
}

func TestGraphErrors(t *testing.T) {
	t.Parallel()

	_, err := runGraph(t, "testdata/graph", nil, &task.Call{Task: "does-not-exist"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist")

	_, err = runGraph(t, "testdata/graph/cycle", nil, &task.Call{Task: "a"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
	for _, name := range []string{"a", "b", "c"} {
		assert.Contains(t, err.Error(), name)
	}

	_, err = runGraph(t, "testdata/graph", []task.ExecutorOption{task.WithGraphFormat("yaml")})
	require.Error(t, err)
}
