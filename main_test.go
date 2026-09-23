package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const sccTestFlag string = "-test.main"

var sccBinPath = os.Args[0]

func TestMain(m *testing.M) {
	idx := slices.Index(os.Args, sccTestFlag)
	if idx != -1 {
		os.Args = slices.Delete(os.Args, idx, idx+1)
		main()
		return
	}

	os.Exit(m.Run())
}

func runSCC(args ...string) (string, error) {
	args = slices.Insert(args, 0, sccTestFlag)
	cmd := exec.Command(sccBinPath, args...)
	res, err := cmd.CombinedOutput()
	return string(res), err
}

func runSCCOutputs(args ...string) (string, string, error) {
	args = slices.Insert(args, 0, sccTestFlag)
	cmd := exec.Command(sccBinPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestNoGitIgnore(t *testing.T) {
	tmpDir := t.TempDir()
	ignoreFileName := filepath.Join(tmpDir, ".gitignore")
	err := os.WriteFile(ignoreFileName, []byte("ignored.xml\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	xmlFileName := filepath.Join(tmpDir, "ignored.xml")
	err = os.WriteFile(xmlFileName, []byte(`<?xml version="1.0" encoding="UTF-8"?>`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	output, err := runSCC(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "XML") {
		t.Fatalf("test --no-gitignore failed, output:\n%s", output)
	}

	output, err = runSCC("--no-gitignore", tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "XML") {
		t.Fatalf("test --no-gitignore failed, output:\n%s", output)
	}
}

func TestIssue82(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/82
	output1, err := runSCC(".")
	if err != nil {
		t.Fatal(err)
	}

	pwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	output2, err := runSCC(pwd)
	if err != nil {
		t.Fatal(err)
	}

	if output1 != output2 {
		t.Fatalf("`./scc .` not equal to `./scc ${PWD}`")
	}
}

func TestIncludeExt(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/108
	output, err := runSCC("--include-ext", "go", "examples/language")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Go") || strings.Contains(output, "Java") {
		t.Fatalf("include-ext check failed, output:\n%s", output)
	}
}

func TestIssue115(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/115
	output, err := runSCC("examples/issue115/.test/file")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "Perl") {
		t.Fatalf("Should not print Perl, output:\n%s", output)
	}
}

func TestIssue120(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/120
	output, err := runSCC("-i", "java", "./examples/issue120")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "Perl") {
		t.Fatal("extension param should ignore Shebang")
	}
}

func TestIssue152(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/152
	output, err := runSCC("-i", "css", "./examples/issue152/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "CSS") {
		t.Fatalf("`-i css` extension check failed, output:\n%s", output)
	}
}

func TestIssue250(t *testing.T) {
	// Regression issue https://github.com/boyter/scc/issues/250
	output1, err := runSCC("--exclude-dir", "examples/")
	if err != nil {
		t.Fatal(err)
	}
	output2, err := runSCC("--exclude-dir", "examples")
	if err != nil {
		t.Fatal(err)
	}

	if output1 != output2 {
		t.Fatalf("examples exclude-dir check failed, output1:\n%s, output2:\n%s", output1, output2)
	}
}

func TestIssue259(t *testing.T) {
	// Regression issue https://github.com/boyter/scc/issues/259
	output, err := runSCC("-f", "csv", "--exclude-ext", "go")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(output, "Go,") {
		t.Fatalf("exclude-ext check failed, output:\n%s", output)
	}
}

func TestIssue260(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/260
	_, err := runSCC("-d", "examples/issue260/")
	if err != nil {
		t.Fatalf("duplicate empty crash: %v", err)
	}
}

func TestIssue345(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/345
	const expectedOutput = "C++,4,3,1,0,0,76,1,0"
	output, err := runSCC("-f", "csv", "--no-scc-ignore", "examples/issue345/")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		t.Fatalf("wrong output: %s", output)
	}
	if lines[1] != expectedOutput {
		t.Fatalf("got: %s, want: %s", lines[1], expectedOutput)
	}
}

func TestIssue379(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/379
	const expectedOutput = "Python,7,4,2,1,1,83,1,0"
	output, err := runSCC("-f", "csv", "--no-scc-ignore", "examples/issue379/")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		t.Fatalf("wrong output: %s", output)
	}
	if lines[1] != expectedOutput {
		t.Fatalf("got: %s, want: %s", lines[1], expectedOutput)
	}
}

func TestIssue457(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/457
	output, err := runSCC("-M", ".*")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "0.000 megabytes") {
		t.Fatalf("Issue 457 test failed, output:\n%s", output)
	}
}

