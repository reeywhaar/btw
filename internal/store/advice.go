package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Slot is a window in somebody's week when the companion thinks a reminder would land well.
//
// Minutes since local midnight, the same units and the same origin a rhythm's waking window
// uses, against the same IANA name. Nothing here knows which zone that is: the conversion
// happens once, in internal/rhythm, and everything downstream compares integers.
type Slot struct {
	// Day is 0 for Monday through 6 for Sunday. Monday because the week starts there for the
	// person answering, and an explicit origin is what stops a Sunday being read as a Monday
	// by whichever end of the wire disagreed.
	Day   int `json:"day"`
	Start int `json:"start"`
	End   int `json:"end"`
}

// Covers reports whether a local moment falls inside the slot.
//
// `day` is Monday-origin like Slot.Day, and `minute` is since local midnight.
func (s Slot) Covers(day, minute int) bool {
	if s.End > s.Start {
		return day == s.Day && minute >= s.Start && minute < s.End
	}
	// An end at or before its start runs past midnight. Two pieces: the evening it began in,
	// and the small hours of the day after — which for a Sunday slot is a Monday.
	//
	// The equal case is here rather than treated as an empty window on purpose. A companion
	// answering 22:00–22:00 meant a whole day, not nothing, and reading it as nothing would
	// silently remove a reminder from consideration for a week.
	if day == s.Day && minute >= s.Start {
		return true
	}
	return day == (s.Day+1)%7 && minute < s.End
}

// Advice is what the companion made of one reminder.
type Advice struct {
	// Exclusive is whether it wants somebody's full attention.
	Exclusive bool
	// Categories is what it called the reminder. Nothing reads this yet — see the migration
	// for why it is stored anyway.
	Categories []string
	Slots      []Slot
	AdvisedAt  time.Time
}

