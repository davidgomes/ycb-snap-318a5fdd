package task_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-task/task/v3"
)

func setupGraph(t *testing.T, files map[string]string, opts ...task.ExecutorOption) (*task.Executor, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	buf := &bytes.Buffer{}
	options := []task.ExecutorOption{
		task.WithDir(dir),
		task.WithStdout(buf),
		task.WithStderr(io.Discard),
	}
	options = append(options, opts...)
	e := task.NewExecutor(options...)
	require.NoError(t, e.Setup())
	return e, buf
}

type graphDoc struct {
	Roots       []string             `json:"roots"`
	Nodes       map[string]graphNode `json:"nodes"`
	Edges       []graphEdge          `json:"edges"`
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

type graphEdge struct {
	From string         `json:"from"`
	To   string         `json:"to"`
	Type string         `json:"type"`
	Vars map[string]any `json:"vars"`
}

func decodeGraph(t *testing.T, buf *bytes.Buffer) graphDoc {
	t.Helper()
	var doc graphDoc
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), buf.String())
	return doc
}

func TestGraphJSONChain(t *testing.T) {
	t.Parallel()
	e, buf := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: "3"
tasks:
  root:
    desc: the root
    deps: [mid]
    cmds:
      - task: side
  mid:
    method: timestamp
    deps: [leaf]
  leaf:
    cmds:
      - echo leaf
  side:
    cmds:
      - echo side
`,
	})
	require.NoError(t, e.Graph(&task.Call{Task: "root"}))
	doc := decodeGraph(t, buf)

	assert.Equal(t, []string{"root"}, doc.Roots)
	assert.Equal(t, []string{"mid", "side"}, doc.Nodes["root"].Deps)
	assert.Equal(t, []string{"leaf"}, doc.Nodes["mid"].Deps)
	assert.Empty(t, doc.Nodes["leaf"].Deps)
	assert.Equal(t, "the root", doc.Nodes["root"].Desc)
	assert.Equal(t, "timestamp", doc.Nodes["mid"].Method)
	assert.Equal(t, "checksum", doc.Nodes["root"].Method)
	assert.Equal(t, "root", doc.Nodes["root"].Name)
	require.NotNil(t, doc.Nodes["leaf"].UpToDate)
	assert.False(t, *doc.Nodes["leaf"].UpToDate)

	stored, ok := e.Taskfile.Tasks.Get("leaf")
	require.True(t, ok)
	assert.Equal(t, stored.Location.Taskfile, doc.Nodes["leaf"].Location.Taskfile)
	assert.Equal(t, stored.Location.Line, doc.Nodes["leaf"].Location.Line)
	assert.Equal(t, stored.Location.Column, doc.Nodes["leaf"].Location.Column)
	assert.NotEmpty(t, doc.Nodes["leaf"].Location.Taskfile)

	assert.Equal(t, []graphEdge{
		{From: "root", To: "mid", Type: "dep"},
		{From: "root", To: "side", Type: "cmd"},
		{From: "mid", To: "leaf", Type: "dep"},
	}, doc.Edges)
	assert.Equal(t, [][]string{{"leaf", "side"}, {"mid"}, {"root"}}, doc.DepthGroups)
	assert.Equal(t, []string{"root", "mid", "leaf"}, doc.LongestPath)
}

func TestGraphAliasWildcardAndDefault(t *testing.T) {
	t.Parallel()
	e, buf := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: "3"
tasks:
  default:
    deps: [foo]
  foo:
    aliases: [f]
    deps: [build-app]
  build-*:
    aliases: [s-*]
    deps: [lint]
  lint:
    cmds:
      - echo lint
`,
	})
	require.NoError(t, e.Graph(&task.Call{Task: "f"}, &task.Call{Task: "s-api"}))
	doc := decodeGraph(t, buf)
	assert.Equal(t, []string{"foo", "build-api"}, doc.Roots)
	assert.Equal(t, []string{"build-app"}, doc.Nodes["foo"].Deps)
	assert.Equal(t, []string{"lint"}, doc.Nodes["build-api"].Deps)
	assert.Contains(t, doc.Nodes, "build-app")
	assert.Contains(t, doc.Nodes, "lint")

	buf.Reset()
	require.NoError(t, e.Graph())
	doc = decodeGraph(t, buf)
	assert.Equal(t, []string{"default"}, doc.Roots)
	assert.Equal(t, []string{"foo"}, doc.Nodes["default"].Deps)
}

