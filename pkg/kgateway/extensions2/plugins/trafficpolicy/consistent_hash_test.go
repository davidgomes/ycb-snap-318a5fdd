package trafficpolicy

import (
	"testing"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoy_type_matcher_v3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/policy"
)

func headerHP(name string, terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
			Header: &envoyroutev3.RouteAction_HashPolicy_Header{HeaderName: name},
		},
		Terminal: terminal,
	}
}

func cookieHP(name string) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
			Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{Name: name},
		},
	}
}

func queryHP(name string) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
			QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{Name: name},
		},
	}
}

func filterStateHP(key string) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
			FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: key},
		},
	}
}

func assertHashPolicies(t *testing.T, expected, actual []*envoyroutev3.RouteAction_HashPolicy) {
	t.Helper()
	require.Len(t, actual, len(expected))
	for i := range expected {
		assert.True(t, proto.Equal(expected[i], actual[i]), "hash policy %d: expected %v, got %v", i, expected[i], actual[i])
	}
}

func TestConstructConsistentHash(t *testing.T) {
	tests := []struct {
		name     string
		spec     *kgateway.ConsistentHash
		expected *consistentHashIR
		wantErr  string
	}{
		{
			name:     "nil spec",
			spec:     nil,
			expected: nil,
		},
		{
			name: "empty spec defaults to sourceIp",
			spec: &kgateway.ConsistentHash{},
			expected: &consistentHashIR{
				hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(false)},
			},
		},
		{
			name:     "disable",
			spec:     &kgateway.ConsistentHash{Disable: new(true)},
			expected: &consistentHashIR{disable: true},
		},
		{
			name: "canonical order and dedupe",
			spec: &kgateway.ConsistentHash{
				SourceIP:        &kgateway.ConsistentHashSourceIP{Terminal: new(true)},
				FilterState:     []kgateway.ConsistentHashFilterState{{Key: "fs"}, {Key: "fs", Terminal: new(true)}},
				QueryParameters: []kgateway.ConsistentHashQueryParameter{{Name: "q"}, {Name: "q", Terminal: new(true)}},
				Cookies: []kgateway.ConsistentHashCookie{
					{
						Name:       "session",
						TTL:        new("1h30m"),
						Path:       new("/"),
						Attributes: []kgateway.ConsistentHashCookieAttribute{{Name: "SameSite", Value: "Strict"}, {Name: "Secure"}},
					},
					{Name: "other", TTL: new("3600")},
					{Name: "session"},
				},
				Headers: []kgateway.ConsistentHashHeader{
					{HeaderName: "X-User", Terminal: new(true)},
					{HeaderName: "x-user"},
					{
						HeaderName:   "x-tenant",
						RegexRewrite: &kgateway.ConsistentHashRegexRewrite{Pattern: "^(.*)-.*$", Substitution: "\\1"},
					},
				},
			},
			expected: &consistentHashIR{
				hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
					headerHP("X-User", true),
					{
						PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
							Header: &envoyroutev3.RouteAction_HashPolicy_Header{
								HeaderName: "x-tenant",
								RegexRewrite: &envoy_type_matcher_v3.RegexMatchAndSubstitute{
									Pattern:      &envoy_type_matcher_v3.RegexMatcher{Regex: "^(.*)-.*$"},
									Substitution: "\\1",
								},
							},
						},
					},
					{
						PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
							Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{
								Name: "session",
								Ttl:  durationpb.New(90 * time.Minute),
								Path: "/",
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
								Name: "other",
								Ttl:  durationpb.New(time.Hour),
							},
						},
					},
					queryHP("q"),
					filterStateHP("fs"),
					sourceIPHashPolicy(true),
				},
			},
		},
		{
			name: "no sourceIp default when other sources are set",
			spec: &kgateway.ConsistentHash{
				QueryParameters: []kgateway.ConsistentHashQueryParameter{{Name: "q"}},
			},
			expected: &consistentHashIR{
				hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{queryHP("q")},
			},
		},
		{
			name: "invalid ttl",
			spec: &kgateway.ConsistentHash{
				Cookies: []kgateway.ConsistentHashCookie{{Name: "c", TTL: new("bogus")}},
			},
			wantErr: `invalid consistentHash cookie "c" ttl`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &trafficPolicySpecIr{}
			err := constructConsistentHash(kgateway.TrafficPolicySpec{ConsistentHash: tt.spec}, out)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, out.consistentHash)
				return
			}
			require.NoError(t, err)
			if tt.expected == nil {
				assert.Nil(t, out.consistentHash)
				return
			}
			require.NotNil(t, out.consistentHash)
			assert.Equal(t, tt.expected.disable, out.consistentHash.disable)
			assertHashPolicies(t, tt.expected.hashPolicies, out.consistentHash.hashPolicies)
			assert.True(t, tt.expected.Equals(out.consistentHash))
			assert.NoError(t, out.consistentHash.Validate())
		})
	}
}

