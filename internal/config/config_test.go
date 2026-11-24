package config

import "testing"

func TestURLs(t *testing.T) {
	c := Config{BaseURL: "https://trove.example"}
	if got := c.ArtifactURL("abc"); got != "https://trove.example/a/abc" {
		t.Errorf("ArtifactURL = %q", got)
	}
	if got := c.RawURL("abc"); got != "https://trove.example/a/abc/raw" {
		t.Errorf("RawURL = %q", got)
	}
}