func TestIssue564(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/564
	const expectedPythonOutput = "Python,3,3,0,0,0,84,3,0"
	const expectedGoOutput = "Go,6,4,0,2,0,58,2,0"
	output, err := runSCC("-f", "csv", "--no-scc-ignore", "examples/issue564/")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(output, "\n")
	if len(lines) < 3 {
		t.Fatalf("wrong output: %s", output)
	}
	if lines[1] != expectedPythonOutput {
		t.Fatalf("got: %s, want: %s", lines[1], expectedPythonOutput)
	}
	if lines[2] != expectedGoOutput {
		t.Fatalf("got: %s, want: %s", lines[2], expectedGoOutput)
	}
}

func TestIssue610(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/610
	const expectedOutput = "TypeScript,11,7,2,2,1,214,1,0"
	output, err := runSCC("-f", "csv", "--no-scc-ignore", "examples/issue610/")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		t.Fatalf("wrong output: %s", output)
	}
	if lines[1] != expectedOutput {
		t.Fatalf("got: %s, want: %s", lines[1], expectedOutput)
	}
}

func TestIssue339(t *testing.T) {
	t.Parallel()
	// Regression issue https://github.com/boyter/scc/issues/339
	output, err := runSCC("-f", "csv", "--no-scc-ignore", "examples/issue339/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "MATLAB") {
		t.Errorf("can not find MATLAB, output: %s", output)
	}
	if !strings.Contains(output, "Objective C") {
		t.Errorf("can not find Objective C, output:\n%s", output)
	}
}

func TestInvalidOption(t *testing.T) {
	t.Parallel()
	output, err := runSCC("--not-a-real-option")
	if err == nil {
		t.Fatal("scc should exit with error code")
	}
	if !strings.Contains(output, "Error: unknown flag: --not-a-real-option") {
		t.Fatalf("scc should report invalid options, output:\n%s", output)
	}
}

func TestFileFlagSyntax(t *testing.T) {
	tmpDir := t.TempDir()
	flagsFileName := filepath.Join(tmpDir, "flags.txt")
	// include \n, \r\n and no line terminators
	testCases := []string{
		"go.mod\ngo.sum\nLICENSE\n",
		"go.mod\r\ngo.sum\r\nLICENSE\r\n",
		"go.mod\ngo.sum\nLICENSE",
		"go.mod\r\ngo.sum\r\nLICENSE",
		"go.mod\ngo.sum\r\nLICENSE",
	}

	for _, tc := range testCases {
		err := os.WriteFile(flagsFileName, []byte(tc), 0644)
		if err != nil {
			t.Fatal(err)
		}
		_, err = runSCC("@" + flagsFileName)
		if err != nil {
			t.Errorf("flag syntax faild: %q, %v", tc, err)
		}
	}
}

func TestLineLength(t *testing.T) {
	t.Parallel()
	output, err := runSCC("-m")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(output, "MaxLine / MeanLine") < 2 {
		t.Fatalf("line length test failed, output:\n%s", output)
	}
}

func TestMultipleFormatStdout(t *testing.T) {
	output, err := runSCC("--format-multi", "tabular:stdout,html:stdout,csv:stdout,sql:stdout")
	if err != nil {
		t.Fatal(err)
	}

	tabularPattern := regexp.MustCompile(`Processed .+? bytes, .+? megabytes \(SI\)`)
	if !tabularPattern.MatchString(output) {
		t.Errorf("multi-format tabular failed, output:\n%s", output)
	}

	if !strings.Contains(output, `<html lang="en"><head><meta charset="utf-8" /><title>scc html output</title>`) {
		t.Errorf("multi-format html failed, output:\n%s", output)
	}

	if !strings.Contains(output, "Language,Lines,Code,Comments,Blanks,Complexity,Bytes,Files,ULOC") {
		t.Errorf("multi-format csv failed, output:\n%s", output)
	}

	sqlPattern := regexp.MustCompile(`insert into t values\(.+?\);`)
	if !sqlPattern.MatchString(output) {
		t.Errorf("multi-format sql failed, output:\n%s", output)
	}
}

