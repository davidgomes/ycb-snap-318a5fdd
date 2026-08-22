package trafficpolicy

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoymatcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
)

type consistentHashIR struct {
	disable  bool
	policies []*envoyroutev3.RouteAction_HashPolicy
}

func (c *consistentHashIR) Equals(other PolicySubIR) bool {
	o, ok := other.(*consistentHashIR)
	if !ok {
		return false
	}
	if c == nil || o == nil {
		return c == nil && o == nil
	}
	return c.disable == o.disable && proto.Equal(&envoyroutev3.RouteAction{HashPolicy: c.policies}, &envoyroutev3.RouteAction{HashPolicy: o.policies})
}

func (c *consistentHashIR) Validate() error { return nil }

func constructConsistentHash(spec kgateway.TrafficPolicySpec, out *trafficPolicySpecIr) {
	if spec.ConsistentHash == nil {
		return
	}
	ch := spec.ConsistentHash
	out.consistentHash = &consistentHashIR{disable: ch.Disable != nil && *ch.Disable}
	if out.consistentHash.disable {
		return
	}
	for _, h := range ch.Headers {
		p := &envoyroutev3.RouteAction_HashPolicy{
			Terminal: h.Terminal,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{HeaderName: h.HeaderName},
			},
		}
		if h.RegexRewrite != nil {
			p.GetHeader().RegexRewrite = &envoymatcher.RegexMatchAndSubstitute{
				Pattern: h.RegexRewrite.Pattern, Substitution: h.RegexRewrite.Substitution,
			}
		}
		out.consistentHash.policies = appendUniqueHashPolicy(out.consistentHash.policies, strings.ToLower(h.HeaderName), p)
	}
	for _, c := range ch.Cookies {
		ttl, err := parseCookieTTL(c.TTL)
		if err != nil {
			continue
		}
		p := &envoyroutev3.RouteAction_HashPolicy{
			Terminal: c.Terminal,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: &envoyroutev3.RouteAction_HashPolicy_Cookie{Name: c.Name, Ttl: durationpb.New(ttl), Path: c.Path},
			},
		}
		for _, a := range c.Attributes {
			p.GetCookie().Attributes = append(p.GetCookie().Attributes, &envoyroutev3.RouteAction_HashPolicy_CookieAttribute{Name: a.Name, Value: a.Value})
		}
		out.consistentHash.policies = appendUniqueHashPolicy(out.consistentHash.policies, c.Name, p)
	}
	for _, q := range ch.QueryParameters {
		out.consistentHash.policies = appendUniqueHashPolicy(out.consistentHash.policies, q.Name, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: q.Terminal, PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{Name: q.Name}},
		})
	}
	for _, f := range ch.FilterState {
		out.consistentHash.policies = appendUniqueHashPolicy(out.consistentHash.policies, f.Key, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: f.Terminal, PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: f.Key}},
		})
	}
	if ch.SourceIP != nil {
		out.consistentHash.policies = append(out.consistentHash.policies, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: ch.SourceIP.Terminal, PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_{
				ConnectionProperties: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties{SourceIp: true},
			},
		})
	}
	if len(out.consistentHash.policies) == 0 {
		out.consistentHash.policies = []*envoyroutev3.RouteAction_HashPolicy{{PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_{
			ConnectionProperties: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties{SourceIp: true},
		}}}
	}
}

func appendUniqueHashPolicy(policies []*envoyroutev3.RouteAction_HashPolicy, key string, policy *envoyroutev3.RouteAction_HashPolicy) []*envoyroutev3.RouteAction_HashPolicy {
	for _, p := range policies {
		if hashPolicyKey(p) == key {
			return policies
		}
	}
	return append(policies, policy)
}

func hashPolicyKey(p *envoyroutev3.RouteAction_HashPolicy) string {
	switch x := p.PolicySpecifier.(type) {
	case *envoyroutev3.RouteAction_HashPolicy_Header_:
		return "header:" + strings.ToLower(x.Header.HeaderName)
	case *envoyroutev3.RouteAction_HashPolicy_Cookie_:
		return "cookie:" + x.Cookie.Name
	case *envoyroutev3.RouteAction_HashPolicy_QueryParameter_:
		return "query:" + x.QueryParameter.Name
	case *envoyroutev3.RouteAction_HashPolicy_FilterState_:
		return "filter:" + x.FilterState.Key
	default:
		return "sourceIp"
	}
}

func parseCookieTTL(value string) (time.Duration, error) {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}
	return time.ParseDuration(value)
}
