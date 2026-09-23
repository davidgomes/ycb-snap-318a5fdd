package task_test

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-task/task/v3"
	"github.com/go-task/task/v3/errors"
)

type (
	graphOutput struct {
		Roots       []string             `json:"roots"`
		Nodes       map[string]graphNode `json:"nodes"`
		Edges       []graphEdge          `json:"edges"`
		DepthGroups [][]string           `json:"depth_groups"`
		LongestPath []string             `json:"longest_path"`
	}
	graphNode struct {
		Name     string `json:"name"`
		Desc     string `json:"desc"`
		Location struct {
			Taskfile string `json:"taskfile"`
			Line     int    `json:"line"`
			Column   int    `json:"column"`
		} `json:"location"`
		UpToDate *bool    `json:"up_to_date"`
		Deps     []string `json:"deps"`
		Method   string   `json:"method"`
	}
	graphEdge struct {
		From string         `json:"from"`
		To   string         `json:"to"`
		Type string         `json:"type"`
		Vars map[string]any `json:"vars"`
	}
)

func runGraph(t *testing.T, dir string, opts []task.ExecutorOption, calls ...*task.Call) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	e := task.NewExecutor(append([]task.ExecutorOption{
		task.WithDir(dir),
		task.WithStdout(&buf),
		task.WithStderr(io.Discard),
	}, opts...)...)
	require.NoError(t, e.Setup())
	err := e.Graph(calls...)
	return buf.String(), err
}

func runGraphJSON(t *testing.T, dir string, opts []task.ExecutorOption, calls ...*task.Call) graphOutput {
	t.Helper()
	out, err := runGraph(t, dir, opts, calls...)
	require.NoError(t, err)
	var g graphOutput
	require.NoError(t, json.Unmarshal([]byte(out), &g))
	return g
}

func TestGraphJSON(t *testing.T) {
	t.Parallel()

	g := runGraphJSON(t, "testdata/graph", nil, &task.Call{Task: "b"})

	assert.Equal(t, []string{"build"}, g.Roots)
	assert.ElementsMatch(t,
		[]string{"build", "generate", "lint", "setup", "lib:compile", "lib:prepare"},
		slices.Collect(maps.Keys(g.Nodes)),
	)

	build := g.Nodes["build"]
	assert.Equal(t, "build", build.Name)
	assert.Equal(t, "Build the project", build.Desc)
	assert.Equal(t, "Taskfile.yml", filepath.Base(build.Location.Taskfile))
	assert.Equal(t, 10, build.Location.Line)
	assert.Equal(t, 3, build.Location.Column)
	assert.Equal(t, []string{"generate", "lib:compile", "lint"}, build.Deps)
	assert.Equal(t, "checksum", build.Method)
	require.NotNil(t, build.UpToDate)
	assert.False(t, *build.UpToDate)

	setup := g.Nodes["setup"]
	assert.Empty(t, setup.Deps)
	require.NotNil(t, setup.UpToDate)
	assert.True(t, *setup.UpToDate)

	compile := g.Nodes["lib:compile"]
	assert.Equal(t, "timestamp", compile.Method)
	assert.Equal(t, filepath.Join("lib", "Taskfile.yml"), filepath.Join(filepath.Base(filepath.Dir(compile.Location.Taskfile)), filepath.Base(compile.Location.Taskfile)))
	assert.Equal(t, []string{"lib:prepare"}, compile.Deps)
	assert.Equal(t, []string{"setup"}, g.Nodes["lib:prepare"].Deps)

	assert.Equal(t, []graphEdge{
		{From: "build", To: "generate", Type: "dep", Vars: map[string]any{}},
		{From: "build", To: "lint", Type: "dep", Vars: map[string]any{}},
		{From: "build", To: "lib:compile", Type: "cmd", Vars: map[string]any{}},
		{From: "generate", To: "setup", Type: "cmd", Vars: map[string]any{}},
		{From: "lib:compile", To: "lib:prepare", Type: "dep", Vars: map[string]any{}},
		{From: "lib:prepare", To: "setup", Type: "cmd", Vars: map[string]any{}},
		{From: "lint", To: "setup", Type: "dep", Vars: map[string]any{}},
	}, g.Edges)

	assert.Equal(t, [][]string{
		{"setup"},
		{"generate", "lib:prepare", "lint"},
		{"lib:compile"},
		{"build"},
	}, g.DepthGroups)
	assert.Equal(t, []string{"build", "lib:compile", "lib:prepare", "setup"}, g.LongestPath)
}

