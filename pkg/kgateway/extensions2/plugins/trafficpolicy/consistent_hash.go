package trafficpolicy

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoy_type_matcher_v3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/regexutils"
)

type consistentHashIR struct {
	disable     bool
	headers     []*envoyroutev3.RouteAction_HashPolicy
	cookies     []*envoyroutev3.RouteAction_HashPolicy
	queryParams []*envoyroutev3.RouteAction_HashPolicy
	filterState []*envoyroutev3.RouteAction_HashPolicy
	sourceIP    *envoyroutev3.RouteAction_HashPolicy
}

var _ PolicySubIR = &consistentHashIR{}

func hashPoliciesEqual(a, b []*envoyroutev3.RouteAction_HashPolicy) bool {
	return slices.EqualFunc(a, b, func(x, y *envoyroutev3.RouteAction_HashPolicy) bool { return proto.Equal(x, y) })
}

func (c *consistentHashIR) Equals(other PolicySubIR) bool {
	o, ok := other.(*consistentHashIR)
	if !ok {
		return false
	}
	if c == nil || o == nil {
		return c == nil && o == nil
	}
	return c.disable == o.disable &&
		hashPoliciesEqual(c.headers, o.headers) &&
		hashPoliciesEqual(c.cookies, o.cookies) &&
		hashPoliciesEqual(c.queryParams, o.queryParams) &&
		hashPoliciesEqual(c.filterState, o.filterState) &&
		proto.Equal(c.sourceIP, o.sourceIP)
}

func (c *consistentHashIR) Validate() error {
	if c == nil {
		return nil
	}
	for _, h := range c.headers {
		if rr := h.GetHeader().GetRegexRewrite(); rr != nil {
			if err := regexutils.CheckRegexString(rr.GetPattern().GetRegex()); err != nil {
				return fmt.Errorf("invalid consistentHash header regex pattern: %w", err)
			}
		}
	}
	return nil
}

// hashPolicies returns the hash policies in canonical type order.
func (c *consistentHashIR) hashPolicies() []*envoyroutev3.RouteAction_HashPolicy {
	if c == nil || c.disable {
		return nil
	}
	out := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(c.headers)+len(c.cookies)+len(c.queryParams)+len(c.filterState)+1)
	out = append(out, c.headers...)
	out = append(out, c.cookies...)
	out = append(out, c.queryParams...)
	out = append(out, c.filterState...)
	if c.sourceIP != nil {
		out = append(out, c.sourceIP)
	}
	return out
}

func headerHashKey(p *envoyroutev3.RouteAction_HashPolicy) string {
	return strings.ToLower(p.GetHeader().GetHeaderName())
}

func cookieHashKey(p *envoyroutev3.RouteAction_HashPolicy) string {
	return p.GetCookie().GetName()
}

func queryParamHashKey(p *envoyroutev3.RouteAction_HashPolicy) string {
	return p.GetQueryParameter().GetName()
}

func filterStateHashKey(p *envoyroutev3.RouteAction_HashPolicy) string {
	return p.GetFilterState().GetKey()
}

// dedupHashPolicies keeps the first occurrence of each key.
func dedupHashPolicies(
	in []*envoyroutev3.RouteAction_HashPolicy,
	key func(*envoyroutev3.RouteAction_HashPolicy) string,
) []*envoyroutev3.RouteAction_HashPolicy {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(in))
	for _, p := range in {
		k := key(p)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, p)
	}
	return out
}

func parseCookieTTL(ttl string) (time.Duration, error) {
	if secs, err := strconv.ParseInt(ttl, 10, 64); err == nil {
		return time.Duration(secs) * time.Second, nil
	}
	return time.ParseDuration(ttl)
}

