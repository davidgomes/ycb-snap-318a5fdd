package trafficpolicy

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoy_type_matcher_v3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/policy"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/regexutils"
)

const consistentHashMergeField = "consistentHash"

// consistentHashIR is the route-level consistent hash configuration.
// Hash policies are built in canonical type order when applied to a route:
// headers, cookies, query parameters, filter state, then source IP.
type consistentHashIR struct {
	disable         bool
	headers         []consistentHashHeaderIR
	cookies         []consistentHashCookieIR
	queryParameters []consistentHashQueryParameterIR
	filterState     []consistentHashFilterStateIR
	// sourceIP is the higher-priority policy's source IP component.
	// A nil value means the field was unset and must not be filled from a lower-priority policy.
	sourceIP *consistentHashSourceIPIR
}

type consistentHashHeaderIR struct {
	headerName      string
	regexPattern    string
	regexSubstitute string
	hasRegex        bool
	terminal        bool
}

type consistentHashCookieAttributeIR struct {
	name  string
	value string
}

type consistentHashCookieIR struct {
	name       string
	ttl        *time.Duration
	path       *string
	attributes []consistentHashCookieAttributeIR
	terminal   bool
}

type consistentHashQueryParameterIR struct {
	name     string
	terminal bool
}

type consistentHashFilterStateIR struct {
	key      string
	terminal bool
}

type consistentHashSourceIPIR struct {
	terminal bool
}

var _ PolicySubIR = &consistentHashIR{}

func (c *consistentHashIR) Equals(other PolicySubIR) bool {
	otherHash, ok := other.(*consistentHashIR)
	if !ok {
		return false
	}
	if c == nil && otherHash == nil {
		return true
	}
	if c == nil || otherHash == nil {
		return false
	}
	if c.disable != otherHash.disable || !c.sourceIP.equal(otherHash.sourceIP) {
		return false
	}
	if len(c.headers) != len(otherHash.headers) ||
		len(c.cookies) != len(otherHash.cookies) ||
		len(c.queryParameters) != len(otherHash.queryParameters) ||
		len(c.filterState) != len(otherHash.filterState) {
		return false
	}
	for i := range c.headers {
		if c.headers[i] != otherHash.headers[i] {
			return false
		}
	}
	for i := range c.cookies {
		if !c.cookies[i].equal(otherHash.cookies[i]) {
			return false
		}
	}
	for i := range c.queryParameters {
		if c.queryParameters[i] != otherHash.queryParameters[i] {
			return false
		}
	}
	for i := range c.filterState {
		if c.filterState[i] != otherHash.filterState[i] {
			return false
		}
	}
	return true
}

func (c *consistentHashCookieIR) equal(other consistentHashCookieIR) bool {
	if c.name != other.name || c.terminal != other.terminal || len(c.attributes) != len(other.attributes) {
		return false
	}
	if !durationPtrEqual(c.ttl, other.ttl) || !stringPtrEqual(c.path, other.path) {
		return false
	}
	for i := range c.attributes {
		if c.attributes[i] != other.attributes[i] {
			return false
		}
	}
	return true
}

func (s *consistentHashSourceIPIR) equal(other *consistentHashSourceIPIR) bool {
	if s == nil || other == nil {
		return s == nil && other == nil
	}
	return s.terminal == other.terminal
}

func durationPtrEqual(a, b *time.Duration) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Validate checks regex patterns used to rewrite header values before hashing.
func (c *consistentHashIR) Validate() error {
	if c == nil {
		return nil
	}
	for _, header := range c.headers {
		if !header.hasRegex {
			continue
		}
		if err := regexutils.CheckRegexString(header.regexPattern); err != nil {
			return fmt.Errorf("consistentHash header %q regex: %w", header.headerName, err)
		}
	}
	return nil
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
	disable := spec.Disable != nil && *spec.Disable
	if disable && consistentHashHasOtherFields(spec) {
		return nil, fmt.Errorf("consistentHash: no other fields may be set when disable is true")
	}

	headers, err := dedupeConsistentHashHeaders(spec.Headers)
	if err != nil {
		return nil, err
	}
	cookies, err := dedupeConsistentHashCookies(spec.Cookies)
	if err != nil {
		return nil, err
	}

	ir := &consistentHashIR{
		disable:         disable,
		headers:         headers,
		cookies:         cookies,
		queryParameters: dedupeConsistentHashQueryParameters(spec.QueryParameters),
		filterState:     dedupeConsistentHashFilterState(spec.FilterState),
	}
	if spec.SourceIp != nil {
		ir.sourceIP = &consistentHashSourceIPIR{terminal: boolVal(spec.SourceIp.Terminal)}
	}
	return ir, nil
}