// SetAdvice replaces what the companion said about one person's reminders, in one transaction.
//
// Wholesale rather than row by row, because the answer is about the set: a reminder the
// companion was asked about and said nothing for has to lose its old advice, and a loop of
// upserts leaves exactly that behind. `keep` names every reminder the question covered.
func (s *Store) SetAdvice(ctx context.Context, keep []string, advice map[string]Advice) error {
	tx, err := s.derived.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set advice: %w", err)
	}
	defer tx.Rollback()

	for _, id := range keep {
		if _, err := tx.ExecContext(ctx, `DELETE FROM advice WHERE reminder_id = ?`, id); err != nil {
			return fmt.Errorf("clear advice %s: %w", id, err)
		}
	}
	for id, a := range advice {
		categories, err := json.Marshal(orEmpty(a.Categories))
		if err != nil {
			return fmt.Errorf("encode categories %s: %w", id, err)
		}
		slots, err := json.Marshal(orEmptySlots(a.Slots))
		if err != nil {
			return fmt.Errorf("encode slots %s: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO advice (reminder_id, exclusive, categories, slots, advised_at)
			 VALUES (?, ?, ?, ?, ?)`,
			id, a.Exclusive, string(categories), string(slots), unix(s.Now())); err != nil {
			return fmt.Errorf("set advice %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// MarkAdviceStale says the companion's answer is worth asking for again.
//
// Called from every write that could change one: a reminder written, ended, revived, deleted
// or described, an account's `about` rewritten, its rhythm moved. Cheap enough to call on all
// of them, which is the point — the loop decides when to act, and a caller only has to be
// honest about having changed something.
//
// Never fatal to its caller. Failing to mark means advice stays a little out of date, and that
// is not a reason to refuse somebody's edit.
func (s *Store) MarkAdviceStale(ctx context.Context, principalID string) error {
	_, err := s.derived.ExecContext(ctx,
		`INSERT INTO advice_state (principal_id, stale) VALUES (?, 1)
		 ON CONFLICT (principal_id) DO UPDATE SET stale = 1`, principalID)
	if err != nil {
		return fmt.Errorf("mark advice stale: %w", err)
	}
	return nil
}

// AdviceState is how the last round of questions went.
type AdviceState struct {
	// Stale is whether the answer wants asking for again.
	//
	// A missing row is stale, not fresh. A derived.db thrown away takes the advice with it, so
	// the state saying "already asked" must not outlive what it was asked about.
	Stale bool

	// AdvisedAt is when an answer last arrived, and AttemptedAt when one was last tried for.
	// They differ exactly when the last attempt failed, which is what lets the interface say
	// "answered an hour ago, and the try since then failed" rather than picking one.
	AdvisedAt   time.Time
	AttemptedAt time.Time

	// Error is why the last attempt failed, in the gateway's own words, or empty.
	Error string

	// Limited is whether that failure was a quota rather than a mistake. Nothing needs doing
	// about one: the next pass is the wait.
	Limited bool

	// Alerted is whether the person has already been told about this failure. Once per
	// episode, not once per pass — see the migration that added it.
	Alerted bool
}

// Advice reads how the last round went. A missing row is the zero value, which is stale.
func (s *Store) Advice(ctx context.Context, principalID string) (AdviceState, error) {
	var (
		st                 AdviceState
		advised, attempted sql.NullInt64
	)
	err := s.derived.QueryRowContext(ctx,
		`SELECT stale, advised_at, attempted_at, error, limited, alerted
		   FROM advice_state WHERE principal_id = ?`,
		principalID).Scan(&st.Stale, &advised, &attempted, &st.Error, &st.Limited, &st.Alerted)
	if errors.Is(err, sql.ErrNoRows) {
		return AdviceState{Stale: true}, nil
	}
	if err != nil {
		return AdviceState{}, fmt.Errorf("read advice state: %w", err)
	}
	st.AdvisedAt = timeFrom(advised)
	st.AttemptedAt = timeFrom(attempted)
	return st, nil
}

// AdviceCoverage counts how much of somebody's open list has been answered for.
//
// Two numbers that exist only to be compared. Neither reaches a response body — see the rule
// in docs/api_design.md about counts — and what the interface is told is "all of them", "some
// of them" or "none", which is what somebody actually needs to know and carries no number that
// goes up.
func (s *Store) AdviceCoverage(ctx context.Context, principalID string) (open, answered int, err error) {
	rows, err := s.main.QueryContext(ctx,
		`SELECT id FROM reminders WHERE principal_id = ? AND done_at IS NULL`, principalID)
	if err != nil {
		return 0, 0, fmt.Errorf("read open reminders: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, 0, fmt.Errorf("read open reminder: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	advice, err := s.adviceFor(ctx, ids)
	if err != nil {
		return 0, 0, err
	}
	return len(ids), len(advice), nil
}

// MarkAdviceAlerted records that the person has been told their companion stopped working.
func (s *Store) MarkAdviceAlerted(ctx context.Context, principalID string) error {
	if _, err := s.derived.ExecContext(ctx,
		`UPDATE advice_state SET alerted = 1 WHERE principal_id = ?`, principalID); err != nil {
		return fmt.Errorf("mark advice alerted: %w", err)
	}
	return nil
}

// ForgetAdvice drops what was said about one reminder.
//
// Called when a reminder is deleted outright, and never when one is merely finished with:
// a done reminder can be revived, and the advice it had is still true about it. A delete is
// the only end that cannot be undone, so it is the only one that should take the advice with
// it — otherwise the row sits in derived.db forever, read by nothing.
func (s *Store) ForgetAdvice(ctx context.Context, reminderID string) error {
	if _, err := s.derived.ExecContext(ctx,
		`DELETE FROM advice WHERE reminder_id = ?`, reminderID); err != nil {
		return fmt.Errorf("forget advice: %w", err)
	}
	return nil
}

// RecordAdvised marks one person's advice fresh.
func (s *Store) RecordAdvised(ctx context.Context, principalID string, at time.Time) error {
	_, err := s.derived.ExecContext(ctx,
		`INSERT INTO advice_state (principal_id, stale, advised_at, attempted_at, error, limited, alerted)
		 VALUES (?, 0, ?, ?, '', 0, 0)
		 ON CONFLICT (principal_id) DO UPDATE SET
		   stale = 0, advised_at = excluded.advised_at,
		   attempted_at = excluded.attempted_at, error = '', limited = 0,
		   -- Lowered by a round that worked, so a key that breaks again months later is
		   -- worth another message.
		   alerted = 0`,
		principalID, unix(at), unix(at))
	if err != nil {
		return fmt.Errorf("record advised: %w", err)
	}
	return nil
}

// RecordAdviceFailure remembers that asking did not work, and leaves it stale.
//
// Stale on purpose: a failure is not an answer, and clearing the flag would mean a key that
// stopped working quietly froze everybody's advice at whatever it last said. The next pass
// tries again — the loop's own interval is the only thing throttling it, which is what keeps a
// broken key from spending fifty requests in a minute.
func (s *Store) RecordAdviceFailure(ctx context.Context, principalID string, at time.Time, reason string, limited bool) error {
	_, err := s.derived.ExecContext(ctx,
		`INSERT INTO advice_state (principal_id, stale, attempted_at, error, limited)
		 VALUES (?, 1, ?, ?, ?)
		 ON CONFLICT (principal_id) DO UPDATE SET
		   attempted_at = excluded.attempted_at, error = excluded.error,
		   limited = excluded.limited`,
		principalID, unix(at), reason, limited)
	if err != nil {
		return fmt.Errorf("record advice failure: %w", err)
	}
	return nil
}

// adviceFor reads what the companion said about a set of reminders.
//
// Two queries and no join, because the reminders are in main.db and this is derived.db and no
// statement may span them. The set is one person's open reminders, so it is small.
func (s *Store) adviceFor(ctx context.Context, ids []string) (map[string]Advice, error) {
	out := make(map[string]Advice, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	query := `SELECT reminder_id, exclusive, categories, slots, advised_at FROM advice WHERE reminder_id IN (?` +
		repeatArgs(len(ids)-1) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	rows, err := s.derived.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read advice: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id                string
			a                 Advice
			categories, slots string
			advisedAt         sql.NullInt64
		)
		if err := rows.Scan(&id, &a.Exclusive, &categories, &slots, &advisedAt); err != nil {
			return nil, fmt.Errorf("read advice row: %w", err)
		}
		// Unparseable JSON is treated as no advice rather than as an error. It can only come
		// from a version that wrote a different shape, and refusing to nudge somebody at all
		// because a model's answer from a month ago no longer decodes is the wrong trade.
		json.Unmarshal([]byte(categories), &a.Categories)
		json.Unmarshal([]byte(slots), &a.Slots)
		a.AdvisedAt = timeFrom(advisedAt)
		out[id] = a
	}
	return out, rows.Err()
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func orEmptySlots(v []Slot) []Slot {
	if v == nil {
		return []Slot{}
	}
	return v
}

// repeatArgs is the `, ?` tail of an IN list holding n more placeholders.
func repeatArgs(n int) string {
	out := make([]byte, 0, n*3)
	for range n {
		out = append(out, ',', '?')
	}
	return string(out)
}
