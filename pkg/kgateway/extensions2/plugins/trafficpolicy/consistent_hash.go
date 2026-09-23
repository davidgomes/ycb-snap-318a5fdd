package trafficpolicy

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoy_type_matcher_v3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/policy"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/regexutils"
)

const consistentHashMergeField = "consistentHash"

// consistentHashIR is the route-level consistent hashing configuration.
// Hash policies are stored by type so they can be merged independently and
// emitted in canonical order: headers, cookies, query parameters, filter state, source IP.
type consistentHashIR struct {
	// disabled suppresses hash policies, including ones inherited from broader-scoped policies.
	disabled bool
	headers  []*envoyroutev3.RouteAction_HashPolicy
	cookies  []*envoyroutev3.RouteAction_HashPolicy
	// queryParameters are hashed query parameter policies.
	queryParameters []*envoyroutev3.RouteAction_HashPolicy
	filterState     []*envoyroutev3.RouteAction_HashPolicy
	// sourceIp is nil when the policy does not set source IP hashing.
	// A higher-priority policy keeps that unset value when merged with a lower-priority policy.
	sourceIp *envoyroutev3.RouteAction_HashPolicy
}

var _ PolicySubIR = &consistentHashIR{}

func (c *consistentHashIR) Equals(other PolicySubIR) bool {
	otherHash, ok := other.(*consistentHashIR)
	if !ok {
		return false
	}
	if c == nil || otherHash == nil {
		return c == nil && otherHash == nil
	}
	if c.disabled != otherHash.disabled {
		return false
	}
	return hashPoliciesEqual(c.headers, otherHash.headers) &&
		hashPoliciesEqual(c.cookies, otherHash.cookies) &&
		hashPoliciesEqual(c.queryParameters, otherHash.queryParameters) &&
		hashPoliciesEqual(c.filterState, otherHash.filterState) &&
		proto.Equal(c.sourceIp, otherHash.sourceIp)
}

func (c *consistentHashIR) Validate() error {
	if c == nil || c.disabled {
		return nil
	}
	for _, hashPolicy := range c.hashPolicies() {
		if err := hashPolicy.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c *consistentHashIR) clone() *consistentHashIR {
	if c == nil {
		return nil
	}
	out := *c
	out.headers = slices.Clone(c.headers)
	out.cookies = slices.Clone(c.cookies)
	out.queryParameters = slices.Clone(c.queryParameters)
	out.filterState = slices.Clone(c.filterState)
	return &out
}

// hashPolicies returns hash policies in canonical type order.
// An enabled policy with no entries defaults to source IP hashing with terminal=false.
func (c *consistentHashIR) hashPolicies() []*envoyroutev3.RouteAction_HashPolicy {
	if c == nil || c.disabled {
		return nil
	}
	out := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(c.headers)+len(c.cookies)+len(c.queryParameters)+len(c.filterState)+1)
	out = append(out, c.headers...)
	out = append(out, c.cookies...)
	out = append(out, c.queryParameters...)
	out = append(out, c.filterState...)
	if c.sourceIp != nil {
		out = append(out, c.sourceIp)
	}
	if len(out) == 0 {
		return []*envoyroutev3.RouteAction_HashPolicy{newSourceIPHashPolicy(false)}
	}
	return out
}

func constructConsistentHash(spec kgateway.TrafficPolicySpec, out *trafficPolicySpecIr) error {
	if spec.ConsistentHash == nil {
		return nil
	}
	built, err := buildConsistentHashIR(spec.ConsistentHash)
	if err != nil {
		return err
	}
	out.consistentHash = built
	return nil
}