func TestGraphDefaultTask(t *testing.T) {
	t.Parallel()

	g := runGraphJSON(t, "testdata/graph", nil)

	assert.Equal(t, []string{"default"}, g.Roots)
	assert.Equal(t, []string{"build"}, g.Nodes["default"].Deps)
	assert.Equal(t, []string{"default", "build", "lib:compile", "lib:prepare", "setup"}, g.LongestPath)
}

func TestGraphForLoop(t *testing.T) {
	t.Parallel()

	g := runGraphJSON(t, "testdata/graph", nil, &task.Call{Task: "release"})

	var releaseEdges []graphEdge
	for _, edge := range g.Edges {
		if edge.From == "release" {
			releaseEdges = append(releaseEdges, edge)
		}
	}
	assert.Equal(t, []graphEdge{
		{From: "release", To: "package", Type: "cmd", Vars: map[string]any{"OS": "linux"}},
		{From: "release", To: "package", Type: "cmd", Vars: map[string]any{"OS": "darwin"}},
		{From: "release", To: "cleanup", Type: "cmd", Vars: map[string]any{}},
	}, releaseEdges)
	assert.Equal(t, []string{"cleanup", "package"}, g.Nodes["release"].Deps)

	var packageEdges int
	for _, edge := range g.Edges {
		if edge.From == "package" {
			packageEdges++
		}
	}
	assert.Equal(t, 1, packageEdges, "calls with different vars should not duplicate identical edges")
}

func TestGraphWildcard(t *testing.T) {
	t.Parallel()

	g := runGraphJSON(t, "testdata/graph", nil, &task.Call{Task: "deploy-prod"}, &task.Call{Task: "build"})

	assert.Equal(t, []string{"deploy-prod", "build"}, g.Roots)
	assert.Equal(t, "deploy-prod", g.Nodes["deploy-prod"].Name)
	assert.Equal(t, []string{"build"}, g.Nodes["deploy-prod"].Deps)
	assert.Equal(t, []string{"deploy-prod", "build", "lib:compile", "lib:prepare", "setup"}, g.LongestPath)
}

func TestGraphDOT(t *testing.T) {
	t.Parallel()

	out, err := runGraph(t, "testdata/graph",
		[]task.ExecutorOption{task.WithGraphFormat("dot")},
		&task.Call{Task: "build"},
	)
	require.NoError(t, err)
	assert.Equal(t, `digraph tasks {
  "build";
  "generate";
  "lib:compile";
  "lib:prepare";
  "lint";
  "setup" [style=dashed];
  "build" -> "generate";
  "build" -> "lint";
  "build" -> "lib:compile";
  "generate" -> "setup";
  "lib:compile" -> "lib:prepare";
  "lib:prepare" -> "setup";
  "lint" -> "setup";
}
`, out)
}

func TestGraphText(t *testing.T) {
	t.Parallel()

	out, err := runGraph(t, "testdata/graph",
		[]task.ExecutorOption{task.WithGraphFormat("text")},
		&task.Call{Task: "build"},
	)
	require.NoError(t, err)
	assert.Equal(t, `build
  generate
    setup
  lib:compile
    lib:prepare
      setup (repeated)
  lint
    setup (repeated)
`, out)
}

