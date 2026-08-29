package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// How long a session lives, and how often its expiry is pushed out.
//
// The refresh throttle is not an optimisation. Without it a polling SPA rewrites the row
// and emits a Set-Cookie on every request, for a window measured in days.
const (
	SessionLifetime = 7 * 24 * time.Hour
	SessionRefresh  = time.Hour
)

// Session is one signed-in browser.
type Session struct {
	PrincipalID string
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
}

// NewSessionToken mints the value that goes in the cookie. It is returned once, here, and
// never stored: what the table holds is its sha256.
func NewSessionToken() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// CreateSession records a signed-in browser and returns nothing but an error: the caller
// already has the token, because it minted it.
func (s *Store) CreateSession(ctx context.Context, token, principalID string) error {
	now := s.Now().Truncate(time.Second)
	_, err := s.derived.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, principal_id, created_at, last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		hashToken(token), principalID, unix(now), unix(now), unix(now.Add(SessionLifetime)))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// Session resolves a cookie value, sliding its expiry forward when the throttle allows.
// The bool says whether the expiry moved, which is the handler's cue to re-issue the
// cookie.
//
// Returned as a value rather than folded into the error, which is what it was first: a
// caller writing the obvious `if err != nil { return 401 }` would then have signed out
// every session that had just been legitimately extended.
//
// An expired row is deleted on the way past rather than left for the sweep — the request
// that found it is already holding the connection.
func (s *Store) Session(ctx context.Context, token string) (Session, bool, error) {
	hash := hashToken(token)
	var (
		sess              Session
		cre, seen, expiry int64
	)
	err := s.derived.QueryRowContext(ctx,
		`SELECT principal_id, created_at, last_seen_at, expires_at FROM sessions WHERE id_hash = ?`, hash).
		Scan(&sess.PrincipalID, &cre, &seen, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, NotFound("not signed in")
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("read session: %w", err)
	}
	sess.CreatedAt = time.Unix(cre, 0).UTC()
	sess.LastSeenAt = time.Unix(seen, 0).UTC()
	sess.ExpiresAt = time.Unix(expiry, 0).UTC()

	now := s.Now().Truncate(time.Second)
	if !now.Before(sess.ExpiresAt) {
		s.derived.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hash)
		return Session{}, false, NotFound("not signed in")
	}
	if now.Sub(sess.LastSeenAt) >= SessionRefresh {
		sess.LastSeenAt = now
		sess.ExpiresAt = now.Add(SessionLifetime)
		_, err := s.derived.ExecContext(ctx,
			`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id_hash = ?`,
			unix(sess.LastSeenAt), unix(sess.ExpiresAt), hash)
		if err != nil {
			return Session{}, false, fmt.Errorf("refresh session: %w", err)
		}
		return sess, true, nil
	}
	return sess, false, nil
}

// DeleteSession ends one, by its cookie value.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	if _, err := s.derived.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hashToken(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteSessionsFor ends every session an account has. See SetPassword for why this runs
// before the thing it accompanies rather than after.
func (s *Store) DeleteSessionsFor(ctx context.Context, principalID string) error {
	if _, err := s.derived.ExecContext(ctx, `DELETE FROM sessions WHERE principal_id = ?`, principalID); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	return nil
}

// SweepSessions removes expired rows. Called on a timer; the amount it finds is only ever
// the sessions nobody came back to.
func (s *Store) SweepSessions(ctx context.Context) (int64, error) {
	res, err := s.derived.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, unix(s.Now()))
	if err != nil {
		return 0, fmt.Errorf("sweep sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