func buildConsistentHashIR(spec *kgateway.ConsistentHash) (*consistentHashIR, error) {
	if spec == nil {
		return nil, nil
	}
	if spec.Disable != nil && *spec.Disable {
		return &consistentHashIR{disabled: true}, nil
	}

	headers, err := buildHeaderHashPolicies(spec.Headers)
	if err != nil {
		return nil, err
	}
	cookies, err := buildCookieHashPolicies(spec.Cookies)
	if err != nil {
		return nil, err
	}
	queryParameters, err := buildQueryParameterHashPolicies(spec.QueryParameters)
	if err != nil {
		return nil, err
	}
	filterState, err := buildFilterStateHashPolicies(spec.FilterState)
	if err != nil {
		return nil, err
	}

	ir := &consistentHashIR{
		headers:         headers,
		cookies:         cookies,
		queryParameters: queryParameters,
		filterState:     filterState,
	}
	if spec.SourceIP != nil {
		ir.sourceIp = newSourceIPHashPolicy(boolValue(spec.SourceIP.Terminal))
	}
	return ir, nil
}

func applyConsistentHash(cfg *consistentHashIR, action *envoyroutev3.RouteAction) {
	if cfg == nil || action == nil {
		return
	}
	if cfg.disabled {
		action.HashPolicy = nil
		return
	}
	action.HashPolicy = cfg.hashPolicies()
}

func buildHeaderHashPolicies(headers []kgateway.ConsistentHashHeader) ([]*envoyroutev3.RouteAction_HashPolicy, error) {
	headers = dedupeBy(headers, func(h kgateway.ConsistentHashHeader) string {
		return strings.ToLower(h.HeaderName)
	})
	policies := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(headers))
	for _, header := range headers {
		if header.HeaderName == "" {
			return nil, fmt.Errorf("consistentHash headerName must be set")
		}
		hashPolicy := &envoyroutev3.RouteAction_HashPolicy{
			Terminal: boolValue(header.Terminal),
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{
					HeaderName: header.HeaderName,
				},
			},
		}
		if header.RegexRewrite != nil {
			if err := regexutils.CheckRegexString(header.RegexRewrite.Pattern); err != nil {
				return nil, fmt.Errorf("invalid consistentHash header %q regex: %w", header.HeaderName, err)
			}
			hashPolicy.GetHeader().RegexRewrite = &envoy_type_matcher_v3.RegexMatchAndSubstitute{
				Pattern: &envoy_type_matcher_v3.RegexMatcher{
					Regex: header.RegexRewrite.Pattern,
				},
				Substitution: header.RegexRewrite.Substitution,
			}
		}
		policies = append(policies, hashPolicy)
	}
	return policies, nil
}

func buildCookieHashPolicies(cookies []kgateway.ConsistentHashCookie) ([]*envoyroutev3.RouteAction_HashPolicy, error) {
	cookies = dedupeBy(cookies, func(c kgateway.ConsistentHashCookie) string {
		return c.Name
	})
	policies := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" {
			return nil, fmt.Errorf("consistentHash cookie name must be set")
		}
		cookiePolicy := &envoyroutev3.RouteAction_HashPolicy_Cookie{
			Name: cookie.Name,
		}
		if cookie.TTL != nil {
			ttl, err := parseCookieTTL(*cookie.TTL)
			if err != nil {
				return nil, fmt.Errorf("invalid consistentHash cookie %q ttl: %w", cookie.Name, err)
			}
			cookiePolicy.Ttl = durationpb.New(ttl)
		}
		if cookie.Path != nil {
			cookiePolicy.Path = *cookie.Path
		}
		if len(cookie.Attributes) > 0 {
			attrs := make([]*envoyroutev3.RouteAction_HashPolicy_CookieAttribute, 0, len(cookie.Attributes))
			for _, attr := range cookie.Attributes {
				attrs = append(attrs, &envoyroutev3.RouteAction_HashPolicy_CookieAttribute{
					Name:  attr.Name,
					Value: attr.Value,
				})
			}
			cookiePolicy.Attributes = attrs
		}
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: boolValue(cookie.Terminal),
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: cookiePolicy,
			},
		})
	}
	return policies, nil
}

