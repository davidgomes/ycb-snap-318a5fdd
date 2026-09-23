/*
Copyright The Helm Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rules

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chart "helm.sh/helm/v4/internal/chart/v3"
	"helm.sh/helm/v4/internal/chart/v3/lint/support"
	chartutil "helm.sh/helm/v4/internal/chart/v3/util"
)

const (
	badChartNameDir       = "testdata/badchartname"
	badChartDir           = "testdata/badchartfile"
	anotherBadChartDir    = "testdata/anotherbadchartfile"
	badMergeStrategiesDir = "testdata/badmergestrategies"
)

var (
	badChartNamePath         = filepath.Join(badChartNameDir, "Chart.yaml")
	badChartFilePath         = filepath.Join(badChartDir, "Chart.yaml")
	nonExistingChartFilePath = filepath.Join(os.TempDir(), "Chart.yaml")
)

var badChart, _ = chartutil.LoadChartfile(badChartFilePath)
var badChartName, _ = chartutil.LoadChartfile(badChartNamePath)

// Validation functions Test
func TestValidateChartYamlNotDirectory(t *testing.T) {
	_ = os.Mkdir(nonExistingChartFilePath, os.ModePerm)
	defer os.Remove(nonExistingChartFilePath)

	err := validateChartYamlNotDirectory(nonExistingChartFilePath)
	if err == nil {
		t.Errorf("validateChartYamlNotDirectory to return a linter error, got no error")
	}
}

func TestValidateChartYamlFormat(t *testing.T) {
	err := validateChartYamlFormat(errors.New("Read error"))
	if err == nil {
		t.Errorf("validateChartYamlFormat to return a linter error, got no error")
	}

	err = validateChartYamlFormat(nil)
	if err != nil {
		t.Errorf("validateChartYamlFormat to return no error, got a linter error")
	}
}

func TestValidateChartName(t *testing.T) {
	err := validateChartName(badChart)
	if err == nil {
		t.Errorf("validateChartName to return a linter error, got no error")
	}

	err = validateChartName(badChartName)
	if err == nil {
		t.Error("expected validateChartName to return a linter error for an invalid name, got no error")
	}
}

func TestValidateChartVersion(t *testing.T) {
	var failTest = []struct {
		Version  string
		ErrorMsg string
	}{
		{"", "version is required"},
		{"1.2.3.4", "version '1.2.3.4' is not a valid SemVerV2"},
		{"waps", "'waps' is not a valid SemVerV2"},
		{"-3", "'-3' is not a valid SemVerV2"},
		{"1.1", "'1.1' is not a valid SemVerV2"},
		{"1", "'1' is not a valid SemVerV2"},
	}

	var successTest = []string{"0.0.1", "0.0.1+build", "0.0.1-beta"}

	for _, test := range failTest {
		badChart.Version = test.Version
		err := validateChartVersion(badChart)
		if err == nil || !strings.Contains(err.Error(), test.ErrorMsg) {
			t.Errorf("validateChartVersion(%s) to return \"%s\", got no error", test.Version, test.ErrorMsg)
		}
	}

	for _, version := range successTest {
		badChart.Version = version
		err := validateChartVersion(badChart)
		if err != nil {
			t.Errorf("validateChartVersion(%s) to return no error, got a linter error", version)
		}
	}
}

func TestValidateChartMaintainer(t *testing.T) {
	var failTest = []struct {
		Name     string
		Email    string
		ErrorMsg string
	}{
		{"", "", "each maintainer requires a name"},
		{"", "test@test.com", "each maintainer requires a name"},
		{"John Snow", "wrongFormatEmail.com", "invalid email"},
	}

	var successTest = []struct {
		Name  string
		Email string
	}{
		{"John Snow", ""},
		{"John Snow", "john@winterfell.com"},
	}

	for _, test := range failTest {
		badChart.Maintainers = []*chart.Maintainer{{Name: test.Name, Email: test.Email}}
		err := validateChartMaintainer(badChart)
		if err == nil || !strings.Contains(err.Error(), test.ErrorMsg) {
			t.Errorf("validateChartMaintainer(%s, %s) to return \"%s\", got no error", test.Name, test.Email, test.ErrorMsg)
		}
	}

	for _, test := range successTest {
		badChart.Maintainers = []*chart.Maintainer{{Name: test.Name, Email: test.Email}}
		err := validateChartMaintainer(badChart)
		if err != nil {
			t.Errorf("validateChartMaintainer(%s, %s) to return no error, got %s", test.Name, test.Email, err.Error())
		}
	}

	// Testing for an empty maintainer
	badChart.Maintainers = []*chart.Maintainer{nil}
	err := validateChartMaintainer(badChart)
	if err == nil {
		t.Errorf("validateChartMaintainer did not return error for nil maintainer as expected")
	}
	if err.Error() != "a maintainer entry is empty" {
		t.Errorf("validateChartMaintainer returned unexpected error for nil maintainer: %s", err.Error())
	}
}

func TestValidateChartSources(t *testing.T) {
	var failTest = []string{"", "RiverRun", "john@winterfell", "riverrun.io"}
	var successTest = []string{"http://riverrun.io", "https://riverrun.io", "https://riverrun.io/blackfish"}
	for _, test := range failTest {
		badChart.Sources = []string{test}
		err := validateChartSources(badChart)
		if err == nil || !strings.Contains(err.Error(), "invalid source URL") {
			t.Errorf("validateChartSources(%s) to return \"invalid source URL\", got no error", test)
		}
	}

	for _, test := range successTest {
		badChart.Sources = []string{test}
		err := validateChartSources(badChart)
		if err != nil {
			t.Errorf("validateChartSources(%s) to return no error, got %s", test, err.Error())
		}
	}
}

func TestValidateChartIconPresence(t *testing.T) {
	t.Run("Icon absent", func(t *testing.T) {
		testChart := &chart.Metadata{
			Icon: "",
		}

		err := validateChartIconPresence(testChart)

		if err == nil {
			t.Errorf("validateChartIconPresence to return a linter error, got no error")
		} else if !strings.Contains(err.Error(), "icon is recommended") {
			t.Errorf("expected %q, got %q", "icon is recommended", err.Error())
		}
	})
	t.Run("Icon present", func(t *testing.T) {
		testChart := &chart.Metadata{
			Icon: "http://example.org/icon.png",
		}

		err := validateChartIconPresence(testChart)

		if err != nil {
			t.Errorf("Unexpected error: %q", err.Error())
		}
	})
}

func TestValidateChartIconURL(t *testing.T) {
	var failTest = []string{"RiverRun", "john@winterfell", "riverrun.io"}
	var successTest = []string{"http://riverrun.io", "https://riverrun.io", "https://riverrun.io/blackfish.png"}
	for _, test := range failTest {
		badChart.Icon = test
		err := validateChartIconURL(badChart)
		if err == nil || !strings.Contains(err.Error(), "invalid icon URL") {
			t.Errorf("validateChartIconURL(%s) to return \"invalid icon URL\", got no error", test)
		}
	}

	for _, test := range successTest {
		badChart.Icon = test
		err := validateChartSources(badChart)
		if err != nil {
			t.Errorf("validateChartIconURL(%s) to return no error, got %s", test, err.Error())
		}
	}
}

func TestV3Chartfile(t *testing.T) {
	t.Run("Chart.yaml basic validity issues", func(t *testing.T) {
		linter := support.Linter{ChartDir: badChartDir}
		Chartfile(&linter)
		msgs := linter.Messages
		expectedNumberOfErrorMessages := 6

		if len(msgs) != expectedNumberOfErrorMessages {
			t.Errorf("Expected %d errors, got %d", expectedNumberOfErrorMessages, len(msgs))
			return
		}

		if !strings.Contains(msgs[0].Err.Error(), "name is required") {
			t.Errorf("Unexpected message 0: %s", msgs[0].Err)
		}

		if !strings.Contains(msgs[1].Err.Error(), "apiVersion is required. The value must be \"v3\"") {
			t.Errorf("Unexpected message 1: %s", msgs[1].Err)
		}

		if !strings.Contains(msgs[2].Err.Error(), "version '0.0.0.0' is not a valid SemVer") {
			t.Errorf("Unexpected message 2: %s", msgs[2].Err)
		}

		if !strings.Contains(msgs[3].Err.Error(), "icon is recommended") {
			t.Errorf("Unexpected message 3: %s", msgs[3].Err)
		}
	})

	t.Run("Chart.yaml validity issues due to type mismatch", func(t *testing.T) {
		linter := support.Linter{ChartDir: anotherBadChartDir}
		Chartfile(&linter)
		msgs := linter.Messages
		expectedNumberOfErrorMessages := 3

		if len(msgs) != expectedNumberOfErrorMessages {
			t.Errorf("Expected %d errors, got %d", expectedNumberOfErrorMessages, len(msgs))
			return
		}

		if !strings.Contains(msgs[0].Err.Error(), "version should be of type string") {
			t.Errorf("Unexpected message 0: %s", msgs[0].Err)
		}

		if !strings.Contains(msgs[1].Err.Error(), "version '7.2445e+06' is not a valid SemVer") {
			t.Errorf("Unexpected message 1: %s", msgs[1].Err)
		}

		if !strings.Contains(msgs[2].Err.Error(), "appVersion should be of type string") {
			t.Errorf("Unexpected message 2: %s", msgs[2].Err)
		}
	})
}

func TestChartfileMergeStrategyAnnotationWarnings(t *testing.T) {
	t.Run("invalid merge strategy annotations", func(t *testing.T) {
		linter := support.Linter{ChartDir: badMergeStrategiesDir}
		Chartfile(&linter)

		expected := []string{
			`merge key for path "orphan" has no matching "helm.sh/merge-strategy/orphan" annotation`,
			`merge strategy path "config" resolves to a non-array value`,
			`merge strategy "merge" for path "containers" requires a merge key`,
			`merge strategy path "missing" not found in chart default values`,
			`merge strategy "prepend" for path "ports" is unsupported`,
		}
		if len(linter.Messages) != len(expected) {
			t.Fatalf("Expected %d messages, got %d: %v", len(expected), len(linter.Messages), linter.Messages)
		}
		for i, msg := range linter.Messages {
			if msg.Severity != support.WarningSev {
				t.Errorf("Expected message %d to be a warning, got severity %d", i, msg.Severity)
			}
			if !strings.Contains(msg.Err.Error(), expected[i]) {
				t.Errorf("Unexpected message %d: %s", i, msg.Err)
			}
		}
	})

	writeChart := func(t *testing.T, chartYaml, valuesYaml string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if valuesYaml != "" {
			if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(valuesYaml), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	chartYaml := `apiVersion: v3
name: mergestrategies
version: 0.1.0
icon: http://riverrun.io
annotations:
  helm.sh/merge-strategy/env: append
  helm.sh/merge-strategy/spec.containers: merge
  helm.sh/merge-key/spec.containers: metadata.name
`

	t.Run("valid merge strategy annotations", func(t *testing.T) {
		linter := support.Linter{ChartDir: writeChart(t, chartYaml, "env: []\nspec:\n  containers: []\n")}
		Chartfile(&linter)
		if len(linter.Messages) != 0 {
			t.Errorf("Expected no messages, got %v", linter.Messages)
		}
	})

	t.Run("merge strategy paths without values.yaml", func(t *testing.T) {
		linter := support.Linter{ChartDir: writeChart(t, chartYaml, "")}
		Chartfile(&linter)
		if len(linter.Messages) != 2 {
			t.Fatalf("Expected 2 messages, got %v", linter.Messages)
		}
		for _, msg := range linter.Messages {
			if !strings.Contains(msg.Err.Error(), "not found") {
				t.Errorf("Unexpected message: %s", msg.Err)
			}
		}
	})
}
