package render

import (
	"regexp"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2/styles"
)

func TestHighlightCSS(t *testing.T) {
	css := HighlightCSS()
	if css != HighlightCSS() {
		t.Error("not deterministic")
	}
	for _, name := range []string{"github", "github-dark"} {
		if _, ok := styles.Registry[name]; !ok {
			t.Errorf("style %s missing", name)
		}
	}
	const lightMedia, darkMedia = "@media not all and (prefers-color-scheme: dark) {\n", "}\n@media (prefers-color-scheme: dark) {\n"
	light, dark, ok := strings.Cut(strings.TrimPrefix(css, lightMedia), darkMedia)
	if !strings.HasPrefix(css, lightMedia) || !ok || !strings.HasSuffix(dark, "}\n") {
		t.Fatalf("want a light and a dark media block: %.200s", css)
	}
	if strings.Contains(css, "<") || !strings.Contains(light, ".hl-chroma .hl-k") || !strings.Contains(dark, ".hl-chroma .hl-k") {
		t.Error("unexpected css content")
	}
	rule := regexp.MustCompile(`(?m)^(?:/\*.*\*/ )?([^{\n]+)\{`)
	for _, m := range rule.FindAllStringSubmatch(light+dark, -1) {
		sel := strings.TrimSpace(m[1])
		if !strings.HasPrefix(sel, ".hl-chroma") && sel != ".hl-bg" {
			t.Errorf("unscoped selector %q", sel)
		}
	}
}

func TestHighlightLightPseudoBuiltinContrast(t *testing.T) {
	if !strings.Contains(HighlightCSS(), ".hl-chroma .hl-bp { color: #57606a }") {
		t.Error("light .hl-bp override missing")
	}
}
