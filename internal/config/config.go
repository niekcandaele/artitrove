// Package config loads the service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Config is the validated service configuration.
type Config struct {
	Token   string // ARTITROVE_TOKEN, required, at least 16 runes
	BaseURL string // ARTITROVE_BASE_URL, default "http://localhost:8080"
	Listen  string // ARTITROVE_LISTEN, default ":8080"
	DataDir string // ARTITROVE_DATA_DIR, default "./data"
	MaxSize int64  // ARTITROVE_MAX_SIZE, default 10485760, must be > 0
	Index   bool   // ARTITROVE_INDEX, default false, parsed with strconv.ParseBool
}

// Load reads configuration through getenv, applying defaults. An empty value counts as
// unset. It is pure: no filesystem access. On failure it returns errors.Join of every
// problem, and each message names its variable:
//   - token missing, or shorter than 16 runes
//   - BaseURL not an absolute http(s) URL with a host
//   - BaseURL with any path (including "/"), a query, a fragment or userinfo; templates use
//     root-absolute paths (/a/{id}/raw, /static/), so a path prefix is unsupported
//   - MaxSize not a positive base-10 integer
//   - Index not a strconv.ParseBool value
func Load(getenv func(string) string) (Config, error) {
	get := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	c := Config{
		Token:   getenv("ARTITROVE_TOKEN"),
		BaseURL: get("ARTITROVE_BASE_URL", "http://localhost:8080"),
		Listen:  get("ARTITROVE_LISTEN", ":8080"),
		DataDir: get("ARTITROVE_DATA_DIR", "./data"),
		MaxSize: 10485760,
	}
	var errs []error

	if c.Token == "" {
		errs = append(errs, errors.New("ARTITROVE_TOKEN is required"))
	} else if n := utf8.RuneCountInString(c.Token); n < 16 {
		errs = append(errs, fmt.Errorf("ARTITROVE_TOKEN must be at least 16 characters, got %d", n))
	}

	if err := checkBaseURL(c.BaseURL); err != nil {
		errs = append(errs, err)
	}

	if v := getenv("ARTITROVE_MAX_SIZE"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("ARTITROVE_MAX_SIZE %q must be a positive integer number of bytes", v))
		} else {
			c.MaxSize = n
		}
	}

	if v := getenv("ARTITROVE_INDEX"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("ARTITROVE_INDEX %q must be true or false", v))
		} else {
			c.Index = b
		}
	}

	return c, errors.Join(errs...)
}

// checkBaseURL returns at most one error. Secrets in userinfo are never echoed.
func checkBaseURL(v string) error {
	u, err := url.Parse(v)
	if err != nil {
		return fmt.Errorf("ARTITROVE_BASE_URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("ARTITROVE_BASE_URL %s must be an absolute http or https URL with a host", u.Redacted())
	}
	if (&url.URL{Scheme: u.Scheme, Host: u.Host}).String() != v {
		return fmt.Errorf("ARTITROVE_BASE_URL %s must be scheme and host only, like https://trove.example (no path, trailing slash, query, fragment or userinfo)", u.Redacted())
	}
	if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("ARTITROVE_BASE_URL %s has an invalid port", u.Redacted())
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.ParseUint(p, 10, 16); err != nil || n == 0 {
			return fmt.Errorf("ARTITROVE_BASE_URL %s has an invalid port", u.Redacted())
		}
	}
	return nil
}

// ArtifactURL returns the share link for id: BaseURL + "/a/" + id.
func (c Config) ArtifactURL(id string) string {
	return c.BaseURL + "/a/" + id
}

// RawURL returns the raw-bytes link for id: BaseURL + "/a/" + id + "/raw".
func (c Config) RawURL(id string) string {
	return c.ArtifactURL(id) + "/raw"
}
