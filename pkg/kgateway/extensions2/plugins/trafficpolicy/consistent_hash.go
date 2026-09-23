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

type consistentHashIR struct {
	// disable suppresses all hash policies on the route, including inherited ones.
	disable bool
	// hashPolicies are kept in canonical type order: headers, cookies,
	// queryParameters, filterState, sourceIp.
	hashPolicies []*envoyroutev3.RouteAction_HashPolicy
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
	return slices.EqualFunc(c.hashPolicies, otherConsistentHash.hashPolicies, func(a, b *envoyroutev3.RouteAction_HashPolicy) bool {
		return proto.Equal(a, b)
	})
}

func (c *consistentHashIR) Validate() error {
	if c == nil {
		return nil
	}
	var errs []error
	for _, hp := range c.hashPolicies {
		if err := hp.ValidateAll(); err != nil {
			errs = append(errs, err)
		}
		if re := hp.GetHeader().GetRegexRewrite().GetPattern().GetRegex(); re != "" {
			if err := regexutils.CheckRegexString(re); err != nil {
				errs = append(errs, fmt.Errorf("invalid consistentHash header regexRewrite pattern: %w", err))
			}
		}
	}
	return errors.Join(errs...)
}

// constructConsistentHash constructs the consistent hash policy IR from the policy specification.
func constructConsistentHash(spec kgateway.TrafficPolicySpec, out *trafficPolicySpecIr) error {
	ch := spec.ConsistentHash
	if ch == nil {
		return nil
	}
	if ptr.Deref(ch.Disable, false) {
		out.consistentHash = &consistentHashIR{disable: true}
		return nil
	}

	var errs []error
	var policies []*envoyroutev3.RouteAction_HashPolicy

	for _, h := range ch.Headers {
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
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{Header: header},
			Terminal:        ptr.Deref(h.Terminal, false),
		})
	}

	for _, c := range ch.Cookies {
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
				Value: attr.Value,
			})
		}
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{Cookie: cookie},
			Terminal:        ptr.Deref(c.Terminal, false),
		})
	}

	for _, q := range ch.QueryParameters {
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
				QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{Name: q.Name},
			},
			Terminal: ptr.Deref(q.Terminal, false),
		})
	}

	for _, f := range ch.FilterState {
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: f.Key},
			},
			Terminal: ptr.Deref(f.Terminal, false),
		})
	}

	sourceIP := ch.SourceIP
	if sourceIP == nil && len(ch.Headers) == 0 && len(ch.Cookies) == 0 &&
		len(ch.QueryParameters) == 0 && len(ch.FilterState) == 0 {
		sourceIP = &kgateway.ConsistentHashSourceIP{}
	}
	if sourceIP != nil {
		policies = append(policies, sourceIPHashPolicy(ptr.Deref(sourceIP.Terminal, false)))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	out.consistentHash = &consistentHashIR{
		hashPolicies: dedupeHashPolicies(policies),
	}
	return nil
}

func sourceIPHashPolicy(terminal bool) *envoyroutev3.RouteAction_HashPolicy {
	return &envoyroutev3.RouteAction_HashPolicy{
		PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_{
			ConnectionProperties: &envoyroutev3.RouteAction_HashPolicy_ConnectionProperties{SourceIp: true},
		},
		Terminal: terminal,
	}
}

// parseCookieTTL parses a TTL given as a Go duration string or an integer number of seconds.
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

// hashPolicyRank returns the canonical ordering rank of the hash policy type.
func hashPolicyRank(hp *envoyroutev3.RouteAction_HashPolicy) int {
	switch hp.GetPolicySpecifier().(type) {
	case *envoyroutev3.RouteAction_HashPolicy_Header_:
		return 0
	case *envoyroutev3.RouteAction_HashPolicy_Cookie_:
		return 1
	case *envoyroutev3.RouteAction_HashPolicy_QueryParameter_:
		return 2
	case *envoyroutev3.RouteAction_HashPolicy_FilterState_:
		return 3
	case *envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_:
		return 4
	default:
		return 5
	}
}

// hashPolicyKey returns the deduplication key of the hash policy, unique across types.
func hashPolicyKey(hp *envoyroutev3.RouteAction_HashPolicy) string {
	switch s := hp.GetPolicySpecifier().(type) {
	case *envoyroutev3.RouteAction_HashPolicy_Header_:
		return "header/" + strings.ToLower(s.Header.GetHeaderName())
	case *envoyroutev3.RouteAction_HashPolicy_Cookie_:
		return "cookie/" + s.Cookie.GetName()
	case *envoyroutev3.RouteAction_HashPolicy_QueryParameter_:
		return "query/" + s.QueryParameter.GetName()
	case *envoyroutev3.RouteAction_HashPolicy_FilterState_:
		return "filterState/" + s.FilterState.GetKey()
	case *envoyroutev3.RouteAction_HashPolicy_ConnectionProperties_:
		return "sourceIp"
	default:
		return ""
	}
}

// dedupeHashPolicies keeps the first occurrence of each hash policy key and
// sorts the result into canonical type order, preserving relative order within a type.
func dedupeHashPolicies(policies []*envoyroutev3.RouteAction_HashPolicy) []*envoyroutev3.RouteAction_HashPolicy {
	if len(policies) == 0 {
		return nil
	}
	seen := sets.New[string]()
	out := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(policies))
	for _, hp := range policies {
		key := hashPolicyKey(hp)
		if seen.Has(key) {
			continue
		}
		seen.Insert(key)
		out = append(out, hp)
	}
	slices.SortStableFunc(out, func(a, b *envoyroutev3.RouteAction_HashPolicy) int {
		return hashPolicyRank(a) - hashPolicyRank(b)
	})
	return out
}

// mergeConsistentHashIR merges two consistent hash IRs, where higher takes priority over lower.
// List entries are unioned with higher's entries first, while sourceIp is taken from higher only.
// The inputs are never mutated.
func mergeConsistentHashIR(higher, lower *consistentHashIR) *consistentHashIR {
	if higher == nil {
		return lower
	}
	if lower == nil || higher.disable || lower.disable {
		return higher
	}
	merged := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(higher.hashPolicies)+len(lower.hashPolicies))
	merged = append(merged, higher.hashPolicies...)
	for _, hp := range lower.hashPolicies {
		if hp.GetConnectionProperties() != nil {
			continue
		}
		merged = append(merged, hp)
	}
	return &consistentHashIR{
		hashPolicies: dedupeHashPolicies(merged),
	}
}

// applyConsistentHash sets the hash policies on the route action.
func applyConsistentHash(ch *consistentHashIR, action *envoyroutev3.RouteAction) {
	if ch == nil || action == nil {
		return
	}
	if ch.disable {
		action.HashPolicy = nil
		return
	}
	action.HashPolicy = slices.Clone(ch.hashPolicies)
}