func buildQueryParameterHashPolicies(params []kgateway.ConsistentHashQueryParameter) ([]*envoyroutev3.RouteAction_HashPolicy, error) {
	params = dedupeBy(params, func(p kgateway.ConsistentHashQueryParameter) string {
		return p.Name
	})
	policies := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(params))
	for _, param := range params {
		if param.Name == "" {
			return nil, fmt.Errorf("consistentHash query parameter name must be set")
		}
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: boolValue(param.Terminal),
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
				QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{
					Name: param.Name,
				},
			},
		})
	}
	return policies, nil
}

func buildFilterStateHashPolicies(entries []kgateway.ConsistentHashFilterState) ([]*envoyroutev3.RouteAction_HashPolicy, error) {
	entries = dedupeBy(entries, func(e kgateway.ConsistentHashFilterState) string {
		return e.Key
	})
	policies := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(entries))
	for _, entry := range entries {
		if entry.Key == "" {
			return nil, fmt.Errorf("consistentHash filterState key must be set")
		}
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: boolValue(entry.Terminal),
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{
					Key: entry.Key,
				},
			},
		})
	}
	return policies, nil
}

func newSourceIPHashPolicy(terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		Terminal: terminal,
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_{
			ConnectionProperties: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties{
				SourceIp: true,
			},
		},
	}
}

// parseCookieTTL accepts a Go duration string, such as "1h30m", or a plain integer number of seconds, such as "3600".
func parseCookieTTL(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("ttl must be a Go duration (for example \"1h30m\") or integer seconds (for example \"3600\")")
	}
	if parsed, err := time.ParseDuration(raw); err == nil {
		if parsed < 0 {
			return 0, fmt.Errorf("ttl must not be negative")
		}
		return parsed, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("ttl %q must be a Go duration (for example \"1h30m\") or integer seconds (for example \"3600\")", raw)
	}
	if seconds > int64(math.MaxInt64/int64(time.Second)) {
		return 0, fmt.Errorf("ttl %q overflows the supported duration range", raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

func boolValue(v *bool) bool {
	return v != nil && *v
}

func dedupeBy[T any](items []T, keyFn func(T) string) []T {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]T, 0, len(items))
	for _, item := range items {
		key := keyFn(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func hashPoliciesEqual(a, b []*envoyroutev3.RouteAction_HashPolicy) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func headerHashPolicyKey(p *envoyroutev3.RouteAction_HashPolicy) string {
	return strings.ToLower(p.GetHeader().GetHeaderName())
}

func cookieHashPolicyKey(p *envoyroutev3.RouteAction_HashPolicy) string {
	return p.GetCookie().GetName()
}

func queryParameterHashPolicyKey(p *envoyroutev3.RouteAction_HashPolicy) string {
	return p.GetQueryParameter().GetName()
}

func filterStateHashPolicyKey(p *envoyroutev3.RouteAction_HashPolicy) string {
	return p.GetFilterState().GetKey()
}

// unionHashPolicies appends policies from src that are not already identified in dst.
// The boolean reports whether src contributed at least one new policy.
func unionHashPolicies(
	dst, src []*envoyroutev3.RouteAction_HashPolicy,
	keyFn func(*envoyroutev3.RouteAction_HashPolicy) string,
) ([]*envoyroutev3.RouteAction_HashPolicy, bool) {
	seen := make(map[string]struct{}, len(dst)+len(src))
	out := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(dst)+len(src))
	for _, hashPolicy := range dst {
		key := keyFn(hashPolicy)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, hashPolicy)
	}
	added := false
	for _, hashPolicy := range src {
		key := keyFn(hashPolicy)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, hashPolicy)
		added = true
	}
	return out, added
}

func mergeConsistentHash(
	p1, p2 *TrafficPolicy,
	p2Ref *ir.AttachedPolicyRef,
	p2MergeOrigins ir.MergeOrigins,
	opts policy.MergeOptions,
	mergeOrigins ir.MergeOrigins,
	_ TrafficPolicyMergeOpts,
) {
	if p2 == nil || p2.spec.consistentHash == nil {
		return
	}
	switch opts.Strategy {
	case policy.OverridableShallowMerge, policy.OverridableDeepMerge:
		mergeConsistentHashPreferIncoming(p1, p2, p2Ref, p2MergeOrigins, mergeOrigins)
	default:
		mergeConsistentHashPreferExisting(p1, p2, p2Ref, p2MergeOrigins, mergeOrigins)
	}
}

