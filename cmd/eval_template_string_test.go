// Copyright 2026 The OPA Authors.  All rights reserved.
// Use of this source code is governed by an Apache2
// license that can be found in the LICENSE file.

package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-policy-agent/opa/cmd/formats"
	"github.com/open-policy-agent/opa/v1/util/test"
)

func TestEvalPartialSourceTemplateString(t *testing.T) {
	files := map[string]string{
		"policy.rego": `package test

p := $"hello {input.name}"
s := $"nested {$"inner {input.z}"} end"
`,
	}
	test.WithTempFS(files, func(root string) {
		params := newEvalCommandParams()
		params.partial = true
		if err := params.outputFormat.Set(formats.Source); err != nil {
			t.Fatal(err)
		}
		params.unknowns = []string{"input"}
		if err := params.dataPaths.Set(filepath.Join(root, "policy.rego")); err != nil {
			t.Fatal(err)
		}

		buf := new(bytes.Buffer)
		if _, err := eval([]string{"data.test.p"}, params, buf, nil); err != nil {
			t.Fatalf("err=%v output=%s", err, buf.String())
		}
		if strings.Contains(buf.String(), "internal.template_string") {
			t.Fatalf("source format leaked internal.template_string:\n%s", buf.String())
		}
		if !strings.Contains(buf.String(), `$"hello {input.name}"`) {
			t.Fatalf("source format: %s", buf.String())
		}

		buf.Reset()
		if _, err := eval([]string{"data.test.s"}, params, buf, nil); err != nil {
			t.Fatalf("err=%v output=%s", err, buf.String())
		}
		if strings.Contains(buf.String(), "internal.template_string") {
			t.Fatalf("nested source format leaked internal.template_string:\n%s", buf.String())
		}
		if !strings.Contains(buf.String(), `$"nested {$"inner {input.z}"} end"`) {
			t.Fatalf("nested source format: %s", buf.String())
		}
	})
}
