package trafficpolicy

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoy_type_matcher_v3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"k8s.io/utils/ptr"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/policy"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/regexutils"
)

const consistentHashField = "consistentHash"

var _ PolicySubIR = &consistentHashIR{}

// consistentHashIR is the route hash policy configuration.
// The default source IP policy for an empty spec is applied when building
// Envoy config so merge can tell an unset sourceIp apart from an explicit one.
type consistentHashIR struct {
	disable         bool
	headers         []consistentHashHeader
	cookies         []consistentHashCookie
	queryParameters []consistentHashQueryParameter
	filterState     []consistentHashFilterState
	sourceIp        *consistentHashSourceIP
}

type consistentHashHeader struct {
	headerName   string
	regexRewrite *envoy_type_matcher_v3.RegexMatchAndSubstitute
	terminal     bool
}

type consistentHashCookie struct {
	name       string
	ttl        *durationpb.Duration
	path       string
	attributes []consistentHashCookieAttribute
	terminal   bool
}

type consistentHashCookieAttribute struct {
	name  string
	value string
}

type consistentHashQueryParameter struct {
	name     string
	terminal bool
}

type consistentHashFilterState struct {
	key      string
	terminal bool
}

type consistentHashSourceIP struct {
	terminal bool
}

func (c *consistentHashIR) Equals(other PolicySubIR) bool {
	b, ok := other.(*consistentHashIR)
	if !ok {
		return false
	}
	if c == nil || b == nil {
		return c == nil && b == nil
	}
	if c.disable != b.disable || len(c.headers) != len(b.headers) || len(c.cookies) != len(b.cookies) ||
		len(c.queryParameters) != len(b.queryParameters) || len(c.filterState) != len(b.filterState) {
		return false
	}
	if (c.sourceIp == nil) != (b.sourceIp == nil) {
		return false
	}
	if c.sourceIp != nil && c.sourceIp.terminal != b.sourceIp.terminal {
		return false
	}
	for i := range c.headers {
		if c.headers[i].headerName != b.headers[i].headerName || c.headers[i].terminal != b.headers[i].terminal ||
			!proto.Equal(c.headers[i].regexRewrite, b.headers[i].regexRewrite) {
			return false
		}
	}
	for i := range c.cookies {
		if c.cookies[i].name != b.cookies[i].name || c.cookies[i].path != b.cookies[i].path ||
			c.cookies[i].terminal != b.cookies[i].terminal || !proto.Equal(c.cookies[i].ttl, b.cookies[i].ttl) ||
			len(c.cookies[i].attributes) != len(b.cookies[i].attributes) {
			return false
		}
		for j := range c.cookies[i].attributes {
			if c.cookies[i].attributes[j] != b.cookies[i].attributes[j] {
				return false
			}
		}
	}
	for i := range c.queryParameters {
		if c.queryParameters[i] != b.queryParameters[i] {
			return false
		}
	}
	for i := range c.filterState {
		if c.filterState[i] != b.filterState[i] {
			return false
		}
	}
	return true
}

