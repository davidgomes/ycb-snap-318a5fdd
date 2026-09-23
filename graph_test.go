package task_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-task/task/v3"
)

type graphLocation struct {
	Taskfile string `json:"taskfile"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

type graphNode struct {
	Name     string        `json:"name"`
	Desc     string        `json:"desc"`
	Location graphLocation `json:"location"`
	UpToDate *bool         `json:"up_to_date,omitempty"`
	Deps     []string      `json:"deps"`
	Method   string        `json:"method"`
}

type graphEdge struct {
	From string         `json:"from"`
	To   string         `json:"to"`
	Type string         `json:"type"`
	Vars map[string]any `json:"vars"`
}

type graphOutput struct {
	Roots       []string             `json:"roots"`
	Nodes       map[string]graphNode `json:"nodes"`
	Edges       []graphEdge          `json:"edges"`
	DepthGroups [][]string           `json:"depth_groups"`
	LongestPath []string             `json:"longest_path"`
}

func writeTaskfile(t *testing.T, dir, rel, contents string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
}

func runGraph(t *testing.T, dir string, calls []*task.Call, opts ...task.ExecutorOption) (string, error) {
	t.Helper()
	var stdout strings.Builder
	var stderr strings.Builder
	options := append([]task.ExecutorOption{
		task.WithDir(dir),
		task.WithStdout(&stdout),
		task.WithStderr(&stderr),
		task.WithDisableFuzzy(true),
	}, opts...)
	e := task.NewExecutor(options...)
	require.NoError(t, e.Setup())
	err := e.Graph(calls...)
	if err != nil {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), nil
}

func decodeGraph(t *testing.T, out string) graphOutput {
	t.Helper()
	var graph graphOutput
	require.NoError(t, json.Unmarshal([]byte(out), &graph))
	return graph
}

func TestGraphJSONShape(t *testing.T) {
	dir := t.TempDir()
	writeTaskfile(t, dir, "Taskfile.yml", `
version: '3'
tasks:
  build:
    desc: Build the app
    deps: [compile, test]
    cmds:
      - task: package
        vars: {NAME: app}
  test:
    deps: [compile]
  compile:
    method: timestamp
    deps: [generate]
  generate:
    desc: Generate code
  package:
    method: none
`)

	out, err := runGraph(t, dir, []*task.Call{{Task: "build"}})
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	for _, key := range []string{"roots", "nodes", "edges", "depth_groups", "longest_path"} {
		_, ok := raw[key]
		require.Truef(t, ok, "missing key %s", key)
	}

	graph := decodeGraph(t, out)
	require.Equal(t, []string{"build"}, graph.Roots)
	require.Equal(t, []string{"build", "test", "compile", "generate"}, graph.LongestPath)
	require.Equal(t, [][]string{
		{"generate", "package"},
		{"compile"},
		{"test"},
		{"build"},
	}, graph.DepthGroups)

	build := graph.Nodes["build"]
	require.Equal(t, "build", build.Name)
	require.Equal(t, "Build the app", build.Desc)
	require.Equal(t, []string{"compile", "package", "test"}, build.Deps)
	require.Equal(t, "checksum", build.Method)
	require.NotNil(t, build.UpToDate)
	require.False(t, *build.UpToDate)
	require.Greater(t, build.Location.Line, 0)
	require.Greater(t, build.Location.Column, 0)
	require.Contains(t, build.Location.Taskfile, "Taskfile.yml")

	require.Equal(t, "timestamp", graph.Nodes["compile"].Method)
	require.Equal(t, "none", graph.Nodes["package"].Method)
	require.Equal(t, []string{"compile"}, graph.Nodes["test"].Deps)
	require.Empty(t, graph.Nodes["generate"].Deps)

	require.Equal(t, []graphEdge{
		{From: "build", To: "compile", Type: "dep", Vars: map[string]any{}},
		{From: "build", To: "package", Type: "cmd", Vars: map[string]any{"NAME": "app"}},
		{From: "build", To: "test", Type: "dep", Vars: map[string]any{}},
		{From: "compile", To: "generate", Type: "dep", Vars: map[string]any{}},
		{From: "test", To: "compile", Type: "dep", Vars: map[string]any{}},
	}, graph.Edges)
}

func TestGraphFormatsAndStatus(t *testing.T) {
	dir := t.TempDir()
	writeTaskfile(t, dir, "Taskfile.yml", `
version: '3'
tasks:
  default:
    deps: [done, fresh]
  done:
    desc: Already done
    status:
      - exit 0
  fresh:
    desc: Needs to run
`)

	out, err := runGraph(t, dir, nil)
	require.NoError(t, err)
	graph := decodeGraph(t, out)
	require.Equal(t, []string{"default"}, graph.Roots)
	require.NotNil(t, graph.Nodes["done"].UpToDate)
	require.True(t, *graph.Nodes["done"].UpToDate)
	require.False(t, *graph.Nodes["fresh"].UpToDate)

	dot, err := runGraph(t, dir, nil, task.WithGraphFormat("dot"))
	require.NoError(t, err)
	require.Contains(t, dot, "digraph tasks {")
	require.Contains(t, dot, `"done" [style=dashed];`)
	require.Contains(t, dot, `"fresh";`)
	require.NotContains(t, dot, `"fresh" [style=dashed];`)
	require.Contains(t, dot, `"default" -> "done";`)
	require.Contains(t, dot, `"default" -> "fresh";`)
	require.Contains(t, dot, "}")

	text, err := runGraph(t, dir, nil, task.WithGraphFormat("TEXT"))
	require.NoError(t, err)
	require.Equal(t, "default\n  done\n  fresh\n", text)

	noStatus, err := runGraph(t, dir, nil, task.WithGraphNoStatus(true))
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(noStatus), &raw))
	var nodes map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["nodes"], &nodes))
	_, hasStatus := nodes["done"]["up_to_date"]
	require.False(t, hasStatus)

	dotNoStatus, err := runGraph(t, dir, nil, task.WithGraphFormat("dot"), task.WithGraphNoStatus(true))
	require.NoError(t, err)
	require.NotContains(t, dotNoStatus, "style=dashed")
	require.Contains(t, dotNoStatus, `"done";`)
}

func TestGraphAliasWildcardForAndInclude(t *testing.T) {
	dir := t.TempDir()
	writeTaskfile(t, dir, "Taskfile.yml", `
version: '3'
includes:
  lib:
    taskfile: ./lib/Taskfile.yml
tasks:
  build:
    aliases: [b]
    cmds:
      - task: pkg-*
      - task: lib:build
  pkg-*:
    deps: [lib:test]
`)
	writeTaskfile(t, dir, "lib/Taskfile.yml", `
version: '3'
tasks:
  build:
    desc: Library build
    cmds:
      - task: test
  test:
    cmds:
      - echo ok
`)

	out, err := runGraph(t, dir, []*task.Call{{Task: "b"}})
	require.NoError(t, err)
	graph := decodeGraph(t, out)
	require.Equal(t, []string{"build"}, graph.Roots)
	require.ElementsMatch(t, []string{"build", "pkg-*", "lib:build", "lib:test"}, keysOf(graph.Nodes))
	require.Contains(t, graph.Nodes["lib:build"].Location.Taskfile, filepath.Join("lib", "Taskfile.yml"))
	require.Equal(t, "Library build", graph.Nodes["lib:build"].Desc)
	require.Equal(t, []string{"lib:test"}, graph.Nodes["pkg-*"].Deps)

	var sawLib, sawPkg bool
	for _, edge := range graph.Edges {
		if edge.From == "build" && edge.To == "lib:build" && edge.Type == "cmd" {
			sawLib = true
		}
		if edge.From == "build" && edge.To == "pkg-*" && edge.Type == "cmd" {
			sawPkg = true
		}
		if edge.From == "lib:build" {
			require.Equal(t, "lib:test", edge.To)
			require.Equal(t, "cmd", edge.Type)
		}
	}
	require.True(t, sawLib)
	require.True(t, sawPkg)

	wildcard, err := runGraph(t, dir, []*task.Call{{Task: "pkg-api"}})
	require.NoError(t, err)
	require.Equal(t, []string{"pkg-*"}, decodeGraph(t, wildcard).Roots)

	loopDir := t.TempDir()
	writeTaskfile(t, loopDir, "Taskfile.yml", `
version: '3'
tasks:
  all:
    deps:
      - for: ["x", "y"]
        task: echo
        vars:
          ITEM_NAME: "{{.ITEM}}"
    cmds:
      - for: ["1", "2"]
        task: "job-{{.ITEM}}"
  echo: {}
  job-1: {}
  job-2: {}
`)
	loopOut, err := runGraph(t, loopDir, []*task.Call{{Task: "all"}})
	require.NoError(t, err)
	loop := decodeGraph(t, loopOut)
	require.Equal(t, []string{"echo", "job-1", "job-2"}, loop.Nodes["all"].Deps)
	require.Equal(t, []graphEdge{
		{From: "all", To: "echo", Type: "dep", Vars: map[string]any{"ITEM_NAME": "x"}},
		{From: "all", To: "echo", Type: "dep", Vars: map[string]any{"ITEM_NAME": "y"}},
		{From: "all", To: "job-1", Type: "cmd", Vars: map[string]any{}},
		{From: "all", To: "job-2", Type: "cmd", Vars: map[string]any{}},
	}, loop.Edges)

	text, err := runGraph(t, loopDir, []*task.Call{{Task: "all"}}, task.WithGraphFormat("text"))
	require.NoError(t, err)
	require.Equal(t, "all\n  echo\n  echo (repeated)\n  job-1\n  job-2\n", text)
}

func TestGraphReverseRepeatedAndErrors(t *testing.T) {
	dir := t.TempDir()
	writeTaskfile(t, dir, "Taskfile.yml", `
version: '3'
tasks:
  build:
    deps: [compile, test]
  test:
    deps: [compile]
  compile:
    deps: [generate]
  generate:
    desc: leaf
  other:
    deps: [generate]
  broken:
    deps: [does-not-exist]
`)

	out, err := runGraph(t, dir, []*task.Call{{Task: "generate"}}, task.WithGraphReverse(true))
	require.NoError(t, err)
	graph := decodeGraph(t, out)
	require.Equal(t, []string{"generate"}, graph.Roots)
	require.ElementsMatch(t, []string{"generate", "compile", "test", "build", "other"}, keysOf(graph.Nodes))
	require.Equal(t, []string{"build", "other"}, graph.DepthGroups[0])
	require.Equal(t, []string{"test"}, graph.DepthGroups[1])
	require.Equal(t, []string{"compile"}, graph.DepthGroups[2])
	require.Equal(t, []string{"generate"}, graph.DepthGroups[3])
	require.Equal(t, []string{"generate", "compile", "test", "build"}, graph.LongestPath)
	require.Equal(t, []string{"compile", "other"}, graph.Nodes["generate"].Deps)
	require.Equal(t, []string{"build", "test"}, graph.Nodes["compile"].Deps)
	require.Equal(t, []string{"build"}, graph.Nodes["test"].Deps)
	require.Empty(t, graph.Nodes["build"].Deps)

	text, err := runGraph(t, dir, []*task.Call{{Task: "generate"}}, task.WithGraphReverse(true), task.WithGraphFormat("text"))
	require.NoError(t, err)
	require.Contains(t, text, "generate\n")
	require.Contains(t, text, "(repeated)")

	forwardText, err := runGraph(t, dir, []*task.Call{{Task: "build"}}, task.WithGraphFormat("text"))
	require.NoError(t, err)
	require.Equal(t, "build\n  compile\n    generate\n  test\n    compile (repeated)\n", forwardText)

	_, err = runGraph(t, dir, []*task.Call{{Task: "missing"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing")

	cycleDir := t.TempDir()
	writeTaskfile(t, cycleDir, "Taskfile.yml", `
version: '3'
tasks:
  a:
    deps: [b]
  b:
    deps: [c]
  c:
    deps: [a]
`)
	_, err = runGraph(t, cycleDir, []*task.Call{{Task: "a"}})
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "cycle")
	require.Contains(t, err.Error(), "a")
	require.Contains(t, err.Error(), "b")
	require.Contains(t, err.Error(), "c")

	_, err = runGraph(t, dir, []*task.Call{{Task: "build"}}, task.WithGraphFormat("yaml"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "yaml")
}

func TestGraphCLI(t *testing.T) {
	bin := taskBinary(t)
	dir := t.TempDir()
	writeTaskfile(t, dir, "Taskfile.yml", `
version: '3'
tasks:
  default:
    deps: [build]
  build:
    cmds:
      - echo ok
`)

	cmd := exec.Command(bin, "--graph", "--format", "text")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err)
	require.Equal(t, "default\n  build\n", string(out))

	missing := exec.Command(bin, "--graph", "nope")
	missing.Dir = dir
	missingOut, err := missing.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(missingOut), "nope")
}

func keysOf(nodes map[string]graphNode) []string {
	keys := make([]string, 0, len(nodes))
	for key := range nodes {
		keys = append(keys, key)
	}
	return keys
}

var (
	taskBinOnce sync.Once
	taskBinPath string
	taskBinErr  error
)

func taskBinary(t *testing.T) string {
	t.Helper()
	taskBinOnce.Do(func() {
		file, err := os.CreateTemp("", "task-graph-*")
		if err != nil {
			taskBinErr = err
			return
		}
		taskBinPath = file.Name()
		file.Close()
		cmd := exec.Command("go", "build", "-o", taskBinPath, "./cmd/task")
		cmd.Dir = moduleRoot(t)
		taskBinErr = cmd.Run()
	})
	require.NoError(t, taskBinErr)
	return taskBinPath
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	return dir
}
