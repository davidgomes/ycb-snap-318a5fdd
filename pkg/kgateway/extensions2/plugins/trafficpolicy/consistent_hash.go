package trafficpolicy

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoy_type_matcher_v3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/pluginsdk/ir"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/regexutils"
)

type consistentHashIR struct {
	disable         bool
	headers         []consistentHashHeaderIR
	cookies         []consistentHashCookieIR
	queryParameters []consistentHashQueryParameterIR
	filterState     []consistentHashFilterStateIR
	// sourceIp is nil when the policy leaves source IP unset.
	// A nil sourceIp is distinct from a set sourceIp with terminal=false.
	sourceIp *consistentHashSourceIpIR
}

type consistentHashHeaderIR struct {
	headerName   string
	regexRewrite *consistentHashRegexRewriteIR
	terminal     bool
}

type consistentHashRegexRewriteIR struct {
	pattern      string
	substitution string
}

type consistentHashCookieIR struct {
	name       string
	ttl        *durationpb.Duration
	path       *string
	attributes []consistentHashCookieAttributeIR
	terminal   bool
}

type consistentHashCookieAttributeIR struct {
	name  string
	value string
}

type consistentHashQueryParameterIR struct {
	name     string
	terminal bool
}

type consistentHashFilterStateIR struct {
	key      string
	terminal bool
}

type consistentHashSourceIpIR struct {
	terminal bool
}

var _ PolicySubIR = &consistentHashIR{}

func (c *consistentHashIR) Equals(other PolicySubIR) bool {
	o, ok := other.(*consistentHashIR)
	if !ok {
		return false
	}
	if c == nil || o == nil {
		return c == nil && o == nil
	}
	if c.disable != o.disable || !sourceIPEqual(c.sourceIp, o.sourceIp) {
		return false
	}
	if len(c.headers) != len(o.headers) || len(c.cookies) != len(o.cookies) ||
		len(c.queryParameters) != len(o.queryParameters) || len(c.filterState) != len(o.filterState) {
		return false
	}
	for i := range c.headers {
		if !c.headers[i].equal(o.headers[i]) {
			return false
		}
	}
	for i := range c.cookies {
		if !c.cookies[i].equal(o.cookies[i]) {
			return false
		}
	}
	for i := range c.queryParameters {
		if c.queryParameters[i] != o.queryParameters[i] {
			return false
		}
	}
	for i := range c.filterState {
		if c.filterState[i] != o.filterState[i] {
			return false
		}
	}
	return true
}

func (h consistentHashHeaderIR) equal(o consistentHashHeaderIR) bool {
	if h.headerName != o.headerName || h.terminal != o.terminal {
		return false
	}
	if h.regexRewrite == nil || o.regexRewrite == nil {
		return h.regexRewrite == nil && o.regexRewrite == nil
	}
	return *h.regexRewrite == *o.regexRewrite
}

func (c consistentHashCookieIR) equal(o consistentHashCookieIR) bool {
	if c.name != o.name || c.terminal != o.terminal || !proto.Equal(c.ttl, o.ttl) {
		return false
	}
	if !stringPtrEqual(c.path, o.path) || len(c.attributes) != len(o.attributes) {
		return false
	}
	for i := range c.attributes {
		if c.attributes[i] != o.attributes[i] {
			return false
		}
	}
	return true
}

func sourceIPEqual(a, b *consistentHashSourceIpIR) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.terminal == b.terminal
}

func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Validate checks regex patterns used to rewrite header values before hashing.
func (c *consistentHashIR) Validate() error {
	if c == nil || c.disable {
		return nil
	}
	for _, header := range c.headers {
		if header.regexRewrite == nil {
			continue
		}
		if err := regexutils.CheckRegexString(header.regexRewrite.pattern); err != nil {
			return fmt.Errorf("invalid consistentHash header %q regex: %w", header.headerName, err)
		}
	}
	return nil
}