// mergeConsistentHashPreferExisting unions p2 into p1, keeping p1's entries first.
// p1 is the higher-priority policy accumulated so far. sourceIp stays p1's value, including when it is unset.
// A disabled higher-priority policy suppresses policies inherited from p2.
func mergeConsistentHashPreferExisting(
	p1, p2 *TrafficPolicy,
	p2Ref *ir.AttachedPolicyRef,
	p2MergeOrigins ir.MergeOrigins,
	mergeOrigins ir.MergeOrigins,
) {
	incoming := p2.spec.consistentHash
	existing := p1.spec.consistentHash
	if existing == nil {
		p1.spec.consistentHash = incoming.clone()
		mergeOrigins.SetOne(consistentHashMergeField, p2Ref, p2MergeOrigins)
		return
	}
	if existing.disabled || incoming.disabled {
		return
	}

	added := false
	var contributed bool
	existing.headers, contributed = unionHashPolicies(existing.headers, incoming.headers, headerHashPolicyKey)
	added = added || contributed
	existing.cookies, contributed = unionHashPolicies(existing.cookies, incoming.cookies, cookieHashPolicyKey)
	added = added || contributed
	existing.queryParameters, contributed = unionHashPolicies(existing.queryParameters, incoming.queryParameters, queryParameterHashPolicyKey)
	added = added || contributed
	existing.filterState, contributed = unionHashPolicies(existing.filterState, incoming.filterState, filterStateHashPolicyKey)
	added = added || contributed
	if added {
		mergeOrigins.Append(consistentHashMergeField, p2Ref, p2MergeOrigins)
	}
}

// mergeConsistentHashPreferIncoming unions policies while giving p2 priority.
// p2's entries come first, and p2's sourceIp value is kept even when unset.
// A disabled p2 suppresses hash policies already accumulated on p1.
func mergeConsistentHashPreferIncoming(
	p1, p2 *TrafficPolicy,
	p2Ref *ir.AttachedPolicyRef,
	p2MergeOrigins ir.MergeOrigins,
	mergeOrigins ir.MergeOrigins,
) {
	incoming := p2.spec.consistentHash.clone()
	existing := p1.spec.consistentHash
	if incoming.disabled || existing == nil || existing.disabled {
		p1.spec.consistentHash = incoming
		mergeOrigins.SetOne(consistentHashMergeField, p2Ref, p2MergeOrigins)
		return
	}

	prevOrigins := mergeOrigins[consistentHashMergeField].Clone()
	addedFromExisting := false
	var contributed bool
	incoming.headers, contributed = unionHashPolicies(incoming.headers, existing.headers, headerHashPolicyKey)
	addedFromExisting = addedFromExisting || contributed
	incoming.cookies, contributed = unionHashPolicies(incoming.cookies, existing.cookies, cookieHashPolicyKey)
	addedFromExisting = addedFromExisting || contributed
	incoming.queryParameters, contributed = unionHashPolicies(incoming.queryParameters, existing.queryParameters, queryParameterHashPolicyKey)
	addedFromExisting = addedFromExisting || contributed
	incoming.filterState, contributed = unionHashPolicies(incoming.filterState, existing.filterState, filterStateHashPolicyKey)
	addedFromExisting = addedFromExisting || contributed
	p1.spec.consistentHash = incoming
	mergeOrigins.SetOne(consistentHashMergeField, p2Ref, p2MergeOrigins)
	if addedFromExisting && len(prevOrigins) > 0 {
		mergeOrigins[consistentHashMergeField] = mergeOrigins[consistentHashMergeField].Union(prevOrigins)
	}
}