func TestGraphNoStatus(t *testing.T) {
	t.Parallel()

	opts := []task.ExecutorOption{task.WithGraphNoStatus(true)}

	out, err := runGraph(t, "testdata/graph", opts, &task.Call{Task: "build"})
	require.NoError(t, err)
	var raw struct {
		Nodes map[string]map[string]any `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	require.NotEmpty(t, raw.Nodes)
	for name, node := range raw.Nodes {
		assert.NotContains(t, node, "up_to_date", name)
		assert.Contains(t, node, "method", name)
	}

	out, err = runGraph(t, "testdata/graph",
		append(opts, task.WithGraphFormat("dot")),
		&task.Call{Task: "build"},
	)
	require.NoError(t, err)
	assert.NotContains(t, out, "style=dashed")
	assert.Contains(t, out, `"setup";`)
}

func TestGraphDoesNotStoreFingerprints(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	opts := []task.ExecutorOption{task.WithTempDir(task.TempDir{Remote: tempDir, Fingerprint: tempDir})}
	for range 2 {
		g := runGraphJSON(t, "testdata/graph", opts, &task.Call{Task: "lint"})
		require.NotNil(t, g.Nodes["lint"].UpToDate)
		assert.False(t, *g.Nodes["lint"].UpToDate)
	}
	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestGraphReverse(t *testing.T) {
	t.Parallel()

	opts := []task.ExecutorOption{task.WithGraphReverse(true)}
	g := runGraphJSON(t, "testdata/graph", opts, &task.Call{Task: "setup"})

	assert.Equal(t, []string{"setup"}, g.Roots)
	assert.ElementsMatch(t, []string{
		"setup", "generate", "lint", "lib:prepare", "lib:compile",
		"build", "default", "package", "release", "deploy-*",
	}, slices.Collect(maps.Keys(g.Nodes)))
	assert.NotContains(t, g.Nodes, "cleanup")
	assert.Equal(t, []string{"generate", "lib:prepare", "lint"}, g.Nodes["setup"].Deps)
	assert.Equal(t, []string{"default", "deploy-*", "package"}, g.Nodes["build"].Deps)
	assert.Empty(t, g.Nodes["release"].Deps)

	assert.Contains(t, g.Edges, graphEdge{From: "setup", To: "lint", Type: "dep", Vars: map[string]any{}})
	assert.Contains(t, g.Edges, graphEdge{From: "package", To: "release", Type: "cmd", Vars: map[string]any{"OS": "linux"}})
	assert.Contains(t, g.Edges, graphEdge{From: "package", To: "release", Type: "cmd", Vars: map[string]any{"OS": "darwin"}})

	assert.Equal(t, [][]string{
		{"default", "deploy-*", "release"},
		{"package"},
		{"build"},
		{"generate", "lib:compile", "lint"},
		{"lib:prepare"},
		{"setup"},
	}, g.DepthGroups)
	assert.Equal(t, []string{"setup", "lib:prepare", "lib:compile", "build", "package", "release"}, g.LongestPath)

	out, err := runGraph(t, "testdata/graph",
		append(opts, task.WithGraphFormat("text")),
		&task.Call{Task: "lib:compile"},
	)
	require.NoError(t, err)
	assert.Equal(t, `lib:compile
  build
    default
    deploy-*
    package
      release
`, out)

	g = runGraphJSON(t, "testdata/graph", opts, &task.Call{Task: "release"})
	assert.Equal(t, []string{"release"}, g.Roots)
	assert.Len(t, g.Nodes, 1)
	assert.Empty(t, g.Edges)
	assert.Equal(t, [][]string{{"release"}}, g.DepthGroups)
	assert.Equal(t, []string{"release"}, g.LongestPath)
}

func TestGraphErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opts     []task.ExecutorOption
		call     string
		contains []string
	}{
		{name: "missing task", call: "does-not-exist-at-all", contains: []string{"does-not-exist-at-all"}},
		{name: "missing task reverse", opts: []task.ExecutorOption{task.WithGraphReverse(true)}, call: "nope", contains: []string{"nope"}},
		{name: "missing dependency", call: "broken", contains: []string{"does-not-exist"}},
		{name: "cycle", call: "a", contains: []string{"cycle", "a -> b -> c -> a"}},
		{name: "cycle reverse", opts: []task.ExecutorOption{task.WithGraphReverse(true)}, call: "a", contains: []string{"cycle", "a -> b -> c -> a"}},
		{name: "invalid format", opts: []task.ExecutorOption{task.WithGraphFormat("yaml")}, call: "broken", contains: []string{"yaml"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			out, err := runGraph(t, "testdata/graph_errors", test.opts, &task.Call{Task: test.call})
			require.Error(t, err)
			assert.Empty(t, out)
			for _, s := range test.contains {
				assert.Contains(t, err.Error(), s)
			}
		})
	}

	_, err := runGraph(t, "testdata/graph_errors", nil, &task.Call{Task: "b"})
	var cycleErr *errors.TaskCycleError
	require.ErrorAs(t, err, &cycleErr)
	assert.Equal(t, []string{"b", "c", "a", "b"}, cycleErr.TaskNames)
}
