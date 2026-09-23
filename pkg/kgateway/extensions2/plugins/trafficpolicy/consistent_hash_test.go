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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/policy"
)

func TestBuildConsistentHashIR(t *testing.T) {
	ttl := "1h30m"
	seconds := "3600"
	path := "/api"
	out := &trafficPolicySpecIr{}
	err := constructConsistentHash(kgateway.TrafficPolicySpec{
		ConsistentHash: &kgateway.ConsistentHash{
			Headers: []kgateway.ConsistentHashHeader{
				{
					HeaderName: "X-User",
					RegexRewrite: &kgateway.ConsistentHashRegexRewrite{
						Pattern:      "^user-(.*)$",
						Substitution: `\1`,
					},
					Terminal: ptr.To(true),
				},
				{HeaderName: "x-user"},
				{HeaderName: "X-Other"},
			},
			Cookies: []kgateway.ConsistentHashCookie{
				{
					Name: "session",
					TTL:  &ttl,
					Path: &path,
					Attributes: []kgateway.ConsistentHashCookieAttribute{
						{Name: "SameSite", Value: "Strict"},
						{Name: "Secure", Value: "true"},
					},
				},
				{Name: "session", TTL: &seconds},
				{Name: "alt", TTL: &seconds, Terminal: ptr.To(true)},
			},
			QueryParameters: []kgateway.ConsistentHashQueryParameter{
				{Name: "user"},
				{Name: "user", Terminal: ptr.To(true)},
				{Name: "region"},
			},
			FilterState: []kgateway.ConsistentHashFilterState{
				{Key: "envoy.router"},
				{Key: "envoy.router", Terminal: ptr.To(true)},
			},
			SourceIp: &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(true)},
		},
	}, out)
	require.NoError(t, err)

	policies := out.consistentHash.hashPolicies()
	expectedTTL := 90 * time.Minute
	expectedSeconds := 3600 * time.Second
	expected := []*envoyroutev3.RouteAction_HashPolicy{
		{
			Terminal: true,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{
					HeaderName: "X-User",
					RegexRewrite: &envoy_type_matcher_v3.RegexMatchAndSubstitute{
						Pattern:      &envoy_type_matcher_v3.RegexMatcher{Regex: "^user-(.*)$"},
						Substitution: `\1`,
					},
				},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{HeaderName: "X-Other"},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{
					Name: "session",
					Ttl:  durationpb.New(expectedTTL),
					Path: path,
					Attributes: []*envoyroutev3.RouteAction_HashPolicy_CookieAttribute{
						{Name: "SameSite", Value: "Strict"},
						{Name: "Secure", Value: "true"},
					},
				},
			},
		},
		{
			Terminal: true,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{
					Name: "alt",
					Ttl:  durationpb.New(expectedSeconds),
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
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: "envoy.router"},
			},
		},
		{
			Terminal: true,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_{
				ConnectionProperties: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties{SourceIp: true},
			},
		},
	}
	require.Len(t, policies, len(expected))
	for i := range expected {
		assert.Truef(t, proto.Equal(expected[i], policies[i]), "policy %d\nexpected: %v\nactual: %v", i, expected[i], policies[i])
	}
}

func TestConsistentHashDefaultsAndDisable(t *testing.T) {
	empty := &trafficPolicySpecIr{}
	require.NoError(t, constructConsistentHash(kgateway.TrafficPolicySpec{
		ConsistentHash: &kgateway.ConsistentHash{},
	}, empty))
	policies := empty.consistentHash.hashPolicies()
	require.Len(t, policies, 1)
	assert.False(t, policies[0].GetTerminal())
	assert.True(t, policies[0].GetConnectionProperties().GetSourceIp())

	disabled := &trafficPolicySpecIr{}
	require.NoError(t, constructConsistentHash(kgateway.TrafficPolicySpec{
		ConsistentHash: &kgateway.ConsistentHash{Disable: ptr.To(true)},
	}, disabled))
	assert.Nil(t, disabled.consistentHash.hashPolicies())

	action := &envoyroutev3.RouteAction{
		HashPolicy: []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(true)},
	}
	applyConsistentHash(disabled.consistentHash, action)
	assert.Nil(t, action.GetHashPolicy())

	_, err := buildConsistentHashIR(&kgateway.ConsistentHash{
		Disable: ptr.To(true),
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "X-User"}},
	})
	require.Error(t, err)

	_, err = buildConsistentHashIR(&kgateway.ConsistentHash{
		Cookies: []kgateway.ConsistentHashCookie{{Name: "session", TTL: ptr.To("not-a-duration")}},
	})
	require.Error(t, err)
}

