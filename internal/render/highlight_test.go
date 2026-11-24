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
	const media = "@media (prefers-color-scheme: dark) {"
	if strings.Count(css, media) != 1 {
		t.Fatal("want one media block")
	}
	light, dark, _ := strings.Cut(css, media)
	if strings.Contains(css, "<") || !strings.Contains(css, ".hl-chroma .hl-k") {
		t.Error("unexpected css content")
	}
	rule := regexp.MustCompile(`(?m)^(?:/\*.*\*/ )?([^{\n]+)\{([^}]*)\}`)
	darkSelectors := map[string]bool{}
	for _, m := range rule.FindAllStringSubmatch(dark, -1) {
		darkSelectors[strings.TrimSpace(m[1])] = true
	}
	for _, m := range rule.FindAllStringSubmatch(light, -1) {
		sel := strings.TrimSpace(m[1])
		if !strings.HasPrefix(sel, ".hl-chroma") && !strings.HasPrefix(sel, ".hl-bg") {
			t.Errorf("unscoped selector %q", sel)
		}
		if strings.Contains(m[2], "color:") && !darkSelectors[sel] {
			t.Errorf("light selector %q has no dark counterpart", sel)
		}
	}
}
