package kgateway

// ConsistentHash configures request hashing for consistent-hash load balancing.
// +kubebuilder:validation:XValidation:rule="!has(self.disable) || !self.disable || (!has(self.headers) && !has(self.cookies) && !has(self.queryParameters) && !has(self.filterState) && !has(self.sourceIp))",message="no other fields may be set when disable is true"
type ConsistentHash struct {
	// Disable suppresses consistent hashing on the route, including hash policies
	// inherited from broader-scoped policies.
	// +optional
	Disable *bool `json:"disable,omitempty"`

	// Headers hashes on request header values.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Headers []ConsistentHashHeader `json:"headers,omitempty"`

	// Cookies hashes on cookie values.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Cookies []ConsistentHashCookie `json:"cookies,omitempty"`

	// QueryParameters hashes on query parameter values.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	QueryParameters []ConsistentHashQueryParameter `json:"queryParameters,omitempty"`

	// FilterState hashes on filter state objects.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	FilterState []ConsistentHashFilterState `json:"filterState,omitempty"`

	// SourceIP hashes on the downstream source IP address.
	// +optional
	SourceIP *ConsistentHashSourceIP `json:"sourceIp,omitempty"`
}

// ConsistentHashHeader hashes on a request header.
type ConsistentHashHeader struct {
	// HeaderName is the name of the request header.
	// +required
	// +kubebuilder:validation:MinLength=1
	HeaderName string `json:"headerName"`

	// RegexRewrite rewrites the header value using a regex before hashing.
	// +optional
	RegexRewrite *RegexMatchAndSubstitute `json:"regexRewrite,omitempty"`

	// Terminal short-circuits hash computation if this policy produces a hash.
	// +optional
	Terminal *bool `json:"terminal,omitempty"`
}

// RegexMatchAndSubstitute rewrites a value using a regex pattern.
type RegexMatchAndSubstitute struct {
	// Pattern is the regex to match.
	// +required
	// +kubebuilder:validation:MinLength=1
	Pattern string `json:"pattern"`

	// Substitution is the replacement string, which may reference capture groups.
	// +required
	Substitution string `json:"substitution"`
}

// ConsistentHashCookie hashes on a cookie. If the cookie is absent and TTL is set,
// Envoy generates the cookie.
type ConsistentHashCookie struct {
	// Name is the cookie name.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// TTL of the generated cookie. Accepts a Go duration (e.g. "1h30m") or integer seconds (e.g. "3600").
	// +optional
	// +kubebuilder:validation:Pattern=`^([0-9]+|([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+)$`
	TTL *string `json:"ttl,omitempty"`

	// Path of the generated cookie.
	// +optional
	Path *string `json:"path,omitempty"`

	// Attributes of the generated cookie, such as SameSite or Secure.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Attributes []CookieAttribute `json:"attributes,omitempty"`

	// Terminal short-circuits hash computation if this policy produces a hash.
	// +optional
	Terminal *bool `json:"terminal,omitempty"`
}

// CookieAttribute is a name/value cookie attribute.
type CookieAttribute struct {
	// Name of the attribute.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Value of the attribute.
	// +optional
	Value string `json:"value,omitempty"`
}

// ConsistentHashQueryParameter hashes on a query parameter.
type ConsistentHashQueryParameter struct {
	// Name of the query parameter.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Terminal short-circuits hash computation if this policy produces a hash.
	// +optional
	Terminal *bool `json:"terminal,omitempty"`
}

// ConsistentHashFilterState hashes on a filter state object.
type ConsistentHashFilterState struct {
	// Key of the filter state object.
	// +required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// Terminal short-circuits hash computation if this policy produces a hash.
	// +optional
	Terminal *bool `json:"terminal,omitempty"`
}

// ConsistentHashSourceIP hashes on the downstream source IP.
type ConsistentHashSourceIP struct {
	// Terminal short-circuits hash computation if this policy produces a hash.
	// +optional
	Terminal *bool `json:"terminal,omitempty"`
}
