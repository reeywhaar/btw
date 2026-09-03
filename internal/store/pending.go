package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ScheduledNudge is when the next nudge is due to go out, or the zero time if none is
// waiting.
func (s *Store) ScheduledNudge(ctx context.Context, principalID string) (time.Time, error) {
	var at int64
	err := s.derived.QueryRowContext(ctx,
		`SELECT at FROM pending_nudge WHERE principal_id = ?`, principalID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read pending nudge: %w", err)
	}
	return time.Unix(at, 0).UTC(), nil
}

// ScheduleNudge sets when the next one goes out, replacing anything already waiting.
func (s *Store) ScheduleNudge(ctx context.Context, principalID string, at time.Time) error {
	_, err := s.derived.ExecContext(ctx,
		`INSERT INTO pending_nudge (principal_id, at) VALUES (?, ?)
		 ON CONFLICT (principal_id) DO UPDATE SET at = excluded.at`,
		principalID, unix(at))
	if err != nil {
		return fmt.Errorf("schedule nudge: %w", err)
	}
	return nil
}

// ClaimScheduledNudge takes the waiting nudge if its moment has come, and reports whether
// this caller got it.
//
// The delete is the claim rather than a note made after deciding to claim, so two ticks
// overlapping cannot both send.
func (s *Store) ClaimScheduledNudge(ctx context.Context, principalID string, now time.Time) (bool, error) {
	res, err := s.derived.ExecContext(ctx,
		`DELETE FROM pending_nudge WHERE principal_id = ? AND at <= ?`, principalID, unix(now))
	if err != nil {
		return false, fmt.Errorf("claim nudge: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ClearScheduledNudge drops a waiting nudge, for when the moment came and the person turned
// out to be asleep.
func (s *Store) ClearScheduledNudge(ctx context.Context, principalID string) error {
	if _, err := s.derived.ExecContext(ctx,
		`DELETE FROM pending_nudge WHERE principal_id = ?`, principalID); err != nil {
		return fmt.Errorf("clear pending nudge: %w", err)
	}
	return nil
}
