package actionlint

import (
	"strings"
	"testing"
)

func TestRuleActionPinningStepActions(t *testing.T) {
	tests := []struct {
		name    string
		uses    string
		config  string
		cli     string
		wantErr string
		noErr   bool
	}{
		{
			name:  "pinned semver ok",
			uses:  "actions/checkout@v4.2.2",
			config: "action-pinning: {}",
			noErr: true,
		},
		{
			name:    "unpinned major tag",
			uses:    "actions/checkout@v4",
			config:  "action-pinning: {}",
			wantErr: "action \"actions/checkout@v4\" must be pinned to a semver tag",
		},
		{
			name:    "dynamic ref",
			uses:    "actions/checkout@${{ github.ref }}",
			config:  "action-pinning: {}",
			wantErr: "uses a dynamic expression for its ref",
		},
		{
			name:  "dynamic action name skipped",
			uses:  "${{ 'actions/checkout' }}@v4.2.2",
			config: "action-pinning: {}",
			noErr: true,
		},
		{
			name:  "local action skipped",
			uses:  "./.github/actions/foo",
			config: "action-pinning: {}",
			noErr: true,
		},
		{
			name:  "docker action skipped",
			uses:  "docker://alpine:3.18",
			config: "action-pinning: {}",
			noErr: true,
		},
		{
			name:  "allowed owner exempt",
			uses:  "trusted-org/some-action@main",
			config: "action-pinning:\n  allowed-owners: [trusted-org]",
			noErr: true,
		},
		{
			name:    "denied owner still checked",
			uses:    "trusted-org/some-action@main",
			config:  "action-pinning:\n  allowed-owners: [trusted-org]\n  denied-owners: [trusted-org]",
			wantErr: "must be pinned",
		},
		{
			name:  "disabled without config",
			uses:  "actions/checkout@v4",
			noErr: true,
		},
		{
			name:  "enabled by cli",
			uses:  "actions/checkout@v4.2.2",
			cli:   "semver",
			noErr: true,
		},
		{
			name:    "commit sha required",
			uses:    "actions/checkout@v4.2.2",
			config:  "action-pinning:\n  level: commit-sha",
			wantErr: "must be pinned to a full-length commit SHA",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRuleActionPinning("test.yaml", tc.cli)
			if tc.config != "" {
				cfg, err := ParseConfig([]byte(tc.config))
				if err != nil {
					t.Fatal(err)
				}
				r.SetConfig(cfg)
			}
			step := &Step{
				Exec: &ExecAction{
					Uses: &String{Value: tc.uses, Pos: &Pos{Line: 1, Col: 1}},
				},
			}
			if err := r.VisitWorkflowPre(&Workflow{}); err != nil {
				t.Fatal(err)
			}
			if err := r.VisitStep(step); err != nil {
				t.Fatal(err)
			}
			errs := r.Errs()
			if tc.noErr {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if len(errs) == 0 {
				t.Fatal("expected error but got none")
			}
			if !strings.Contains(errs[0].Message, tc.wantErr) {
				t.Fatalf("wanted error containing %q but got %q", tc.wantErr, errs[0].Message)
			}
		})
	}
}

func TestRuleActionPinningReusableWorkflow(t *testing.T) {
	tests := []struct {
		name    string
		uses    string
		config  string
		wantErr string
		noErr   bool
	}{
		{
			name:   "pinned reusable workflow",
			uses:   "owner/repo/.github/workflows/ci.yml@v1.2.3",
			config: "action-pinning: {}",
			noErr:  true,
		},
		{
			name:    "unpinned reusable workflow",
			uses:    "owner/repo/.github/workflows/ci.yml@main",
			config:  "action-pinning: {}",
			wantErr: "reusable workflow",
		},
		{
			name:    "dynamic reusable workflow ref",
			uses:    "owner/repo/.github/workflows/ci.yml@${{ github.ref }}",
			config:  "action-pinning: {}",
			wantErr: "reusable workflow",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRuleActionPinning("test.yaml", "")
			cfg, err := ParseConfig([]byte(tc.config))
			if err != nil {
				t.Fatal(err)
			}
			r.SetConfig(cfg)
			job := &Job{
				WorkflowCall: &WorkflowCall{
					Uses: &String{Value: tc.uses, Pos: &Pos{Line: 1, Col: 1}},
				},
			}
			if err := r.VisitWorkflowPre(&Workflow{}); err != nil {
				t.Fatal(err)
			}
			if err := r.VisitJobPre(job); err != nil {
				t.Fatal(err)
			}
			errs := r.Errs()
			if tc.noErr {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if len(errs) == 0 {
				t.Fatal("expected error but got none")
			}
			if !strings.Contains(errs[0].Message, tc.wantErr) {
				t.Fatalf("wanted error containing %q but got %q", tc.wantErr, errs[0].Message)
			}
		})
	}
}

func TestRuleActionPinningSuggestion(t *testing.T) {
	r := NewRuleActionPinning("test.yaml", "")
	cfg, err := ParseConfig([]byte("action-pinning: {}"))
	if err != nil {
		t.Fatal(err)
	}
	r.SetConfig(cfg)
	step := &Step{
		Exec: &ExecAction{
			Uses: &String{Value: "actions/checkout@v4", Pos: &Pos{Line: 1, Col: 1}},
		},
	}
	if err := r.VisitWorkflowPre(&Workflow{}); err != nil {
		t.Fatal(err)
	}
	if err := r.VisitStep(step); err != nil {
		t.Fatal(err)
	}
	errs := r.Errs()
	if len(errs) == 0 {
		t.Fatal("expected error")
	}
	if !strings.Contains(errs[0].Message, "Did you mean") {
		t.Fatalf("expected suggestion in error message: %q", errs[0].Message)
	}
}
