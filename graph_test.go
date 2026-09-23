package task_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-task/task/v3"
)

func TestGraphJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Taskfile.yml"), `
version: '3'
tasks:
  default:
    cmds:
      - task: build
  build:
    desc: Build the project
    deps: [lint]
    cmds:
      - task: compile
        vars: {FOO: bar}
  lint:
    method: none
  compile:
    deps: [lint]
  aliasme:
    aliases: [al]
    deps: [lint]
  loop:
    deps:
      - task: compile
        for: ['a', 'b']
        vars:
          ITEM: '{{.ITEM}}'
  gen-*:
    desc: Generated
`)
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	writeFile(t, filepath.Join(sub, "Taskfile.yml"), `
version: '3'
tasks:
  echo:
    cmds:
      - echo hi
`)
	// namespace include is a separate taskfile so the main fixture stays focused;
	// covered by TestGraphNamespace.

	var buf bytes.Buffer
	e := task.NewExecutor(
		task.WithDir(dir),
		task.WithStdout(&buf),
		task.WithStderr(&buf),
		task.WithGraphNoStatus(true),
	)
	require.NoError(t, e.Setup())
	require.NoError(t, e.Graph(&task.Call{Task: "build"}, &task.Call{Task: "al"}))

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, []any{"build", "aliasme"}, got["roots"])

	nodes := got["nodes"].(map[string]any)
	build := nodes["build"].(map[string]any)
	require.Equal(t, "Build the project", build["desc"])
	require.Equal(t, []any{"compile", "lint"}, build["deps"])
	require.NotContains(t, build, "up_to_date")
	require.Equal(t, "checksum", build["method"])
	require.Equal(t, "none", nodes["lint"].(map[string]any)["method"])
	loc := build["location"].(map[string]any)
	require.NotEmpty(t, loc["taskfile"])
	require.Greater(t, loc["line"], float64(0))

	edges := got["edges"].([]any)
	require.GreaterOrEqual(t, len(edges), 2)
	var sawCmd bool
	for _, raw := range edges {
		edge := raw.(map[string]any)
		if edge["from"] == "build" && edge["to"] == "compile" && edge["type"] == "cmd" {
			sawCmd = true
			require.Equal(t, "bar", edge["vars"].(map[string]any)["FOO"])
		}
	}
	require.True(t, sawCmd)

	groups := got["depth_groups"].([]any)
	// lint has no deps; compile depends on lint; build and aliasme depend on those.
	require.Equal(t, []any{"lint"}, groups[0])
	require.Contains(t, groups[len(groups)-1], "build")

	path := got["longest_path"].([]any)
	require.Equal(t, "build", path[0])
	require.Equal(t, "lint", path[len(path)-1])
}

func TestGraphForLoopEdges(t *testing.T) {
	dir := writeTasks(t, `
version: '3'
tasks:
  loop:
    deps:
      - task: compile
        for: ['a', 'b']
  compile:
`)
	model := graphJSON(t, dir, false, "loop")
	var n int
	for _, raw := range model["edges"].([]any) {
		edge := raw.(map[string]any)
		if edge["from"] == "loop" && edge["to"] == "compile" && edge["type"] == "dep" {
			n++
		}
	}
	require.Equal(t, 2, n)
}

func TestGraphFormatsAndReverse(t *testing.T) {
	dir := writeTasks(t, `
version: '3'
tasks:
  base:
  a:
    deps: [base]
  b:
    deps: [base]
  c:
    deps: [a]
  top:
    deps: [a, b]
  a2:
    deps: [shared]
  b2:
    deps: [shared]
  shared:
`)
	text := graphOut(t, dir, "text", false, false, "top")
	require.Equal(t, "top\n  a\n    base\n  b\n    base (repeated)\n", text)

	repeated := graphOut(t, dir, "text", false, false, "top")
	require.Contains(t, repeated, "(repeated)")

	dot := graphOut(t, dir, "dot", false, false, "top")
	require.Contains(t, dot, "digraph tasks {")
	require.Contains(t, dot, `"top" -> "a";`)
	require.Contains(t, dot, `"a" -> "base";`)
	require.NotContains(t, dot, "style=dashed")

	rev := graphJSON(t, dir, true, "base")
	require.Equal(t, []any{"base"}, rev["roots"])
	groups := rev["depth_groups"].([]any)
	require.Equal(t, []any{"c", "top"}, groups[0])
	require.Equal(t, []any{"a", "b"}, groups[1])
	require.Equal(t, []any{"base"}, groups[2])
	require.Equal(t, []any{"base", "a", "c"}, rev["longest_path"])

	revText := graphOut(t, dir, "text", true, false, "base")
	require.Contains(t, revText, "base\n")
	require.Contains(t, revText, "a")
	require.Contains(t, revText, "c")
}

func TestGraphUpToDateDot(t *testing.T) {
	dir := writeTasks(t, `
