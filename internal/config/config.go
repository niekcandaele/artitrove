// Package config loads the service configuration from environment variables.
package config

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
	panic("not implemented")
}

// ArtifactURL returns the share link for id: BaseURL + "/a/" + id.
func (c Config) ArtifactURL(id string) string {
	return c.BaseURL + "/a/" + id
}

// RawURL returns the raw-bytes link for id: BaseURL + "/a/" + id + "/raw".
func (c Config) RawURL(id string) string {
	return c.ArtifactURL(id) + "/raw"
}
