package render

import "testing"

func TestDetect(t *testing.T) {
	cases := map[Kind][]string{
		KindHTML:     {"text/html", "TEXT/HTML; charset=UTF-8", "text/html; charset"},
		KindMarkdown: {"text/markdown; charset=utf-8", "text/x-markdown"},
		KindJSON: {
			"application/json", "application/json; charset=utf-8",
			"application/ld+json", "application/vnd.api+json",
		},
		KindText: {
			"text/plain", "text/csv", "text/x-go", "text/javascript", "application/javascript",
			"application/xml", "application/atom+xml", "application/xhtml+xml",
			"application/yaml", "application/x-yaml", "application/toml", "application/x-sh",
			"application/x-ndjson", "application/x-python", "application/typescript", "application/x-php",
			"application/x-perl", "application/x-ruby",
		},
		KindBinary: {
			"image/svg+xml", "image/png", "application/octet-stream", "application/pdf",
			"", "garbage", "/",
		},
	}
	for want, types := range cases {
		for _, ct := range types {
			if got := Detect(ct); got != want {
				t.Errorf("Detect(%q) = %q, want %q", ct, got, want)
			}
		}
	}
}
