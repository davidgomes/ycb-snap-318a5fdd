package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/cmd/formats"
	"github.com/open-policy-agent/opa/v1/util/test"
)

func TestEvalPartialSourceReconstructsTemplateStrings(t *testing.T) {
	test.WithTempFS(map[string]string{
		"test.rego": `package test
import rego.v1

msg := $"hello {input.name}"

p if {
	data.test.msg == input.expected
}`,
	}, func(root string) {
		params := newEvalCommandParams()
		params.partial = true
		params.unknowns = []string{"input.name", "input.expected"}
		params.disableInlining = []string{"data.test.msg"}
		params.dataPaths = newrepeatedStringFlag([]string{root})
		_ = params.outputFormat.Set(string(formats.Source))

		var out bytes.Buffer

		_, err := eval([]string{"data.test.p"}, params, &out, nil)
		if err != nil {
			t.Fatalf("unexpected eval error: %v", err)
		}

		got := out.String()

		if strings.Contains(got, "internal.template_string") {
			t.Fatalf("expected source output to hide internal builtin but got %s", got)
		}

		if !strings.Contains(got, filepath.ToSlash(`data.partial.test.msg = input.expected`)) {
			t.Fatalf("expected source output to keep residual query but got %s", got)
		}

		if !strings.Contains(got, `$"hello {input.name}"`) {
			t.Fatalf("expected source output to contain reconstructed template string but got %s", got)
		}
	})
}