func TestMergeConsistentHash(t *testing.T) {
	gk := schema.GroupKind{Group: "gateway.kgateway.dev", Kind: "TrafficPolicy"}
	high := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: &consistentHashIR{
		headers: []consistentHashHeaderIR{{headerName: "X-User"}, {headerName: "X-A"}},
		cookies: []consistentHashCookieIR{{name: "a"}},
	}}}
	low := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: &consistentHashIR{
		headers:         []consistentHashHeaderIR{{headerName: "x-a"}, {headerName: "X-B"}},
		cookies:         []consistentHashCookieIR{{name: "b"}},
		queryParameters: []consistentHashQueryParameterIR{{name: "q"}},
		filterState:     []consistentHashFilterStateIR{{key: "fs"}},
		sourceIP:        &consistentHashSourceIPIR{terminal: true},
	}}}

	merged := policy.MergePolicies([]ir.PolicyAtt{
		policyAtt(gk, "high", high),
		policyAtt(gk, "low", low),
	}, mergeTrafficPolicies, "")
	got := merged.PolicyIr.(*TrafficPolicy).spec.consistentHash
	require.NotNil(t, got)
	assert.Equal(t, []string{"X-User", "X-A", "X-B"}, headerNames(got))
	assert.Equal(t, []string{"a", "b"}, cookieNames(got))
	require.Len(t, got.queryParameters, 1)
	assert.Equal(t, "q", got.queryParameters[0].name)
	require.Len(t, got.filterState, 1)
	assert.Equal(t, "fs", got.filterState[0].key)
	assert.Nil(t, got.sourceIP, "unset sourceIp on the higher-priority policy is preserved")

	origins := merged.MergeOrigins[consistentHashMergeField].UnsortedList()
	assert.ElementsMatch(t, []string{
		"gateway.kgateway.dev/TrafficPolicy/default/high",
		"gateway.kgateway.dev/TrafficPolicy/default/low",
	}, origins)

	// Canonical emission order is by type, not by the order policies were merged.
	built := got.hashPolicies()
	require.Len(t, built, 7)
	assert.NotNil(t, built[0].GetHeader())
	assert.NotNil(t, built[1].GetHeader())
	assert.NotNil(t, built[2].GetHeader())
	assert.NotNil(t, built[3].GetCookie())
	assert.NotNil(t, built[4].GetCookie())
	assert.NotNil(t, built[5].GetQueryParameter())
	assert.NotNil(t, built[6].GetFilterState())

	disabled := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: &consistentHashIR{disable: true}}}
	suppressed := policy.MergePolicies([]ir.PolicyAtt{
		policyAtt(gk, "disabled", disabled),
		policyAtt(gk, "low", low),
	}, mergeTrafficPolicies, "")
	suppressedHash := suppressed.PolicyIr.(*TrafficPolicy).spec.consistentHash
	require.NotNil(t, suppressedHash)
	assert.True(t, suppressedHash.disable)
	assert.Nil(t, suppressedHash.hashPolicies())
	assert.Equal(t, []string{"gateway.kgateway.dev/TrafficPolicy/default/disabled"}, suppressed.MergeOrigins[consistentHashMergeField].UnsortedList())
}

func TestMergeConsistentHashPreferParentSourceIP(t *testing.T) {
	gk := schema.GroupKind{Group: "gateway.kgateway.dev", Kind: "TrafficPolicy"}
	child := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: &consistentHashIR{
		headers:  []consistentHashHeaderIR{{headerName: "X-Child"}},
		sourceIP: &consistentHashSourceIPIR{terminal: true},
	}}}
	parent := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: &consistentHashIR{
		headers: []consistentHashHeaderIR{{headerName: "X-Parent"}},
	}}}
	// Overridable strategies treat p2 as higher priority. Parent sourceIp stays unset.
	merged := policy.MergePolicies([]ir.PolicyAtt{
		{
			GroupKind:               gk,
			PolicyIr:                child,
			PolicyRef:               &ir.AttachedPolicyRef{Group: gk.Group, Kind: gk.Kind, Namespace: "default", Name: "child"},
			InheritedPolicyPriority: "DeepMergePreferParent",
			HierarchicalPriority:    0,
		},
		{
			GroupKind:               gk,
			PolicyIr:                parent,
			PolicyRef:               &ir.AttachedPolicyRef{Group: gk.Group, Kind: gk.Kind, Namespace: "default", Name: "parent"},
			InheritedPolicyPriority: "DeepMergePreferParent",
			HierarchicalPriority:    -1,
		},
	}, mergeTrafficPolicies, "")
	got := merged.PolicyIr.(*TrafficPolicy).spec.consistentHash
	require.NotNil(t, got)
	assert.Equal(t, []string{"X-Parent", "X-Child"}, headerNames(got))
	assert.Nil(t, got.sourceIP)
}

func policyAtt(gk schema.GroupKind, name string, pol *TrafficPolicy) ir.PolicyAtt {
	return ir.PolicyAtt{
		GroupKind: gk,
		PolicyIr:  pol,
		PolicyRef: &ir.AttachedPolicyRef{Group: gk.Group, Kind: gk.Kind, Namespace: "default", Name: name},
	}
}

func headerNames(c *consistentHashIR) []string {
	names := make([]string, 0, len(c.headers))
	for _, header := range c.headers {
		names = append(names, header.headerName)
	}
	return names
}

func cookieNames(c *consistentHashIR) []string {
	names := make([]string, 0, len(c.cookies))
	for _, cookie := range c.cookies {
		names = append(names, cookie.name)
	}
	return names
}
