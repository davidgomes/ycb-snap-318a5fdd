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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/policy"
)

func TestConsistentHashEmptyDefaultsToSourceIP(t *testing.T) {
	action := translateConsistentHash(t, &kgateway.ConsistentHash{})
	require.Len(t, action.GetHashPolicy(), 1)
	assert.Empty(t, cmp.Diff(newSourceIPHashPolicy(false), action.GetHashPolicy()[0], protocmp.Transform()))
}

func TestConsistentHashDisableSuppressesHashPolicies(t *testing.T) {
	action := &envoyroutev3.RouteAction{
		HashPolicy: []*envoyroutev3.RouteAction_HashPolicy{newSourceIPHashPolicy(true)},
	}
	var spec trafficPolicySpecIr
	require.NoError(t, constructConsistentHash(kgateway.TrafficPolicySpec{
		ConsistentHash: &kgateway.ConsistentHash{Disable: ptr.To(true)},
	}, &spec))
	applyConsistentHash(spec.consistentHash, action)
	assert.Nil(t, action.HashPolicy)
}

func TestConsistentHashCanonicalOrderDedupAndCookieTTL(t *testing.T) {
	action := translateConsistentHash(t, &kgateway.ConsistentHash{
		SourceIP: &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(true)},
		FilterState: []kgateway.ConsistentHashFilterState{
			{Key: "role", Terminal: ptr.To(true)},
			{Key: "role", Terminal: ptr.To(false)},
		},
		QueryParameters: []kgateway.ConsistentHashQueryParameter{
			{Name: "user"},
			{Name: "user", Terminal: ptr.To(true)},
			{Name: "region"},
		},
		Cookies: []kgateway.ConsistentHashCookie{
			{
				Name: "session",
				TTL:  ptr.To("1h30m"),
				Path: ptr.To("/app"),
				Attributes: []kgateway.ConsistentHashCookieAttribute{
					{Name: "SameSite", Value: "Lax"},
					{Name: "Secure", Value: "true"},
				},
				Terminal: ptr.To(true),
			},
			{Name: "session", TTL: ptr.To("10s")},
			{Name: "alt", TTL: ptr.To("3600")},
		},
		Headers: []kgateway.ConsistentHashHeader{
			{
				HeaderName: "X-User",
				RegexRewrite: &kgateway.ConsistentHashRegexRewrite{
					Pattern:      "^user-(.*)$",
					Substitution: `\\1`,
				},
				Terminal: ptr.To(true),
			},
			{HeaderName: "x-user"},
			{HeaderName: "X-Tenant"},
		},
	})

	expected := []*envoyroutev3.RouteAction_HashPolicy{
		{
			Terminal: true,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{
					HeaderName: "X-User",
					RegexRewrite: &envoy_type_matcher_v3.RegexMatchAndSubstitute{
						Pattern:      &envoy_type_matcher_v3.RegexMatcher{Regex: "^user-(.*)$"},
						Substitution: `\\1`,
					},
				},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{HeaderName: "X-Tenant"},
			},
		},
		{
			Terminal: true,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{
					Name: "session",
					Ttl:  durationpb.New(90 * time.Minute),
					Path: "/app",
					Attributes: []*envoyroutev3.RouteAction_HashPolicy_CookieAttribute{
						{Name: "SameSite", Value: "Lax"},
						{Name: "Secure", Value: "true"},
					},
				},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{
					Name: "alt",
					Ttl:  durationpb.New(time.Hour),
				},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
				QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{Name: "user"},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
				QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{Name: "region"},
			},
		},
		{
			Terminal: true,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: "role"},
			},
		},
		newSourceIPHashPolicy(true),
	}
	assert.Empty(t, cmp.Diff(expected, action.GetHashPolicy(), protocmp.Transform()))
}

