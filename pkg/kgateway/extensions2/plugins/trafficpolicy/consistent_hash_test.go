package trafficpolicy

import (
	"testing"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoy_type_matcher_v3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/policy"
)

func headerHashPolicy(name string, terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
			Header: &envoyroutev3.RouteAction_HashPolicy_Header{HeaderName: name},
		},
		Terminal: terminal,
	}
}

func cookieHashPolicy(name string, terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
			Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{Name: name},
		},
		Terminal: terminal,
	}
}

func queryParameterHashPolicy(name string, terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
			QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{Name: name},
		},
		Terminal: terminal,
	}
}

func filterStateHashPolicy(key string, terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
			FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: key},
		},
		Terminal: terminal,
	}
}

func TestConstructConsistentHash(t *testing.T) {
	tests := []struct {
		name        string
		in          *kgateway.ConsistentHash
		expected    *consistentHashIR
		expectedErr string
	}{
		{
			name:     "unset consistentHash produces no IR",
			in:       nil,
			expected: nil,
		},
		{
			name: "empty consistentHash defaults to a non-terminal source IP hash policy",
			in:   &kgateway.ConsistentHash{},
			expected: &consistentHashIR{
				policies: []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(false)},
			},
		},
		{
			name: "disable=false without other fields defaults to a source IP hash policy",
			in:   &kgateway.ConsistentHash{Disable: new(false)},
			expected: &consistentHashIR{
				policies: []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(false)},
			},
		},
		{
			name:     "disable produces a disabled IR without hash policies",
			in:       &kgateway.ConsistentHash{Disable: new(true)},
			expected: &consistentHashIR{disabled: true},
		},
		{
			name: "source IP only",
			in: &kgateway.ConsistentHash{
				SourceIP: &kgateway.ConsistentHashSourceIP{Terminal: new(true)},
			},
			expected: &consistentHashIR{
				policies: []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(true)},
			},
		},
		{
			name: "hash policies are ordered by type and deduplicated by key keeping the first occurrence",
			in: &kgateway.ConsistentHash{
				SourceIP: &kgateway.ConsistentHashSourceIP{},
				FilterState: []kgateway.ConsistentHashFilterState{
					{Key: "fs-1", Terminal: new(true)},
					{Key: "fs-1"},
					{Key: "fs-2"},
				},
				QueryParameters: []kgateway.ConsistentHashQueryParameter{
					{Name: "q1"},
					{Name: "q1", Terminal: new(true)},
				},
				Cookies: []kgateway.ConsistentHashCookie{
					{Name: "c1", Terminal: new(true)},
					{Name: "c2"},
					{Name: "c1"},
					{Name: "C1"},
				},
				Headers: []kgateway.ConsistentHashHeader{
					{HeaderName: "X-User", Terminal: new(true)},
					{HeaderName: "x-tenant"},
					{HeaderName: "x-user"},
					{HeaderName: "X-USER"},
				},
			},
			expected: &consistentHashIR{
				policies: []*envoyroutev3.RouteAction_HashPolicy{
					headerHashPolicy("X-User", true),
					headerHashPolicy("x-tenant", false),
					cookieHashPolicy("c1", true),
					cookieHashPolicy("c2", false),
					cookieHashPolicy("C1", false),
					queryParameterHashPolicy("q1", false),
					filterStateHashPolicy("fs-1", true),
					filterStateHashPolicy("fs-2", false),
					sourceIPHashPolicy(false),
				},
			},
		},
		{
			name: "header with regex rewrite",
			in: &kgateway.ConsistentHash{
				Headers: []kgateway.ConsistentHashHeader{
					{
						HeaderName: "x-tenant",
						RegexRewrite: &kgateway.ConsistentHashRegexRewrite{
							Pattern:      "^tenant-(.*)$",
							Substitution: `\1`,
						},
					},
				},
			},
			expected: &consistentHashIR{
				policies: []*envoyroutev3.RouteAction_HashPolicy{
					{
						PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
							Header: &envoyroutev3.RouteAction_HashPolicy_Header{
								HeaderName: "x-tenant",
								RegexRewrite: &envoy_type_matcher_v3.RegexMatchAndSubstitute{
									Pattern:      &envoy_type_matcher_v3.RegexMatcher{Regex: "^tenant-(.*)$"},
									Substitution: `\1`,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "cookies with ttl, path and attributes",
			in: &kgateway.ConsistentHash{
				Cookies: []kgateway.ConsistentHashCookie{
					{
						Name: "go-duration",
						TTL:  new("1h30m"),
						Path: new("/api"),
						Attributes: []kgateway.ConsistentHashCookieAttribute{
							{Name: "SameSite", Value: new("Strict")},
							{Name: "Secure"},
						},
					},
					{Name: "seconds", TTL: new("3600")},
					{Name: "session", TTL: new("0")},
				},
			},
			expected: &consistentHashIR{
				policies: []*envoyroutev3.RouteAction_HashPolicy{
					{
						PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
							Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{
								Name: "go-duration",
								Ttl:  &durationpb.Duration{Seconds: 5400},
								Path: "/api",
								Attributes: []*envoyroutev3.RouteAction_HashPolicy_CookieAttribute{
									{Name: "SameSite", Value: "Strict"},
									{Name: "Secure"},
								},
							},
						},
					},
					{
						PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
							Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{
								Name: "seconds",
								Ttl:  &durationpb.Duration{Seconds: 3600},
							},
						},
					},
					{
						PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
							Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{
								Name: "session",
								Ttl:  &durationpb.Duration{},
							},
						},
					},
				},
			},
		},
		{
			name: "invalid cookie ttl",
			in: &kgateway.ConsistentHash{
				Cookies: []kgateway.ConsistentHashCookie{{Name: "c", TTL: new("1 hour")}},
			},
			expectedErr: `consistentHash cookie "c": invalid ttl "1 hour"`,
		},
		{
			name: "negative cookie ttl",
			in: &kgateway.ConsistentHash{
				Cookies: []kgateway.ConsistentHashCookie{{Name: "c", TTL: new("-1s")}},
			},
			expectedErr: `consistentHash cookie "c": invalid ttl "-1s": must not be negative`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &trafficPolicySpecIr{}
			err := constructConsistentHash(kgateway.TrafficPolicySpec{ConsistentHash: tt.in}, out)
			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
				assert.Nil(t, out.consistentHash, "no IR should be produced on error")
				return
			}
			require.NoError(t, err)
			if tt.expected == nil {
				assert.Nil(t, out.consistentHash)
				return
			}
			require.NotNil(t, out.consistentHash)
			assert.Equal(t, tt.expected.disabled, out.consistentHash.disabled)
			assert.Empty(t, cmp.Diff(tt.expected.policies, out.consistentHash.policies, protocmp.Transform()))
			assert.NoError(t, out.consistentHash.Validate())
		})
	}
}

