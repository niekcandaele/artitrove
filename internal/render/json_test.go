package render

import (
	"strings"
	"testing"
)

func json1(t *testing.T, src string) string {
	t.Helper()
	out, ok := JSON([]byte(src))
	if !ok {
		t.Fatalf("%q invalid", src)
	}
	assertSafe(t, string(out))
	return string(out)
}

func TestJSONInvalid(t *testing.T) {
	for _, src := range []string{"", "{", `{"a":}`, "{} {}", "NaN", "[1,]", strings.Repeat("[", 10001) + strings.Repeat("]", 10001)} {
		if out, ok := JSON([]byte(src)); ok || out != "" {
			t.Errorf("%.20q accepted", src)
		}
	}
}

func TestJSONDeep(t *testing.T) {
	out, ok := JSON([]byte(strings.Repeat("[", 10000) + strings.Repeat("]", 10000)))
	if !ok {
		t.Fatal("rejected")
	}
	assertSafe(t, string(out))
}

func TestJSONScalarsAndOrder(t *testing.T) {
	for _, src := range []string{"1", `"s"`, "true", "null"} {
		if out := json1(t, src); strings.Contains(out, "<details") {
			t.Errorf("%s: %s", src, out)
		}
	}
	out := json1(t, `{"b":1,"a":2,"b":3}`)
	if strings.Index(out, "&#34;b&#34;") > strings.Index(out, "&#34;a&#34;") || strings.Count(out, "&#34;b&#34;") != 2 {
		t.Errorf("order: %s", out)
	}
}

func TestJSONNumbersAndStrings(t *testing.T) {
	out := json1(t, `[1.0,1e400,-0,12345678901234567890123,0.1000]`)
	for _, n := range []string{">1.0<", ">1e400<", ">-0<", ">12345678901234567890123<", ">0.1000<"} {
		if !strings.Contains(out, n) {
			t.Errorf("missing %s: %s", n, out)
		}
	}
	for src, want := range map[string]string{
		`"a\", \"b"`: `&#34;a\&#34;, \&#34;b&#34;`, `"a\nb"`: `a\nb`, `"\u0000"`: `\u0000`, `"\ud800"`: "�",
		`{"k\"q":1}`: `k\&#34;q`, `"<script>"`: `&lt;script&gt;`,
		`{"<img src=x onerror=alert(1)>":1}`: `&lt;img src=x onerror=alert(1)&gt;`,
	} {
		if out := json1(t, src); !strings.Contains(out, want) {
			t.Errorf("%s: want %q in %s", src, want, out)
		}
	}
	if out := json1(t, "{\"a\xff\":\"b\xfe\"}"); !strings.Contains(out, "�") {
		t.Errorf("bad bytes: %s", out)
	}
	if out := json1(t, `"javascript:alert(1)"`); strings.Contains(out, "href") {
		t.Errorf("href: %s", out)
	}
}

func TestJSONStructure(t *testing.T) {
	out := json1(t, `{"a":[1,2],"b":{"c":1},"d":[],"e":{}}`)
	if !strings.HasPrefix(out, `<div class="json-tree">`) || strings.Count(out, "<details open>") != 3 {
		t.Errorf("structure: %s", out)
	}
	for _, want := range []string{"4 keys", "2 items", "1 key"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %s", want, out)
		}
	}
	if !strings.Contains(json1(t, `[1]`), "1 item<") {
		t.Error("singular item")
	}
}
