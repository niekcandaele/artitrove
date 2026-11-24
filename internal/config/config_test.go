package config

import (
	"math"
	"strings"
	"testing"
)

func TestURLs(t *testing.T) {
	c := Config{BaseURL: "https://trove.example"}
	if got := c.ArtifactURL("abc"); got != "https://trove.example/a/abc" {
		t.Errorf("ArtifactURL = %q", got)
	}
	if got := c.RawURL("abc"); got != "https://trove.example/a/abc/raw" {
		t.Errorf("RawURL = %q", got)
	}
}

const validToken = "0123456789abcdef"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func with(over map[string]string) map[string]string {
	m := map[string]string{"ARTITROVE_TOKEN": validToken}
	for k, v := range over {
		m[k] = v
	}
	return m
}

func unwrap(t *testing.T, err error) []error {
	t.Helper()
	j, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("error is not a joined error: %v", err)
	}
	return j.Unwrap()
}

func TestLoadDefaults(t *testing.T) {
	got, err := Load(env(with(nil)))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{validToken, "http://localhost:8080", ":8080", "./data", 10485760, false}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestLoadValid(t *testing.T) {
	d := Config{validToken, "http://localhost:8080", ":8080", "./data", 10485760, false}
	tests := []struct {
		name string
		env  map[string]string
		want Config
	}{
		{"all explicit", map[string]string{
			"ARTITROVE_BASE_URL": "https://trove.example", "ARTITROVE_LISTEN": "127.0.0.1:9000",
			"ARTITROVE_DATA_DIR": "/var/lib/artitrove", "ARTITROVE_MAX_SIZE": "1", "ARTITROVE_INDEX": "true"},
			Config{validToken, "https://trove.example", "127.0.0.1:9000", "/var/lib/artitrove", 1, true}},
		{"optionals empty", map[string]string{
			"ARTITROVE_BASE_URL": "", "ARTITROVE_LISTEN": "", "ARTITROVE_DATA_DIR": "",
			"ARTITROVE_MAX_SIZE": "", "ARTITROVE_INDEX": ""}, d},
		{"token 16 runes bytes", map[string]string{"ARTITROVE_TOKEN": strings.Repeat("日", 16)},
			Config{strings.Repeat("日", 16), d.BaseURL, d.Listen, d.DataDir, d.MaxSize, false}},
		{"url ipv6", map[string]string{"ARTITROVE_BASE_URL": "http://[::1]:8080"},
			Config{validToken, "http://[::1]:8080", d.Listen, d.DataDir, d.MaxSize, false}},
		{"url port 1", map[string]string{"ARTITROVE_BASE_URL": "http://x.example:1"},
			Config{validToken, "http://x.example:1", d.Listen, d.DataDir, d.MaxSize, false}},
		{"url port 65535", map[string]string{"ARTITROVE_BASE_URL": "http://x.example:65535"},
			Config{validToken, "http://x.example:65535", d.Listen, d.DataDir, d.MaxSize, false}},
		{"size plus", map[string]string{"ARTITROVE_MAX_SIZE": "+5"}, Config{validToken, d.BaseURL, d.Listen, d.DataDir, 5, false}},
		{"size leading zero", map[string]string{"ARTITROVE_MAX_SIZE": "010"}, Config{validToken, d.BaseURL, d.Listen, d.DataDir, 10, false}},
		{"size max", map[string]string{"ARTITROVE_MAX_SIZE": "9223372036854775807"},
			Config{validToken, d.BaseURL, d.Listen, d.DataDir, math.MaxInt64, false}},
	}
	for _, v := range []string{"1", "t", "TRUE", "True"} {
		tests = append(tests, struct {
			name string
			env  map[string]string
			want Config
		}{"index " + v, map[string]string{"ARTITROVE_INDEX": v}, Config{validToken, d.BaseURL, d.Listen, d.DataDir, d.MaxSize, true}})
	}
	for _, v := range []string{"0", "f", "FALSE", "false"} {
		tests = append(tests, struct {
			name string
			env  map[string]string
			want Config
		}{"index " + v, map[string]string{"ARTITROVE_INDEX": v}, d})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(env(with(tt.env)))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	const tok, url, size, idx = "ARTITROVE_TOKEN", "ARTITROVE_BASE_URL", "ARTITROVE_MAX_SIZE", "ARTITROVE_INDEX"
	tests := []struct {
		name, key, val, mustNotContain string
		unset                          bool
	}{
		{name: "token empty", key: tok, val: ""},
		{name: "token unset", key: tok, unset: true},
		{name: "token 15 ascii", key: tok, val: "0123456789abcde"},
		{name: "token 15 runes", key: tok, val: strings.Repeat("日", 15)},
		{name: "token secret", key: tok, val: "secret-abc", mustNotContain: "secret-abc"},
		{name: "trailing slash", key: url, val: "http://localhost:8080/"},
		{name: "path", key: url, val: "https://x.example/base"},
		{name: "query", key: url, val: "https://x.example?q=1"},
		{name: "bare query", key: url, val: "https://x.example?"},
		{name: "fragment", key: url, val: "https://x.example#f"},
		{name: "bare fragment", key: url, val: "https://x.example#"},
		{name: "userinfo", key: url, val: "https://user:" + "secret@x.example", mustNotContain: "secret"},
		{name: "ftp", key: url, val: "ftp://x.example"},
		{name: "ftp path", key: url, val: "ftp://x.example/p"},
		{name: "no scheme", key: url, val: "localhost:8080"},
		{name: "scheme relative", key: url, val: "//x.example"},
		{name: "no host", key: url, val: "http://"},
		{name: "port only", key: url, val: "http://:8080"},
		{name: "opaque", key: url, val: "http:x.example"},
		{name: "parse error", key: url, val: "http://x .example"},
		{name: "uppercase scheme", key: url, val: "HTTP://x.example"},
		{name: "javascript", key: url, val: "javascript:alert(1)"},
		{name: "empty port", key: url, val: "http://x.example:"},
		{name: "port 0", key: url, val: "http://x.example:0"},
		{name: "port too big", key: url, val: "http://x.example:99999"},
		{name: "size 0", key: size, val: "0"},
		{name: "size negative", key: size, val: "-1"},
		{name: "size abc", key: size, val: "abc"},
		{name: "size hex", key: size, val: "0x10"},
		{name: "size underscore", key: size, val: "1_000"},
		{name: "size space", key: size, val: " 5"},
		{name: "size float", key: size, val: "1.5"},
		{name: "size unit", key: size, val: "10MB"},
		{name: "size overflow", key: size, val: "9223372036854775808"},
		{name: "index yes", key: idx, val: "yes"},
		{name: "index on", key: idx, val: "on"},
		{name: "index 2", key: idx, val: "2"},
		{name: "index space", key: idx, val: " true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := with(map[string]string{tt.key: tt.val})
			if tt.unset {
				delete(m, tt.key)
			}
			_, err := Load(env(m))
			if err == nil {
				t.Fatal("expected error")
			}
			errs := unwrap(t, err)
			if len(errs) != 1 {
				t.Fatalf("want 1 error, got %d: %v", len(errs), err)
			}
			if !strings.Contains(errs[0].Error(), tt.key) {
				t.Errorf("error %q does not name %s", errs[0], tt.key)
			}
			if tt.mustNotContain != "" && strings.Contains(err.Error(), tt.mustNotContain) {
				t.Errorf("error %q leaks %q", err, tt.mustNotContain)
			}
		})
	}
}

func TestLoadJoinsAllErrors(t *testing.T) {
	_, err := Load(env(map[string]string{
		"ARTITROVE_TOKEN": "short-tok-xyz", "ARTITROVE_BASE_URL": "http://x/",
		"ARTITROVE_MAX_SIZE": "0", "ARTITROVE_INDEX": "yes",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	errs := unwrap(t, err)
	if len(errs) != 4 {
		t.Fatalf("want 4 errors, got %d: %v", len(errs), err)
	}
	for _, name := range []string{"ARTITROVE_TOKEN", "ARTITROVE_BASE_URL", "ARTITROVE_MAX_SIZE", "ARTITROVE_INDEX"} {
		n := 0
		for _, e := range errs {
			if strings.Contains(e.Error(), name) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s appears in %d errors, want 1", name, n)
		}
	}
	if strings.Contains(err.Error(), "short-tok-xyz") {
		t.Errorf("error leaks token: %v", err)
	}
}