// constructConsistentHash translates spec.consistentHash into IR.
// Duplicate entries in each array are dropped, keeping the first occurrence.
// Header names are compared case-insensitively.
func constructConsistentHash(spec kgateway.TrafficPolicySpec, out *trafficPolicySpecIr) error {
	if spec.ConsistentHash == nil {
		return nil
	}
	built, err := translateConsistentHash(spec.ConsistentHash)
	if err != nil {
		return err
	}
	out.consistentHash = built
	return nil
}

func translateConsistentHash(spec *kgateway.ConsistentHash) (*consistentHashIR, error) {
	if spec == nil {
		return nil, nil
	}
	if spec.Disable != nil && *spec.Disable {
		// Disable wins even if other fields were set. API validation rejects that combination.
		return &consistentHashIR{disable: true}, nil
	}

	headers, err := translateConsistentHashHeaders(spec.Headers)
	if err != nil {
		return nil, err
	}
	cookies, err := translateConsistentHashCookies(spec.Cookies)
	if err != nil {
		return nil, err
	}
	queryParameters, err := translateConsistentHashQueryParameters(spec.QueryParameters)
	if err != nil {
		return nil, err
	}
	filterState, err := translateConsistentHashFilterState(spec.FilterState)
	if err != nil {
		return nil, err
	}

	ir := &consistentHashIR{
		headers:         headers,
		cookies:         cookies,
		queryParameters: queryParameters,
		filterState:     filterState,
	}
	if spec.SourceIp != nil {
		ir.sourceIp = &consistentHashSourceIpIR{terminal: boolVal(spec.SourceIp.Terminal)}
	}
	return ir, nil
}

func translateConsistentHashHeaders(in []kgateway.ConsistentHashHeader) ([]consistentHashHeaderIR, error) {
	out := make([]consistentHashHeaderIR, 0, len(in))
	for _, header := range in {
		if header.HeaderName == "" {
			return nil, fmt.Errorf("consistentHash headerName must not be empty")
		}
		translated := consistentHashHeaderIR{
			headerName: header.HeaderName,
			terminal:   boolVal(header.Terminal),
		}
		if header.RegexRewrite != nil {
			if header.RegexRewrite.Pattern == "" {
				return nil, fmt.Errorf("consistentHash header %q regex pattern must not be empty", header.HeaderName)
			}
			if err := regexutils.CheckRegexString(header.RegexRewrite.Pattern); err != nil {
				return nil, fmt.Errorf("invalid consistentHash header %q regex: %w", header.HeaderName, err)
			}
			translated.regexRewrite = &consistentHashRegexRewriteIR{
				pattern:      header.RegexRewrite.Pattern,
				substitution: header.RegexRewrite.Substitution,
			}
		}
		out = append(out, translated)
	}
	return dedupeHeaders(out), nil
}

func translateConsistentHashCookies(in []kgateway.ConsistentHashCookie) ([]consistentHashCookieIR, error) {
	out := make([]consistentHashCookieIR, 0, len(in))
	for _, cookie := range in {
		if cookie.Name == "" {
			return nil, fmt.Errorf("consistentHash cookie name must not be empty")
		}
		translated := consistentHashCookieIR{
			name:       cookie.Name,
			path:       cloneStringPtr(cookie.Path),
			terminal:   boolVal(cookie.Terminal),
			attributes: translateCookieAttributes(cookie.Attributes),
		}
		if cookie.TTL != nil {
			ttl, err := parseCookieTTL(*cookie.TTL)
			if err != nil {
				return nil, fmt.Errorf("consistentHash cookie %q: %w", cookie.Name, err)
			}
			translated.ttl = ttl
		}
		out = append(out, translated)
	}
	return dedupeCookies(out), nil
}

