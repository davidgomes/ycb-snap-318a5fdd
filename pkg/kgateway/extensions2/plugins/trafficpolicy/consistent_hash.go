package trafficpolicy

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	envoyroutev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoy_type_matcher_v3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/regexutils"
)

const consistentHashMergeField = "consistentHash"

// consistentHashIR holds the route hash policies grouped by type so that policies can be
// merged per type and emitted in canonical order: headers, cookies, queryParameters,
// filterState, sourceIp.
type consistentHashIR struct {
	disable         bool
	headers         []*envoyroutev3.RouteAction_HashPolicy
	cookies         []*envoyroutev3.RouteAction_HashPolicy
	queryParameters []*envoyroutev3.RouteAction_HashPolicy
	filterState     []*envoyroutev3.RouteAction_HashPolicy
	sourceIP        *envoyroutev3.RouteAction_HashPolicy
}

var _ PolicySubIR = &consistentHashIR{}

func (c *consistentHashIR) Equals(other PolicySubIR) bool {
	otherConsistentHash, ok := other.(*consistentHashIR)
	if !ok {
		return false
	}
	if c == nil && otherConsistentHash == nil {
		return true
	}
	if c == nil || otherConsistentHash == nil {
		return false
	}
	if c.disable != otherConsistentHash.disable {
		return false
	}
	protoEqual := func(a, b *envoyroutev3.RouteAction_HashPolicy) bool { return proto.Equal(a, b) }
	return slices.EqualFunc(c.headers, otherConsistentHash.headers, protoEqual) &&
		slices.EqualFunc(c.cookies, otherConsistentHash.cookies, protoEqual) &&
		slices.EqualFunc(c.queryParameters, otherConsistentHash.queryParameters, protoEqual) &&
		slices.EqualFunc(c.filterState, otherConsistentHash.filterState, protoEqual) &&
		proto.Equal(c.sourceIP, otherConsistentHash.sourceIP)
}

// Validate performs PGV validation on the hash policies and verifies header regex rewrites.
func (c *consistentHashIR) Validate() error {
	if c == nil {
		return nil
	}
	for _, hp := range c.hashPolicies() {
		if err := hp.Validate(); err != nil {
			return err
		}
		if pattern := hp.GetHeader().GetRegexRewrite().GetPattern(); pattern != nil {
			if err := regexutils.CheckRegexString(pattern.GetRegex()); err != nil {
				return fmt.Errorf("invalid consistentHash header regexRewrite pattern: %w", err)
			}
		}
	}
	return nil
}

// hashPolicies returns the hash policies in canonical type order, or nil when disabled.
func (c *consistentHashIR) hashPolicies() []*envoyroutev3.RouteAction_HashPolicy {
	if c == nil || c.disable {
		return nil
	}
	out := make([]*envoyroutev3.RouteAction_HashPolicy, 0,
		len(c.headers)+len(c.cookies)+len(c.queryParameters)+len(c.filterState)+1)
	out = append(out, c.headers...)
	out = append(out, c.cookies...)
	out = append(out, c.queryParameters...)
	out = append(out, c.filterState...)
	if c.sourceIP != nil {
		out = append(out, c.sourceIP)
	}
	return out
}

// union returns a new IR containing the entries of c followed by the entries of other that
// are not already present in c. sourceIP is always taken from c.
func (c *consistentHashIR) union(other *consistentHashIR) *consistentHashIR {
	return &consistentHashIR{
		headers:         dedupHashPolicies(slices.Concat(c.headers, other.headers), headerHashKey),
		cookies:         dedupHashPolicies(slices.Concat(c.cookies, other.cookies), cookieHashKey),
		queryParameters: dedupHashPolicies(slices.Concat(c.queryParameters, other.queryParameters), queryParameterHashKey),
		filterState:     dedupHashPolicies(slices.Concat(c.filterState, other.filterState), filterStateHashKey),
		sourceIP:        c.sourceIP,
	}
}

// HTTP header names are case-insensitive
func headerHashKey(hp *envoyroutev3.RouteAction_HashPolicy) string {
	return strings.ToLower(hp.GetHeader().GetHeaderName())
}

func cookieHashKey(hp *envoyroutev3.RouteAction_HashPolicy) string {
	return hp.GetCookie().GetName()
}

func queryParameterHashKey(hp *envoyroutev3.RouteAction_HashPolicy) string {
	return hp.GetQueryParameter().GetName()
}

func filterStateHashKey(hp *envoyroutev3.RouteAction_HashPolicy) string {
	return hp.GetFilterState().GetKey()
}

