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

	"btw/internal/ids"
)

// InviteLifetime is how long a link is good for.
const InviteLifetime = 7 * 24 * time.Hour

// Invite is a link that turns into an account exactly once.
type Invite struct {
	ID         string
	Role       string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	AcceptedAt time.Time
}

// Accepted reports whether this invitation has already been used.
func (i Invite) Accepted() bool { return !i.AcceptedAt.IsZero() }

// CreateInvite returns the invitation and its token. The token is readable here and
// nowhere else, ever: the stored form is a hash, so a lost link is reissued rather than
// recovered — the same stance as sessions and for the same reason.
func (s *Store) CreateInvite(ctx context.Context, createdBy, role string) (Invite, string, error) {
	if role != RoleAdmin && role != RoleUser {
		return Invite{}, "", Invalid("%q is not a role", role)
	}
	var raw [32]byte
	rand.Read(raw[:])
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(token))

	now := s.Now().Truncate(time.Second)
	inv := Invite{
		ID:        ids.New(ids.Invite),
		Role:      role,
		CreatedAt: now,
		ExpiresAt: now.Add(InviteLifetime),
	}
	var by any
	if createdBy != "" {
		by = createdBy
	}
	_, err := s.main.ExecContext(ctx,
		`INSERT INTO invites (id, token_hash, created_by, role, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		inv.ID, sum[:], by, inv.Role, unix(inv.CreatedAt), unix(inv.ExpiresAt))
	if err != nil {
		return Invite{}, "", fmt.Errorf("create invite: %w", err)
	}
	return inv, token, nil
}

// InviteByToken reads an invitation without accepting it, so a page can say whether a link
// is live before somebody types a password into it. It reveals nothing but its own
// validity.
func (s *Store) InviteByToken(ctx context.Context, token string) (Invite, error) {
	sum := sha256.Sum256([]byte(token))
	var (
		inv           Invite
		cre, exp      int64
		acceptedAtRaw sql.NullInt64
	)
	err := s.main.QueryRowContext(ctx,
		`SELECT id, role, created_at, expires_at, accepted_at FROM invites WHERE token_hash = ?`, sum[:]).
		Scan(&inv.ID, &inv.Role, &cre, &exp, &acceptedAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return Invite{}, NotFound("that invitation link is not valid")
	}
	if err != nil {
		return Invite{}, fmt.Errorf("read invite: %w", err)
	}
	inv.CreatedAt = time.Unix(cre, 0).UTC()
	inv.ExpiresAt = time.Unix(exp, 0).UTC()
	inv.AcceptedAt = timeFrom(acceptedAtRaw)

	if inv.Accepted() {
		return Invite{}, Conflict("that invitation has already been used")
	}
	if !s.Now().Before(inv.ExpiresAt) {
		return Invite{}, NotFound("that invitation link has expired")
	}
	return inv, nil
}

// AcceptInvite turns a link into an account, in one transaction, so a link cannot be
// redeemed twice by two requests arriving together.
func (s *Store) AcceptInvite(ctx context.Context, token, username, password string) (Principal, error) {
	inv, err := s.InviteByToken(ctx, token)
	if err != nil {
		return Principal{}, err
	}
	p, err := s.CreatePrincipal(ctx, username, password, inv.Role)
	if err != nil {
		return Principal{}, err
	}
	// The WHERE clause carries accepted_at IS NULL, so two requests racing on one link
	// produce one account and one refusal rather than two accounts.
	res, err := s.main.ExecContext(ctx,
		`UPDATE invites SET accepted_at = ?, principal_id = ? WHERE id = ? AND accepted_at IS NULL`,
		unix(s.Now()), p.ID, inv.ID)
	if err != nil {
		return Principal{}, fmt.Errorf("accept invite: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Principal{}, Conflict("that invitation has already been used")
	}
	return p, nil
}
