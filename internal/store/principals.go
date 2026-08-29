package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"btw/internal/ids"
)

// Roles. Two, because a third would need a set of permissions and nobody has wanted one.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// bcryptCost is 12. Bcrypt truncates at 72 bytes, so the input is length-checked rather
// than silently cut — a password longer than that would otherwise authenticate against any
// prefix of itself.
const (
	bcryptCost        = 12
	maxPasswordBytes  = 72
	minPasswordLength = 8
	maxUsernameLength = 32
)

// Principal is an account.
type Principal struct {
	ID         string
	Username   string
	Role       string
	CreatedAt  time.Time
	DisabledAt time.Time
}

// Disabled reports whether this account has been switched off.
func (p Principal) Disabled() bool { return !p.DisabledAt.IsZero() }

// CreatePrincipal makes an account. The password is validated here rather than only at the
// handler, because the store is the backstop and every path in eventually reaches it.
func (s *Store) CreatePrincipal(ctx context.Context, username, password, role string) (Principal, error) {
	username = strings.TrimSpace(username)
	if err := validateUsername(username); err != nil {
		return Principal{}, err
	}
	if err := validatePassword(password); err != nil {
		return Principal{}, err
	}
	if role != RoleAdmin && role != RoleUser {
		return Principal{}, Invalid("%q is not a role", role)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return Principal{}, fmt.Errorf("hash password: %w", err)
	}

	p := Principal{
		ID:        ids.New(ids.Principal),
		Username:  username,
		Role:      role,
		CreatedAt: s.Now().Truncate(time.Second),
	}
	_, err = s.main.ExecContext(ctx,
		`INSERT INTO principals (id, username, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.Username, string(hash), p.Role, unix(p.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return Principal{}, Conflict("the name %q is taken", username)
		}
		return Principal{}, fmt.Errorf("create principal: %w", err)
	}
	return p, nil
}

// Principal reads one account by id.
func (s *Store) Principal(ctx context.Context, id string) (Principal, error) {
	return s.scanPrincipal(s.main.QueryRowContext(ctx,
		`SELECT id, username, role, created_at, disabled_at FROM principals WHERE id = ?`, id), id)
}

// Authenticate checks a username and password together.
//
// Every failure returns the same error. Which half was wrong is not information a caller
// should be able to extract, and a handler that told them apart would be a username
// oracle.
func (s *Store) Authenticate(ctx context.Context, username, password string) (Principal, error) {
	var (
		p    Principal
		hash string
		dis  sql.NullInt64
		cre  int64
	)
	err := s.main.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, created_at, disabled_at
		   FROM principals WHERE lower(username) = lower(?)`, strings.TrimSpace(username)).
		Scan(&p.ID, &p.Username, &hash, &p.Role, &cre, &dis)
	if errors.Is(err, sql.ErrNoRows) {
		// Compare against a hash anyway, so a missing account and a wrong password take
		// the same time. Without this, response latency alone says which usernames exist.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return Principal{}, Invalid("that username and password do not match")
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return Principal{}, Invalid("that username and password do not match")
	}
	p.CreatedAt = time.Unix(cre, 0).UTC()
	p.DisabledAt = timeFrom(dis)
	if p.Disabled() {
		return Principal{}, Invalid("that account has been disabled")
	}
	return p, nil
}

// dummyHash is a real bcrypt hash at the same cost, compared against when no account
// matched so that the timing of the two cases agrees.
var dummyHash = []byte("$2a$12$C6UzMDM.H6dfI/f/IKcEe.7BAoUlBqIYnJgxRXwjMwqDoLl6kEO2m")

// CountAdmins is how the first run knows whether to bootstrap, and how the admin routes
// later refuse to remove the last one.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.main.QueryRowContext(ctx,
		`SELECT count(*) FROM principals WHERE role = ? AND disabled_at IS NULL`, RoleAdmin).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return n, nil
}

// SetPassword changes a password and ends that account's sessions.
//
// The order is the whole of it, and it is the price of keeping sessions in the other
// database: no transaction spans the two, so one of the halves can fail alone. Sessions go
// first. Fail between the two and somebody has been signed out without their password
// changing — visible, harmless, and retryable by pressing the button again. The other
// order fails the other way, leaving live sessions behind a changed password, and that is
// a security bug rather than an inconvenience.
func (s *Store) SetPassword(ctx context.Context, principalID, password string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.DeleteSessionsFor(ctx, principalID); err != nil {
		return err
	}
	res, err := s.main.ExecContext(ctx,
		`UPDATE principals SET password_hash = ? WHERE id = ?`, string(hash), principalID)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("no such account")
	}
	return nil
}

func (s *Store) scanPrincipal(row *sql.Row, id string) (Principal, error) {
	var (
		p   Principal
		cre int64
		dis sql.NullInt64
	)
	err := row.Scan(&p.ID, &p.Username, &p.Role, &cre, &dis)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, NotFound("no account %s", id)
	}
	if err != nil {
		return Principal{}, fmt.Errorf("read principal: %w", err)
	}
	p.CreatedAt = time.Unix(cre, 0).UTC()
	p.DisabledAt = timeFrom(dis)
	return p, nil
}

func validateUsername(u string) error {
	switch {
	case u == "":
		return Invalid("a username is required")
	case len(u) > maxUsernameLength:
		return Invalid("that username is too long; %d characters at most", maxUsernameLength)
	}
	for _, r := range u {
		// A deliberately narrow set. A username appears in a URL and in a log line, and
		// every character that needs escaping in either is a character somebody will
		// eventually find a way to abuse.
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return Invalid("a username may hold letters, digits, and - _ . only")
		}
	}
	return nil
}

func validatePassword(p string) error {
	switch {
	case len([]rune(p)) < minPasswordLength:
		return Invalid("a password needs at least %d characters", minPasswordLength)
	case len(p) > maxPasswordBytes:
		// Bytes, not runes: bcrypt's limit is on the encoded form, so an emoji costs four.
		return Invalid("that password is too long; %d bytes at most", maxPasswordBytes)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	// The driver's error strings rather than its codes, because modernc's error type is
	// not part of its API surface. Checked in one place so there is one thing to fix if
	// that changes.
	s := err.Error()
	return strings.Contains(s, "UNIQUE constraint failed") || strings.Contains(s, "constraint failed: UNIQUE")
}