func translateCookieAttributes(in []kgateway.ConsistentHashCookieAttribute) []consistentHashCookieAttributeIR {
	if len(in) == 0 {
		return nil
	}
	out := make([]consistentHashCookieAttributeIR, len(in))
	for i, attr := range in {
		out[i] = consistentHashCookieAttributeIR{name: attr.Name, value: attr.Value}
	}
	return out
}

func translateConsistentHashQueryParameters(in []kgateway.ConsistentHashQueryParameter) ([]consistentHashQueryParameterIR, error) {
	out := make([]consistentHashQueryParameterIR, 0, len(in))
	for _, param := range in {
		if param.Name == "" {
			return nil, fmt.Errorf("consistentHash query parameter name must not be empty")
		}
		out = append(out, consistentHashQueryParameterIR{
			name:     param.Name,
			terminal: boolVal(param.Terminal),
		})
	}
	return dedupeQueryParameters(out), nil
}

func translateConsistentHashFilterState(in []kgateway.ConsistentHashFilterState) ([]consistentHashFilterStateIR, error) {
	out := make([]consistentHashFilterStateIR, 0, len(in))
	for _, state := range in {
		if state.Key == "" {
			return nil, fmt.Errorf("consistentHash filterState key must not be empty")
		}
		out = append(out, consistentHashFilterStateIR{
			key:      state.Key,
			terminal: boolVal(state.Terminal),
		})
	}
	return dedupeFilterState(out), nil
}

// parseCookieTTL accepts a Go duration (for example "1h30m") or a plain integer number of seconds (for example "3600").
func parseCookieTTL(raw string) (*durationpb.Duration, error) {
	if raw == "" {
		return nil, fmt.Errorf("ttl must not be empty")
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d < 0 {
			return nil, fmt.Errorf("ttl must not be negative")
		}
		return durationpb.New(d), nil
	}
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid ttl %q: must be a Go duration or integer seconds", raw)
	}
	if secs < 0 {
		return nil, fmt.Errorf("ttl must not be negative")
	}
	if secs > int64(math.MaxInt64/int64(time.Second)) {
		return nil, fmt.Errorf("ttl %q is too large", raw)
	}
	return durationpb.New(time.Duration(secs) * time.Second), nil
}

func boolVal(b *bool) bool {
	return b != nil && *b
}

func cloneStringPtr(in *string) *string {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

// hashPolicies builds Envoy hash policies in canonical type order.
// An empty, non-disabled policy defaults to a single source IP policy with terminal=false.
func (c *consistentHashIR) hashPolicies() []*envoyroutev3.RouteAction_HashPolicy {
	if c == nil || c.disable {
		return nil
	}
	policies := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(c.headers)+len(c.cookies)+len(c.queryParameters)+len(c.filterState)+1)
	for _, header := range c.headers {
		policies = append(policies, header.hashPolicy())
	}
	for _, cookie := range c.cookies {
		policies = append(policies, cookie.hashPolicy())
	}
	for _, param := range c.queryParameters {
		policies = append(policies, param.hashPolicy())
	}
	for _, state := range c.filterState {
		policies = append(policies, state.hashPolicy())
	}
	if c.sourceIp != nil {
		policies = append(policies, sourceIPHashPolicy(c.sourceIp.terminal))
	}
	if len(policies) == 0 {
		policies = append(policies, sourceIPHashPolicy(false))
	}
	return policies
}

func (h consistentHashHeaderIR) hashPolicy() *envoyroutev3.RouteAction_HashPolicy {
	header := &envoyroutev3.RouteAction_HashPolicy_Header{
		HeaderName: h.headerName,
	}
	if h.regexRewrite != nil {
		header.RegexRewrite = &envoy_type_matcher_v3.RegexMatchAndSubstitute{
			Pattern: &envoy_type_matcher_v3.RegexMatcher{
				Regex: h.regexRewrite.pattern,
			},
			Substitution: h.regexRewrite.substitution,
		}
	}
	return &envoyroutev3.RouteAction_HashPolicy{
		Terminal: h.terminal,
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{
			Header: header,
		},
	}
}