func constructConsistentHash(spec kgateway.TrafficPolicySpec, out *trafficPolicySpecIr) error {
	ch := spec.ConsistentHash
	if ch == nil {
		return nil
	}
	ir := &consistentHashIR{}
	if ch.Disable != nil && *ch.Disable {
		ir.disable = true
		out.consistentHash = ir
		return nil
	}
	terminal := func(t *bool) bool { return t != nil && *t }

	for _, h := range ch.Headers {
		hdr := &envoyroutev3.RouteAction_HashPolicy_Header{HeaderName: h.HeaderName}
		if h.RegexRewrite != nil {
			hdr.RegexRewrite = &envoy_type_matcher_v3.RegexMatchAndSubstitute{
				Pattern:      &envoy_type_matcher_v3.RegexMatcher{Regex: h.RegexRewrite.Pattern},
				Substitution: h.RegexRewrite.Substitution,
			}
		}
		ir.headers = append(ir.headers, &envoyroutev3.RouteAction_HashPolicy{
			Terminal:        terminal(h.Terminal),
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{Header: hdr},
		})
	}
	for _, c := range ch.Cookies {
		cookie := &envoyroutev3.RouteAction_HashPolicy_Cookie{Name: c.Name}
		if c.TTL != nil {
			d, err := parseCookieTTL(*c.TTL)
			if err != nil {
				return fmt.Errorf("invalid consistentHash cookie ttl %q: %w", *c.TTL, err)
			}
			cookie.Ttl = durationpb.New(d)
		}
		if c.Path != nil {
			cookie.Path = *c.Path
		}
		for _, a := range c.Attributes {
			cookie.Attributes = append(cookie.Attributes, &envoyroutev3.RouteAction_HashPolicy_CookieAttribute{
				Name:  a.Name,
				Value: a.Value,
			})
		}
		ir.cookies = append(ir.cookies, &envoyroutev3.RouteAction_HashPolicy{
			Terminal:        terminal(c.Terminal),
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{Cookie: cookie},
		})
	}
	for _, q := range ch.QueryParameters {
		ir.queryParams = append(ir.queryParams, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: terminal(q.Terminal),
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
				QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{Name: q.Name},
			},
		})
	}
	for _, f := range ch.FilterState {
		ir.filterState = append(ir.filterState, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: terminal(f.Terminal),
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: f.Key},
			},
		})
	}
	if ch.SourceIP != nil {
		ir.sourceIP = sourceIPHashPolicy(terminal(ch.SourceIP.Terminal))
	}

	ir.headers = dedupHashPolicies(ir.headers, headerHashKey)
	ir.cookies = dedupHashPolicies(ir.cookies, cookieHashKey)
	ir.queryParams = dedupHashPolicies(ir.queryParams, queryParamHashKey)
	ir.filterState = dedupHashPolicies(ir.filterState, filterStateHashKey)

	if len(ir.headers)+len(ir.cookies)+len(ir.queryParams)+len(ir.filterState) == 0 && ir.sourceIP == nil {
		ir.sourceIP = sourceIPHashPolicy(false)
	}
	out.consistentHash = ir
	return nil
}

func sourceIPHashPolicy(terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		Terminal: terminal,
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_{
			ConnectionProperties: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties{SourceIp: true},
		},
	}
}

// unionConsistentHash merges two IRs, with hi's entries taking precedence.
func unionConsistentHash(hi, lo *consistentHashIR) *consistentHashIR {
	// A disabled higher-priority policy suppresses everything; a disabled
	// lower-priority policy contributes nothing.
	if hi.disable || lo.disable {
		return hi
	}
	return &consistentHashIR{
		headers:     dedupHashPolicies(slices.Concat(hi.headers, lo.headers), headerHashKey),
		cookies:     dedupHashPolicies(slices.Concat(hi.cookies, lo.cookies), cookieHashKey),
		queryParams: dedupHashPolicies(slices.Concat(hi.queryParams, lo.queryParams), queryParamHashKey),
		filterState: dedupHashPolicies(slices.Concat(hi.filterState, lo.filterState), filterStateHashKey),
		sourceIP:    hi.sourceIP,
	}
}

func applyConsistentHash(c *consistentHashIR, action *envoyroutev3.RouteAction) {
	if c == nil || action == nil {
		return
	}
	action.HashPolicy = c.hashPolicies()
}
