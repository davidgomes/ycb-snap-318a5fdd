package actionlint

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestActionPinningLevelSatisfies(t *testing.T) {
	tests := []struct {
		ref   string
		level ActionPinningLevel
		want  bool
	}{
		{"0123456789abcdef0123456789abcdef01234567", ActionPinningLevelCommitSHA, true},
		{"0123456789abcdef0123456789abcdef01234567", ActionPinningLevelSemver, true},
		{"0123456789abcdef0123456789abcdef01234567", ActionPinningLevelMajorMinor, true},
		{"0123456789ABCDEF0123456789ABCDEF01234567", ActionPinningLevelCommitSHA, false},
		{"0123456789abcdef0123456789abcdef0123456", ActionPinningLevelCommitSHA, false},
		{"v1.2.3", ActionPinningLevelSemver, true},
		{"v1.2.3", ActionPinningLevelMajorMinor, true},
		{"v1.2.3", ActionPinningLevelCommitSHA, false},
		{"v1.2.3-beta.1", ActionPinningLevelSemver, true},
		{"v1.2", ActionPinningLevelMajorMinor, true},
		{"v1.2", ActionPinningLevelSemver, false},
		{"v4", ActionPinningLevelMajorMinor, false},
		{"main", ActionPinningLevelMajorMinor, false},
	}

	for _, tc := range tests {
		t.Run(tc.ref+"_"+string(tc.level), func(t *testing.T) {
			have := satisfiesActionPinningLevel(tc.ref, tc.level)
			if have != tc.want {
				t.Fatalf("wanted %v but got %v", tc.want, have)
			}
		})
	}
}

func TestActionPinningConfigParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:  "empty enables defaults",
			input: "action-pinning: {}",
		},
		{
			name:  "null disables",
			input: "action-pinning: null",
		},
		{
			name:  "valid level",
			input: "action-pinning:\n  level: commit-sha",
		},
		{
			name:    "invalid level",
			input:   "action-pinning:\n  level: latest",
			wantErr: `invalid action-pinning level "latest"`,
		},
		{
			name:    "owner with slash in allowed-owners",
			input:   "action-pinning:\n  allowed-owners: [foo/bar]",
			wantErr: `invalid owner "foo/bar" in "allowed-owners"`,
		},
		{
			name:    "malformed allowed-actions",
			input:   "action-pinning:\n  allowed-actions: [foo]",
			wantErr: `invalid action "foo" in "allowed-actions"`,
		},
		{
			name:    "malformed denied-actions",
			input:   "action-pinning:\n  denied-actions: [foo/bar/baz]",
			wantErr: `invalid action "foo/bar/baz" in "denied-actions"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tc.input))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error but got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("wanted error containing %q but got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestResolveEffectiveActionPinning(t *testing.T) {
	cfg, err := ParseConfig([]byte(`
action-pinning:
  level: semver
  allowed-owners: [trusted]
  denied-owners: [trusted]
paths:
  .github/workflows/release.yaml:
    action-pinning:
      level: commit-sha
`))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path     string
		cliLevel string
		enabled  bool
		level    ActionPinningLevel
		exempt   bool
	}{
		{".github/workflows/ci.yaml", "", true, ActionPinningLevelSemver, false},
		{".github/workflows/release.yaml", "", true, ActionPinningLevelCommitSHA, false},
		{".github/workflows/ci.yaml", "major-minor", true, ActionPinningLevelMajorMinor, false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			eff := resolveEffectiveActionPinning(cfg, cfg.PathConfigs(tc.path), tc.cliLevel)
			if eff.enabled != tc.enabled {
				t.Fatalf("enabled: wanted %v got %v", tc.enabled, eff.enabled)
			}
			if eff.level != tc.level {
				t.Fatalf("level: wanted %q got %q", tc.level, eff.level)
			}
		})
	}

	eff := resolveEffectiveActionPinning(cfg, cfg.PathConfigs(".github/workflows/ci.yaml"), "")
	if eff.isExempt("trusted", "repo") {
		t.Fatal("denied owner should not be exempt from pinning")
	}
	if eff.isExempt("other", "repo") {
		t.Fatal("unexpected exemption")
	}
}

func TestActionPinningDisabledByNull(t *testing.T) {
	cfg, err := ParseConfig([]byte(`action-pinning: null`))
	if err != nil {
		t.Fatal(err)
	}
	eff := resolveEffectiveActionPinning(cfg, nil, "")
	if eff.enabled {
		t.Fatal("rule should be disabled")
	}

	eff = resolveEffectiveActionPinning(cfg, nil, "semver")
	if !eff.enabled {
		t.Fatal("CLI flag should enable the rule")
	}
	if eff.level != ActionPinningLevelSemver {
		t.Fatalf("wanted semver level, got %q", eff.level)
	}
}

func TestActionPinningPerPathEnablesWithoutGlobal(t *testing.T) {
	cfg, err := ParseConfig([]byte(`
paths:
  .github/workflows/release.yaml:
    action-pinning:
      level: major-minor
`))
	if err != nil {
		t.Fatal(err)
	}
	eff := resolveEffectiveActionPinning(cfg, cfg.PathConfigs(".github/workflows/release.yaml"), "")
	if !eff.enabled {
		t.Fatal("per-path config should enable the rule")
	}
	if eff.level != ActionPinningLevelMajorMinor {
		t.Fatalf("wanted major-minor, got %q", eff.level)
	}
}

func TestSuggestKnownActionVersion(t *testing.T) {
	s := suggestKnownActionVersion("actions", "checkout", ActionPinningLevelMajorMinor)
	if s == "" {
		t.Fatal("expected suggestion for actions/checkout")
	}
	if !strings.HasPrefix(s, "actions/checkout@") {
		t.Fatalf("unexpected suggestion %q", s)
	}
}

func TestStrictestActionPinningLevel(t *testing.T) {
	got := strictestActionPinningLevel(ActionPinningLevelMajorMinor, ActionPinningLevelCommitSHA, ActionPinningLevelSemver)
	if diff := cmp.Diff(ActionPinningLevelCommitSHA, got); diff != "" {
		t.Fatal(diff)
	}
}