func consistentHashHasOtherFields(spec *kgateway.ConsistentHash) bool {
	return len(spec.Headers) > 0 ||
		len(spec.Cookies) > 0 ||
		len(spec.QueryParameters) > 0 ||
		len(spec.FilterState) > 0 ||
		spec.SourceIp != nil
}

func dedupeConsistentHashHeaders(in []kgateway.ConsistentHashHeader) ([]consistentHashHeaderIR, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]consistentHashHeaderIR, 0, len(in))
	for _, header := range in {
		key := strings.ToLower(header.HeaderName)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		built := consistentHashHeaderIR{
			headerName: header.HeaderName,
			terminal:   boolVal(header.Terminal),
		}
		if header.RegexRewrite != nil {
			if err := regexutils.CheckRegexString(header.RegexRewrite.Pattern); err != nil {
				return nil, fmt.Errorf("consistentHash header %q regex: %w", header.HeaderName, err)
			}
			built.hasRegex = true
			built.regexPattern = header.RegexRewrite.Pattern
			built.regexSubstitute = header.RegexRewrite.Substitution
		}
		out = append(out, built)
	}
	return out, nil
}

func dedupeConsistentHashCookies(in []kgateway.ConsistentHashCookie) ([]consistentHashCookieIR, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]consistentHashCookieIR, 0, len(in))
	for _, cookie := range in {
		if _, ok := seen[cookie.Name]; ok {
			continue
		}
		seen[cookie.Name] = struct{}{}
		built := consistentHashCookieIR{
			name:     cookie.Name,
			path:     cookie.Path,
			terminal: boolVal(cookie.Terminal),
		}
		if cookie.TTL != nil {
			ttl, err := parseConsistentHashCookieTTL(*cookie.TTL)
			if err != nil {
				return nil, err
			}
			built.ttl = &ttl
		}
		if len(cookie.Attributes) > 0 {
			built.attributes = make([]consistentHashCookieAttributeIR, 0, len(cookie.Attributes))
			for _, attr := range cookie.Attributes {
				built.attributes = append(built.attributes, consistentHashCookieAttributeIR{
					name:  attr.Name,
					value: attr.Value,
				})
			}
		}
		out = append(out, built)
	}
	return out, nil
}

func dedupeConsistentHashQueryParameters(in []kgateway.ConsistentHashQueryParameter) []consistentHashQueryParameterIR {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]consistentHashQueryParameterIR, 0, len(in))
	for _, param := range in {
		if _, ok := seen[param.Name]; ok {
			continue
		}
		seen[param.Name] = struct{}{}
		out = append(out, consistentHashQueryParameterIR{
			name:     param.Name,
			terminal: boolVal(param.Terminal),
		})
	}
	return out
}

func dedupeConsistentHashFilterState(in []kgateway.ConsistentHashFilterState) []consistentHashFilterStateIR {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]consistentHashFilterStateIR, 0, len(in))
	for _, state := range in {
		if _, ok := seen[state.Key]; ok {
			continue
		}
		seen[state.Key] = struct{}{}
		out = append(out, consistentHashFilterStateIR{
			key:      state.Key,
			terminal: boolVal(state.Terminal),
		})
	}
	return out
}

// parseConsistentHashCookieTTL accepts a Go duration (for example "1h30m") or an integer number of seconds.
func parseConsistentHashCookieTTL(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("consistentHash cookie ttl must not be empty")
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d, nil
	}
	secs, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("consistentHash cookie ttl %q must be a Go duration (for example \"1h30m\") or an integer number of seconds", raw)
	}
	maxSecs := uint64(math.MaxInt64 / int64(time.Second))
	if secs > maxSecs {
		return 0, fmt.Errorf("consistentHash cookie ttl %q is too large", raw)
	}
	return time.Duration(secs) * time.Second, nil
}

