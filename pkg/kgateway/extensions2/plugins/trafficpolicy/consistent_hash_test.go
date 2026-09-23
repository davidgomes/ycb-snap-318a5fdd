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

func TestConsistentHashEmptyDefaultsToSourceIP(t *testing.T) {
	spec := buildConsistentHash(t, &kgateway.ConsistentHash{})
	action := &envoyroutev3.RouteAction{}
	applyConsistentHash(spec, action)

	require.Len(t, action.GetHashPolicy(), 1)
	assert.False(t, action.GetHashPolicy()[0].GetTerminal())
	assert.True(t, action.GetHashPolicy()[0].GetConnectionProperties().GetSourceIp())
}

func TestConsistentHashDisableProducesNoPolicies(t *testing.T) {
	spec := buildConsistentHash(t, &kgateway.ConsistentHash{
		Disable: ptr.To(true),
	})
	action := &envoyroutev3.RouteAction{
		HashPolicy: []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(true)},
	}
	applyConsistentHash(spec, action)
	assert.Empty(t, action.GetHashPolicy())
}

func TestConsistentHashCanonicalOrderDedupeAndRewrite(t *testing.T) {
	spec := buildConsistentHash(t, &kgateway.ConsistentHash{
		SourceIp: &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(true)},
		FilterState: []kgateway.ConsistentHashFilterState{{
			Key:      "envoy.lb",
			Terminal: ptr.To(true),
		}, {
			Key: "envoy.lb",
		}},
		QueryParameters: []kgateway.ConsistentHashQueryParameter{{
			Name: "region",
		}, {
			Name:     "region",
			Terminal: ptr.To(true),
		}},
		Cookies: []kgateway.ConsistentHashCookie{{
			Name: "sticky",
			TTL:  ptr.To("3600"),
			Attributes: []kgateway.ConsistentHashCookieAttribute{{
				Name:  "SameSite",
				Value: "Lax",
			}},
		}, {
			Name: "sticky",
			Path: ptr.To("/ignored"),
		}},
		Headers: []kgateway.ConsistentHashHeader{{
			HeaderName: "X-User",
			Terminal:   ptr.To(true),
			RegexRewrite: &kgateway.ConsistentHashRegexRewrite{
				Pattern:      "^user-(.*)$",
				Substitution: `\1`,
			},
		}, {
			HeaderName: "x-user",
		}, {
			HeaderName: "X-Tenant",
		}},
	})

	got := spec.hashPolicies()
	want := []*envoyroutev3.RouteAction_HashPolicy{
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
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{HeaderName: "X-Tenant"},
			},
		},
		{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{
					Name: "sticky",
					Ttl:  durationpb.New(3600 * time.Second),
					Attributes: []*envoyroutev3.RouteAction_HashPolicy_CookieAttribute{{
						Name:  "SameSite",
						Value: "Lax",
					}},
				},
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
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: "envoy.lb"},
			},
		},
		{
			Terminal: true,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_{
				ConnectionProperties: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties{SourceIp: true},
			},
		},
	}
	require.Len(t, got, len(want))
	for i := range want {
		assert.Truef(t, proto.Equal(want[i], got[i]), "policy %d\nwant: %v\ngot:  %v", i, want[i], got[i])
	}
}

func TestParseCookieTTL(t *testing.T) {
	durationTTL, err := parseCookieTTL("1h30m")
	require.NoError(t, err)
	assert.True(t, proto.Equal(durationpb.New(90*time.Minute), durationTTL))

	secondsTTL, err := parseCookieTTL("3600")
	require.NoError(t, err)
	assert.True(t, proto.Equal(durationpb.New(time.Hour), secondsTTL))

	_, err = parseCookieTTL("soon")
	require.Error(t, err)
}

func TestConsistentHashInvalidRegex(t *testing.T) {
	var out trafficPolicySpecIr
	err := constructConsistentHash(kgateway.TrafficPolicySpec{
		ConsistentHash: &kgateway.ConsistentHash{
			Headers: []kgateway.ConsistentHashHeader{{
				HeaderName: "X-User",
				RegexRewrite: &kgateway.ConsistentHashRegexRewrite{
					Pattern:      "(",
					Substitution: "x",
				},
			}},
		},
	}, &out)
	require.Error(t, err)
	assert.Nil(t, out.consistentHash)
}