func (c *consistentHashIR) Validate() error {
	if c == nil || c.disable {
		return nil
	}
	for _, header := range c.headers {
		if header.regexRewrite != nil && header.regexRewrite.GetPattern() != nil {
			if err := regexutils.CheckRegexString(header.regexRewrite.GetPattern().GetRegex()); err != nil {
				return fmt.Errorf("invalid consistentHash header regex pattern: %w", err)
			}
		}
	}
	for _, policy := range c.hashPolicies() {
		if err := policy.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func constructConsistentHash(spec kgateway.TrafficPolicySpec, out *trafficPolicySpecIr) error {
	if spec.ConsistentHash == nil {
		return nil
	}
	ir, err := consistentHashIRFromSpec(spec.ConsistentHash)
	if err != nil {
		return err
	}
	out.consistentHash = ir
	return nil
}

func consistentHashIRFromSpec(spec *kgateway.ConsistentHash) (*consistentHashIR, error) {
	if spec == nil {
		return nil, nil
	}
	out := &consistentHashIR{
		disable: ptr.Deref(spec.Disable, false),
	}
	if out.disable {
		return out, nil
	}

	seenHeaders := make(map[string]struct{}, len(spec.Headers))
	for _, header := range spec.Headers {
		key := strings.ToLower(header.HeaderName)
		if _, ok := seenHeaders[key]; ok {
			continue
		}
		seenHeaders[key] = struct{}{}
		entry := consistentHashHeader{
			headerName: header.HeaderName,
			terminal:   ptr.Deref(header.Terminal, false),
		}
		if header.RegexRewrite != nil {
			entry.regexRewrite = &envoy_type_matcher_v3.RegexMatchAndSubstitute{
				Pattern: &envoy_type_matcher_v3.RegexMatcher{
					Regex: header.RegexRewrite.Pattern,
				},
				Substitution: header.RegexRewrite.Substitution,
			}
		}
		out.headers = append(out.headers, entry)
	}

	seenCookies := make(map[string]struct{}, len(spec.Cookies))
	for _, cookie := range spec.Cookies {
		if _, ok := seenCookies[cookie.Name]; ok {
			continue
		}
		seenCookies[cookie.Name] = struct{}{}
		entry := consistentHashCookie{
			name:     cookie.Name,
			path:     ptr.Deref(cookie.Path, ""),
			terminal: ptr.Deref(cookie.Terminal, false),
		}
		if cookie.TTL != nil {
			ttl, err := parseCookieTTL(*cookie.TTL)
			if err != nil {
				return nil, err
			}
			entry.ttl = ttl
		}
		for _, attr := range cookie.Attributes {
			entry.attributes = append(entry.attributes, consistentHashCookieAttribute{
				name:  attr.Name,
				value: attr.Value,
			})
		}
		out.cookies = append(out.cookies, entry)
	}

	seenQuery := make(map[string]struct{}, len(spec.QueryParameters))
	for _, query := range spec.QueryParameters {
		if _, ok := seenQuery[query.Name]; ok {
			continue
		}
		seenQuery[query.Name] = struct{}{}
		out.queryParameters = append(out.queryParameters, consistentHashQueryParameter{
			name:     query.Name,
			terminal: ptr.Deref(query.Terminal, false),
		})
	}

	seenFilterState := make(map[string]struct{}, len(spec.FilterState))
	for _, filterState := range spec.FilterState {
		if _, ok := seenFilterState[filterState.Key]; ok {
			continue
		}
		seenFilterState[filterState.Key] = struct{}{}
		out.filterState = append(out.filterState, consistentHashFilterState{
			key:      filterState.Key,
			terminal: ptr.Deref(filterState.Terminal, false),
		})
	}

	if spec.SourceIp != nil {
		out.sourceIp = &consistentHashSourceIP{terminal: ptr.Deref(spec.SourceIp.Terminal, false)}
	}
	return out, nil
}

func parseCookieTTL(raw string) (*durationpb.Duration, error) {
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return durationpb.New(time.Duration(seconds) * time.Second), nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid consistentHash cookie ttl %q: %w", raw, err)
	}
	return durationpb.New(d), nil
}

func (c *consistentHashIR) hashPolicies() []*envoyroutev3.RouteAction_HashPolicy {
	if c == nil || c.disable {
		return nil
	}
	if len(c.headers) == 0 && len(c.cookies) == 0 && len(c.queryParameters) == 0 && len(c.filterState) == 0 && c.sourceIp == nil {
		return []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(false)}
	}

	policies := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(c.headers)+len(c.cookies)+len(c.queryParameters)+len(c.filterState)+1)
	for _, header := range c.headers {
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: &envoyroutev3.RouteAction_HashPolicy_Header{
					HeaderName:   header.headerName,
					RegexRewrite: header.regexRewrite,
				},
			},
			Terminal: header.terminal,
		})
	}
	for _, cookie := range c.cookies {
		cookiePolicy := &envoyroutev3.RouteAction_HashPolicy_Cookie{
			Name: cookie.name,
			Ttl:  cookie.ttl,
			Path: cookie.path,
		}
		for _, attr := range cookie.attributes {
			cookiePolicy.Attributes = append(cookiePolicy.Attributes, &envoyroutev3.RouteAction_HashPolicy_CookieAttribute{
				Name:  attr.name,
				Value: attr.value,
			})
		}
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: cookiePolicy,
			},
			Terminal: cookie.terminal,
		})
	}
	for _, query := range c.queryParameters {
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
				QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{
					Name: query.name,
				},
			},
			Terminal: query.terminal,
		})
	}
	for _, filterState := range c.filterState {
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{
					Key: filterState.key,
				},
			},
			Terminal: filterState.terminal,
		})
	}
	if c.sourceIp != nil {
		policies = append(policies, sourceIPHashPolicy(c.sourceIp.terminal))
	}
	return policies
}