func TestGraphForLoopAndCallVars(t *testing.T) {
	t.Parallel()
	e, buf := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: "3"
tasks:
  root:
    deps:
      - for: ["x", "y"]
        task: echo
        vars:
          TEXT: "{{.ITEM}}"
      - task: mid
        vars: {WHICH: a}
      - task: mid
        vars: {WHICH: b}
  echo:
    cmds:
      - echo "{{.TEXT}}"
  mid:
    deps:
      - "leaf-{{.WHICH}}"
  leaf-a:
    cmds: [echo a]
  leaf-b:
    cmds: [echo b]
`,
	})
	require.NoError(t, e.Graph(&task.Call{Task: "root"}))
	doc := decodeGraph(t, buf)

	var echoEdges, midEdges []graphEdge
	for _, edge := range doc.Edges {
		switch {
		case edge.From == "root" && edge.To == "echo":
			echoEdges = append(echoEdges, edge)
		case edge.From == "root" && edge.To == "mid":
			midEdges = append(midEdges, edge)
		}
	}
	require.Len(t, echoEdges, 2)
	assert.Equal(t, "dep", echoEdges[0].Type)
	assert.Equal(t, "x", echoEdges[0].Vars["TEXT"])
	assert.Equal(t, "y", echoEdges[1].Vars["TEXT"])
	require.Len(t, midEdges, 2)
	assert.Equal(t, []string{"echo", "mid"}, doc.Nodes["root"].Deps)
	assert.Equal(t, []string{"leaf-a", "leaf-b"}, doc.Nodes["mid"].Deps)
	assert.Equal(t, []string{"root", "mid", "leaf-a"}, doc.LongestPath)
}

func TestGraphNamespace(t *testing.T) {
	t.Parallel()
	e, buf := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: "3"
includes:
  lib: ./lib/Taskfile.yml
tasks:
  default:
    cmds:
      - task: lib:build
`,
		"lib/Taskfile.yml": `
version: "3"
tasks:
  build:
    deps: [test]
    cmds:
      - task: lint
  test:
    cmds: [echo test]
  lint:
    cmds: [echo lint]
`,
	})
	require.NoError(t, e.Graph(&task.Call{Task: "default"}))
	doc := decodeGraph(t, buf)
	assert.Equal(t, []string{"lib:build"}, doc.Nodes["default"].Deps)
	assert.Equal(t, []string{"lib:lint", "lib:test"}, doc.Nodes["lib:build"].Deps)
	assert.Equal(t, []graphEdge{
		{From: "default", To: "lib:build", Type: "cmd"},
		{From: "lib:build", To: "lib:test", Type: "dep"},
		{From: "lib:build", To: "lib:lint", Type: "cmd"},
	}, doc.Edges)
	assert.Contains(t, doc.Nodes["lib:test"].Location.Taskfile, filepath.Join("lib", "Taskfile.yml"))
}

func TestGraphTextRepeatedAndDOTStatus(t *testing.T) {
	t.Parallel()
	e, buf := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: "3"
tasks:
  a:
    deps: [b, c]
  b:
    deps: [d]
  c:
    deps: [d]
  d:
    status:
      - exit 0
    cmds:
      - echo d
`,
	}, task.WithGraphFormat("text"))
	require.NoError(t, e.Graph(&task.Call{Task: "a"}))
	assert.Equal(t, "a\n  b\n    d\n  c\n    d (repeated)\n", buf.String())

	buf.Reset()
	e.Options(task.WithGraphFormat("dot"))
	require.NoError(t, e.Graph(&task.Call{Task: "a"}))
	dot := buf.String()
	assert.Contains(t, dot, "digraph tasks {")
	assert.Contains(t, dot, "}\n")
	assert.Contains(t, dot, "\"d\" [style=dashed];")
	assert.NotContains(t, dot, "\"a\" [style=dashed];")
	assert.Contains(t, dot, "\"a\" -> \"b\";")
	assert.Contains(t, dot, "\"b\" -> \"d\";")
	assert.Regexp(t, `(?s)digraph tasks \{\n  "a";\n  "b";\n  "c";\n  "d" \[style=dashed\];`, dot)

	buf.Reset()
	e.Options(task.WithGraphFormat("DOT"), task.WithGraphNoStatus(true))
	require.NoError(t, e.Graph(&task.Call{Task: "a"}))
	assert.NotContains(t, buf.String(), "dashed")
	assert.Contains(t, buf.String(), "digraph tasks {")

	buf.Reset()
	e.Options(task.WithGraphFormat("json"), task.WithGraphNoStatus(true))
	require.NoError(t, e.Graph(&task.Call{Task: "a"}))
	var raw map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &raw))
	nodes := raw["nodes"].(map[string]any)
	for _, node := range nodes {
		_, hasStatus := node.(map[string]any)["up_to_date"]
		assert.False(t, hasStatus)
	}
}

func TestGraphReverse(t *testing.T) {
	t.Parallel()
	e, buf := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: "3"
tasks:
  a:
    deps: [b, c]
  b:
    deps: [d]
  c:
    deps: [d]
  d:
    cmds: [echo d]
  other:
    cmds: [echo other]
`,
	}, task.WithGraphReverse(true))
	require.NoError(t, e.Graph(&task.Call{Task: "d"}))
	doc := decodeGraph(t, buf)
	assert.Equal(t, []string{"d"}, doc.Roots)
	assert.Equal(t, []string{"b", "c"}, doc.Nodes["d"].Deps)
	assert.Equal(t, []string{"a"}, doc.Nodes["b"].Deps)
	assert.NotContains(t, doc.Nodes, "other")
	assert.Equal(t, [][]string{{"a"}, {"b", "c"}, {"d"}}, doc.DepthGroups)
	assert.Equal(t, []string{"d", "b", "a"}, doc.LongestPath)

	buf.Reset()
	e.Options(task.WithGraphFormat("text"))
	require.NoError(t, e.Graph(&task.Call{Task: "d"}))
	assert.Equal(t, "d\n  b\n    a\n  c\n    a (repeated)\n", buf.String())
}

