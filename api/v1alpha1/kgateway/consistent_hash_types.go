package kgateway

// ConsistentHash configures the hash policies used by hashing load balancers to select an upstream host.
//
// +kubebuilder:validation:XValidation:rule="!has(self.disable) || !self.disable || (!has(self.headers) && !has(self.cookies) && !has(self.queryParameters) && !has(self.filterState) && !has(self.sourceIp))",message="no other consistentHash fields may be set when disable is true"
type ConsistentHash struct {
	// Disable suppresses consistent hashing on the route, including any hash policies
	// inherited from policies applied at a broader scope in the config hierarchy.
	// When true, no other fields may be set.
	// +optional
	Disable *bool `json:"disable,omitempty"`

	// Headers specifies request headers whose values are used as components of the hash key.
	// Entries are unique by headerName (case-insensitive); only the first occurrence of a header is used.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Headers []ConsistentHashHeader `json:"headers,omitempty"`

	// Cookies specifies cookies whose values are used as components of the hash key.
	// Entries are unique by name; only the first occurrence of a cookie is used.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Cookies []ConsistentHashCookie `json:"cookies,omitempty"`

	// QueryParameters specifies URL query parameters whose values are used as components of the hash key.
	// Entries are unique by name; only the first occurrence of a query parameter is used.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	QueryParameters []ConsistentHashQueryParameter `json:"queryParameters,omitempty"`

	// FilterState specifies filter state objects whose values are used as components of the hash key.
	// Entries are unique by key; only the first occurrence of a key is used.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	FilterState []ConsistentHashFilterState `json:"filterState,omitempty"`

	// SourceIP specifies that the source IP address of the request is used as a component of the hash key.
	// +optional
	SourceIP *ConsistentHashSourceIP `json:"sourceIp,omitempty"`
}

// ConsistentHashHeader hashes the value of a request header.
type ConsistentHashHeader struct {
	// HeaderName is the name of the request header whose value is hashed.
	// If the header is not present, no hash is produced.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^:?[A-Za-z0-9!#$%&'*+\-.^_\x60|~]+$`
	HeaderName string `json:"headerName"`

	// RegexRewrite, if set, rewrites the header value using a regular expression before it is hashed.
	// +optional
	RegexRewrite *ConsistentHashRegexRewrite `json:"regexRewrite,omitempty"`

	// Terminal, if true and a hash key is produced by this entry, skips the evaluation of subsequent
	// hash policies and uses the key as it is.
	// This is useful for defining "fallback" policies and limiting the time spent generating hash keys.
	// Defaults to false.
	// +optional
	Terminal *bool `json:"terminal,omitempty"`
}

// ConsistentHashRegexRewrite rewrites a value using a regular expression.
type ConsistentHashRegexRewrite struct {
	// Pattern is the RE2 regular expression matched against the value.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Pattern string `json:"pattern"`

	// Substitution is the string that replaces the portions of the value matched by pattern.
	// It can include numbered capture groups from the pattern (e.g., \1, \2).
	// +required
	// +kubebuilder:validation:MaxLength=1024
	Substitution string `json:"substitution"`
}

// ConsistentHashCookie hashes the value of a cookie.
type ConsistentHashCookie struct {
	// Name is the name of the cookie whose value is hashed.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name"`

	// TTL, if set, causes a cookie with this time to live to be generated when the cookie is not present
	// in the request. A TTL of zero generates a session cookie.
	// It is specified either as a duration (e.g. "1h30m") or as an integer number of seconds (e.g. "3600").
	// +optional
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:XValidation:rule="self.matches('^([0-9]+|([0-9]+([.][0-9]+)?(h|m|s|ms|us|ns))+)$')",message="ttl must be a duration (e.g. 1h30m) or an integer number of seconds (e.g. 3600)"
	TTL *string `json:"ttl,omitempty"`

	// Path is the path of the generated cookie.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Path *string `json:"path,omitempty"`

	// Attributes are additional attributes of the generated cookie, such as SameSite, Secure or HttpOnly.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Attributes []ConsistentHashCookieAttribute `json:"attributes,omitempty"`

	// Terminal, if true and a hash key is produced by this entry, skips the evaluation of subsequent
	// hash policies and uses the key as it is.
	// Defaults to false.
	// +optional
	Terminal *bool `json:"terminal,omitempty"`
}

// ConsistentHashCookieAttribute is an attribute of a generated cookie.
type ConsistentHashCookieAttribute struct {
	// Name is the name of the cookie attribute, e.g. SameSite.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name"`

	// Value is the value of the cookie attribute, e.g. Strict.
	// If unset, the attribute is set without a value.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Value *string `json:"value,omitempty"`
}

// ConsistentHashQueryParameter hashes the value of a URL query parameter.
type ConsistentHashQueryParameter struct {
	// Name is the name of the URL query parameter whose value is hashed.
	// If the query parameter is not present, no hash is produced.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name"`

	// Terminal, if true and a hash key is produced by this entry, skips the evaluation of subsequent
	// hash policies and uses the key as it is.
	// Defaults to false.
	// +optional
	Terminal *bool `json:"terminal,omitempty"`
}

// ConsistentHashFilterState hashes the value of a filter state object.
type ConsistentHashFilterState struct {
	// Key is the name of the filter state object whose value is hashed.
	// The object must support being hashed; otherwise, no hash is produced.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Key string `json:"key"`

	// Terminal, if true and a hash key is produced by this entry, skips the evaluation of subsequent
	// hash policies and uses the key as it is.
	// Defaults to false.
	// +optional
	Terminal *bool `json:"terminal,omitempty"`
}

// ConsistentHashSourceIP hashes the source IP address of the request.
type ConsistentHashSourceIP struct {
	// Terminal, if true and a hash key is produced by this entry, skips the evaluation of subsequent
	// hash policies and uses the key as it is.
	// Defaults to false.
	// +optional
	Terminal *bool `json:"terminal,omitempty"`
}