func TestConsistentHashIREquals(t *testing.T) {
	tests := []struct {
		name     string
		a        *consistentHashIR
		b        *consistentHashIR
		expected bool
	}{
		{
			name:     "both nil are equal",
			expected: true,
		},
		{
			name:     "nil vs non-nil are not equal",
			b:        &consistentHashIR{},
			expected: false,
		},
		{
			name:     "non-nil vs nil are not equal",
			a:        &consistentHashIR{},
			expected: false,
		},
		{
			name:     "same policies are equal",
			a:        &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{headerHashPolicy("x-a", true)}},
			b:        &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{headerHashPolicy("x-a", true)}},
			expected: true,
		},
		{
			name:     "different terminal is not equal",
			a:        &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{headerHashPolicy("x-a", true)}},
			b:        &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{headerHashPolicy("x-a", false)}},
			expected: false,
		},
		{
			name: "different order is not equal",
			a: &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHashPolicy("x-a", false), headerHashPolicy("x-b", false),
			}},
			b: &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHashPolicy("x-b", false), headerHashPolicy("x-a", false),
			}},
			expected: false,
		},
		{
			name:     "different hash policy types with the same name are not equal",
			a:        &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{headerHashPolicy("user", true)}},
			b:        &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{queryParameterHashPolicy("user", true)}},
			expected: false,
		},
		{
			name:     "different disabled is not equal",
			a:        &consistentHashIR{disabled: true},
			b:        &consistentHashIR{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.a.Equals(tt.b))
			assert.Equal(t, tt.expected, tt.b.Equals(tt.a), "Equals should be symmetric")
		})
	}
}