func TestGraphDynamicDeferCall(t *testing.T) {
	t.Parallel()
	e, buf := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: '3'
tasks:
  root:
    cmds:
      - task: leaf
      - task: 'cleanup-{{.EXIT_CODE}}'
        defer: true
  leaf:
    cmds:
      - echo leaf
`,
	})
	require.NoError(t, e.Graph(&task.Call{Task: "root"}))
	doc := decodeGraph(t, buf)
	assert.Equal(t, []string{"leaf"}, doc.Nodes["root"].Deps)
	assert.NotContains(t, doc.Nodes, "cleanup-{{.EXIT_CODE}}")
}

func TestGraphErrors(t *testing.T) {
	t.Parallel()
	e, _ := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: "3"
tasks:
  a:
    deps: [b]
  b:
    deps: [c]
  c:
    deps: [a]
  ok:
    cmds: [echo ok]
`,
	})
	err := e.Graph(&task.Call{Task: "missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
	assert.NotContains(t, err.Error(), "cycle")

	err = e.Graph(&task.Call{Task: "a"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
	assert.Contains(t, err.Error(), "a")
	assert.Contains(t, err.Error(), "b")
	assert.Contains(t, err.Error(), "c")

	err = e.Graph(&task.Call{Task: "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")

	e.Options(task.WithGraphFormat("yaml"))
	err = e.Graph(&task.Call{Task: "ok"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "yaml")
}

func TestGraphMatrixAndDefer(t *testing.T) {
	t.Parallel()
	e, buf := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: "3"
tasks:
  root:
    deps:
      - for:
          matrix:
            OS: ["linux", "darwin"]
            ARCH: ["amd64"]
        task: build
        vars:
          PAIR: "{{.ITEM.OS}}-{{.ITEM.ARCH}}"
    cmds:
      - defer: {task: cleanup, vars: {MODE: last}}
  build:
    cmds: [echo build]
  cleanup:
    cmds: [echo cleanup]
`,
	})
	require.NoError(t, e.Graph(&task.Call{Task: "root"}))
	doc := decodeGraph(t, buf)
	var builds []graphEdge
	var cleanup []graphEdge
	for _, edge := range doc.Edges {
		switch edge.To {
		case "build":
			builds = append(builds, edge)
		case "cleanup":
			cleanup = append(cleanup, edge)
		}
	}
	require.Len(t, builds, 2)
	assert.Equal(t, "linux-amd64", builds[0].Vars["PAIR"])
	assert.Equal(t, "darwin-amd64", builds[1].Vars["PAIR"])
	require.Len(t, cleanup, 1)
	assert.Equal(t, "cmd", cleanup[0].Type)
	assert.Equal(t, "last", cleanup[0].Vars["MODE"])
	assert.Equal(t, []string{"build", "cleanup"}, doc.Nodes["root"].Deps)
}

func TestGraphLiteralMissingDep(t *testing.T) {
	t.Parallel()
	e, _ := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: "3"
tasks:
  a:
    deps: [missing]
`,
	})
	err := e.Graph(&task.Call{Task: "a"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestGraphShellForLoop(t *testing.T) {
	t.Parallel()
	e, buf := setupGraph(t, map[string]string{
		"Taskfile.yml": `
version: "3"
tasks:
  root:
    vars:
      LIST:
        sh: echo a b
    deps:
      - for: {var: LIST}
        task: "item-{{.ITEM}}"
  item-a:
    cmds: [echo a]
  item-b:
    cmds: [echo b]
`,
	})
	require.NoError(t, e.Graph(&task.Call{Task: "root"}))
	doc := decodeGraph(t, buf)
	assert.Equal(t, []string{"item-a", "item-b"}, doc.Nodes["root"].Deps)
	require.Len(t, doc.Edges, 2)
	assert.Equal(t, "item-a", doc.Edges[0].To)
	assert.Equal(t, "item-b", doc.Edges[1].To)
}
