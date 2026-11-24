// Package render turns stored content into safe HTML fragments for app pages.
package render

// Kind is how the human view presents an artifact.
type Kind string

// The kinds Detect returns.
const (
	KindHTML     Kind = "html"
	KindMarkdown Kind = "markdown"
	KindJSON     Kind = "json"
	KindText     Kind = "text"
	KindBinary   Kind = "binary"
)

// HighlightClassPrefix prefixes every chroma token class in Markdown output.
const HighlightClassPrefix = "hl-"
