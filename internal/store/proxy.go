package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"btw/internal/proxy"
)

// Proxy reads the proxy an operator configured, or the zero value if there is none.
//
// A missing row is not an error. "No proxy" is a state the interface renders — and the state
// almost every instance is in — rather than a failure of the read.
func (s *Store) Proxy(ctx context.Context) (proxy.Settings, error) {
	var (
		set  proxy.Settings
		kind string
	)
	err := s.main.QueryRowContext(ctx,
		`SELECT kind, url, username, token, enabled FROM proxy WHERE singleton = 1`).
		Scan(&kind, &set.URL, &set.Username, &set.Token, &set.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return proxy.Settings{}, nil
	}
	if err != nil {
		return proxy.Settings{}, fmt.Errorf("read proxy: %w", err)
	}
	set.Kind = proxy.Kind(kind)
	return set, nil
}

// SetProxy replaces the proxy, and switches it on.
//
// Saving enables. Somebody who has just corrected an address or a token is telling us the
// thing should work now, and making them press a second control to say so would leave a proxy
// switched off holding settings that were fixed — which looks exactly like settings that are
// still broken.
func (s *Store) SetProxy(ctx context.Context, set proxy.Settings) error {
	set, err := ValidateProxy(set)
	if err != nil {
		return err
	}

	_, err = s.main.ExecContext(ctx,
		`INSERT INTO proxy (singleton, kind, url, username, token, enabled, updated_at)
		 VALUES (1, ?, ?, ?, ?, 1, ?)
		 ON CONFLICT (singleton) DO UPDATE SET
		   kind = excluded.kind, url = excluded.url, username = excluded.username,
		   token = excluded.token, enabled = 1, updated_at = excluded.updated_at`,
		string(set.Kind), set.URL, set.Username, set.Token, unix(s.Now()))
	if err != nil {
		return fmt.Errorf("set proxy: %w", err)
	}
	return nil
}

// EnableProxy switches an existing proxy on or off without touching what it holds.
//
// Off is the point of it: a proxy is the first thing to suspect when the gateway stops
// answering, and finding out means going direct for a minute. Deleting to do that costs the
// token, and a token is not something somebody can read back off the screen to type again.
func (s *Store) EnableProxy(ctx context.Context, on bool) error {
	res, err := s.main.ExecContext(ctx,
		`UPDATE proxy SET enabled = ?, updated_at = ? WHERE singleton = 1`, on, unix(s.Now()))
	if err != nil {
		return fmt.Errorf("enable proxy: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return NotFound("there is no proxy to switch")
	}
	return nil
}

// ClearProxy forgets the proxy, credential and all.
func (s *Store) ClearProxy(ctx context.Context) error {
	if _, err := s.main.ExecContext(ctx, `DELETE FROM proxy WHERE singleton = 1`); err != nil {
		return fmt.Errorf("clear proxy: %w", err)
	}
	return nil
}

// ValidateProxy checks a proxy and tidies what can be tidied.
//
// Exported so the handler can refuse before it reaches a query, and so a test can drive the
// tidying without a database. It returns the settings as they should be stored, which is not
// always what was typed — see the credential and the path below.
func ValidateProxy(in proxy.Settings) (proxy.Settings, error) {
	if !in.Kind.Valid() {
		return in, Invalid("%q is not a kind of proxy this knows about", in.Kind)
	}
	schemes := in.Kind.Schemes()

	raw := strings.TrimSpace(in.URL)
	if raw == "" {
		return in, Invalid("a proxy needs an address, like %s", in.Kind.Example())
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return in, Invalid("%q is not an address: %v", raw, err)
	}
	if parsed.Scheme == "" {
		// Named rather than guessed at. Prefixing a scheme for somebody would pick one of two
		// for a socks endpoint and the wrong one of two for proxio.
		return in, Invalid("%q has no scheme; write it in full, like %s://%s", raw, schemes[0], raw)
	}
	if !slices.Contains(schemes, strings.ToLower(parsed.Scheme)) {
		return in, Invalid("a %s proxy's address begins %s, not %q",
			in.Kind, strings.Join(schemes, " or "), parsed.Scheme)
	}
	if parsed.Host == "" {
		return in, Invalid("%q names no host", raw)
	}

	// Credentials belong in their own columns, not in the address that gets shown and logged.
	// Somebody who pasted `socks5://user:pass@host` gets the parts put where they belong
	// rather than a refusal — or, worse, a password rendered on screen for anyone behind them.
	if parsed.User != nil {
		if in.Username == "" {
			in.Username = parsed.User.Username()
		}
		if pass, ok := parsed.User.Password(); ok && in.Token == "" {
			in.Token = pass
		}
		parsed.User = nil
	}
	// The path a proxy serves on is the kind's business, so anything typed after the host is
	// dropped rather than kept and quietly ignored: somebody who pasted the whole
	// `/proxy?url=…&token=…` example should get back an address that works, and get the
	// credential out of the address rather than left on screen.
	if in.Kind == proxy.Proxio && in.Token == "" {
		if token := parsed.Query().Get("token"); token != "" {
			in.Token = token
		}
	}
	parsed.Path, parsed.RawQuery, parsed.Fragment = "", "", ""
	parsed.Scheme, parsed.Host = strings.ToLower(parsed.Scheme), strings.ToLower(parsed.Host)
	in.URL = parsed.String()

	in.Token = strings.TrimSpace(in.Token)
	in.Username = strings.TrimSpace(in.Username)
	if in.Token == "" {
		what := "a token"
		if in.Kind.NeedsUsername() {
			what = "a password"
		}
		return in, Invalid("a %s proxy needs %s; switch it off or forget it instead", in.Kind, what)
	}
	if in.Kind.NeedsUsername() && in.Username == "" {
		return in, Invalid("a %s proxy needs a username", in.Kind)
	}
	if !in.Kind.NeedsUsername() {
		// Not merely ignored: a name stored against a kind that never reads one is a field
		// somebody will one day believe is doing something.
		in.Username = ""
	}
	return in, nil
}
