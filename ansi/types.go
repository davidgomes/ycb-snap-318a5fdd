package ansi

// TokenType identifies the kind of an ANSI token.
type TokenType int

const (
	// TokenText is visible text content.
	TokenText TokenType = iota
	// TokenSGR is a Select Graphic Rendition CSI sequence.
	TokenSGR
	// TokenReset is an SGR reset sequence.
	TokenReset
	// TokenHyperlinkOpen opens an OSC 8 hyperlink.
	TokenHyperlinkOpen
	// TokenHyperlinkClose closes an OSC 8 hyperlink.
	TokenHyperlinkClose
)

// Token is a single text or escape sequence token.
type Token struct {
	Type TokenType
	Raw  string
	Text string
}

// TruncateOptions configures ANSI-aware truncation.
type TruncateOptions struct {
	Tail           string
	PreserveResets bool
}
