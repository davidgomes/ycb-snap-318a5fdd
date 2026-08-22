package actionlint

import (
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v4"
)

// ActionPinningLevel is a pinning strictness level for the action-pinning rule.
type ActionPinningLevel string

const (
	// ActionPinningLevelMajorMinor requires refs like vMAJOR.MINOR.
	ActionPinningLevelMajorMinor ActionPinningLevel = "major-minor"
	// ActionPinningLevelSemver requires refs like vMAJOR.MINOR.PATCH including prerelease.
	ActionPinningLevelSemver ActionPinningLevel = "semver"
	// ActionPinningLevelCommitSHA requires a full 40-character lowercase hex commit SHA.
	ActionPinningLevelCommitSHA ActionPinningLevel = "commit-sha"
)

var (
	actionPinningSemverRefPattern     = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	actionPinningMajorMinorRefPattern = regexp.MustCompile(`^v\d+\.\d+$`)
)

// ActionPinningConfig is configuration for the action-pinning rule.
type ActionPinningConfig struct {
	Level          ActionPinningLevel `yaml:"level"`
	AllowedOwners  []string           `yaml:"allowed-owners"`
	AllowedActions []string           `yaml:"allowed-actions"`
	DeniedOwners   []string           `yaml:"denied-owners"`
	DeniedActions  []string           `yaml:"denied-actions"`
}

// ActionPinningConfigField represents the action-pinning config field in YAML. It distinguishes
// absent key, null (explicitly disabled), and present config (enabled).
type ActionPinningConfigField struct {
	Config   *ActionPinningConfig
	Disabled bool
	Present  bool
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (f *ActionPinningConfigField) UnmarshalYAML(n *yaml.Node) error {
	f.Present = true
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null" {
		f.Disabled = true
		return nil
	}
	var c ActionPinningConfig
	if err := n.Decode(&c); err != nil {
		return err
	}
	f.Config = &c
	return nil
}

func validateActionPinningLevel(level ActionPinningLevel) error {
	switch level {
	case "", ActionPinningLevelMajorMinor, ActionPinningLevelSemver, ActionPinningLevelCommitSHA:
		return nil
	default:
		return fmt.Errorf("invalid action-pinning level %q", level)
	}
}

func validateActionPinningOwner(owner string, field string) error {
	if strings.Contains(owner, "/") {
		return fmt.Errorf("invalid owner %q in %q: owner must not contain '/'", owner, field)
	}
	return nil
}

func validateActionPinningOwnerRepo(action string, field string) error {
	parts := strings.Split(action, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid action %q in %q: must be in owner/repo format", action, field)
	}
	return nil
}

func validateActionPinningConfig(c *ActionPinningConfig) error {
	if c == nil {
		return nil
	}
	if err := validateActionPinningLevel(c.Level); err != nil {
		return err
	}
	for _, o := range c.AllowedOwners {
		if err := validateActionPinningOwner(o, "allowed-owners"); err != nil {
			return err
		}
	}
	for _, o := range c.DeniedOwners {
		if err := validateActionPinningOwner(o, "denied-owners"); err != nil {
			return err
		}
	}
	for _, a := range c.AllowedActions {
		if err := validateActionPinningOwnerRepo(a, "allowed-actions"); err != nil {
			return err
		}
	}
	for _, a := range c.DeniedActions {
		if err := validateActionPinningOwnerRepo(a, "denied-actions"); err != nil {
			return err
		}
	}
	return nil
}

func actionPinningLevelStrictness(level ActionPinningLevel) int {
	switch level {
	case ActionPinningLevelMajorMinor:
		return 0
	case ActionPinningLevelSemver:
		return 1
	case ActionPinningLevelCommitSHA:
		return 2
	default:
		return -1
	}
}

func strictestActionPinningLevel(levels ...ActionPinningLevel) ActionPinningLevel {
	var best ActionPinningLevel
	bestRank := -1
	for _, level := range levels {
		if level == "" {
			level = ActionPinningLevelSemver
		}
		rank := actionPinningLevelStrictness(level)
		if rank > bestRank {
			bestRank = rank
			best = level
		}
	}
	if bestRank < 0 {
		return ActionPinningLevelSemver
	}
	return best
}