func sourceIPHashPolicy(terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_{
			ConnectionProperties: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties{
				SourceIp: true,
			},
		},
		Terminal: terminal,
	}
}

func applyConsistentHash(spec *consistentHashIR, action *envoyroutev3.RouteAction) {
	if spec == nil || action == nil {
		return
	}
	if spec.disable {
		action.HashPolicy = nil
		return
	}
	action.HashPolicy = spec.hashPolicies()
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
	if p1.spec.consistentHash == nil {
		p1.spec.consistentHash = p2.spec.consistentHash.clone()
		mergeOrigins.SetOne(consistentHashField, p2Ref, p2MergeOrigins)
		return
	}

	// p1 is the policy accumulated so far (higher priority under augmented strategies).
	// Overridable strategies treat p2 as the winner for scalars such as sourceIp and disable.
	preferP2 := opts.Strategy == policy.OverridableShallowMerge || opts.Strategy == policy.OverridableDeepMerge
	higher := p1.spec.consistentHash
	lower := p2.spec.consistentHash
	if preferP2 {
		higher = p2.spec.consistentHash
		lower = p1.spec.consistentHash
	}
	if higher.disable {
		if preferP2 {
			p1.spec.consistentHash = higher.clone()
			mergeOrigins.SetOne(consistentHashField, p2Ref, p2MergeOrigins)
		}
		return
	}

	merged := higher.clone()
	// A disabled lower-priority policy contributes no hash policies.
	// sourceIp is intentionally left as the higher-priority value, including when unset.
	if lower != nil && !lower.disable {
		merged.headers = appendUniqueHeaders(merged.headers, lower.headers)
		merged.cookies = appendUniqueCookies(merged.cookies, lower.cookies)
		merged.queryParameters = appendUniqueQueryParameters(merged.queryParameters, lower.queryParameters)
		merged.filterState = appendUniqueFilterState(merged.filterState, lower.filterState)
	}
	p1.spec.consistentHash = merged
	mergeOrigins.Append(consistentHashField, p2Ref, p2MergeOrigins)
}

func (c *consistentHashIR) clone() *consistentHashIR {
	if c == nil {
		return nil
	}
	out := *c
	out.headers = append([]consistentHashHeader(nil), c.headers...)
	out.cookies = make([]consistentHashCookie, len(c.cookies))
	for i, cookie := range c.cookies {
		out.cookies[i] = cookie
		out.cookies[i].attributes = append([]consistentHashCookieAttribute(nil), cookie.attributes...)
	}
	out.queryParameters = append([]consistentHashQueryParameter(nil), c.queryParameters...)
	out.filterState = append([]consistentHashFilterState(nil), c.filterState...)
	if c.sourceIp != nil {
		source := *c.sourceIp
		out.sourceIp = &source
	}
	return &out
}

func appendUniqueHeaders(dst, src []consistentHashHeader) []consistentHashHeader {
	seen := make(map[string]struct{}, len(dst))
	for _, header := range dst {
		seen[strings.ToLower(header.headerName)] = struct{}{}
	}
	for _, header := range src {
		key := strings.ToLower(header.headerName)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, header)
	}
	return dst
}

func appendUniqueCookies(dst, src []consistentHashCookie) []consistentHashCookie {
	seen := make(map[string]struct{}, len(dst))
	for _, cookie := range dst {
		seen[cookie.name] = struct{}{}
	}
	for _, cookie := range src {
		if _, ok := seen[cookie.name]; ok {
			continue
		}
		seen[cookie.name] = struct{}{}
		dst = append(dst, cookie)
	}
	return dst
}

func appendUniqueQueryParameters(dst, src []consistentHashQueryParameter) []consistentHashQueryParameter {
	seen := make(map[string]struct{}, len(dst))
	for _, query := range dst {
		seen[query.name] = struct{}{}
	}
	for _, query := range src {
		if _, ok := seen[query.name]; ok {
			continue
		}
		seen[query.name] = struct{}{}
		dst = append(dst, query)
	}
	return dst
}

func appendUniqueFilterState(dst, src []consistentHashFilterState) []consistentHashFilterState {
	seen := make(map[string]struct{}, len(dst))
	for _, filterState := range dst {
		seen[filterState.key] = struct{}{}
	}
	for _, filterState := range src {
		if _, ok := seen[filterState.key]; ok {
			continue
		}
		seen[filterState.key] = struct{}{}
		dst = append(dst, filterState)
	}
	return dst
}