func boolVal(v *bool) bool {
	return v != nil && *v
}

func (c *consistentHashIR) clone() *consistentHashIR {
	if c == nil {
		return nil
	}
	out := *c
	out.headers = append([]consistentHashHeaderIR(nil), c.headers...)
	if len(c.cookies) > 0 {
		out.cookies = make([]consistentHashCookieIR, len(c.cookies))
		for i, cookie := range c.cookies {
			out.cookies[i] = cookie
			out.cookies[i].attributes = append([]consistentHashCookieAttributeIR(nil), cookie.attributes...)
			if cookie.ttl != nil {
				ttl := *cookie.ttl
				out.cookies[i].ttl = &ttl
			}
			if cookie.path != nil {
				path := *cookie.path
				out.cookies[i].path = &path
			}
		}
	}
	out.queryParameters = append([]consistentHashQueryParameterIR(nil), c.queryParameters...)
	out.filterState = append([]consistentHashFilterStateIR(nil), c.filterState...)
	if c.sourceIP != nil {
		sourceIP := *c.sourceIP
		out.sourceIP = &sourceIP
	}
	return &out
}

// unionFrom appends entries from lower that are not already present.
// sourceIP is left unchanged so an unset higher-priority value is preserved.
// It returns whether lower contributed at least one array entry.
func (c *consistentHashIR) unionFrom(lower *consistentHashIR) bool {
	if c == nil || lower == nil || lower.disable {
		return false
	}
	added := false
	headerSeen := make(map[string]struct{}, len(c.headers))
	for _, header := range c.headers {
		headerSeen[strings.ToLower(header.headerName)] = struct{}{}
	}
	for _, header := range lower.headers {
		key := strings.ToLower(header.headerName)
		if _, ok := headerSeen[key]; ok {
			continue
		}
		headerSeen[key] = struct{}{}
		c.headers = append(c.headers, header)
		added = true
	}

	cookieSeen := make(map[string]struct{}, len(c.cookies))
	for _, cookie := range c.cookies {
		cookieSeen[cookie.name] = struct{}{}
	}
	for _, cookie := range lower.cookies {
		if _, ok := cookieSeen[cookie.name]; ok {
			continue
		}
		cookieSeen[cookie.name] = struct{}{}
		cloned := cookie
		cloned.attributes = append([]consistentHashCookieAttributeIR(nil), cookie.attributes...)
		if cookie.ttl != nil {
			ttl := *cookie.ttl
			cloned.ttl = &ttl
		}
		if cookie.path != nil {
			path := *cookie.path
			cloned.path = &path
		}
		c.cookies = append(c.cookies, cloned)
		added = true
	}

	querySeen := make(map[string]struct{}, len(c.queryParameters))
	for _, param := range c.queryParameters {
		querySeen[param.name] = struct{}{}
	}
	for _, param := range lower.queryParameters {
		if _, ok := querySeen[param.name]; ok {
			continue
		}
		querySeen[param.name] = struct{}{}
		c.queryParameters = append(c.queryParameters, param)
		added = true
	}

	filterSeen := make(map[string]struct{}, len(c.filterState))
	for _, state := range c.filterState {
		filterSeen[state.key] = struct{}{}
	}
	for _, state := range lower.filterState {
		if _, ok := filterSeen[state.key]; ok {
			continue
		}
		filterSeen[state.key] = struct{}{}
		c.filterState = append(c.filterState, state)
		added = true
	}
	return added
}

func (c *consistentHashIR) hasSubfields() bool {
	return c != nil && (len(c.headers) > 0 || len(c.cookies) > 0 || len(c.queryParameters) > 0 || len(c.filterState) > 0 || c.sourceIP != nil)
}

