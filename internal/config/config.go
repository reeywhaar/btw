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
	"time"
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

	// BackupURLEnv is where a backup agent takes an archive — its POST /backup, so on a
	// compose network something like "http://backup:8080/backup".
	//
	// The whole address rather than a host, because the endpoint is the agent's to name and
	// btw should not be the place that knows the path it happens to serve on today.
	//
	// Nothing is backed up unless this is set. There is no default, because a default would
	// be a guess at a hostname on a network btw cannot see, and the failure it produces is a
	// log line every few minutes about somewhere nobody meant to send anything.
	BackupURLEnv = "BTW_BACKUP_URL"

	// BackupModeEnv is what goes in the archive and what makes one happen — see [BackupMode].
	BackupModeEnv = "BTW_BACKUP_MODE"
)

// BackupMode is what goes into an archive and what makes one happen.
//
// Two questions with three useful answers between them, rather than two switches with a
// meaningless fourth combination. "derived only, when main changes" is not a policy anybody
// wants, and a pair of booleans would offer it.
type BackupMode string

const (
	// BackupMain carries main.db alone.
	//
	// The smallest thing worth keeping. main.db is what somebody typed — accounts, reminders,
	// devices, rhythms — plus the VAPID keypair, whose loss invalidates every push
	// subscription on the instance at once and cannot be repaired by anybody re-registering.
	BackupMain BackupMode = "main"

	// BackupRelaxed carries both, and is the default.
	//
	// derived.db comes along for the ride but never decides that a copy is due. Sending a
	// nudge writes to both — the reminder's last_nudged_at in main.db, the log row in
	// derived.db — so the record of what was sent rides along with the change that main.db
	// already noticed. What moves in derived.db *alone* is sessions and the nudge waiting to
	// go out, and neither is worth waking up for: the cost of losing them is that everybody
	// signs in again and the next nudge is scheduled afresh.
	BackupRelaxed BackupMode = "relaxed"

	// BackupAll is relaxed with a floor under it: a copy at least every [BackupAllPeriod],
	// whether or not anybody typed anything.
	//
	// The one mode that sends when nothing has changed, and the reason to want that is
	// upstream of the archive. An agent told how often to expect one can report a btw that
	// has stopped backing up; with copies arriving only when somebody adds a reminder, it
	// cannot tell a broken instance from a quiet one, and the alarm is unusable on exactly
	// the instances that are quiet for weeks.
	BackupAll BackupMode = "all"
)

// How long a copy waits, and how long an instance can go without one.
//
// Constants, not settings, and deliberately. Neither is a number an operator can reason about
// better than btw can: the delay trades "how much of a burst becomes one archive" against "how
// long a change sits uncopied", and the floor exists to be a heartbeat rather than to protect
// anything. What an operator actually chooses is which of those promises they want, and that
// is the mode.
const (
	// BackupDelay is how long after a change the copy goes out, in every mode.
	//
	// A delay and a throttle at once: nothing here reacts to a write, so somebody adding six
	// reminders in a minute gets one archive holding all six rather than six archives.
	BackupDelay = 5 * time.Minute

	// BackupAllPeriod is how long [BackupAll] will go without sending anything.
	BackupAllPeriod = 30 * time.Minute
)

// Valid reports whether m is a mode btw knows.
func (m BackupMode) Valid() bool {
	return m == BackupMain || m == BackupRelaxed || m == BackupAll
}

// Derived reports whether the archive carries derived.db.
func (m BackupMode) Derived() bool { return m == BackupRelaxed || m == BackupAll }

// Period is how long a mode will go without sending anything, or zero for no floor at all.
//
// Only [BackupAll] has one. Every mode sends when main.db changes; this is the extra promise
// that one of them makes on top.
func (m BackupMode) Period() time.Duration {
	if m == BackupAll {
		return BackupAllPeriod
	}
	return 0
}

// Defaults for everything that has one.
const (
	DefaultDataDir = "/data"
	DefaultWebDir  = "/srv/web"

	// DefaultBackupMode carries both databases and copies them when main.db changes.
	//
	// Relaxed rather than main, because derived.db is a fraction of the archive and holds the
	// record of what was actually delivered. Not all, because a floor is a heartbeat for an
	// operator who has set one up, and sending an archive every half hour to an instance
	// nobody has configured that for is postage spent on nothing.
	DefaultBackupMode = BackupRelaxed
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

	// BackupURL is where an archive is posted, or empty for no backups at all.
	BackupURL string
	// BackupMode is what goes in the archive, and what makes one happen.
	BackupMode BackupMode
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
	if err := loadBackup(cfg); err != nil {
		return nil, err
	}
	if v := strings.TrimSpace(os.Getenv(LogLevelEnv)); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return nil, fmt.Errorf("%s: %q is not a level (debug, info, warn, error)", LogLevelEnv, v)
		}
	}
	return cfg, nil
}

// loadBackup reads the backup group. The URL is the switch; the mode is the policy.
func loadBackup(cfg *Config) error {
	cfg.BackupURL = strings.TrimSpace(os.Getenv(BackupURLEnv))
	if cfg.BackupURL != "" {
		u, err := url.Parse(cfg.BackupURL)
		if err != nil || u.Host == "" {
			return fmt.Errorf("%s: %q is not a URL; write it in full, e.g. http://backup:8080/backup", BackupURLEnv, cfg.BackupURL)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("%s: %q is not http or https", BackupURLEnv, cfg.BackupURL)
		}
	}

	cfg.BackupMode = DefaultBackupMode
	if v := strings.TrimSpace(os.Getenv(BackupModeEnv)); v != "" {
		mode := BackupMode(strings.ToLower(v))
		if !mode.Valid() {
			return fmt.Errorf("%s: %q is not a mode (%s, %s, %s)", BackupModeEnv, v, BackupMain, BackupRelaxed, BackupAll)
		}
		cfg.BackupMode = mode

		// Choosing a policy for archives that are not going anywhere is a setting that cannot
		// mean what it was written to mean, and the mistake it usually is — believing backups
		// are on — stays invisible until somebody needs one.
		if cfg.BackupURL == "" {
			return fmt.Errorf("%s is set but %s is not: nothing is backed up until %s names an agent", BackupModeEnv, BackupURLEnv, BackupURLEnv)
		}
	}
	return nil
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