func TestConsistentHashValidate(t *testing.T) {
	out := &trafficPolicySpecIr{}
	err := constructConsistentHash(kgateway.TrafficPolicySpec{ConsistentHash: &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{
			HeaderName:   "x",
			RegexRewrite: &kgateway.ConsistentHashRegexRewrite{Pattern: "(", Substitution: ""},
		}},
	}}, out)
	require.NoError(t, err)
	assert.ErrorContains(t, out.consistentHash.Validate(), "invalid consistentHash header regexRewrite pattern")
}

func TestConsistentHashIREquals(t *testing.T) {
	a := &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{headerHP("a", false)}}
	assert.True(t, (*consistentHashIR)(nil).Equals((*consistentHashIR)(nil)))
	assert.False(t, a.Equals((*consistentHashIR)(nil)))
	assert.True(t, a.Equals(&consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{headerHP("a", false)}}))
	assert.False(t, a.Equals(&consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{headerHP("a", true)}}))
	assert.False(t, a.Equals(&consistentHashIR{disable: true, hashPolicies: a.hashPolicies}))
}

func TestMergeConsistentHash(t *testing.T) {
	p2Ref := &ir.AttachedPolicyRef{Group: "gateway.kgateway.dev", Kind: "TrafficPolicy", Namespace: "default", Name: "p2"}

	tests := []struct {
		name            string
		strategy        policy.MergeStrategy
		p1              *consistentHashIR
		p2              *consistentHashIR
		expected        *consistentHashIR
		expectOriginSet bool
	}{
		{
			name:     "union with higher priority first, sorted, sourceIp from higher only",
			strategy: policy.AugmentedShallowMerge,
			p1: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("X-A", true), queryHP("q1"),
			}},
			p2: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("x-a", false), headerHP("x-b", false), cookieHP("c"), queryHP("q1"), filterStateHP("fs"), sourceIPHashPolicy(false),
			}},
			expected: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("X-A", true), headerHP("x-b", false), cookieHP("c"), queryHP("q1"), filterStateHP("fs"),
			}},
			expectOriginSet: true,
		},
		{
			name:     "overridable strategy prefers p2",
			strategy: policy.OverridableDeepMerge,
			p1: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("x-a", false), sourceIPHashPolicy(false),
			}},
			p2: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("x-a", true), cookieHP("affinity"),
			}},
			expected: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("x-a", true), cookieHP("affinity"),
			}},
			expectOriginSet: true,
		},
		{
			name:            "p1 unset takes p2",
			strategy:        policy.AugmentedDeepMerge,
			p1:              nil,
			p2:              &consistentHashIR{disable: true},
			expected:        &consistentHashIR{disable: true},
			expectOriginSet: true,
		},
		{
			name:     "higher priority disable suppresses lower",
			strategy: policy.AugmentedDeepMerge,
			p1:       &consistentHashIR{disable: true},
			p2: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("x-a", false),
			}},
			expected:        &consistentHashIR{disable: true},
			expectOriginSet: false,
		},
		{
			name:     "lower priority disable is ignored",
			strategy: policy.AugmentedDeepMerge,
			p1: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("x-a", false),
			}},
			p2: &consistentHashIR{disable: true},
			expected: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("x-a", false),
			}},
			expectOriginSet: false,
		},
		{
			name:     "p2 unset is a no-op",
			strategy: policy.AugmentedShallowMerge,
			p1: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("x-a", false),
			}},
			p2: nil,
			expected: &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("x-a", false),
			}},
			expectOriginSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p1 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: tt.p1}}
			p2 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: tt.p2}}
			var p2Before *consistentHashIR
			if tt.p2 != nil {
				p2Before = &consistentHashIR{disable: tt.p2.disable, hashPolicies: append([]*envoyroutev3.RouteAction_HashPolicy{}, tt.p2.hashPolicies...)}
			}
			origins := ir.MergeOrigins{}

			MergeTrafficPolicies(p1, p2, p2Ref, nil, policy.MergeOptions{Strategy: tt.strategy}, origins, TrafficPolicyMergeOpts{})

			require.NotNil(t, p1.spec.consistentHash)
			assert.Equal(t, tt.expected.disable, p1.spec.consistentHash.disable)
			assertHashPolicies(t, tt.expected.hashPolicies, p1.spec.consistentHash.hashPolicies)
			if tt.expectOriginSet {
				assert.Equal(t, []string{p2Ref.ID()}, origins.Get("consistentHash"))
			} else {
				assert.Empty(t, origins.Get("consistentHash"))
			}
			assert.True(t, p2Before.Equals(p2.spec.consistentHash), "p2 must not be mutated")
		})
	}
}

func TestApplyConsistentHash(t *testing.T) {
	action := &envoyroutev3.RouteAction{HashPolicy: []*envoyroutev3.RouteAction_HashPolicy{headerHP("inherited", false)}}
	applyConsistentHash(&consistentHashIR{disable: true}, action)
	assert.Empty(t, action.GetHashPolicy())

	ch := &consistentHashIR{hashPolicies: []*envoyroutev3.RouteAction_HashPolicy{headerHP("a", false)}}
	applyConsistentHash(ch, action)
	assertHashPolicies(t, ch.hashPolicies, action.GetHashPolicy())

	applyConsistentHash(nil, action)
	assertHashPolicies(t, ch.hashPolicies, action.GetHashPolicy())
}