func TestParseCookieTTL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "go duration", raw: "1h30m", want: 90 * time.Minute},
		{name: "integer seconds", raw: "3600", want: time.Hour},
		{name: "zero seconds", raw: "0", want: 0},
		{name: "zero duration", raw: "0s", want: 0},
		{name: "negative", raw: "-1s", wantErr: true},
		{name: "invalid", raw: "forever", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCookieTTL(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConstructConsistentHashInvalidRegex(t *testing.T) {
	var spec trafficPolicySpecIr
	err := constructConsistentHash(kgateway.TrafficPolicySpec{
		ConsistentHash: &kgateway.ConsistentHash{
			Headers: []kgateway.ConsistentHashHeader{{
				HeaderName:   "x-user",
				RegexRewrite: &kgateway.ConsistentHashRegexRewrite{Pattern: "(", Substitution: "x"},
			}},
		},
	}, &spec)
	require.Error(t, err)
	assert.Nil(t, spec.consistentHash)
}

func TestMergeConsistentHashUnionsHigherPriorityFirst(t *testing.T) {
	higher := mustConsistentHashIR(t, &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "X-User", Terminal: ptr.To(true)}},
		Cookies: []kgateway.ConsistentHashCookie{{Name: "session"}},
		SourceIP: &kgateway.ConsistentHashSourceIP{
			Terminal: ptr.To(true),
		},
	})
	lower := mustConsistentHashIR(t, &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{
			{HeaderName: "x-user"},
			{HeaderName: "X-Tenant"},
		},
		Cookies:         []kgateway.ConsistentHashCookie{{Name: "session"}, {Name: "alt"}},
		QueryParameters: []kgateway.ConsistentHashQueryParameter{{Name: "region", Terminal: ptr.To(true)}},
		FilterState:     []kgateway.ConsistentHashFilterState{{Key: "role"}},
		SourceIP:        &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(false)},
	})

	gk := schema.GroupKind{Group: "gateway.kgateway.dev", Kind: "TrafficPolicy"}
	highRef := &ir.AttachedPolicyRef{Group: gk.Group, Kind: gk.Kind, Namespace: "ns", Name: "high"}
	lowRef := &ir.AttachedPolicyRef{Group: gk.Group, Kind: gk.Kind, Namespace: "ns", Name: "low"}
	merged := policy.MergePolicies([]ir.PolicyAtt{
		{GroupKind: gk, PolicyRef: highRef, PolicyIr: &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: higher}}},
		{GroupKind: gk, PolicyRef: lowRef, PolicyIr: &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: lower}}},
	}, mergeTrafficPolicies, "")

	mergedPolicy := merged.PolicyIr.(*TrafficPolicy)
	action := &envoyroutev3.RouteAction{}
	applyConsistentHash(mergedPolicy.spec.consistentHash, action)

	expected := []*envoyroutev3.RouteAction_HashPolicy{
		{
			Terminal: true,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{HeaderName: "X-User"},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{HeaderName: "X-Tenant"},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{Name: "session"},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{Name: "alt"},
			},
		},
		{
			Terminal: true,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
				QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{Name: "region"},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: "role"},
			},
		},
		newSourceIPHashPolicy(true),
	}
	assert.Empty(t, cmp.Diff(expected, action.GetHashPolicy(), protocmp.Transform()))
	assert.ElementsMatch(t, []string{highRef.ID(), lowRef.ID()}, merged.MergeOrigins.Get(consistentHashMergeField))
}

func TestMergeConsistentHashSourceIPKeepsHigherPriorityUnset(t *testing.T) {
	higher := mustConsistentHashIR(t, &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "x-user"}},
	})
	lower := mustConsistentHashIR(t, &kgateway.ConsistentHash{
		SourceIP: &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(true)},
		Headers:  []kgateway.ConsistentHashHeader{{HeaderName: "x-tenant"}},
	})

	p1 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: higher}}
	mergeConsistentHash(p1, &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: lower}}, nil, nil, policy.MergeOptions{
		Strategy: policy.AugmentedShallowMerge,
	}, ir.MergeOrigins{}, TrafficPolicyMergeOpts{})

	require.NotNil(t, p1.spec.consistentHash)
	assert.Nil(t, p1.spec.consistentHash.sourceIp, "unset sourceIp on the higher-priority policy is retained")

	action := &envoyroutev3.RouteAction{}
	applyConsistentHash(p1.spec.consistentHash, action)
	require.Len(t, action.GetHashPolicy(), 2)
	assert.Equal(t, "x-user", action.GetHashPolicy()[0].GetHeader().GetHeaderName())
	assert.Equal(t, "x-tenant", action.GetHashPolicy()[1].GetHeader().GetHeaderName())
}