func isActionPinningCommitSHA(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, c := range ref {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func satisfiesActionPinningLevel(ref string, level ActionPinningLevel) bool {
	if level == "" {
		level = ActionPinningLevelSemver
	}
	if isActionPinningCommitSHA(ref) {
		return true
	}
	switch level {
	case ActionPinningLevelCommitSHA:
		return false
	case ActionPinningLevelSemver:
		return actionPinningSemverRefPattern.MatchString(ref)
	case ActionPinningLevelMajorMinor:
		return actionPinningSemverRefPattern.MatchString(ref) || actionPinningMajorMinorRefPattern.MatchString(ref)
	default:
		return false
	}
}

func actionPinningLevelDescription(level ActionPinningLevel) string {
	switch level {
	case ActionPinningLevelMajorMinor:
		return "a version tag in vMAJOR.MINOR format"
	case ActionPinningLevelSemver:
		return "a semver tag in vMAJOR.MINOR.PATCH format"
	case ActionPinningLevelCommitSHA:
		return "a full-length commit SHA"
	default:
		return "a pinned version"
	}
}

type effectiveActionPinningConfig struct {
	enabled        bool
	level          ActionPinningLevel
	allowedOwners  map[string]struct{}
	allowedActions map[string]struct{}
	deniedOwners   map[string]struct{}
	deniedActions  map[string]struct{}
}

func mergeActionPinningLists(dst map[string]struct{}, values []string, normalizeOwner bool) {
	for _, v := range values {
		key := v
		if normalizeOwner {
			key = strings.ToLower(v)
		}
		dst[key] = struct{}{}
	}
}

func mergeActionPinningConfig(dst *effectiveActionPinningConfig, src *ActionPinningConfig) {
	if src == nil {
		return
	}
	if dst.allowedOwners == nil {
		dst.allowedOwners = map[string]struct{}{}
	}
	if dst.allowedActions == nil {
		dst.allowedActions = map[string]struct{}{}
	}
	if dst.deniedOwners == nil {
		dst.deniedOwners = map[string]struct{}{}
	}
	if dst.deniedActions == nil {
		dst.deniedActions = map[string]struct{}{}
	}
	mergeActionPinningLists(dst.allowedOwners, src.AllowedOwners, true)
	mergeActionPinningLists(dst.allowedActions, src.AllowedActions, false)
	mergeActionPinningLists(dst.deniedOwners, src.DeniedOwners, true)
	mergeActionPinningLists(dst.deniedActions, src.DeniedActions, false)
}

func resolveEffectiveActionPinning(cfg *Config, pathConfigs []PathConfig, cliLevel string) effectiveActionPinningConfig {
	eff := effectiveActionPinningConfig{}

	enabled := cliLevel != ""
	globalDisabled := false
	var levels []ActionPinningLevel

	if cfg != nil && cfg.ActionPinning.Present {
		if cfg.ActionPinning.Disabled {
			globalDisabled = true
		} else {
			enabled = true
			mergeActionPinningConfig(&eff, cfg.ActionPinning.Config)
			if cfg.ActionPinning.Config != nil && cfg.ActionPinning.Config.Level != "" {
				levels = append(levels, cfg.ActionPinning.Config.Level)
			}
		}
	}

	for _, pc := range pathConfigs {
		if !pc.ActionPinning.Present || pc.ActionPinning.Disabled {
			continue
		}
		enabled = true
		mergeActionPinningConfig(&eff, pc.ActionPinning.Config)
		if pc.ActionPinning.Config != nil && pc.ActionPinning.Config.Level != "" {
			levels = append(levels, pc.ActionPinning.Config.Level)
		}
	}

	if globalDisabled && cliLevel == "" {
		pathEnabled := false
		for _, pc := range pathConfigs {
			if pc.ActionPinning.Present && !pc.ActionPinning.Disabled {
				pathEnabled = true
				break
			}
		}
		if !pathEnabled {
			enabled = false
		}
	}

	if cliLevel != "" {
		enabled = true
		eff.level = ActionPinningLevel(cliLevel)
	} else if len(levels) > 0 {
		eff.level = strictestActionPinningLevel(levels...)
	} else if enabled {
		eff.level = ActionPinningLevelSemver
	}

	eff.enabled = enabled
	return eff
}

func (eff *effectiveActionPinningConfig) isExempt(owner, repo string) bool {
	ownerKey := strings.ToLower(owner)
	actionKey := owner + "/" + repo

	if _, ok := eff.deniedOwners[ownerKey]; ok {
		return false
	}
	if _, ok := eff.deniedActions[actionKey]; ok {
		return false
	}
	if _, ok := eff.allowedOwners[ownerKey]; ok {
		return true
	}
	if _, ok := eff.allowedActions[actionKey]; ok {
		return true
	}
	return false
}

func splitUsesAtRef(spec string) (name, ref string, ok bool) {
	idx := strings.LastIndex(spec, "@")
	if idx <= 0 || idx >= len(spec)-1 {
		return "", "", false
	}
	return spec[:idx], spec[idx+1:], true
}

func parseActionOwnerRepo(name string) (owner, repo string, ok bool) {
	idx := strings.IndexRune(name, '/')
	if idx <= 0 || idx >= len(name)-1 {
		return "", "", false
	}
	owner = name[:idx]
	rest := name[idx+1:]
	repoIdx := strings.IndexRune(rest, '/')
	if repoIdx >= 0 {
		repo = rest[:repoIdx]
	} else {
		repo = rest
	}
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}

func suggestKnownActionVersion(owner, repo string, level ActionPinningLevel) string {
	prefix := owner + "/" + repo + "@"
	var best, fallback string
	for spec := range PopularActions {
		if !strings.HasPrefix(spec, prefix) {
			continue
		}
		ref := spec[len(prefix):]
		if fallback == "" || ref > fallback {
			fallback = ref
		}
		if !satisfiesActionPinningLevel(ref, level) {
			continue
		}
		if best == "" || ref > best {
			best = ref
		}
	}
	if best != "" {
		return prefix + best
	}
	if fallback != "" {
		return prefix + fallback
	}
	return ""
}
