package render

import (
	"errors"
	"mime"
	"strings"
)

var textApplicationTypes = map[string]bool{
	"application/javascript":    true,
	"application/x-javascript":  true,
	"application/ecmascript":    true,
	"application/xml":           true,
	"application/yaml":          true,
	"application/x-yaml":        true,
	"application/toml":          true,
	"application/x-sh":          true,
	"application/x-shellscript": true,
	"application/sql":           true,
	"application/graphql":       true,
	"application/x-ndjson":      true,
}

// Detect maps a stored Content-Type to a Kind. The media type is parsed with
// mime.ParseMediaType and lower-cased; a bad parameter is tolerated, any other parse error
// gives KindBinary.
//
//	text/html                                        -> KindHTML
//	text/markdown, text/x-markdown                   -> KindMarkdown
//	application/json, application/*+json             -> KindJSON
//	text/*, application/{javascript,x-javascript,ecmascript,xml,yaml,x-yaml,toml,x-sh,
//	  x-shellscript,sql,graphql,x-ndjson}, application/*+xml -> KindText
//	everything else (image/* including svg+xml, octet-stream, pdf) -> KindBinary
func Detect(contentType string) Kind {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil && !errors.Is(err, mime.ErrInvalidMediaParameter) {
		return KindBinary
	}
	mt = strings.ToLower(mt)
	switch {
	case mt == "text/html":
		return KindHTML
	case mt == "text/markdown", mt == "text/x-markdown":
		return KindMarkdown
	case mt == "application/json", strings.HasPrefix(mt, "application/") && strings.HasSuffix(mt, "+json"):
		return KindJSON
	case strings.HasPrefix(mt, "text/"), textApplicationTypes[mt],
		strings.HasPrefix(mt, "application/") && strings.HasSuffix(mt, "+xml"):
		return KindText
	}
	return KindBinary
}