// hashPolicies returns Envoy hash policies in canonical type order.
// An empty configuration becomes a single source IP policy with terminal false.
// disable produces no policies.
func (c *consistentHashIR) hashPolicies() []*envoyroutev3.RouteAction_HashPolicy {
	if c == nil || c.disable {
		return nil
	}
	if !c.hasSubfields() {
		return []*envoyroutev3.RouteAction_HashPolicy{sourceIPHashPolicy(false)}
	}

	policies := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(c.headers)+len(c.cookies)+len(c.queryParameters)+len(c.filterState)+1)
	for _, header := range c.headers {
		headerPolicy := &envoyroutev3.RouteAction_HashPolicy_Header{
			HeaderName: header.headerName,
		}
		if header.hasRegex {
			headerPolicy.RegexRewrite = &envoy_type_matcher_v3.RegexMatchAndSubstitute{
				Pattern: &envoy_type_matcher_v3.RegexMatcher{
					Regex: header.regexPattern,
				},
				Substitution: header.regexSubstitute,
			}
		}
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: header.terminal,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
				Header: headerPolicy,
			},
		})
	}
	for _, cookie := range c.cookies {
		cookiePolicy := &envoyroutev3.RouteAction_HashPolicy_Cookie{
			Name: cookie.name,
		}
		if cookie.ttl != nil {
			cookiePolicy.Ttl = durationpb.New(*cookie.ttl)
		}
		if cookie.path != nil {
			cookiePolicy.Path = *cookie.path
		}
		if len(cookie.attributes) > 0 {
			cookiePolicy.Attributes = make([]*envoyroutev3.RouteAction_HashPolicy_CookieAttribute, 0, len(cookie.attributes))
			for _, attr := range cookie.attributes {
				cookiePolicy.Attributes = append(cookiePolicy.Attributes, &envoyroutev3.RouteAction_HashPolicy_CookieAttribute{
					Name:  attr.name,
					Value: attr.value,
				})
			}
		}
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: cookie.terminal,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
				Cookie: cookiePolicy,
			},
		})
	}
	for _, param := range c.queryParameters {
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: param.terminal,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
				QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{
					Name: param.name,
				},
			},
		})
	}
	for _, state := range c.filterState {
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			Terminal: state.terminal,
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{
					Key: state.key,
				},
			},
		})
	}
	if c.sourceIP != nil {
		policies = append(policies, sourceIPHashPolicy(c.sourceIP.terminal))
	}
	return policies
}

func sourceIPHashPolicy(terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		Terminal: terminal,
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_{
			ConnectionProperties: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties{
				SourceIp: true,
			},
		},
	}
}

func applyConsistentHash(consistentHash *consistentHashIR, action *envoyroutev3.RouteAction) {
	if consistentHash == nil || action == nil {
		return
	}
	if consistentHash.disable {
		action.HashPolicy = nil
		return
	}
	action.HashPolicy = consistentHash.hashPolicies()
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
		mergeOrigins.SetOne(consistentHashMergeField, p2Ref, p2MergeOrigins)
		return
	}

	preferP2 := false
	switch opts.Strategy {
	case policy.AugmentedShallowMerge, policy.AugmentedDeepMerge:
		preferP2 = false
	case policy.OverridableShallowMerge, policy.OverridableDeepMerge:
		preferP2 = true
	default:
		logger.Warn("unsupported merge strategy for policy", "strategy", opts.Strategy, "policy", p2Ref, "field", consistentHashMergeField)
	}

	higher, lower := p1.spec.consistentHash, p2.spec.consistentHash
	if preferP2 {
		higher, lower = lower, higher
	}

	// A higher-priority disable suppresses every lower-priority hash policy.
	if higher.disable {
		if preferP2 {
			p1.spec.consistentHash = higher.clone()
			mergeOrigins.SetOne(consistentHashMergeField, p2Ref, p2MergeOrigins)
		}
		return
	}

	merged := higher.clone()
	// sourceIP stays at the higher-priority value, including when that value is unset.
	added := merged.unionFrom(lower)
	p1.spec.consistentHash = merged
	if preferP2 {
		if added {
			mergeOrigins.Append(consistentHashMergeField, p2Ref, p2MergeOrigins)
			return
		}
		mergeOrigins.SetOne(consistentHashMergeField, p2Ref, p2MergeOrigins)
		return
	}
	if added {
		mergeOrigins.Append(consistentHashMergeField, p2Ref, p2MergeOrigins)
	}
}
