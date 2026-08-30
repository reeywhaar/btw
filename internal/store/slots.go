package store

import (
	"context"
	"fmt"
	"time"
)

// Slot is a minute, chosen at random ahead of time, when a nudge is due.
//
// Nothing returns these to a browser. A person who can see that the next nudge is at 14:32
// is a person waiting for 14:32, and the surprise is the entire mechanism.
type Slot struct {
	PrincipalID string
	LocalDate   string
	Index       int
	At          time.Time
	FiredAt     time.Time
}

// HasPlan reports whether a person's day has already been planned. The planner is lazy —
// it plans when it finds a day unplanned — so this is what stops it planning twice.
func (s *Store) HasPlan(ctx context.Context, principalID, localDate string) (bool, error) {
	var n int
	err := s.derived.QueryRowContext(ctx,
		`SELECT count(*) FROM slots WHERE principal_id = ? AND local_date = ?`, principalID, localDate).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("read plan: %w", err)
	}
	return n > 0, nil
}

// PlanDay records a day's slots in one transaction, so a crash half way through leaves a
// day unplanned rather than half planned — and an unplanned day is planned again on the
// next tick, which is the recoverable direction.
//
// A budget of zero is a legitimate plan: it writes nothing, and HasPlan then says the day
// is unplanned, so a person who wants no nudges is re-planned once a minute for nothing.
// That is cheap enough to prefer over a marker row that would have to be kept in step.
func (s *Store) PlanDay(ctx context.Context, principalID, localDate string, at []time.Time) error {
	tx, err := s.derived.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("plan day: %w", err)
	}
	defer tx.Rollback()

	for i, t := range at {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO slots (principal_id, local_date, idx, at) VALUES (?, ?, ?, ?)
			 ON CONFLICT (principal_id, local_date, idx) DO NOTHING`,
			principalID, localDate, i, unix(t)); err != nil {
			return fmt.Errorf("plan day: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("plan day: %w", err)
	}
	return nil
}

// DueSlots returns the slots whose minute has come and which have not fired.
//
// `since` is the grace window: a slot older than that is not returned, because a missed
// slot is missed. An instance that was down for three hours does not catch up on restart —
// three nudges arriving at once, all about moments that have passed, is indistinguishable
// from a broken app and teaches somebody to swipe the channel away.
func (s *Store) DueSlots(ctx context.Context, now time.Time, grace time.Duration) ([]Slot, error) {
	rows, err := s.derived.QueryContext(ctx,
		`SELECT principal_id, local_date, idx, at FROM slots
		  WHERE fired_at IS NULL AND at <= ? AND at > ?
		  ORDER BY at`, unix(now), unix(now.Add(-grace)))
	if err != nil {
		return nil, fmt.Errorf("read due slots: %w", err)
	}
	defer rows.Close()

	var out []Slot
	for rows.Next() {
		var (
			sl Slot
			at int64
		)
		if err := rows.Scan(&sl.PrincipalID, &sl.LocalDate, &sl.Index, &at); err != nil {
			return nil, fmt.Errorf("read slot: %w", err)
		}
		sl.At = time.Unix(at, 0).UTC()
		out = append(out, sl)
	}
	return out, rows.Err()
}

// ClaimSlot marks a slot fired and reports whether this caller is the one that got it.
//
// The UPDATE carries `fired_at IS NULL`, so it is the claim rather than a note made after
// deciding to claim: two ticks overlapping cannot both send for one slot.
func (s *Store) ClaimSlot(ctx context.Context, sl Slot, now time.Time) (bool, error) {
	res, err := s.derived.ExecContext(ctx,
		`UPDATE slots SET fired_at = ? WHERE principal_id = ? AND local_date = ? AND idx = ? AND fired_at IS NULL`,
		unix(now), sl.PrincipalID, sl.LocalDate, sl.Index)
	if err != nil {
		return false, fmt.Errorf("claim slot: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ExpireSlots marks everything older than the grace window as fired without sending, so
// the due query stays small and a restart does not reconsider last week.
func (s *Store) ExpireSlots(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.derived.ExecContext(ctx,
		`UPDATE slots SET fired_at = ? WHERE fired_at IS NULL AND at <= ?`, unix(s.Now()), unix(before))
	if err != nil {
		return 0, fmt.Errorf("expire slots: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ReplanDay redraws the rest of a day under a rhythm that has just changed.
//
// Slots that already fired are left exactly as they are: they happened, and rewriting
// history to match a setting changed afterwards would be a lie about a notification
// somebody already saw. Everything still ahead is replaced.
//
// Only future instants are kept. A day re-planned at nine in the evening cannot hold a
// morning, and inserting slots already in the past would either fire them all at once or be
// swept as missed — both worse than an honest short evening.
//
// New rows are numbered above whatever survived, because idx is part of the key and reusing
// one would collide with a slot that already fired.
func (s *Store) ReplanDay(ctx context.Context, principalID, localDate string, at []time.Time, now time.Time) (int, error) {
	tx, err := s.derived.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("replan day: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM slots WHERE principal_id = ? AND local_date = ? AND fired_at IS NULL`,
		principalID, localDate); err != nil {
		return 0, fmt.Errorf("replan day: %w", err)
	}

	var next int
	if err := tx.QueryRowContext(ctx,
		`SELECT coalesce(max(idx) + 1, 0) FROM slots WHERE principal_id = ? AND local_date = ?`,
		principalID, localDate).Scan(&next); err != nil {
		return 0, fmt.Errorf("replan day: %w", err)
	}

	planned := 0
	for _, t := range at {
		if !t.After(now) {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO slots (principal_id, local_date, idx, at) VALUES (?, ?, ?, ?)`,
			principalID, localDate, next, unix(t)); err != nil {
			return 0, fmt.Errorf("replan day: %w", err)
		}
		next++
		planned++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("replan day: %w", err)
	}
	return planned, nil
}
