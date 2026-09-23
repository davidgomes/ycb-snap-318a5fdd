package trafficpolicy

import (
	"cmp"
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
	"k8s.io/utils/ptr"

	"github.com/kgateway-dev/kgateway/v2/api/v1alpha1/kgateway"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/regexutils"
)

type consistentHashIR struct {
	// disabled suppresses hash policies on the route, including those inherited from broader-scoped policies
	disabled bool
	// policies are unique by hashPolicyKey and sorted by hashPolicyKind
	policies []*envoyroutev3.RouteAction_HashPolicy
}

var _ PolicySubIR = &consistentHashIR{}

func (c *consistentHashIR) Equals(other PolicySubIR) bool {
	otherConsistentHash, ok := other.(*consistentHashIR)
	if !ok {
		return false
	}
	if c == nil || otherConsistentHash == nil {
		return c == nil && otherConsistentHash == nil
	}
	return c.disabled == otherConsistentHash.disabled &&
		slices.EqualFunc(c.policies, otherConsistentHash.policies, func(a, b *envoyroutev3.RouteAction_HashPolicy) bool {
			return proto.Equal(a, b)
		})
}

func (c *consistentHashIR) Validate() error {
	if c == nil {
		return nil
	}
	for _, p := range c.policies {
		if err := p.Validate(); err != nil {
			return err
		}
		if pattern := p.GetHeader().GetRegexRewrite().GetPattern(); pattern != nil {
			if err := regexutils.CheckRegexString(pattern.GetRegex()); err != nil {
				return fmt.Errorf("invalid consistentHash header regexRewrite pattern: %w", err)
			}
		}
	}
	return nil
}

// hashPolicyKind orders hash policies by type
type hashPolicyKind int

const (
	hashPolicyKindHeader hashPolicyKind = iota
	hashPolicyKindCookie
	hashPolicyKindQueryParameter
	hashPolicyKindFilterState
	hashPolicyKindSourceIP
)

// hashPolicyKey identifies a hash policy among the hash policies of a route
type hashPolicyKey struct {
	kind hashPolicyKind
	name string
}

func keyForHashPolicy(p *envoyroutev3.RouteAction_HashPolicy) hashPolicyKey {
	switch {
	case p.GetHeader() != nil:
		// header names are case-insensitive
		return hashPolicyKey{kind: hashPolicyKindHeader, name: strings.ToLower(p.GetHeader().GetHeaderName())}
	case p.GetCookie() != nil:
		return hashPolicyKey{kind: hashPolicyKindCookie, name: p.GetCookie().GetName()}
	case p.GetQueryParameter() != nil:
		return hashPolicyKey{kind: hashPolicyKindQueryParameter, name: p.GetQueryParameter().GetName()}
	case p.GetFilterState() != nil:
		return hashPolicyKey{kind: hashPolicyKindFilterState, name: p.GetFilterState().GetKey()}
	default:
		return hashPolicyKey{kind: hashPolicyKindSourceIP}
	}
}

