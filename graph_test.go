package task_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-task/task/v3"
	"github.com/go-task/task/v3/errors"
)

func setupGraphExecutor(t *testing.T, files map[string]string, opts ...task.ExecutorOption) (*task.Executor, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	options := []task.ExecutorOption{
		task.WithDir(dir),
		task.WithStdout(stdout),
		task.WithStderr(stderr),
		task.WithSilent(true),
		task.WithColor(false),
	}
	options = append(options, opts...)
	e := task.NewExecutor(options...)
	require.NoError(t, e.Setup(), stderr.String())
	return e, stdout
}

func graphJSON(t *testing.T, e *task.Executor, stdout *bytes.Buffer, calls ...*task.Call) graphDocument {
	t.Helper()
	stdout.Reset()
	require.NoError(t, e.Graph(calls...))
	var doc graphDocument
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &doc), stdout.String())
	return doc
}

type graphDocument struct {
	Roots []string `json:"roots"`
	Nodes map[string]struct {
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
	} `json:"nodes"`
	Edges []struct {
		From string         `json:"from"`
		To   string         `json:"to"`
		Type string         `json:"type"`
		Vars map[string]any `json:"vars"`
	} `json:"edges"`
	DepthGroups [][]string `json:"depth_groups"`
	LongestPath []string   `json:"longest_path"`
}

const basicTaskfile = `
version: '3'

tasks:
  default:
    desc: Run everything
    deps:
      - build
    cmds:
      - task: test
  build:
    desc: Build
    method: timestamp
    deps:
      - lint
      - test
  test:
    desc: Test
    deps:
      - lint
    cmds:
      - echo test
  lint:
    desc: Lint
    method: none
    status:
      - test 1 = 1
    cmds:
      - echo lint
  alias-target:
    desc: Aliased
    aliases: [aliased]
    deps:
      - lint
  other:
    desc: Unrelated
    cmds:
      - echo other
`

func TestGraphJSON(t *testing.T) {
	t.Parallel()
	e, stdout := setupGraphExecutor(t, map[string]string{"Taskfile.yml": basicTaskfile})
	doc := graphJSON(t, e, stdout, &task.Call{Task: "default"})

	assert.Equal(t, []string{"default"}, doc.Roots)
	assert.Equal(t, []string{"build", "test"}, doc.Nodes["default"].Deps)
	assert.Equal(t, []string{"lint", "test"}, doc.Nodes["build"].Deps)
	assert.Equal(t, []string{"lint"}, doc.Nodes["test"].Deps)
	assert.Empty(t, doc.Nodes["lint"].Deps)
	assert.NotContains(t, doc.Nodes, "alias-target")
	assert.NotContains(t, doc.Nodes, "other")
	assert.NotContains(t, doc.Nodes, "aliased")

	assert.Equal(t, "checksum", doc.Nodes["default"].Method)
	assert.Equal(t, "timestamp", doc.Nodes["build"].Method)
	assert.Equal(t, "none", doc.Nodes["lint"].Method)
	assert.Equal(t, "Run everything", doc.Nodes["default"].Desc)
	assert.Equal(t, "default", doc.Nodes["default"].Name)

	require.NotNil(t, doc.Nodes["lint"].UpToDate)
	assert.True(t, *doc.Nodes["lint"].UpToDate)
	require.NotNil(t, doc.Nodes["build"].UpToDate)
	assert.False(t, *doc.Nodes["build"].UpToDate)

	assert.Greater(t, doc.Nodes["lint"].Location.Line, doc.Nodes["build"].Location.Line)
	assert.Greater(t, doc.Nodes["default"].Location.Column, 0)
	assert.Contains(t, doc.Nodes["lint"].Location.Taskfile, "Taskfile.yml")

	assert.Equal(t, [][]string{{"lint"}, {"test"}, {"build"}, {"default"}}, doc.DepthGroups)
	assert.Equal(t, []string{"default", "build", "test", "lint"}, doc.LongestPath)

	require.Len(t, doc.Edges, 5)
	var rootDep, rootCmd bool
	for _, edge := range doc.Edges {
		assert.Empty(t, edge.Vars)
		if edge.From == "default" && edge.To == "build" && edge.Type == "dep" {
			rootDep = true
		}
		if edge.From == "default" && edge.To == "test" && edge.Type == "cmd" {
			rootCmd = true
		}
	}
	assert.True(t, rootDep)
	assert.True(t, rootCmd)
}

