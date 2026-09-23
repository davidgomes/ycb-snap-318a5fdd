package trafficpolicy

import (
	"testing"
	"time"

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

func headerHP(name string, terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
			Header: &envoyroutev3.RouteAction_HashPolicy_Header{HeaderName: name},
		},
		Terminal: terminal,
	}
}

func cookieHP(name string, terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
			Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{Name: name},
		},
		Terminal: terminal,
	}
}

func queryParameterHP(name string, terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
			QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{Name: name},
		},
		Terminal: terminal,
	}
}

func filterStateHP(key string, terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
			FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: key},
		},
		Terminal: terminal,
	}
}

func assertHashPolicies(t *testing.T, expected, actual []*envoyroutev3.RouteAction_HashPolicy) {
	t.Helper()
	assert.Empty(t, cmp.Diff(expected, actual, protocmp.Transform()), "unexpected hash policies")
}

func TestConstructConsistentHash(t *testing.T) {
	tests := []struct {
		name     string
		spec     *kgateway.ConsistentHash
		expected []*envoyroutev3.RouteAction_HashPolicy
		disabled bool
		wantErr  string
	}{
		{
			name:     "empty defaults to source ip",
			spec:     &kgateway.ConsistentHash{},
			expected: []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(false)},
		},
		{
			name:     "disable produces no hash policies",
			spec:     &kgateway.ConsistentHash{Disable: new(true)},
			disabled: true,
		},
		{
			name: "disable false behaves like empty",
			spec: &kgateway.ConsistentHash{Disable: new(false)},
			expected: []*envoyroutev3.RouteAction_HashPolicy{
				sourceIPHashPolicy(false),
			},
		},
		{
			name: "entries are emitted in canonical type order",
			spec: &kgateway.ConsistentHash{
				SourceIP:        &kgateway.ConsistentHashSourceIP{Terminal: new(true)},
				FilterState:     []kgateway.ConsistentHashFilterState{{Key: "fs"}},
				QueryParameters: []kgateway.ConsistentHashQueryParameter{{Name: "q", Terminal: new(true)}},
				Cookies:         []kgateway.ConsistentHashCookie{{Name: "c"}},
				Headers:         []kgateway.ConsistentHashHeader{{HeaderName: "x-user"}},
			},
			expected: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("x-user", false),
				cookieHP("c", false),
				queryParameterHP("q", true),
				filterStateHP("fs", false),
				sourceIPHashPolicy(true),
			},
		},
		{
			name: "entries are deduplicated by key keeping the first occurrence",
			spec: &kgateway.ConsistentHash{
				Headers: []kgateway.ConsistentHashHeader{
					{HeaderName: "X-User", Terminal: new(true)},
					{HeaderName: "x-other"},
					{HeaderName: "x-user"},
				},
				Cookies: []kgateway.ConsistentHashCookie{
					{Name: "c", Terminal: new(true)},
					{Name: "c"},
					{Name: "C"},
				},
				QueryParameters: []kgateway.ConsistentHashQueryParameter{
					{Name: "q"},
					{Name: "q", Terminal: new(true)},
				},
				FilterState: []kgateway.ConsistentHashFilterState{
					{Key: "fs", Terminal: new(true)},
					{Key: "fs"},
				},
			},
			expected: []*envoyroutev3.RouteAction_HashPolicy{
				headerHP("X-User", true),
				headerHP("x-other", false),
				cookieHP("c", true),
				cookieHP("C", false),
				queryParameterHP("q", false),
				filterStateHP("fs", true),
			},
		},
		{
			name: "header regex rewrite",
			spec: &kgateway.ConsistentHash{
				Headers: []kgateway.ConsistentHashHeader{{
					HeaderName: "x-session",
					RegexRewrite: &kgateway.ConsistentHashRegexRewrite{
						Pattern:      "^([^.]+)\\..*$",
						Substitution: "\\1",
					},
				}},
			},
			expected: []*envoyroutev3.RouteAction_HashPolicy{{
				PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
					Header: &envoyroutev3.RouteAction_HashPolicy_Header{
						HeaderName: "x-session",
						RegexRewrite: &envoy_type_matcher_v3.RegexMatchAndSubstitute{
							Pattern:      &envoy_type_matcher_v3.RegexMatcher{Regex: "^([^.]+)\\..*$"},
							Substitution: "\\1",
						},
					},
				},
			}},
		},
		{
			name: "cookie with duration ttl, path and attributes",
			spec: &kgateway.ConsistentHash{
				Cookies: []kgateway.ConsistentHashCookie{{
					Name: "session",
					TTL:  new("1h30m"),
					Path: new("/"),
					Attributes: []kgateway.ConsistentHashCookieAttribute{
						{Name: "SameSite", Value: new("Strict")},
						{Name: "Secure"},
					},
					Terminal: new(true),
				}},
			},
			expected: []*envoyroutev3.RouteAction_HashPolicy{{
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
				Terminal: true,
			}},
		},
		{
			name: "cookie with integer seconds ttl",
			spec: &kgateway.ConsistentHash{
				Cookies: []kgateway.ConsistentHashCookie{{Name: "session", TTL: new("3600")}},
			},
			expected: []*envoyroutev3.RouteAction_HashPolicy{{
				PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
					Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{
						Name: "session",
						Ttl:  durationpb.New(time.Hour),
					},
				},
			}},
		},
		{
			name: "invalid cookie ttl",
			spec: &kgateway.ConsistentHash{
				Cookies: []kgateway.ConsistentHashCookie{{Name: "session", TTL: new("forever")}},
			},
			wantErr: `invalid consistentHash cookie "session" ttl: time: invalid duration "forever"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &trafficPolicySpecIr{}
			err := constructConsistentHash(kgateway.TrafficPolicySpec{ConsistentHash: tt.spec}, out)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.Nil(t, out.consistentHash, "IR should not be set on error")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, out.consistentHash)
			assert.Equal(t, tt.disabled, out.consistentHash.disable)
			assertHashPolicies(t, tt.expected, out.consistentHash.hashPolicies())
			require.NoError(t, out.consistentHash.Validate())
		})
	}

	t.Run("nil spec", func(t *testing.T) {
		out := &trafficPolicySpecIr{}
		require.NoError(t, constructConsistentHash(kgateway.TrafficPolicySpec{}, out))
		assert.Nil(t, out.consistentHash)
	})
}

func TestParseCookieTTL(t *testing.T) {
	tests := []struct {
		in       string
		expected time.Duration
		wantErr  bool
	}{
		{in: "0", expected: 0},
		{in: "3600", expected: time.Hour},
		{in: "1h30m", expected: 90 * time.Minute},
		{in: "500ms", expected: 500 * time.Millisecond},
		{in: "-5", wantErr: true},
		{in: "-1s", wantErr: true},
		{in: "abc", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			d, err := parseCookieTTL(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, d)
		})
	}
}

func TestConsistentHashValidate(t *testing.T) {
	c := &consistentHashIR{
		headers: []*envoyroutev3.RouteAction_HashPolicy{{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{
					HeaderName: "x-session",
					RegexRewrite: &envoy_type_matcher_v3.RegexMatchAndSubstitute{
						Pattern:      &envoy_type_matcher_v3.RegexMatcher{Regex: "(unclosed"},
						Substitution: "x",
					},
				},
			},
		}},
	}
	assert.ErrorContains(t, c.Validate(), "invalid consistentHash header regexRewrite pattern")
}

func TestConsistentHashIREquals(t *testing.T) {
	base := func() *consistentHashIR {
		return &consistentHashIR{
			headers:  []*envoyroutev3.RouteAction_HashPolicy{headerHP("x", false)},
			sourceIP: sourceIPHashPolicy(false),
		}
	}
	assert.True(t, (*consistentHashIR)(nil).Equals((*consistentHashIR)(nil)))
	assert.False(t, base().Equals((*consistentHashIR)(nil)))
	assert.True(t, base().Equals(base()))

	other := base()
	other.headers = []*envoyroutev3.RouteAction_HashPolicy{headerHP("x", true)}
	assert.False(t, base().Equals(other), "terminal differs")

	other = base()
	other.sourceIP = nil
	assert.False(t, base().Equals(other), "sourceIP differs")

	other = base()
	other.disable = true
	assert.False(t, base().Equals(other), "disable differs")

	other = base()
	other.filterState = []*envoyroutev3.RouteAction_HashPolicy{filterStateHP("other", false)}
	assert.False(t, base().Equals(other), "filterState differs")
}

func TestApplyConsistentHash(t *testing.T) {
	t.Run("sets hash policies", func(t *testing.T) {
		action := &envoyroutev3.RouteAction{}
		applyConsistentHash(&consistentHashIR{sourceIP: sourceIPHashPolicy(false)}, action)
		assertHashPolicies(t, []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(false)}, action.GetHashPolicy())
	})
	t.Run("disable clears hash policies", func(t *testing.T) {
		action := &envoyroutev3.RouteAction{HashPolicy: []*envoyroutev3.RouteAction_HashPolicy{headerHP("x", false)}}
		applyConsistentHash(&consistentHashIR{disable: true}, action)
		assert.Empty(t, action.GetHashPolicy())
	})
	t.Run("nil IR leaves action untouched", func(t *testing.T) {
		action := &envoyroutev3.RouteAction{HashPolicy: []*envoyroutev3.RouteAction_HashPolicy{headerHP("x", false)}}
		applyConsistentHash(nil, action)
		assertHashPolicies(t, []*envoyroutev3.RouteAction_HashPolicy{headerHP("x", false)}, action.GetHashPolicy())
	})
}

func TestMergeConsistentHash(t *testing.T) {
	p1Ref := &ir.AttachedPolicyRef{Group: "gateway.kgateway.dev", Kind: "TrafficPolicy", Namespace: "ns", Name: "p1"}
	p2Ref := &ir.AttachedPolicyRef{Group: "gateway.kgateway.dev", Kind: "TrafficPolicy", Namespace: "ns", Name: "p2"}

	higher := func() *consistentHashIR {
		return &consistentHashIR{
			headers:         []*envoyroutev3.RouteAction_HashPolicy{headerHP("X-User", true)},
			queryParameters: []*envoyroutev3.RouteAction_HashPolicy{queryParameterHP("q1", false)},
		}
	}
	lower := func() *consistentHashIR {
		return &consistentHashIR{
			headers:     []*envoyroutev3.RouteAction_HashPolicy{headerHP("x-user", false), headerHP("x-tenant", false)},
			cookies:     []*envoyroutev3.RouteAction_HashPolicy{cookieHP("c", false)},
			filterState: []*envoyroutev3.RouteAction_HashPolicy{filterStateHP("fs", false)},
			sourceIP:    sourceIPHashPolicy(true),
		}
	}
	expectedUnion := []*envoyroutev3.RouteAction_HashPolicy{
		headerHP("X-User", true),
		headerHP("x-tenant", false),
		cookieHP("c", false),
		queryParameterHP("q1", false),
		filterStateHP("fs", false),
	}

	tests := []struct {
		name            string
		strategy        policy.MergeStrategy
		p1              *consistentHashIR
		p2              *consistentHashIR
		expected        []*envoyroutev3.RouteAction_HashPolicy
		expectedDisable bool
		expectedOrigins []string
	}{
		{
			name:            "p1 unset takes p2",
			strategy:        policy.AugmentedShallowMerge,
			p2:              lower(),
			expected:        lower().hashPolicies(),
			expectedOrigins: []string{p2Ref.ID()},
		},
		{
			name:            "augmented merge unions with p1 first and keeps p1 sourceIp even when unset",
			strategy:        policy.AugmentedShallowMerge,
			p1:              higher(),
			p2:              lower(),
			expected:        expectedUnion,
			expectedOrigins: []string{p1Ref.ID(), p2Ref.ID()},
		},
		{
			name:            "augmented deep merge unions with p1 first",
			strategy:        policy.AugmentedDeepMerge,
			p1:              higher(),
			p2:              lower(),
			expected:        expectedUnion,
			expectedOrigins: []string{p1Ref.ID(), p2Ref.ID()},
		},
		{
			name:            "overridable merge unions with p2 first and keeps p2 sourceIp even when unset",
			strategy:        policy.OverridableShallowMerge,
			p1:              lower(),
			p2:              higher(),
			expected:        expectedUnion,
			expectedOrigins: []string{p1Ref.ID(), p2Ref.ID()},
		},
		{
			name:            "p2 contributing nothing new does not record origin",
			strategy:        policy.AugmentedShallowMerge,
			p1:              higher(),
			p2:              &consistentHashIR{headers: []*envoyroutev3.RouteAction_HashPolicy{headerHP("x-user", false)}},
			expected:        higher().hashPolicies(),
			expectedOrigins: []string{p1Ref.ID()},
		},
		{
			name:            "higher priority disable suppresses lower priority entries",
			strategy:        policy.AugmentedShallowMerge,
			p1:              &consistentHashIR{disable: true},
			p2:              lower(),
			expectedDisable: true,
			expectedOrigins: []string{p1Ref.ID()},
		},
		{
			name:            "lower priority disable is ignored",
			strategy:        policy.AugmentedShallowMerge,
			p1:              higher(),
			p2:              &consistentHashIR{disable: true},
			expected:        higher().hashPolicies(),
			expectedOrigins: []string{p1Ref.ID()},
		},
		{
			name:            "overridable merge with p2 disable suppresses p1 entries",
			strategy:        policy.OverridableShallowMerge,
			p1:              lower(),
			p2:              &consistentHashIR{disable: true},
			expectedDisable: true,
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
			var p1Before []*envoyroutev3.RouteAction_HashPolicy
			if tt.p1 != nil {
				p1Before = tt.p1.hashPolicies()
			}
			p2Before := tt.p2.hashPolicies()

			MergeTrafficPolicies(p1, p2, p2Ref, nil, policy.MergeOptions{Strategy: tt.strategy}, mergeOrigins, TrafficPolicyMergeOpts{})

			require.NotNil(t, p1.spec.consistentHash)
			assert.Equal(t, tt.expectedDisable, p1.spec.consistentHash.disable)
			assertHashPolicies(t, tt.expected, p1.spec.consistentHash.hashPolicies())
			assert.ElementsMatch(t, tt.expectedOrigins, mergeOrigins.Get("consistentHash"))

			// the source IRs must not be mutated by the merge
			if tt.p1 != nil {
				assertHashPolicies(t, p1Before, tt.p1.hashPolicies())
			}
			assertHashPolicies(t, p2Before, tt.p2.hashPolicies())
		})
	}
}
