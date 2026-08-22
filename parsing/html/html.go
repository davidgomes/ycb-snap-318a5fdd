package html

import "github.com/tomwright/dasel/v3/parsing"

const HTML parsing.Format = "html"

func init() {
	parsing.RegisterReader(HTML, newHTMLReader)
	parsing.RegisterWriter(HTML, newHTMLWriter)
}
