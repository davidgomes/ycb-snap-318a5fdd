package trafficpolicy

import (
	"testing"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"k8s.io/utils/ptr"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/policy"
)

func TestConstructConsistentHash(t *testing.T) {
	t.Run("empty object defaults to source ip", func(t *testing.T) {
		var spec trafficPolicySpecIr
		require.NoError(t, constructConsistentHash(kgateway.TrafficPolicySpec{
			ConsistentHash: &kgateway.ConsistentHash{},
		}, &spec))
		policies := spec.consistentHash.hashPolicies()
		require.Len(t, policies, 1)
		require.True(t, policies[0].GetConnectionProperties().GetSourceIp())
		require.False(t, policies[0].GetTerminal())
	})

	t.Run("disable produces no policies", func(t *testing.T) {
		var spec trafficPolicySpecIr
		require.NoError(t, constructConsistentHash(kgateway.TrafficPolicySpec{
			ConsistentHash: &kgateway.ConsistentHash{Disable: ptr.To(true)},
		}, &spec))
		require.True(t, spec.consistentHash.disable)
		require.Nil(t, spec.consistentHash.hashPolicies())
	})

	t.Run("dedup and canonical order", func(t *testing.T) {
		var spec trafficPolicySpecIr
		require.NoError(t, constructConsistentHash(kgateway.TrafficPolicySpec{
			ConsistentHash: &kgateway.ConsistentHash{
				SourceIp: &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(true)},
				FilterState: []kgateway.ConsistentHashFilterState{
					{Key: "fs"},
					{Key: "fs", Terminal: ptr.To(true)},
				},
				QueryParameters: []kgateway.ConsistentHashQueryParameter{
					{Name: "q"},
					{Name: "q"},
				},
				Cookies: []kgateway.ConsistentHashCookie{
					{
						Name: "session",
						TTL:  ptr.To("1h30m"),
						Path: ptr.To("/"),
						Attributes: []kgateway.ConsistentHashCookieAttribute{
							{Name: "SameSite", Value: "Lax"},
						},
					},
					{Name: "session", TTL: ptr.To("10s")},
					{Name: "id", TTL: ptr.To("3600")},
				},
				Headers: []kgateway.ConsistentHashHeader{
					{HeaderName: "X-User", Terminal: ptr.To(true), RegexRewrite: &kgateway.ConsistentHashRegexRewrite{
						Pattern:      "^user-(.*)$",
						Substitution: `\1`,
					}},
					{HeaderName: "x-user"},
				},
			},
		}, &spec))

		policies := spec.consistentHash.hashPolicies()
		require.Len(t, policies, 6)
		require.Equal(t, "X-User", policies[0].GetHeader().GetHeaderName())
		require.True(t, policies[0].GetTerminal())
		require.Equal(t, "^user-(.*)$", policies[0].GetHeader().GetRegexRewrite().GetPattern().GetRegex())
		require.Equal(t, `\1`, policies[0].GetHeader().GetRegexRewrite().GetSubstitution())
		require.Equal(t, "session", policies[1].GetCookie().GetName())
		require.True(t, proto.Equal(durationpb.New(90*time.Minute), policies[1].GetCookie().GetTtl()))
		require.Equal(t, "/", policies[1].GetCookie().GetPath())
		require.Equal(t, "SameSite", policies[1].GetCookie().GetAttributes()[0].GetName())
		require.Equal(t, "Lax", policies[1].GetCookie().GetAttributes()[0].GetValue())
		require.Equal(t, "id", policies[2].GetCookie().GetName())
		require.True(t, proto.Equal(durationpb.New(time.Hour), policies[2].GetCookie().GetTtl()))
		require.Equal(t, "q", policies[3].GetQueryParameter().GetName())
		require.Equal(t, "fs", policies[4].GetFilterState().GetKey())
		require.False(t, policies[4].GetTerminal())
		require.True(t, policies[5].GetConnectionProperties().GetSourceIp())
		require.True(t, policies[5].GetTerminal())
	})
}

func TestMergeConsistentHash(t *testing.T) {
	higher, err := consistentHashIRFromSpec(&kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "X-User"}},
		Cookies: []kgateway.ConsistentHashCookie{{Name: "a"}},
	})
	require.NoError(t, err)
	lower, err := consistentHashIRFromSpec(&kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{
			{HeaderName: "x-user", Terminal: ptr.To(true)},
			{HeaderName: "X-Other"},
		},
		Cookies:         []kgateway.ConsistentHashCookie{{Name: "b"}},
		QueryParameters: []kgateway.ConsistentHashQueryParameter{{Name: "user"}},
		SourceIp:        &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(true)},
	})
	require.NoError(t, err)

	p1 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: higher}}
	p2 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: lower}}
	origins := ir.MergeOrigins{}
	mergeConsistentHash(p1, p2, &ir.AttachedPolicyRef{Name: "low"}, nil, policy.MergeOptions{
		Strategy: policy.AugmentedShallowMerge,
	}, origins, TrafficPolicyMergeOpts{})

	policies := p1.spec.consistentHash.hashPolicies()
	require.Len(t, policies, 5)
	require.Equal(t, "X-User", policies[0].GetHeader().GetHeaderName())
	require.False(t, policies[0].GetTerminal())
	require.Equal(t, "X-Other", policies[1].GetHeader().GetHeaderName())
	require.Equal(t, "a", policies[2].GetCookie().GetName())
	require.Equal(t, "b", policies[3].GetCookie().GetName())
	require.Equal(t, "user", policies[4].GetQueryParameter().GetName())
	require.Nil(t, p1.spec.consistentHash.sourceIp)
	require.Contains(t, origins, consistentHashField)

	// Lower policy is unchanged by the merge.
	require.NotNil(t, p2.spec.consistentHash.sourceIp)
}

func TestMergeConsistentHashDisableSuppressesInherited(t *testing.T) {
	disabled, err := consistentHashIRFromSpec(&kgateway.ConsistentHash{Disable: ptr.To(true)})
	require.NoError(t, err)
	inherited, err := consistentHashIRFromSpec(&kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "X-User"}},
	})
	require.NoError(t, err)

	p1 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: disabled}}
	p2 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: inherited}}
	mergeConsistentHash(p1, p2, &ir.AttachedPolicyRef{Name: "parent"}, nil, policy.MergeOptions{
		Strategy: policy.AugmentedDeepMerge,
	}, ir.MergeOrigins{}, TrafficPolicyMergeOpts{})

	require.True(t, p1.spec.consistentHash.disable)
	require.Empty(t, p1.spec.consistentHash.hashPolicies())

	action := &envoyroutev3.RouteAction{
		HashPolicy: []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(false)},
	}
	applyConsistentHash(p1.spec.consistentHash, action)
	require.Nil(t, action.HashPolicy)
}