func TestMergeConsistentHashUnionsAndRecordsOrigins(t *testing.T) {
	higher := consistentHashPolicy("high", &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{
			HeaderName: "X-User",
			Terminal:   ptr.To(true),
		}},
		Cookies: []kgateway.ConsistentHashCookie{{
			Name: "high",
		}},
	})
	lower := consistentHashPolicy("low", &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{
			HeaderName: "x-user",
		}, {
			HeaderName: "X-Extra",
		}},
		Cookies: []kgateway.ConsistentHashCookie{{
			Name: "low",
			TTL:  ptr.To("1h30m"),
			Path: ptr.To("/app"),
			Attributes: []kgateway.ConsistentHashCookieAttribute{{
				Name:  "SameSite",
				Value: "Lax",
			}},
		}},
		QueryParameters: []kgateway.ConsistentHashQueryParameter{{
			Name: "region",
		}},
		FilterState: []kgateway.ConsistentHashFilterState{{
			Key: "io.kgateway.user",
		}},
		SourceIp: &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(true)},
	})

	merged := policy.MergePolicies([]ir.PolicyAtt{higher, lower}, mergeTrafficPolicies, "")
	got := merged.PolicyIr.(*TrafficPolicy).spec.consistentHash
	require.NotNil(t, got)
	assert.Nil(t, got.sourceIp, "sourceIp stays with the higher-priority policy when that policy leaves it unset")

	action := &envoyroutev3.RouteAction{}
	applyConsistentHash(got, action)
	require.Len(t, action.GetHashPolicy(), 6)
	assert.Equal(t, "X-User", action.GetHashPolicy()[0].GetHeader().GetHeaderName())
	assert.True(t, action.GetHashPolicy()[0].GetTerminal())
	assert.Equal(t, "X-Extra", action.GetHashPolicy()[1].GetHeader().GetHeaderName())
	assert.Equal(t, "high", action.GetHashPolicy()[2].GetCookie().GetName())
	assert.Equal(t, "low", action.GetHashPolicy()[3].GetCookie().GetName())
	assert.Equal(t, "/app", action.GetHashPolicy()[3].GetCookie().GetPath())
	assert.True(t, proto.Equal(durationpb.New(90*time.Minute), action.GetHashPolicy()[3].GetCookie().GetTtl()))
	require.Len(t, action.GetHashPolicy()[3].GetCookie().GetAttributes(), 1)
	assert.Equal(t, "SameSite", action.GetHashPolicy()[3].GetCookie().GetAttributes()[0].GetName())
	assert.Equal(t, "Lax", action.GetHashPolicy()[3].GetCookie().GetAttributes()[0].GetValue())
	assert.Equal(t, "region", action.GetHashPolicy()[4].GetQueryParameter().GetName())
	assert.Equal(t, "io.kgateway.user", action.GetHashPolicy()[5].GetFilterState().GetKey())

	assert.ElementsMatch(t, []string{
		"gateway.kgateway.dev/TrafficPolicy/default/high",
		"gateway.kgateway.dev/TrafficPolicy/default/low",
	}, merged.MergeOrigins.Get("consistentHash"))
}

func TestMergeConsistentHashDisableSuppressesInherited(t *testing.T) {
	route := consistentHashPolicy("route", &kgateway.ConsistentHash{Disable: ptr.To(true)})
	parent := consistentHashPolicy("parent", &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "X-User"}},
	})

	merged := policy.MergePolicies([]ir.PolicyAtt{route, parent}, mergeTrafficPolicies, "")
	got := merged.PolicyIr.(*TrafficPolicy).spec.consistentHash
	require.NotNil(t, got)
	assert.True(t, got.disable)
	assert.Empty(t, got.hashPolicies())
	assert.Equal(t, []string{"gateway.kgateway.dev/TrafficPolicy/default/route"}, merged.MergeOrigins.Get("consistentHash"))
}

func TestMergeConsistentHashLowerDisableDoesNotSuppress(t *testing.T) {
	higher := consistentHashPolicy("route", &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "X-User"}},
	})
	lower := consistentHashPolicy("parent", &kgateway.ConsistentHash{Disable: ptr.To(true)})

	merged := policy.MergePolicies([]ir.PolicyAtt{higher, lower}, mergeTrafficPolicies, "")
	got := merged.PolicyIr.(*TrafficPolicy).spec.consistentHash
	require.NotNil(t, got)
	assert.False(t, got.disable)
	require.Len(t, got.hashPolicies(), 1)
	assert.Equal(t, "X-User", got.hashPolicies()[0].GetHeader().GetHeaderName())
	assert.Equal(t, []string{"gateway.kgateway.dev/TrafficPolicy/default/route"}, merged.MergeOrigins.Get("consistentHash"))
}

func TestMergeConsistentHashSourceIPFromHigherPriority(t *testing.T) {
	higher := consistentHashPolicy("high", &kgateway.ConsistentHash{
		SourceIp: &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(true)},
	})
	lower := consistentHashPolicy("low", &kgateway.ConsistentHash{
		Headers:  []kgateway.ConsistentHashHeader{{HeaderName: "X-User"}},
		SourceIp: &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(false)},
	})

	merged := policy.MergePolicies([]ir.PolicyAtt{higher, lower}, mergeTrafficPolicies, "")
	got := merged.PolicyIr.(*TrafficPolicy).spec.consistentHash
	policies := got.hashPolicies()
	require.Len(t, policies, 2)
	assert.Equal(t, "X-User", policies[0].GetHeader().GetHeaderName())
	assert.True(t, policies[1].GetTerminal())
	assert.True(t, policies[1].GetConnectionProperties().GetSourceIp())
}

func buildConsistentHash(t *testing.T, spec *kgateway.ConsistentHash) *consistentHashIR {
	t.Helper()
	var out trafficPolicySpecIr
	require.NoError(t, constructConsistentHash(kgateway.TrafficPolicySpec{ConsistentHash: spec}, &out))
	require.NotNil(t, out.consistentHash)
	return out.consistentHash
}

func consistentHashPolicy(name string, spec *kgateway.ConsistentHash) ir.PolicyAtt {
	var out trafficPolicySpecIr
	if err := constructConsistentHash(kgateway.TrafficPolicySpec{ConsistentHash: spec}, &out); err != nil {
		panic(err)
	}
	return ir.PolicyAtt{
		GroupKind: schema.GroupKind{Group: "gateway.kgateway.dev", Kind: "TrafficPolicy"},
		PolicyRef: &ir.AttachedPolicyRef{
			Group:     "gateway.kgateway.dev",
			Kind:      "TrafficPolicy",
			Namespace: "default",
			Name:      name,
		},
		PolicyIr: &TrafficPolicy{
			ct:   time.Now(),
			spec: out,
		},
	}
}
