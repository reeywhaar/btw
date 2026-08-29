package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"time"
)

// How long a code is good for, and how many guesses it takes.
//
// Forty bits is not a key and does not need to be: it is bounded to five attempts, expires
// in fifteen minutes, and authorises nothing but the address it went to.
const (
	RecoveryCodeLifetime = 15 * time.Minute
	RecoveryCodeAttempts = 5
	recoveryCodeLength   = 8
)

// codeAlphabet is Crockford's base32 without I, L, O or U — it is read off one screen and
// typed into another, so a character that can be misread for another is a support request.
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// RecoveryAddress is a proved address, or empty if the account has none.
//
// Only ever a proved one. An address partway through being proved is one somebody typed,
// possibly somebody holding a borrowed session pointing recovery at an inbox of their own.
func (s *Store) RecoveryAddress(ctx context.Context, principalID string) (string, time.Time, error) {
	var email string
	var at int64
	err := s.main.QueryRowContext(ctx,
		`SELECT email, proved_at FROM user_recovery WHERE principal_id = ?`, principalID).Scan(&email, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read recovery address: %w", err)
	}
	return email, time.Unix(at, 0).UTC(), nil
}

// PendingRecovery is the address an account is partway through proving, if any.
func (s *Store) PendingRecovery(ctx context.Context, principalID string) (string, error) {
	var email string
	var expires int64
	err := s.main.QueryRowContext(ctx,
		`SELECT email, expires_at FROM recovery_pending WHERE principal_id = ?`, principalID).Scan(&email, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read pending recovery: %w", err)
	}
	if !s.Now().Before(time.Unix(expires, 0).UTC()) {
		return "", nil
	}
	return email, nil
}

// StartRecovery records an attempt and returns the code to send.
//
// The row is written before the send, because the code has to exist before it travels — and
// a send that fails must call DropRecovery, or the interface says it is waiting on a code
// that never left, and the way out of that state is the button somebody just watched fail.
//
// Replaces any attempt already in flight rather than adding to it. Two live codes for one
// account is two chances at the same guess, and starting again is what somebody does when
// the mail did not arrive.
func (s *Store) StartRecovery(ctx context.Context, principalID, email string) (string, error) {
	email = strings.TrimSpace(email)
	addr, err := netmail.ParseAddress(email)
	if err != nil {
		return "", Invalid("%q is not an address", email)
	}
	email = addr.Address

	code := newRecoveryCode()
	sum := sha256.Sum256([]byte(code))
	now := s.Now().Truncate(time.Second)

	_, err = s.main.ExecContext(ctx,
		`INSERT INTO recovery_pending (principal_id, email, code_hash, attempts, created_at, expires_at)
		 VALUES (?, ?, ?, 0, ?, ?)
		 ON CONFLICT (principal_id) DO UPDATE SET
		   email = excluded.email, code_hash = excluded.code_hash, attempts = 0,
		   created_at = excluded.created_at, expires_at = excluded.expires_at`,
		principalID, email, sum[:], unix(now), unix(now.Add(RecoveryCodeLifetime)))
	if err != nil {
		return "", fmt.Errorf("start recovery: %w", err)
	}
	// Returned once, here, and never stored — the table holds its hash, so the only way to
	// know a code is to be the recipient. The tests rely on that rather than working around
	// it, by reading the code from the fake relay.
	return code, nil
}

// DropRecovery throws away an attempt without touching a proved address.
//
// Its own method rather than a flag on ForgetRecovery, because a *failed change* must leave
// the address that already worked.
func (s *Store) DropRecovery(ctx context.Context, principalID string) error {
	if _, err := s.main.ExecContext(ctx,
		`DELETE FROM recovery_pending WHERE principal_id = ?`, principalID); err != nil {
		return fmt.Errorf("drop recovery: %w", err)
	}
	return nil
}

