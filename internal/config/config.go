// Package config models btw's configuration, which is entirely environment-driven.
//
// There is no config file and does not need to be one. What this program *is* — its name,
// its version, the address it listens on — lives in internal/app instead.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

// Environment variables read by this package.
const (
	// PublicURLEnv is the origin somebody opens in a browser.
	//
	// It has to be told and cannot be inferred: Host and X-Forwarded-Host are both
	// client-supplied, and an invitation link built from a header a stranger controls is
	// an invitation link a stranger controls.
	//
	// It does three jobs, and the third is the one that surprises people. It decides
	// whether the session cookie carries Secure; it is the VAPID `sub`, the contact URI
	// push services are given; and it is the origin every push subscription is bound to,
	// so changing it silently kills every registered device.
	PublicURLEnv = "BTW_PUBLIC_URL"

	// DataDirEnv is where main.db and derived.db live.
	DataDirEnv = "BTW_DATA_DIR"

	// LogLevelEnv sets the slog level: debug, info, warn or error.
	LogLevelEnv = "BTW_LOG_LEVEL"

	// WebDirEnv is where the built frontend lives. Set in the image, rarely anywhere else.
	WebDirEnv = "BTW_WEB_DIR"
)

// Defaults for everything that has one.
const (
	DefaultDataDir = "/data"
	DefaultWebDir  = "/srv/web"
)

// Config is everything the process was told at startup.
type Config struct {
	// PublicURL is normalised: scheme and host lowercased, no trailing slash, no query or
	// fragment. A path is kept, so btw can live under a prefix.
	PublicURL *url.URL

	DataDir  string
	WebDir   string
	LogLevel slog.Level

	// Secure is whether the session cookie carries Secure, which is PublicURL being https
	// and nothing else. Derived once here rather than re-decided at each Set-Cookie.
	Secure bool
}

// Load reads the environment. The returned error is written for somebody looking at a
// container that refused to start.
func Load() (*Config, error) {
	raw := strings.TrimSpace(os.Getenv(PublicURLEnv))
	if raw == "" {
		return nil, fmt.Errorf("%s is required: set it to the address you open in a browser, e.g. https://btw.example.com", PublicURLEnv)
	}
	public, err := parsePublicURL(raw)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		PublicURL: public,
		DataDir:   DefaultDataDir,
		WebDir:    DefaultWebDir,
		LogLevel:  slog.LevelInfo,
		Secure:    public.Scheme == "https",
	}
	if dir := strings.TrimSpace(os.Getenv(DataDirEnv)); dir != "" {
		cfg.DataDir = dir
	}
	if dir := strings.TrimSpace(os.Getenv(WebDirEnv)); dir != "" {
		cfg.WebDir = dir
	}
	if v := strings.TrimSpace(os.Getenv(LogLevelEnv)); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return nil, fmt.Errorf("%s: %q is not a level (debug, info, warn, error)", LogLevelEnv, v)
		}
	}
	return cfg, nil
}

func parsePublicURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %q is not a URL: %w", PublicURLEnv, raw, err)
	}
	switch u.Scheme {
	case "http", "https":
	case "":
		return nil, fmt.Errorf("%s: %q has no scheme; write it in full, e.g. https://%s", PublicURLEnv, raw, raw)
	default:
		return nil, fmt.Errorf("%s: %q is not http or https", PublicURLEnv, raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%s: %q names no host", PublicURLEnv, raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

// Link builds an absolute URL for a path within this service, for the links that leave the
// process: an invitation printed to a log or pasted into a chat.
func (c *Config) Link(path string) string {
	return c.PublicURL.String() + "/" + strings.TrimPrefix(path, "/")
}

// Origin is the scheme and host alone, which is what a push subscription is bound to and
// what the VAPID `sub` claim carries.
func (c *Config) Origin() string { return c.PublicURL.Scheme + "://" + c.PublicURL.Host }

// InsecurePublicURL reports whether the public URL is plain http, which means the session
// cookie ships without Secure — and, here, that no browser will register a service worker
// against it unless the host is localhost. serve warns rather than refuses.
func (c *Config) InsecurePublicURL() bool { return c.PublicURL.Scheme == "http" }