func TestGraphAliasAndMultipleRoots(t *testing.T) {
	t.Parallel()
	e, stdout := setupGraphExecutor(t, map[string]string{"Taskfile.yml": basicTaskfile})

	aliased := graphJSON(t, e, stdout, &task.Call{Task: "aliased"})
	assert.Equal(t, []string{"alias-target"}, aliased.Roots)
	assert.Contains(t, aliased.Nodes, "alias-target")
	assert.Contains(t, aliased.Nodes, "lint")
	assert.NotContains(t, aliased.Nodes, "aliased")

	doc := graphJSON(t, e, stdout, &task.Call{Task: "lint"}, &task.Call{Task: "build"})
	assert.Equal(t, []string{"lint", "build"}, doc.Roots)
	assert.Equal(t, []string{"build", "test", "lint"}, doc.LongestPath)
	assert.NotContains(t, doc.Nodes, "default")
	assert.NotContains(t, doc.Nodes, "other")
}

func TestGraphDefaultTask(t *testing.T) {
	t.Parallel()
	e, stdout := setupGraphExecutor(t, map[string]string{"Taskfile.yml": basicTaskfile})
	doc := graphJSON(t, e, stdout)
	assert.Equal(t, []string{"default"}, doc.Roots)
}

func TestGraphTextAndDot(t *testing.T) {
	t.Parallel()
	e, stdout := setupGraphExecutor(t, map[string]string{"Taskfile.yml": basicTaskfile}, task.WithGraphFormat("text"))
	stdout.Reset()
	require.NoError(t, e.Graph(&task.Call{Task: "default"}))
	assert.Equal(t, ""+
		"default\n"+
		"  build\n"+
		"    lint\n"+
		"    test\n"+
		"      lint (repeated)\n"+
		"  test (repeated)\n", stdout.String())

	e.Options(task.WithGraphFormat("dot"))
	stdout.Reset()
	require.NoError(t, e.Graph(&task.Call{Task: "default"}))
	dot := stdout.String()
	assert.Contains(t, dot, "digraph tasks {")
	assert.Contains(t, dot, `"lint" [style=dashed];`)
	assert.Contains(t, dot, `"build";`)
	assert.NotContains(t, dot, `"build" [style=dashed]`)
	assert.Contains(t, dot, `"default" -> "build";`)
	assert.Contains(t, dot, `"default" -> "test";`)
	assert.Contains(t, dot, "\n}\n")
}

func TestGraphNoStatus(t *testing.T) {
	t.Parallel()
	e, stdout := setupGraphExecutor(t, map[string]string{"Taskfile.yml": basicTaskfile}, task.WithGraphNoStatus(true))
	stdout.Reset()
	require.NoError(t, e.Graph(&task.Call{Task: "default"}))

	var raw map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &raw))
	nodes := raw["nodes"].(map[string]any)
	lint := nodes["lint"].(map[string]any)
	_, hasStatus := lint["up_to_date"]
	assert.False(t, hasStatus)
	assert.Equal(t, "none", lint["method"])

	e.Options(task.WithGraphFormat("dot"))
	stdout.Reset()
	require.NoError(t, e.Graph(&task.Call{Task: "default"}))
	assert.NotContains(t, stdout.String(), "dashed")
	assert.Contains(t, stdout.String(), "digraph tasks {")
}