func TestMergeConsistentHashEmptyHigherPriorityDoesNotTakeLowerSourceIP(t *testing.T) {
	higher := mustConsistentHashIR(t, &kgateway.ConsistentHash{})
	lower := mustConsistentHashIR(t, &kgateway.ConsistentHash{
		SourceIP: &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(true)},
	})
	p1 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: higher}}
	mergeConsistentHash(p1, &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: lower}}, nil, nil, policy.MergeOptions{
		Strategy: policy.AugmentedDeepMerge,
	}, ir.MergeOrigins{}, TrafficPolicyMergeOpts{})

	action := &envoyroutev3.RouteAction{}
	applyConsistentHash(p1.spec.consistentHash, action)
	require.Len(t, action.GetHashPolicy(), 1)
	assert.Empty(t, cmp.Diff(newSourceIPHashPolicy(false), action.GetHashPolicy()[0], protocmp.Transform()))
}

func TestMergeConsistentHashDisableSuppressesInherited(t *testing.T) {
	gk := schema.GroupKind{Group: "gateway.kgateway.dev", Kind: "TrafficPolicy"}
	child := mustConsistentHashIR(t, &kgateway.ConsistentHash{Disable: ptr.To(true)})
	parent := mustConsistentHashIR(t, &kgateway.ConsistentHash{
		Headers:  []kgateway.ConsistentHashHeader{{HeaderName: "x-user"}},
		SourceIP: &kgateway.ConsistentHashSourceIP{},
	})
	merged := policy.MergePolicies([]ir.PolicyAtt{
		{
			GroupKind:            gk,
			HierarchicalPriority: 0,
			PolicyRef:            &ir.AttachedPolicyRef{Group: gk.Group, Kind: gk.Kind, Namespace: "ns", Name: "child"},
			PolicyIr:             &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: child}},
		},
		{
			GroupKind:            gk,
			HierarchicalPriority: -1,
			PolicyRef:            &ir.AttachedPolicyRef{Group: gk.Group, Kind: gk.Kind, Namespace: "ns", Name: "parent"},
			PolicyIr:             &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: parent}},
		},
	}, mergeTrafficPolicies, "")

	mergedPolicy := merged.PolicyIr.(*TrafficPolicy)
	require.NotNil(t, mergedPolicy.spec.consistentHash)
	assert.True(t, mergedPolicy.spec.consistentHash.disabled)
	action := &envoyroutev3.RouteAction{}
	applyConsistentHash(mergedPolicy.spec.consistentHash, action)
	assert.Empty(t, action.GetHashPolicy())
	assert.ElementsMatch(t, []string{"gateway.kgateway.dev/TrafficPolicy/ns/child"}, merged.MergeOrigins.Get(consistentHashMergeField))
}

func TestMergeConsistentHashLowerDisableDoesNotSuppressHigher(t *testing.T) {
	higher := mustConsistentHashIR(t, &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "x-user"}},
	})
	lower := mustConsistentHashIR(t, &kgateway.ConsistentHash{Disable: ptr.To(true)})
	p1 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: higher}}
	mergeConsistentHash(p1, &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: lower}}, nil, nil, policy.MergeOptions{
		Strategy: policy.AugmentedShallowMerge,
	}, ir.MergeOrigins{}, TrafficPolicyMergeOpts{})

	action := &envoyroutev3.RouteAction{}
	applyConsistentHash(p1.spec.consistentHash, action)
	require.Len(t, action.GetHashPolicy(), 1)
	assert.Equal(t, "x-user", action.GetHashPolicy()[0].GetHeader().GetHeaderName())
}

func TestConsistentHashIREquals(t *testing.T) {
	left := mustConsistentHashIR(t, &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "x-user"}},
	})
	right := mustConsistentHashIR(t, &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "x-user"}},
	})
	different := mustConsistentHashIR(t, &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "x-other"}},
	})
	assert.True(t, left.Equals(right))
	require.NoError(t, left.Validate())
	assert.True(t, (*consistentHashIR)(nil).Equals((*consistentHashIR)(nil)))
	assert.False(t, left.Equals(different))
	assert.False(t, left.Equals(nil))
	assert.False(t, (*consistentHashIR)(nil).Equals(left))
}

func translateConsistentHash(t *testing.T, spec *kgateway.ConsistentHash) *envoyroutev3.RouteAction {
	t.Helper()
	var out trafficPolicySpecIr
	require.NoError(t, constructConsistentHash(kgateway.TrafficPolicySpec{ConsistentHash: spec}, &out))
	action := &envoyroutev3.RouteAction{}
	applyConsistentHash(out.consistentHash, action)
	return action
}

func mustConsistentHashIR(t *testing.T, spec *kgateway.ConsistentHash) *consistentHashIR {
	t.Helper()
	built, err := buildConsistentHashIR(spec)
	require.NoError(t, err)
	return built
}
