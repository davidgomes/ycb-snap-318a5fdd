package actionlint

import (
	"fmt"
	"strings"
)

// RuleActionPinning is a rule checker to ensure actions and reusable workflows are pinned.
type RuleActionPinning struct {
	RuleBase
	workflowPath string
	cliLevel     string
	eff          effectiveActionPinningConfig
}

// NewRuleActionPinning creates a new RuleActionPinning instance.
func NewRuleActionPinning(workflowPath, cliLevel string) *RuleActionPinning {
	return &RuleActionPinning{
		RuleBase: RuleBase{
			name: "action-pinning",
			desc: "Checks that actions and reusable workflows are pinned to immutable refs",
		},
		workflowPath: workflowPath,
		cliLevel:     cliLevel,
	}
}

// VisitWorkflowPre resolves effective configuration for the workflow file.
func (rule *RuleActionPinning) VisitWorkflowPre(n *Workflow) error {
	var pathConfigs []PathConfig
	if cfg := rule.Config(); cfg != nil {
		pathConfigs = cfg.PathConfigs(rule.workflowPath)
	}
	rule.eff = resolveEffectiveActionPinning(rule.Config(), pathConfigs, rule.cliLevel)
	return nil
}

// VisitStep checks step-level action uses references.
func (rule *RuleActionPinning) VisitStep(n *Step) error {
	if !rule.eff.enabled {
		return nil
	}

	e, ok := n.Exec.(*ExecAction)
	if !ok || e.Uses == nil || e.Uses.Value == "" {
		return nil
	}

	spec := e.Uses.Value
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "docker://") {
		return nil
	}

	rule.checkUsesRef(e.Uses.Pos, spec, false)
	return nil
}

// VisitJobPre checks job-level reusable workflow uses references.
func (rule *RuleActionPinning) VisitJobPre(n *Job) error {
	if !rule.eff.enabled || n.WorkflowCall == nil {
		return nil
	}

	u := n.WorkflowCall.Uses
	if u == nil || u.Value == "" {
		return nil
	}

	if strings.HasPrefix(u.Value, "./") {
		return nil
	}

	if !isWorkflowCallUsesRepoFormat(u.Value) {
		return nil
	}

	rule.checkUsesRef(u.Pos, u.Value, true)
	return nil
}

func (rule *RuleActionPinning) checkUsesRef(pos *Pos, spec string, reusable bool) {
	name, ref, ok := splitUsesAtRef(spec)
	if !ok {
		return
	}

	if ContainsExpression(name) {
		return
	}

	if ContainsExpression(ref) {
		rule.reportDynamicRefError(pos, spec, reusable)
		return
	}

	owner, repo, ok := parseActionOwnerRepo(name)
	if !ok {
		return
	}

	if rule.eff.isExempt(owner, repo) {
		return
	}

	if satisfiesActionPinningLevel(ref, rule.eff.level) {
		return
	}

	rule.reportUnpinnedError(pos, spec, owner, repo, ref, reusable)
}

func (rule *RuleActionPinning) reportDynamicRefError(pos *Pos, spec string, reusable bool) {
	if reusable {
		rule.Errorf(
			pos,
			"reusable workflow %q uses a dynamic expression for its ref, which cannot be verified for pinning",
			spec,
		)
		return
	}
	rule.Errorf(
		pos,
		"action %q uses a dynamic expression for its ref, which cannot be verified for pinning",
		spec,
	)
}

func (rule *RuleActionPinning) reportUnpinnedError(pos *Pos, spec, owner, repo, ref string, reusable bool) {
	want := actionPinningLevelDescription(rule.eff.level)
	suggestion := suggestKnownActionVersion(owner, repo, rule.eff.level)
	suffix := ""
	if suggestion != "" {
		suffix = fmt.Sprintf(". Did you mean %q?", suggestion)
	} else if ref != "" {
		suffix = fmt.Sprintf(". Got ref %q", ref)
	}

	if reusable {
		rule.Errorf(
			pos,
			"reusable workflow %q must be pinned to %s%s",
			spec,
			want,
			suffix,
		)
		return
	}
	rule.Errorf(
		pos,
		"action %q must be pinned to %s%s",
		spec,
		want,
		suffix,
	)
}