func TestGraphReverse(t *testing.T) {
	t.Parallel()
	e, stdout := setupGraphExecutor(t, map[string]string{"Taskfile.yml": basicTaskfile}, task.WithGraphReverse(true))
	doc := graphJSON(t, e, stdout, &task.Call{Task: "lint"})

	assert.Equal(t, []string{"lint"}, doc.Roots)
	assert.ElementsMatch(t, []string{"lint", "build", "test", "default", "alias-target"}, keysOf(doc.Nodes))
	assert.NotContains(t, doc.Nodes, "other")
	assert.Equal(t, []string{"alias-target", "build", "test"}, doc.Nodes["lint"].Deps)
	assert.Equal(t, []string{"default"}, doc.Nodes["build"].Deps)
	assert.Equal(t, []string{"build", "default"}, doc.Nodes["test"].Deps)
	assert.Empty(t, doc.Nodes["default"].Deps)
	assert.Equal(t, [][]string{{"alias-target", "default"}, {"build"}, {"test"}, {"lint"}}, doc.DepthGroups)
	assert.Equal(t, []string{"lint", "test", "build", "default"}, doc.LongestPath)

	var cmdEdge bool
	for _, edge := range doc.Edges {
		if edge.From == "test" && edge.To == "default" && edge.Type == "cmd" {
			cmdEdge = true
		}
		assert.Contains(t, []string{"dep", "cmd"}, edge.Type)
	}
	assert.True(t, cmdEdge)

	e.Options(task.WithGraphFormat("text"))
	stdout.Reset()
	require.NoError(t, e.Graph(&task.Call{Task: "lint"}))
	assert.Equal(t, ""+
		"lint\n"+
		"  build\n"+
		"    default\n"+
		"  test\n"+
		"    build (repeated)\n"+
		"    default (repeated)\n"+
		"  alias-target\n", stdout.String())
}

func TestGraphForLoopAndNamespaceAndWildcard(t *testing.T) {
	t.Parallel()

	t.Run("for", func(t *testing.T) {
		t.Parallel()
		e, stdout := setupGraphExecutor(t, map[string]string{"Taskfile.yml": `
version: '3'
tasks:
  default:
    deps:
      - task: echo
        for:
          - one
          - two
        vars:
          NAME: '{{.ITEM}}'
    cmds:
      - task: echo
        for:
          - three
        vars:
          NAME: '{{.ITEM}}'
  echo:
    cmds:
      - echo '{{.NAME}}'
  all:
    deps:
      - task: 'item-{{.ITEM}}'
        for:
          - a
          - b
  item-a:
    cmds:
      - echo a
  item-b:
    cmds:
      - echo b
`})
		doc := graphJSON(t, e, stdout, &task.Call{Task: "default"})
		require.Len(t, doc.Edges, 3)
		assert.Equal(t, []string{"echo"}, doc.Nodes["default"].Deps)
		assert.Equal(t, "dep", doc.Edges[0].Type)
		assert.Equal(t, "one", doc.Edges[0].Vars["NAME"])
		assert.Equal(t, "dep", doc.Edges[1].Type)
		assert.Equal(t, "two", doc.Edges[1].Vars["NAME"])
		assert.Equal(t, "cmd", doc.Edges[2].Type)
		assert.Equal(t, "three", doc.Edges[2].Vars["NAME"])

		all := graphJSON(t, e, stdout, &task.Call{Task: "all"})
		assert.Equal(t, []string{"item-a", "item-b"}, all.Nodes["all"].Deps)
		require.Len(t, all.Edges, 2)
		assert.Equal(t, "item-a", all.Edges[0].To)
		assert.Equal(t, "item-b", all.Edges[1].To)
	})

	t.Run("namespace", func(t *testing.T) {
		t.Parallel()
		e, stdout := setupGraphExecutor(t, map[string]string{
			"Taskfile.yml": `
version: '3'
includes:
  lib: ./lib
tasks:
  default:
    cmds:
      - task: lib:build
`,
			"lib/Taskfile.yml": `
version: '3'
tasks:
  build:
    desc: Build lib
    deps:
      - lint
  lint:
    desc: Lint lib
    cmds:
      - echo lint
`,
		})
		doc := graphJSON(t, e, stdout, &task.Call{Task: "default"})
		assert.Equal(t, []string{"lib:build"}, doc.Nodes["default"].Deps)
		assert.Equal(t, []string{"lib:lint"}, doc.Nodes["lib:build"].Deps)
		assert.Contains(t, doc.Nodes, "lib:lint")
		var called bool
		for _, edge := range doc.Edges {
			if edge.From == "default" && edge.To == "lib:build" && edge.Type == "cmd" {
				called = true
			}
		}
		assert.True(t, called)
		assert.Contains(t, filepath.ToSlash(doc.Nodes["lib:lint"].Location.Taskfile), "lib/Taskfile.yml")
		assert.Equal(t, "Lint lib", doc.Nodes["lib:lint"].Desc)
	})

	t.Run("wildcard", func(t *testing.T) {
		t.Parallel()
		e, stdout := setupGraphExecutor(t, map[string]string{"Taskfile.yml": `
version: '3'
tasks:
  default:
    deps:
      - build-app
  build-*:
    desc: Build a target
    deps:
      - 'prep-{{index .MATCH 0}}'
  prep-app:
    desc: Prep app
    cmds:
      - echo prep
`})
		doc := graphJSON(t, e, stdout, &task.Call{Task: "default"})
		assert.Contains(t, doc.Nodes, "build-app")
		assert.Contains(t, doc.Nodes, "prep-app")
		assert.NotContains(t, doc.Nodes, "build-*")
		assert.Equal(t, []string{"build-app"}, doc.Nodes["default"].Deps)
		assert.Equal(t, []string{"prep-app"}, doc.Nodes["build-app"].Deps)

		e.Options(task.WithGraphReverse(true))
		rev := graphJSON(t, e, stdout, &task.Call{Task: "prep-app"})
		assert.ElementsMatch(t, []string{"prep-app", "build-app", "default"}, keysOf(rev.Nodes))
		assert.Equal(t, []string{"prep-app", "build-app", "default"}, rev.LongestPath)
	})
}