// dedupHashPolicies returns a new slice keeping only the first occurrence of each key.
func dedupHashPolicies(
	policies []*envoyroutev3.RouteAction_HashPolicy,
	key func(*envoyroutev3.RouteAction_HashPolicy) string,
) []*envoyroutev3.RouteAction_HashPolicy {
	if len(policies) == 0 {
		return nil
	}
	seen := sets.New[string]()
	out := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(policies))
	for _, hp := range policies {
		k := key(hp)
		if seen.Has(k) {
			continue
		}
		seen.Insert(k)
		out = append(out, hp)
	}
	return out
}

// constructConsistentHash constructs the consistent hash policy IR from the policy specification.
func constructConsistentHash(spec kgateway.TrafficPolicySpec, out *trafficPolicySpecIr) error {
	in := spec.ConsistentHash
	if in == nil {
		return nil
	}
	if ptr.Deref(in.Disable, false) {
		out.consistentHash = &consistentHashIR{disable: true}
		return nil
	}

	var errs []error
	chIR := &consistentHashIR{}

	headers := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(in.Headers))
	for _, h := range in.Headers {
		header := &envoyroutev3.RouteAction_HashPolicy_Header{
			HeaderName: h.HeaderName,
		}
		if h.RegexRewrite != nil {
			header.RegexRewrite = &envoy_type_matcher_v3.RegexMatchAndSubstitute{
				Pattern: &envoy_type_matcher_v3.RegexMatcher{
					Regex: h.RegexRewrite.Pattern,
				},
				Substitution: h.RegexRewrite.Substitution,
			}
		}
		headers = append(headers, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{Header: header},
			Terminal:        ptr.Deref(h.Terminal, false),
		})
	}
	chIR.headers = dedupHashPolicies(headers, headerHashKey)

	cookies := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(in.Cookies))
	for _, c := range in.Cookies {
		cookie := &envoyroutev3.RouteAction_HashPolicy_Cookie{
			Name: c.Name,
			Path: ptr.Deref(c.Path, ""),
		}
		if c.TTL != nil {
			ttl, err := parseCookieTTL(*c.TTL)
			if err != nil {
				errs = append(errs, fmt.Errorf("invalid consistentHash cookie %q ttl: %w", c.Name, err))
				continue
			}
			cookie.Ttl = durationpb.New(ttl)
		}
		for _, attr := range c.Attributes {
			cookie.Attributes = append(cookie.Attributes, &envoyroutev3.RouteAction_HashPolicy_CookieAttribute{
				Name:  attr.Name,
				Value: ptr.Deref(attr.Value, ""),
			})
		}
		cookies = append(cookies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{Cookie: cookie},
			Terminal:        ptr.Deref(c.Terminal, false),
		})
	}
	chIR.cookies = dedupHashPolicies(cookies, cookieHashKey)

	queryParameters := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(in.QueryParameters))
	for _, q := range in.QueryParameters {
		queryParameters = append(queryParameters, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
				QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{
					Name: q.Name,
				},
			},
			Terminal: ptr.Deref(q.Terminal, false),
		})
	}
	chIR.queryParameters = dedupHashPolicies(queryParameters, queryParameterHashKey)

	filterState := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(in.FilterState))
	for _, f := range in.FilterState {
		filterState = append(filterState, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{
					Key: f.Key,
				},
			},
			Terminal: ptr.Deref(f.Terminal, false),
		})
	}
	chIR.filterState = dedupHashPolicies(filterState, filterStateHashKey)

	if in.SourceIP != nil {
		chIR.sourceIP = sourceIPHashPolicy(ptr.Deref(in.SourceIP.Terminal, false))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	if len(chIR.hashPolicies()) == 0 {
		chIR.sourceIP = sourceIPHashPolicy(false)
	}

	out.consistentHash = chIR
	return nil
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

// parseCookieTTL parses a Go duration string (e.g. "1h30m") or an integer number of seconds (e.g. "3600").
func parseCookieTTL(s string) (time.Duration, error) {
	if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
		if secs < 0 {
			return 0, fmt.Errorf("must not be negative: %s", s)
		}
		return time.Duration(secs) * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("must not be negative: %s", s)
	}
	return d, nil
}

// applyConsistentHash sets the route hash policies. A disabled policy clears any hash policies.
func applyConsistentHash(consistentHash *consistentHashIR, action *envoyroutev3.RouteAction) {
	if consistentHash == nil || action == nil {
		return
	}
	action.HashPolicy = consistentHash.hashPolicies()
}