func TestMultipleFormatWriteFile(t *testing.T) {
	tmpDir := t.TempDir()
	outputTabular := filepath.Join(tmpDir, "output.tab")
	outputWide := filepath.Join(tmpDir, "output.wide")
	outputJSON1 := filepath.Join(tmpDir, "output.json")
	outputJSON2 := filepath.Join(tmpDir, "output2.json")
	outputCSV := filepath.Join(tmpDir, "output.csv")
	outputYAML := filepath.Join(tmpDir, "output.yaml")
	outputHTML := filepath.Join(tmpDir, "output.html")
	outputHTMLTable := filepath.Join(tmpDir, "output_table.html")
	outputSQL := filepath.Join(tmpDir, "output.sql")

	multiFormatArgs := fmt.Sprintf(
		"tabular:%s,wide:%s,json:%s,json2:%s,csv:%s,cloc-yaml:%s,html:%s,html-table:%s,sql:%s",
		outputTabular,
		outputWide,
		outputJSON1,
		outputJSON2,
		outputCSV,
		outputYAML,
		outputHTML,
		outputHTMLTable,
		outputSQL,
	)

	_, err := runSCC("--format-multi", multiFormatArgs)
	if err != nil {
		t.Fatal(err)
	}

	if info, err := os.Stat(outputTabular); err != nil || info.Size() <= 0 {
		t.Fatal("tabular write file test failed")
	}
	if info, err := os.Stat(outputWide); err != nil || info.Size() <= 0 {
		t.Fatal("wide write file test failed")
	}
	if info, err := os.Stat(outputJSON1); err != nil || info.Size() <= 0 {
		t.Fatal("json write file test failed")
	}
	if info, err := os.Stat(outputJSON2); err != nil || info.Size() <= 0 {
		t.Fatal("json2 write file test failed")
	}
	if info, err := os.Stat(outputCSV); err != nil || info.Size() <= 0 {
		t.Fatal("csv write file test failed")
	}
	if info, err := os.Stat(outputYAML); err != nil || info.Size() <= 0 {
		t.Fatal("cloc-yaml write file test failed")
	}
	if info, err := os.Stat(outputHTML); err != nil || info.Size() <= 0 {
		t.Fatal("html write file test failed")
	}
	if info, err := os.Stat(outputHTMLTable); err != nil || info.Size() <= 0 {
		t.Fatal("html-table write file test failed")
	}
	if info, err := os.Stat(outputSQL); err != nil || info.Size() <= 0 {
		t.Fatal("sql write file test failed")
	}
}