func TestGraphMissingTaskAndCycle(t *testing.T) {
	t.Parallel()

	t.Run("missing root", func(t *testing.T) {
		t.Parallel()
		e, _ := setupGraphExecutor(t, map[string]string{"Taskfile.yml": "version: '3'\ntasks: {}\n"})
		err := e.Graph(&task.Call{Task: "missing-task"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing-task")
	})

	t.Run("missing dependency", func(t *testing.T) {
		t.Parallel()
		e, _ := setupGraphExecutor(t, map[string]string{"Taskfile.yml": `
version: '3'
tasks:
  default:
    deps: [missing-dep]
`})
		err := e.Graph(&task.Call{Task: "default"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing-dep")
	})

	t.Run("cycle", func(t *testing.T) {
		t.Parallel()
		e, _ := setupGraphExecutor(t, map[string]string{"Taskfile.yml": `
version: '3'
tasks:
  a:
    deps: [b]
  b:
    deps: [c]
  c:
    deps: [a]
`})
		err := e.Graph(&task.Call{Task: "a"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cycle")
		assert.Contains(t, err.Error(), "a")
		assert.Contains(t, err.Error(), "b")
		assert.Contains(t, err.Error(), "c")
		var cycleErr *errors.TaskDependencyCycleError
		require.ErrorAs(t, err, &cycleErr)
	})

	t.Run("unrelated cycle is not reported", func(t *testing.T) {
		t.Parallel()
		e, stdout := setupGraphExecutor(t, map[string]string{"Taskfile.yml": `
version: '3'
tasks:
  ok:
    cmds:
      - echo ok
  a:
    deps: [b]
  b:
    deps: [a]
`})
		doc := graphJSON(t, e, stdout, &task.Call{Task: "ok"})
		assert.Equal(t, []string{"ok"}, keysOf(doc.Nodes))

		e.Options(task.WithGraphReverse(true))
		rev := graphJSON(t, e, stdout, &task.Call{Task: "ok"})
		assert.Equal(t, []string{"ok"}, keysOf(rev.Nodes))

		err := e.Graph(&task.Call{Task: "a"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cycle")
	})
}

func TestGraphUnsupportedFormat(t *testing.T) {
	t.Parallel()
	e, _ := setupGraphExecutor(t, map[string]string{"Taskfile.yml": basicTaskfile}, task.WithGraphFormat("yaml"))
	err := e.Graph(&task.Call{Task: "default"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "yaml")
}

func keysOf[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