// ConfirmRecovery checks a code and, if it is right, proves the address.
//
// One refusal covers wrong, expired, exhausted and absent alike. Which of the four it was
// tells a caller something about an account that may not be theirs, and tells the owner
// nothing they could not learn by trying again.
func (s *Store) ConfirmRecovery(ctx context.Context, principalID, code string) (string, error) {
	refuse := Invalid("that code is wrong or has expired")

	tx, err := s.main.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("confirm recovery: %w", err)
	}
	defer tx.Rollback()

	var (
		email    string
		hash     []byte
		attempts int
		expires  int64
	)
	err = tx.QueryRowContext(ctx,
		`SELECT email, code_hash, attempts, expires_at FROM recovery_pending WHERE principal_id = ?`,
		principalID).Scan(&email, &hash, &attempts, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", refuse
	}
	if err != nil {
		return "", fmt.Errorf("confirm recovery: %w", err)
	}

	now := s.Now().Truncate(time.Second)
	if !now.Before(time.Unix(expires, 0).UTC()) || attempts >= RecoveryCodeAttempts {
		return "", refuse
	}

	given := sha256.Sum256([]byte(normaliseCode(code)))
	if !equalHash(given[:], hash) {
		// Counted inside the transaction, so guesses arriving together cannot each read the
		// same count and all be the fifth.
		if _, err := tx.ExecContext(ctx,
			`UPDATE recovery_pending SET attempts = attempts + 1 WHERE principal_id = ?`, principalID); err != nil {
			return "", fmt.Errorf("confirm recovery: %w", err)
		}
		// Five wrong answers throws the attempt away rather than locking anything: a lockout
		// is a state somebody has to wait out, and starting again is faster and no weaker.
		if attempts+1 >= RecoveryCodeAttempts {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM recovery_pending WHERE principal_id = ?`, principalID); err != nil {
				return "", fmt.Errorf("confirm recovery: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("confirm recovery: %w", err)
		}
		return "", refuse
	}

	// One address, one account, held by whoever proved it last. Whoever can read that inbox
	// today is who recovery through it would reach — a work address gets reassigned, somebody
	// moves out of a shared one — and refusing would refuse the person who really can read it
	// while leaving the one who cannot on record. It concedes nothing: anybody able to prove
	// control of that inbox could already recover the account attached to it.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM user_recovery WHERE lower(email) = lower(?) AND principal_id <> ?`,
		email, principalID); err != nil {
		return "", fmt.Errorf("confirm recovery: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_recovery (principal_id, email, proved_at) VALUES (?, ?, ?)
		 ON CONFLICT (principal_id) DO UPDATE SET email = excluded.email, proved_at = excluded.proved_at`,
		principalID, email, unix(now)); err != nil {
		return "", fmt.Errorf("confirm recovery: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM recovery_pending WHERE principal_id = ?`, principalID); err != nil {
		return "", fmt.Errorf("confirm recovery: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("confirm recovery: %w", err)
	}
	return email, nil
}

// ForgetRecovery removes the address and anything in flight, in that order, so a failure
// between the two leaves nothing that could later be proved into an address the account no
// longer wanted.
func (s *Store) ForgetRecovery(ctx context.Context, principalID string) error {
	if _, err := s.main.ExecContext(ctx,
		`DELETE FROM user_recovery WHERE principal_id = ?`, principalID); err != nil {
		return fmt.Errorf("forget recovery: %w", err)
	}
	return s.DropRecovery(ctx, principalID)
}

func newRecoveryCode() string {
	b := make([]byte, recoveryCodeLength)
	rand.Read(b)
	out := make([]byte, recoveryCodeLength)
	for i, v := range b {
		out[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	return string(out)
}

// normaliseCode forgives how somebody types one back: lower case, spaces, and the letters
// Crockford's alphabet leaves out because they look like digits.
func normaliseCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch r {
		case ' ', '-':
		case 'I', 'L':
			b.WriteRune('1')
		case 'O':
			b.WriteRune('0')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// equalHash compares in constant time, so a wrong code cannot be narrowed by how long the
// refusal took.
func equalHash(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