func TestConsistentHashIRValidate(t *testing.T) {
	invalidRegex := &consistentHashIR{
		policies: []*envoyroutev3.RouteAction_HashPolicy{
			{
				PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
					Header: &envoyroutev3.RouteAction_HashPolicy_Header{
						HeaderName: "x-tenant",
						RegexRewrite: &envoy_type_matcher_v3.RegexMatchAndSubstitute{
							Pattern:      &envoy_type_matcher_v3.RegexMatcher{Regex: "[invalid("},
							Substitution: "x",
						},
					},
				},
			},
		},
	}
	assert.ErrorContains(t, invalidRegex.Validate(), "invalid consistentHash header regexRewrite pattern")

	var nilIR *consistentHashIR
	assert.NoError(t, nilIR.Validate())
	assert.NoError(t, (&consistentHashIR{disabled: true}).Validate())
}

func TestApplyConsistentHash(t *testing.T) {
	existing := []*envoyroutev3.RouteAction_HashPolicy{headerHashPolicy("x-existing", false)}

	t.Run("nil IR leaves hash policies untouched", func(t *testing.T) {
		action := &envoyroutev3.RouteAction{HashPolicy: existing}
		applyConsistentHash(nil, action)
		assert.Empty(t, cmp.Diff(existing, action.GetHashPolicy(), protocmp.Transform()))
	})

	t.Run("disabled IR removes hash policies", func(t *testing.T) {
		action := &envoyroutev3.RouteAction{HashPolicy: existing}
		applyConsistentHash(&consistentHashIR{disabled: true}, action)
		assert.Nil(t, action.GetHashPolicy())
	})

	t.Run("IR sets hash policies", func(t *testing.T) {
		policies := []*envoyroutev3.RouteAction_HashPolicy{headerHashPolicy("x-a", true), sourceIPHashPolicy(false)}
		action := &envoyroutev3.RouteAction{HashPolicy: existing}
		applyConsistentHash(&consistentHashIR{policies: policies}, action)
		assert.Empty(t, cmp.Diff(policies, action.GetHashPolicy(), protocmp.Transform()))
	})
}