func (c consistentHashCookieIR) hashPolicy() *envoyroutev3.RouteAction_HashPolicy {
	cookie := &envoyroutev3.RouteAction_HashPolicy_Cookie{
		Name: c.name,
		Ttl:  c.ttl,
	}
	if c.path != nil {
		cookie.Path = *c.path
	}
	if len(c.attributes) > 0 {
		cookie.Attributes = make([]*envoyroutev3.RouteAction_HashPolicy_CookieAttribute, len(c.attributes))
		for i, attr := range c.attributes {
			cookie.Attributes[i] = &envoyroutev3.RouteAction_HashPolicy_CookieAttribute{
				Name:  attr.name,
				Value: attr.value,
			}
		}
	}
	return &envoyroutev3.RouteAction_HashPolicy{
		Terminal: c.terminal,
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{
			Cookie: cookie,
		},
	}
}

func (q consistentHashQueryParameterIR) hashPolicy() *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		Terminal: q.terminal,
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
			QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{
				Name: q.name,
			},
		},
	}
}

func (f consistentHashFilterStateIR) hashPolicy() *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		Terminal: f.terminal,
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
			FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{
				Key: f.key,
			},
		},
	}
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

// InheritConsistentHashPolicy returns a TrafficPolicy view that carries only consistentHash.
// Broader-scoped policies are merged onto routes through this view so other TrafficPolicy
// fields continue to apply at their original attachment point.
func InheritConsistentHashPolicy(in ir.PolicyIR) ir.PolicyIR {
	tp, ok := in.(*TrafficPolicy)
	if !ok || tp == nil || tp.spec.consistentHash == nil {
		return nil
	}
	clone := *tp
	clone.spec = trafficPolicySpecIr{
		consistentHash: tp.spec.consistentHash.clone(),
	}
	return &clone
}

func (c *consistentHashIR) clone() *consistentHashIR {
	if c == nil {
		return nil
	}
	out := &consistentHashIR{
		disable:         c.disable,
		headers:         cloneHeaders(c.headers),
		cookies:         cloneCookies(c.cookies),
		queryParameters: append([]consistentHashQueryParameterIR(nil), c.queryParameters...),
		filterState:     append([]consistentHashFilterStateIR(nil), c.filterState...),
	}
	if c.sourceIp != nil {
		src := *c.sourceIp
		out.sourceIp = &src
	}
	return out
}

func cloneHeaders(in []consistentHashHeaderIR) []consistentHashHeaderIR {
	if len(in) == 0 {
		return nil
	}
	out := make([]consistentHashHeaderIR, len(in))
	for i, header := range in {
		out[i] = header
		if header.regexRewrite != nil {
			rw := *header.regexRewrite
			out[i].regexRewrite = &rw
		}
	}
	return out
}

func cloneCookies(in []consistentHashCookieIR) []consistentHashCookieIR {
	if len(in) == 0 {
		return nil
	}
	out := make([]consistentHashCookieIR, len(in))
	for i, cookie := range in {
		out[i] = cookie
		out[i].path = cloneStringPtr(cookie.path)
		if cookie.ttl != nil {
			out[i].ttl = proto.Clone(cookie.ttl).(*durationpb.Duration)
		}
		if len(cookie.attributes) > 0 {
			out[i].attributes = append([]consistentHashCookieAttributeIR(nil), cookie.attributes...)
		}
	}
	return out
}