// normalizeHashPolicies drops hash policies whose key was already seen, keeping the first occurrence,
// and stably sorts the remaining ones by kind.
func normalizeHashPolicies(policies []*envoyroutev3.RouteAction_HashPolicy) []*envoyroutev3.RouteAction_HashPolicy {
	seen := make(map[hashPolicyKey]struct{}, len(policies))
	out := make([]*envoyroutev3.RouteAction_HashPolicy, 0, len(policies))
	for _, p := range policies {
		key := keyForHashPolicy(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(a, b *envoyroutev3.RouteAction_HashPolicy) int {
		return cmp.Compare(keyForHashPolicy(a).kind, keyForHashPolicy(b).kind)
	})
	return out
}

// unionConsistentHash combines the hash policies of preferred and other, with the hash policies of
// preferred taking precedence. The source IP hash policy is only taken from preferred.
// If either is disabled, preferred is returned as is.
func unionConsistentHash(preferred, other *consistentHashIR) *consistentHashIR {
	if preferred.disabled || other.disabled {
		return preferred
	}
	policies := slices.Clone(preferred.policies)
	for _, p := range other.policies {
		if keyForHashPolicy(p).kind != hashPolicyKindSourceIP {
			policies = append(policies, p)
		}
	}
	return &consistentHashIR{policies: normalizeHashPolicies(policies)}
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

// parseCookieTTL parses a TTL specified either as a duration (e.g. "1h30m") or as an integer
// number of seconds (e.g. "3600").
func parseCookieTTL(ttl string) (*durationpb.Duration, error) {
	if seconds, err := strconv.ParseInt(ttl, 10, 64); err == nil {
		if seconds < 0 {
			return nil, fmt.Errorf("invalid ttl %q: must not be negative", ttl)
		}
		d := &durationpb.Duration{Seconds: seconds}
		if err := d.CheckValid(); err != nil {
			return nil, fmt.Errorf("invalid ttl %q: %w", ttl, err)
		}
		return d, nil
	}
	d, err := time.ParseDuration(ttl)
	if err != nil {
		return nil, fmt.Errorf("invalid ttl %q: must be a duration (e.g. 1h30m) or an integer number of seconds (e.g. 3600)", ttl)
	}
	if d < 0 {
		return nil, fmt.Errorf("invalid ttl %q: must not be negative", ttl)
	}
	return durationpb.New(d), nil
}

// constructConsistentHash constructs the consistent hash policy IR from the policy specification.
func constructConsistentHash(spec kgateway.TrafficPolicySpec, out *trafficPolicySpecIr) error {
	in := spec.ConsistentHash
	if in == nil {
		return nil
	}
	if ptr.Deref(in.Disable, false) {
		out.consistentHash = &consistentHashIR{disabled: true}
		return nil
	}

	var errs []error
	policies := make([]*envoyroutev3.RouteAction_HashPolicy, 0,
		len(in.Headers)+len(in.Cookies)+len(in.QueryParameters)+len(in.FilterState)+1)

	for _, h := range in.Headers {
		header := &envoyroutev3.RouteAction_HashPolicy_Header{
			HeaderName: h.HeaderName,
		}
		if h.RegexRewrite != nil {
			header.RegexRewrite = &envoy_type_matcher_v3.RegexMatchAndSubstitute{
				Pattern:      &envoy_type_matcher_v3.RegexMatcher{Regex: h.RegexRewrite.Pattern},
				Substitution: h.RegexRewrite.Substitution,
			}
		}
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Header_{Header: header},
			Terminal:        ptr.Deref(h.Terminal, false),
		})
	}

	for _, c := range in.Cookies {
		cookie := &envoyroutev3.RouteAction_HashPolicy_Cookie{
			Name: c.Name,
			Path: ptr.Deref(c.Path, ""),
		}
		if c.TTL != nil {
			ttl, err := parseCookieTTL(*c.TTL)
			if err != nil {
				errs = append(errs, fmt.Errorf("consistentHash cookie %q: %w", c.Name, err))
				continue
			}
			cookie.Ttl = ttl
		}
		for _, attr := range c.Attributes {
			cookie.Attributes = append(cookie.Attributes, &envoyroutev3.RouteAction_HashPolicy_CookieAttribute{
				Name:  attr.Name,
				Value: ptr.Deref(attr.Value, ""),
			})
		}
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_Cookie_{Cookie: cookie},
			Terminal:        ptr.Deref(c.Terminal, false),
		})
	}

	for _, q := range in.QueryParameters {
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_QueryParameter_{
				QueryParameter: &envoyroutev3.RouteAction_HashPolicy_QueryParameter{Name: q.Name},
			},
			Terminal: ptr.Deref(q.Terminal, false),
		})
	}

	for _, f := range in.FilterState {
		policies = append(policies, &envoyroutev3.RouteAction_HashPolicy{
			PolicySpecifier: &envoyroutev3.RouteAction_HashPolicy_FilterState_{
				FilterState: &envoyroutev3.RouteAction_HashPolicy_FilterState{Key: f.Key},
			},
			Terminal: ptr.Deref(f.Terminal, false),
		})
	}

	if in.SourceIP != nil {
		policies = append(policies, sourceIPHashPolicy(ptr.Deref(in.SourceIP.Terminal, false)))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	if len(policies) == 0 {
		policies = append(policies, sourceIPHashPolicy(false))
	}

	out.consistentHash = &consistentHashIR{
		policies: normalizeHashPolicies(policies),
	}
	return nil
}

// applyConsistentHash applies the consistent hash policies to the Envoy route action.
func applyConsistentHash(in *consistentHashIR, action *envoyroutev3.RouteAction) {
	if in == nil {
		return
	}
	if in.disabled {
		action.HashPolicy = nil
		return
	}
	action.HashPolicy = slices.Clone(in.policies)
}