func TestRecursivelyIgnore(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("ignore-git.txt\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(tmpDir, ".ignore"), []byte("vendor/\nignore.txt\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Mkdir(filepath.Join(tmpDir, "ignore"), 0755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(tmpDir, "ignore", "README.md"), []byte("Files in here are to ensure that .ignore and .gitignore work recursively\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(tmpDir, "ignore", "ignore.txt"), []byte("testing\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(tmpDir, "ignore", "ignore-git.txt"), []byte("git\ntesting\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	output, err := runSCC("--by-file", "--no-scc-ignore", tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "ignore.txt") || strings.Contains(output, "ignore-git.txt") {
		t.Errorf("ignore recursive filter failed, output:\n%s", output)
	}

	output, err = runSCC("--by-file", "--no-scc-ignore", "--no-ignore", tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "ignore.txt") || strings.Contains(output, "ignore-git.txt") {
		t.Errorf("ignore recursive filter failed, output:\n%s", output)
	}

	output, err = runSCC("--by-file", "--no-scc-ignore", "--no-gitignore", tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "ignore.txt") || !strings.Contains(output, "ignore-git.txt") {
		t.Errorf("ignore recursive filter failed, output:\n%s", output)
	}

	output, err = runSCC("--by-file", "--no-scc-ignore", "--no-ignore", "--no-gitignore", tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "ignore.txt") || !strings.Contains(output, "ignore-git.txt") {
		t.Errorf("ignore recursive filter failed, output:\n%s", output)
	}
}

func TestMultipleGitIgnore(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("ignore.txt\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Mkdir(filepath.Join(tmpDir, "ignore"), 0755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(tmpDir, "ignore", ".gitignore"), []byte("ignore.java\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(tmpDir, "ignore", "ignore.java"), []byte("//test\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	output, err := runSCC(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "Java") {
		t.Fatalf("multiple gitignore failed, output:\n%s", output)
	}
}

func TestFlagSuggestion(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		args           []string
		expectedOutput string
	}{
		{
			args:           []string{"--farmat"},
			expectedOutput: "The most similar flag of --farmat is:\n\t--format\n",
		},
		{
			args:           []string{"--no-gignore"},
			expectedOutput: "The most similar flags of --no-gignore are:\n\t--no-ignore\n\t--no-gitignore\n",
		},
	}

	for _, tc := range testCases {
		output, err := runSCC(tc.args...)
		if err == nil {
			t.Fatal("scc should exit with error code")
		}
		if !strings.Contains(output, tc.expectedOutput) {
			t.Errorf("wrong suggestion for %v, want: %s, got: %s", tc.args, tc.expectedOutput, output)
		}
	}
}

func TestDeterministicOutput(t *testing.T) {
	output, err := runSCC(".")
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		output2, err := runSCC(".")
		if err != nil {
			t.Fatal(err)
		}
		if output != output2 {
			t.Fatalf("want:\n%s, got:\n%s", output, output2)
		}
	}
}

func TestLanguageNameTruncate(t *testing.T) {
	output, err := runSCC("examples/language")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(output, "Bitbucket Pipe…") != 1 {
		t.Errorf("`Bitbucket Pipeline` truncate test failed")
	}
	if strings.Count(output, "CloudFormation…") != 2 {
		t.Errorf("`CloudFormation (JSON)` and `CloudFormation (YAML)` truncate test failed")
	}
}

func TestSpecificLanguages(t *testing.T) {
	languages := [...]string{
		"ABNF",
		"Alchemist",
		"Algol 68",
		"Alloy",
		"Amber",
		"Apex",
		"ArkTs",
		"Arturo",
		"Astro",
		"AWK",
		"BASH",
		"Bean",
		"Bicep",
		"Bitbucket Pipeline",
		"Blueprint",
		"Boo",
		"Bosque",
		"Bru",
		"C3",
		"C Shell",
		"C#",
		"Cairo",
		"Cangjie",
		"Chapel",
		"Circom",
		"Clipper",
		"Clojure",
		"CMake",
		"Cuda",
		"Cypher",
		"D2",
		"DAML",
		"DM",
		"Docker ignore",
		"Dockerfile",
		"DOT",
		"Elixir Template",
		"Elm",
		"EmiT",
		"F#",
		"Factor",
		"Flow9",
		"FSL",
		"Futhark",
		"FXML",
		"Gemfile",
		"Gleam",
		"Go",
		"Go+",
		"Godot Scene",
		"GraphQL",
		"Gremlin",
		"Gwion",
		"HAML",
		"Hare",
		"Haskell",
		"HCL",
		"ignore",
		"INI",
		"Java",
		"JavaScript",
		"JCL",
		"JSON5",
		"JSONC",
		"jq",
		"Korn Shell",
		"Koto",
		"LALRPOP",
		"License",
		"LiveScript",
		"LLVM IR",
		"Lua",
		"Luau",
		"Luna",
		"MLIR",
		"Makefile",
		"Metal",
		"Monkey C",
		"Moonbit",
		"Nature",
		"Nushell",
		"OpenQASM",
		"OpenTofu",
		"Perl",
		"Pkl",
		"Plain Text",
		"POML",
		"PostScript",
		"Proto",
		"Python",
		"Q#",
		"R",
		"Racket",
		"Rakefile",
		"RAML",
		"Rebol",
		"Redscript",
		"Rich Text Format",
		"Scallop",
		"Seed7",
		"Shell",
		"Sieve",
		"Slang",
		"Slint",
		"Smalltalk",
		"Snakemake",
		"Stan",
		"Systemd",
		"Tact",
		"Teal",
		"Tera",
		"Templ",
		"Terraform",
		"TOML",
		"TOON",
		"TTCN-3",
		"TypeScript",
		"TypeSpec",
		"Typst",
		"Up",
		"Vala",
		"Vim Script",
		"Web Services Description Language",
		"wenyan",
		"Wren",
		"XMake",
		"XML Schema",
		"YAML",
		"Yarn",
		"Zig",
		"ZoKrates",
		"Zsh",
	}

	output, err := runSCC("-f", "csv", "examples/language")
	if err != nil {
		t.Fatal(err)
	}

	for _, language := range languages {
		if !strings.Contains(output, language+",") {
			t.Errorf("language not found in output: %v", language)
		}
	}
}

func TestBoundedMemoryCLI(t *testing.T) {
	src := t.TempDir()
	for i, body := range []string{
		"package a\n\nfunc A() int {\n\tif true {\n\t\treturn 1\n\t}\n\treturn 0\n}\n",
		"package b\n\nfunc B() {}\n",
		"# comment\ndef c():\n    return 1\n\n",
		"package d\n\n// note\nfunc D() int { return 2 }\n",
	} {
		name := fmt.Sprintf("f%d.go", i)
		if i == 2 {
			name = "c.py"
		}
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	spill := filepath.Join(t.TempDir(), "nested", "spill")
	common := []string{
		"--file-process-job-workers", "1",
		"--directory-walker-job-workers", "1",
		"--sort", "lines",
	}

	formats := []string{
		"json:stdout",
		"json2:stdout",
		"csv:stdout",
		"csv-stream:stdout",
		"tabular:stdout,wide:stdout",
		"json:stdout,csv:stdout",
	}
	for _, format := range formats {
		unbounded, _, err := runSCCOutputs(append(append([]string{"--format-multi", format}, common...), src)...)
		if err != nil {
			t.Fatalf("unbounded %s: %v\n%s", format, err, unbounded)
		}
		bounded, stderr, err := runSCCOutputs(append(append([]string{
			"--bounded-memory",
			"--bounded-memory-dir", spill,
			"--bounded-memory-max-in-memory-files", "1",
			"--bounded-memory-stats",
			"--format-multi", format,
		}, common...), src)...)
		if err != nil {
			t.Fatalf("bounded %s: %v\nstdout:\n%s\nstderr:\n%s", format, err, bounded, stderr)
		}
		if format == "tabular:stdout,wide:stdout" {
			if tabularTotals(unbounded) != tabularTotals(bounded) {
				t.Fatalf("totals mismatch\nunbounded:\n%s\nbounded:\n%s", unbounded, bounded)
			}
		} else if unbounded != bounded {
			t.Fatalf("stdout mismatch for %s\nunbounded:\n%s\nbounded:\n%s", format, unbounded, bounded)
		}
		stats := boundedMemoryLines(stderr)
		if len(stats) != 1 {
			t.Fatalf("format %s stats lines=%d stderr=%q", format, len(stats), stderr)
		}
		spills, peak := parseBoundedStats(t, stats[0])
		if spills <= 0 || peak != 1 {
			t.Fatalf("format %s spills=%d peak=%d", format, spills, peak)
		}
	}

	if countSpillFiles(t, spill) < 1 {
		t.Fatal("expected spill files to remain after exit")
	}

	streamFile := filepath.Join(t.TempDir(), "stream.csv")
	unboundedStream, _, err := runSCCOutputs(append(append([]string{"--format-multi", "csv-stream:stdout"}, common...), src)...)
	if err != nil {
		t.Fatal(err)
	}
	boundedStdout, stderr, err := runSCCOutputs(append(append([]string{
		"--bounded-memory",
		"--bounded-memory-dir", spill,
		"--bounded-memory-max-in-memory-files", "1",
		"--format-multi", "csv-stream:" + streamFile,
	}, common...), src)...)
	if err != nil {
		t.Fatalf("csv-stream file: %v\n%s", err, stderr)
	}
	if boundedStdout != "" {
		t.Fatalf("csv-stream file destination wrote stdout: %q", boundedStdout)
	}
	got, err := os.ReadFile(streamFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != unboundedStream {
		t.Fatalf("csv-stream file mismatch\nfile:\n%s\nstdout:\n%s", got, unboundedStream)
	}
	if !streamLinesNonIncreasing(string(got)) {
		t.Fatalf("csv-stream not sorted by lines:\n%s", got)
	}

	excluded := t.TempDir()
	if err := os.WriteFile(filepath.Join(excluded, "keep.go"), []byte("package keep\n\nfunc K() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(excluded, "spill")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("package secret\nfunc S() { return 1 }\n", 40)
	if err := os.WriteFile(filepath.Join(inside, "secret.go"), []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		body := fmt.Sprintf("package p%d\n\nfunc F() int { return %d }\n", i, i)
		if err := os.WriteFile(filepath.Join(excluded, fmt.Sprintf("p%d.go", i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A different directory whose name ends in "spill" must still be counted.
	otherSpill := filepath.Join(excluded, "other", "spill")
	if err := os.MkdirAll(otherSpill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherSpill, "extra.go"), []byte("package extra\n\nfunc E() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runSCCOutputs(
		"--bounded-memory",
		"--bounded-memory-dir", inside,
		"--bounded-memory-max-in-memory-files", "1",
		"--bounded-memory-stats",
		"--format-multi", "tabular:stdout",
		"--file-process-job-workers", "1",
		excluded,
	)
	if err != nil {
		t.Fatalf("exclude: %v\n%s\n%s", err, stdout, stderr)
	}
	if strings.Contains(stdout, "secret") {
		t.Fatalf("spill directory was counted:\n%s", stdout)
	}
	files := tabularFileCount(t, stdout)
	if files != 5 {
		t.Fatalf("expected 5 files excluding spill dir, got %d\n%s", files, stdout)
	}
	stats := boundedMemoryLines(stderr)
	if len(stats) != 1 {
		t.Fatalf("exclude stats: %q", stderr)
	}

	if _, _, err := runSCCOutputs("--bounded-memory", "--format-multi", "json:stdout", src); err == nil {
		t.Fatal("missing spill dir should fail")
	}
	if _, _, err := runSCCOutputs("--bounded-memory", "--bounded-memory-dir", spill, "--bounded-memory-max-in-memory-files", "0", "--format-multi", "json:stdout", src); err == nil {
		t.Fatal("max 0 should fail")
	}
}

func boundedMemoryLines(stderr string) []string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "bounded-memory:") {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseBoundedStats(t *testing.T, line string) (int, int) {
	t.Helper()
	var spills, peak int
	if _, err := fmt.Sscanf(line, "bounded-memory: spills=%d peak_in_memory_files=%d", &spills, &peak); err != nil {
		t.Fatal(err)
	}
	return spills, peak
}

func tabularTotals(out string) string {
	var totals []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Total") {
			totals = append(totals, strings.Join(strings.Fields(line), " "))
		}
	}
	return strings.Join(totals, "\n")
}

func tabularFileCount(t *testing.T, out string) int {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "Total" {
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatalf("no total line:\n%s", out)
	return 0
}

func countSpillFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() && info.Size() > 0 {
			n++
		}
	}
	return n
}

func streamLinesNonIncreasing(out string) bool {
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(rows) < 2 {
		return false
	}
	prev := int64(1 << 62)
	for _, row := range rows[1:] {
		fields := strings.Split(row, ",")
		if len(fields) < 4 {
			return false
		}
		n, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return false
		}
		if n > prev {
			return false
		}
		prev = n
	}
	return true
}
