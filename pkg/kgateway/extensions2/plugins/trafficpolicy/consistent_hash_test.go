package trafficpolicy

import (
	"testing"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/policy"
)

func buildConsistentHash(t *testing.T, ch *kgateway.ConsistentHash) *consistentHashIR {
	t.Helper()
	out := &trafficPolicySpecIr{}
	NewWithT(t).Expect(constructConsistentHash(kgateway.TrafficPolicySpec{ConsistentHash: ch}, out)).To(Succeed())
	return out.consistentHash
}

func TestConsistentHashEmptyDefaultsToSourceIP(t *testing.T) {
	g := NewWithT(t)
	hp := buildConsistentHash(t, &kgateway.ConsistentHash{}).hashPolicies()
	g.Expect(hp).To(HaveLen(1))
	g.Expect(hp[0].GetConnectionProperties().GetSourceIp()).To(BeTrue())
	g.Expect(hp[0].GetTerminal()).To(BeFalse())
}

func TestConsistentHashDisable(t *testing.T) {
	g := NewWithT(t)
	action := &envoyroutev3.RouteAction{HashPolicy: []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(true)}}
	applyConsistentHash(buildConsistentHash(t, &kgateway.ConsistentHash{Disable: ptr.To(true)}), action)
	g.Expect(action.GetHashPolicy()).To(BeEmpty())
}

func TestConsistentHashOrderingDedupAndTTL(t *testing.T) {
	g := NewWithT(t)
	c := buildConsistentHash(t, &kgateway.ConsistentHash{
		SourceIP:        &kgateway.ConsistentHashSourceIP{Terminal: ptr.To(true)},
		FilterState:     []kgateway.ConsistentHashFilterState{{Key: "k"}, {Key: "k"}},
		QueryParameters: []kgateway.ConsistentHashQueryParameter{{Name: "q"}},
		Cookies: []kgateway.ConsistentHashCookie{
			{Name: "c", TTL: ptr.To("3600"), Attributes: []kgateway.CookieAttribute{{Name: "SameSite", Value: "Strict"}}},
			{Name: "d", TTL: ptr.To("1h30m")},
		},
		Headers: []kgateway.ConsistentHashHeader{
			{HeaderName: "X-User", RegexRewrite: &kgateway.RegexMatchAndSubstitute{Pattern: "^(.*)$", Substitution: "\\1"}},
			{HeaderName: "x-user", Terminal: ptr.To(true)},
		},
	})
	hp := c.hashPolicies()
	g.Expect(hp).To(HaveLen(6))
	g.Expect(hp[0].GetHeader().GetHeaderName()).To(Equal("X-User"))
	g.Expect(hp[0].GetHeader().GetRegexRewrite().GetPattern().GetRegex()).To(Equal("^(.*)$"))
	g.Expect(hp[1].GetCookie().GetTtl().AsDuration()).To(Equal(time.Hour))
	g.Expect(hp[1].GetCookie().GetAttributes()[0].GetName()).To(Equal("SameSite"))
	g.Expect(hp[2].GetCookie().GetTtl().AsDuration()).To(Equal(90 * time.Minute))
	g.Expect(hp[3].GetQueryParameter().GetName()).To(Equal("q"))
	g.Expect(hp[4].GetFilterState().GetKey()).To(Equal("k"))
	g.Expect(hp[5].GetConnectionProperties().GetSourceIp()).To(BeTrue())
	g.Expect(hp[5].GetTerminal()).To(BeTrue())
}

func TestMergeConsistentHashUnion(t *testing.T) {
	g := NewWithT(t)
	p1 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: buildConsistentHash(t, &kgateway.ConsistentHash{
		Headers: []kgateway.ConsistentHashHeader{{HeaderName: "a", Terminal: ptr.To(true)}},
	})}}
	p2 := &TrafficPolicy{spec: trafficPolicySpecIr{consistentHash: buildConsistentHash(t, &kgateway.ConsistentHash{
		Headers:  []kgateway.ConsistentHashHeader{{HeaderName: "A"}, {HeaderName: "b"}},
		Cookies:  []kgateway.ConsistentHashCookie{{Name: "c"}},
		SourceIP: &kgateway.ConsistentHashSourceIP{},
	})}}
	p2Ref := &ir.AttachedPolicyRef{Name: "p2"}
	origins := ir.MergeOrigins{}
	mergeConsistentHash(p1, p2, p2Ref, nil, policy.MergeOptions{Strategy: policy.AugmentedDeepMerge}, origins, TrafficPolicyMergeOpts{})

	hp := p1.spec.consistentHash.hashPolicies()
	g.Expect(hp).To(HaveLen(3), "sourceIp should retain the unset higher-priority value")
	g.Expect(hp[0].GetHeader().GetHeaderName()).To(Equal("a"))
	g.Expect(hp[0].GetTerminal()).To(BeTrue())
	g.Expect(hp[1].GetHeader().GetHeaderName()).To(Equal("b"))
	g.Expect(hp[2].GetCookie().GetName()).To(Equal("c"))
	g.Expect(origins).To(HaveKey("consistentHash"))
}