// unionConsistentHash unions array fields, keeping higher-priority entries first.
// sourceIp is taken entirely from the higher-priority policy, including when it is unset.
// The returned bool reports whether the lower-priority policy contributed an array entry.
func unionConsistentHash(higher, lower *consistentHashIR) (*consistentHashIR, bool) {
	if higher == nil {
		return lower.clone(), lower != nil
	}
	if higher.disable || lower == nil || lower.disable {
		return higher.clone(), false
	}
	headers, headersAdded := mergeHeaders(higher.headers, lower.headers)
	cookies, cookiesAdded := mergeCookies(higher.cookies, lower.cookies)
	queryParameters, queryAdded := mergeQueryParameters(higher.queryParameters, lower.queryParameters)
	filterState, filterAdded := mergeFilterState(higher.filterState, lower.filterState)
	merged := &consistentHashIR{
		headers:         headers,
		cookies:         cookies,
		queryParameters: queryParameters,
		filterState:     filterState,
	}
	if higher.sourceIp != nil {
		src := *higher.sourceIp
		merged.sourceIp = &src
	}
	return merged, headersAdded || cookiesAdded || queryAdded || filterAdded
}

func dedupeHeaders(in []consistentHashHeaderIR) []consistentHashHeaderIR {
	out, _ := mergeKeySlice(in, nil, func(h consistentHashHeaderIR) string {
		return strings.ToLower(h.headerName)
	})
	return out
}

func dedupeCookies(in []consistentHashCookieIR) []consistentHashCookieIR {
	out, _ := mergeKeySlice(in, nil, func(c consistentHashCookieIR) string {
		return c.name
	})
	return out
}

func dedupeQueryParameters(in []consistentHashQueryParameterIR) []consistentHashQueryParameterIR {
	out, _ := mergeKeySlice(in, nil, func(q consistentHashQueryParameterIR) string {
		return q.name
	})
	return out
}

func dedupeFilterState(in []consistentHashFilterStateIR) []consistentHashFilterStateIR {
	out, _ := mergeKeySlice(in, nil, func(f consistentHashFilterStateIR) string {
		return f.key
	})
	return out
}

func mergeHeaders(higher, lower []consistentHashHeaderIR) ([]consistentHashHeaderIR, bool) {
	return mergeKeyed(higher, lower, func(h consistentHashHeaderIR) string {
		return strings.ToLower(h.headerName)
	}, cloneHeaders)
}

func mergeCookies(higher, lower []consistentHashCookieIR) ([]consistentHashCookieIR, bool) {
	return mergeKeyed(higher, lower, func(c consistentHashCookieIR) string {
		return c.name
	}, cloneCookies)
}

func mergeQueryParameters(higher, lower []consistentHashQueryParameterIR) ([]consistentHashQueryParameterIR, bool) {
	return mergeKeyed(higher, lower, func(q consistentHashQueryParameterIR) string {
		return q.name
	}, func(in []consistentHashQueryParameterIR) []consistentHashQueryParameterIR {
		if len(in) == 0 {
			return nil
		}
		return append([]consistentHashQueryParameterIR(nil), in...)
	})
}

func mergeFilterState(higher, lower []consistentHashFilterStateIR) ([]consistentHashFilterStateIR, bool) {
	return mergeKeyed(higher, lower, func(f consistentHashFilterStateIR) string {
		return f.key
	}, func(in []consistentHashFilterStateIR) []consistentHashFilterStateIR {
		if len(in) == 0 {
			return nil
		}
		return append([]consistentHashFilterStateIR(nil), in...)
	})
}

func mergeKeyed[T any](higher, lower []T, key func(T) string, clone func([]T) []T) ([]T, bool) {
	merged, added := mergeKeySlice(higher, lower, key)
	// clone detaches nested slices and protos from both inputs.
	return clone(merged), added > 0
}

func mergeKeySlice[T any](higher, lower []T, key func(T) string) ([]T, int) {
	seen := make(map[string]struct{}, len(higher)+len(lower))
	out := make([]T, 0, len(higher)+len(lower))
	for _, item := range higher {
		k := key(item)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, item)
	}
	added := 0
	for _, item := range lower {
		k := key(item)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, item)
		added++
	}
	if len(out) == 0 {
		return nil, 0
	}
	return out, added
}