func TestMergeConsistentHash(t *testing.T) {
	p1Ref := &ir.AttachedPolicyRef{Group: "gateway.kgateway.dev", Kind: "TrafficPolicy", Namespace: "ns", Name: "p1"}
	p2Ref := &ir.AttachedPolicyRef{Group: "gateway.kgateway.dev", Kind: "TrafficPolicy", Namespace: "ns", Name: "p2"}

	higher := &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{
		headerHashPolicy("x-a", false),
		headerHashPolicy("X-B", false),
		queryParameterHashPolicy("q1", false),
	}}
	lower := &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{
		headerHashPolicy("x-b", true),
		headerHashPolicy("x-c", false),
		cookieHashPolicy("c1", false),
		filterStateHashPolicy("fs-1", false),
		sourceIPHashPolicy(true),
	}}

	tests := []struct {
		name            string
		strategy        policy.MergeStrategy
		p1              *consistentHashIR
		p2              *consistentHashIR
		expected        *consistentHashIR
		expectedOrigins []string
	}{
		{
			name:            "unset p1 takes p2",
			strategy:        policy.AugmentedShallowMerge,
			p1:              nil,
			p2:              lower,
			expected:        lower,
			expectedOrigins: []string{p2Ref.ID()},
		},
		{
			name:            "unset p2 leaves p1 untouched",
			strategy:        policy.AugmentedShallowMerge,
			p1:              higher,
			p2:              nil,
			expected:        higher,
			expectedOrigins: []string{p1Ref.ID()},
		},
		{
			name:     "augmented merge unions hash policies with p1 first and keeps p1's unset source IP",
			strategy: policy.AugmentedShallowMerge,
			p1:       higher,
			p2:       lower,
			expected: &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHashPolicy("x-a", false),
				headerHashPolicy("X-B", false),
				headerHashPolicy("x-c", false),
				cookieHashPolicy("c1", false),
				queryParameterHashPolicy("q1", false),
				filterStateHashPolicy("fs-1", false),
			}},
			expectedOrigins: []string{p1Ref.ID(), p2Ref.ID()},
		},
		{
			name:     "augmented deep merge unions hash policies with p1 first",
			strategy: policy.AugmentedDeepMerge,
			p1:       lower,
			p2:       higher,
			expected: &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHashPolicy("x-b", true),
				headerHashPolicy("x-c", false),
				headerHashPolicy("x-a", false),
				cookieHashPolicy("c1", false),
				queryParameterHashPolicy("q1", false),
				filterStateHashPolicy("fs-1", false),
				sourceIPHashPolicy(true),
			}},
			expectedOrigins: []string{p1Ref.ID(), p2Ref.ID()},
		},
		{
			name:     "overridable merge unions hash policies with p2 first and keeps p2's unset source IP",
			strategy: policy.OverridableShallowMerge,
			p1:       lower,
			p2:       higher,
			expected: &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHashPolicy("x-a", false),
				headerHashPolicy("X-B", false),
				headerHashPolicy("x-c", false),
				cookieHashPolicy("c1", false),
				queryParameterHashPolicy("q1", false),
				filterStateHashPolicy("fs-1", false),
			}},
			expectedOrigins: []string{p1Ref.ID(), p2Ref.ID()},
		},
		{
			name:            "augmented merge does not record p2 when it contributes no new hash policies",
			strategy:        policy.AugmentedShallowMerge,
			p1:              higher,
			p2:              &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{headerHashPolicy("X-A", true), sourceIPHashPolicy(false)}},
			expected:        higher,
			expectedOrigins: []string{p1Ref.ID()},
		},
		{
			name:            "overridable merge replaces p1's origin when p1 contributes no new hash policies",
			strategy:        policy.OverridableDeepMerge,
			p1:              &consistentHashIR{policies: []*envoyroutev3.RouteAction_HashPolicy{headerHashPolicy("X-A", true), sourceIPHashPolicy(false)}},
			p2:              higher,
			expected:        higher,
			expectedOrigins: []string{p2Ref.ID()},
		},
		{
			name:            "augmented merge keeps disabled p1",
			strategy:        policy.AugmentedShallowMerge,
			p1:              &consistentHashIR{disabled: true},
			p2:              lower,
			expected:        &consistentHashIR{disabled: true},
			expectedOrigins: []string{p1Ref.ID()},
		},
		{
			name:            "augmented merge ignores disabled p2",
			strategy:        policy.AugmentedShallowMerge,
			p1:              higher,
			p2:              &consistentHashIR{disabled: true},
			expected:        higher,
			expectedOrigins: []string{p1Ref.ID()},
		},
		{
			name:            "overridable merge takes disabled p2",
			strategy:        policy.OverridableShallowMerge,
			p1:              higher,
			p2:              &consistentHashIR{disabled: true},
			expected:        &consistentHashIR{disabled: true},
			expectedOrigins: []string{p2Ref.ID()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p1 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: tt.p1}}
			p2 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: tt.p2}}
			mergeOrigins := ir.MergeOrigins{}
			if tt.p1 != nil {
				mergeOrigins.SetOne("consistentHash", p1Ref, nil)
			}
			var p1Before, p2Before []*envoyroutev3.RouteAction_HashPolicy
			if tt.p1 != nil {
				p1Before = append(p1Before, tt.p1.policies...)
			}
			if tt.p2 != nil {
				p2Before = append(p2Before, tt.p2.policies...)
			}

			mergeConsistentHash(p1, p2, p2Ref, nil, policy.MergeOptions{Strategy: tt.strategy}, mergeOrigins, TrafficPolicyMergeOpts{})

			merged := p1.spec.consistentHash
			require.NotNil(t, merged)
			assert.Equal(t, tt.expected.disabled, merged.disabled)
			assert.Empty(t, cmp.Diff(tt.expected.policies, merged.policies, protocmp.Transform()))
			assert.ElementsMatch(t, tt.expectedOrigins, mergeOrigins.Get("consistentHash"))

			if tt.p1 != nil {
				assert.Empty(t, cmp.Diff(p1Before, tt.p1.policies, protocmp.Transform()), "p1 IR must not be mutated")
			}
			if tt.p2 != nil {
				assert.Empty(t, cmp.Diff(p2Before, tt.p2.policies, protocmp.Transform()), "p2 IR must not be mutated")
			}
		})
	}
}