version: '3'
tasks:
  ok:
    status:
      - exit 0
    cmds:
      - task: other
  other:
`)
	dot := graphOut(t, dir, "dot", false, false, "ok")
	require.Contains(t, dot, `"ok" [style=dashed];`)
	require.NotContains(t, dot, `"other" [style=dashed];`)

	noStatus := graphOut(t, dir, "dot", false, true, "ok")
	require.NotContains(t, noStatus, "style=dashed")

	js := graphJSONOpts(t, dir, false, false, "ok")
	node := js["nodes"].(map[string]any)["ok"].(map[string]any)
	require.Equal(t, true, node["up_to_date"])
}

func TestGraphWildcardAliasAndMissingAndCycle(t *testing.T) {
	dir := writeTasks(t, `
version: '3'
tasks:
  gen-*:
    desc: Generated {{.MATCH}}
  use:
    deps: [gen-foo]
  al:
    aliases: [short]
  a:
    deps: [b]
  b:
    deps: [a]
`)
	js := graphJSON(t, dir, false, "gen-foo")
	require.Equal(t, []any{"gen-foo"}, js["roots"])
	require.Contains(t, js["nodes"], "gen-foo")

	aliased := graphJSON(t, dir, false, "short")
	require.Equal(t, []any{"al"}, aliased["roots"])

	var buf bytes.Buffer
	e := executorIn(t, dir, &buf)
	err := e.Graph(&task.Call{Task: "missing-task"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing-task")

	err = e.Graph(&task.Call{Task: "a"})
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "cycle")
	require.Contains(t, err.Error(), "a")
	require.Contains(t, err.Error(), "b")
}

func TestGraphNamespace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Taskfile.yml"), `
version: '3'
includes:
  sub:
    taskfile: ./sub/Taskfile.yml
tasks:
  root:
    cmds:
      - task: sub:echo
`)
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	writeFile(t, filepath.Join(sub, "Taskfile.yml"), `
version: '3'
tasks:
  echo:
    deps: [inner]
  inner:
`)
	js := graphJSON(t, dir, false, "root")
	require.Contains(t, js["nodes"], "sub:echo")
	require.Contains(t, js["nodes"], "sub:inner")
	deps := js["nodes"].(map[string]any)["sub:echo"].(map[string]any)["deps"]
	require.Equal(t, []any{"sub:inner"}, deps)
}

func TestGraphDefaultFormatIsJSON(t *testing.T) {
	dir := writeTasks(t, `
version: '3'
tasks:
  default:
    cmds:
      - echo hi
`)
	out := graphOut(t, dir, "", false, true)
	require.True(t, strings.HasPrefix(strings.TrimSpace(out), "{"))
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, []any{"default"}, got["roots"])
}

func writeTasks(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Taskfile.yml"), content)
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644))
}

func executorIn(t *testing.T, dir string, buf *bytes.Buffer) *task.Executor {
	t.Helper()
	e := task.NewExecutor(
		task.WithDir(dir),
		task.WithStdout(buf),
		task.WithStderr(buf),
	)
	require.NoError(t, e.Setup())
	return e
}

func graphOut(t *testing.T, dir, format string, reverse, noStatus bool, tasks ...string) string {
	t.Helper()
	var buf bytes.Buffer
	e := task.NewExecutor(
		task.WithDir(dir),
		task.WithStdout(&buf),
		task.WithStderr(&buf),
		task.WithGraphFormat(format),
		task.WithGraphReverse(reverse),
		task.WithGraphNoStatus(noStatus),
	)
	require.NoError(t, e.Setup())
	var calls []*task.Call
	for _, name := range tasks {
		calls = append(calls, &task.Call{Task: name})
	}
	require.NoError(t, e.Graph(calls...))
	return buf.String()
}

func graphJSON(t *testing.T, dir string, reverse bool, tasks ...string) map[string]any {
	t.Helper()
	return graphJSONOpts(t, dir, reverse, true, tasks...)
}

func graphJSONOpts(t *testing.T, dir string, reverse, noStatus bool, tasks ...string) map[string]any {
	t.Helper()
	out := graphOut(t, dir, "json", reverse, noStatus, tasks...)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	return got
}
