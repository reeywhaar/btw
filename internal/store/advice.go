package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AdviceVersion is the shape [Advice] is in. A stored row in any other version is read as no
// advice at all: it is recomputable, and half-understanding an older answer is worse than one
// round of questions.
const AdviceVersion = 4

// The shape of a week. Half an hour is as fine as the answer can honestly be — a person's sense
// of "the evening" has no sharper edges, and finer is precision a model does not have.
const (
	Days          = 7
	Windows       = 48
	WindowMinutes = 24 * 60 / Windows
)

// Curve is how well each half hour of somebody's week suits one reminder, from 0 to 1.
//
// Seven days of forty-eight, Monday first, rather than one list of 336: asked for the long list
// a model loses count in the middle and every value after names the wrong hour. Nothing here
// converts anything — the day and the minute are worked out in internal/rhythm.
type Curve [][]float64

func (c Curve) Valid() bool {
	if len(c) != Days {
		return false
	}
	for _, day := range c {
		if len(day) != Windows {
			return false
		}
	}
	return true
}

// At is how well the half hour containing a local moment suits this reminder. `day` is 0 for
// Monday. The bool is false for a curve that could not be understood, which wants the opposite
// treatment from one saying "no hour suits this".
func (c Curve) At(day, minute int) (float64, bool) {
	if !c.Valid() {
		return 0, false
	}
	if minute < 0 {
		minute = 0
	}
	// Modulo, so a minute past the end of a day — an hour written as 24:00 — wraps.
	return c[((day%Days)+Days)%Days][(minute/WindowMinutes)%Windows], true
}

// Advice is what the companion made of one reminder. One JSON body under [AdviceVersion] rather
// than a column per field, since what is worth asking for changes whenever the prompt does.
type Advice struct {
	// Exclusive is whether it wants somebody's full attention. Nothing reads it — the curve
	// says everything the weighting needs. See docs/nudges.md.
	Exclusive bool `json:"exclusive"`

	// Categories is what it called the reminder. Nothing reads this either.
	Categories []string `json:"categories"`

	Curve Curve `json:"curve"`

	// Shape is what arrived when the curve could not be read — "7x24", "obj:7", "336" — so a
	// screen can say which. It says nothing about the reminder it belongs to.
	Shape string `json:"shape,omitempty"`

	AdvisedAt time.Time `json:"-"`
}

// SetAdvice replaces what the companion said about one person's reminders, in one transaction.
// Wholesale, because the answer is about the set: a reminder asked about and not mentioned has
// to lose its old advice, which a loop of upserts would leave behind. `keep` names the set.
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
		if a.Categories == nil {
			a.Categories = []string{}
		}
		if a.Curve == nil {
			a.Curve = Curve{}
		}
		body, err := json.Marshal(a)
		if err != nil {
			return fmt.Errorf("encode advice %s: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO advice (reminder_id, version, body, advised_at) VALUES (?, ?, ?, ?)`,
			id, AdviceVersion, string(body), unix(s.Now())); err != nil {
			return fmt.Errorf("set advice %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// MarkAdviceStale says the companion's answer is worth asking for again.
//
// Cheap enough to call from every write that could change one, which is the point: the loop
// decides when to act and a caller only has to be honest. Never fatal — out-of-date advice is
// not a reason to refuse somebody's edit.
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
	// A missing row is stale, not fresh: a derived.db thrown away takes the advice with it, so
	// "already asked" must not outlive what it was asked about. So is an answer under a shape
	// the reader does not understand, which is what makes changing [AdviceVersion] enough.
	Stale bool

	// AdvisedAt is when an answer last arrived, AttemptedAt when one was last tried for. They
	// differ exactly when the last attempt failed.
	AdvisedAt   time.Time
	AttemptedAt time.Time

	// Error is why the last attempt failed, in the gateway's own words, or empty.
	Error string

	// Limited is whether that failure was a quota rather than a mistake. Nothing needs doing
	// about one: the next pass is the wait.
	Limited bool

	// Alerted is whether the person has been told about this failure. Once per episode.
	Alerted bool
}

// Advice reads how the last round went. A missing row is the zero value, which is stale.
func (s *Store) Advice(ctx context.Context, principalID string) (AdviceState, error) {
	var (
		st                 AdviceState
		advised, attempted sql.NullInt64
	)
	var version int
	err := s.derived.QueryRowContext(ctx,
		`SELECT stale, advised_at, attempted_at, error, limited, alerted, version
		   FROM advice_state WHERE principal_id = ?`,
		principalID).Scan(&st.Stale, &advised, &attempted, &st.Error, &st.Limited, &st.Alerted, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return AdviceState{Stale: true}, nil
	}
	if err != nil {
		return AdviceState{}, fmt.Errorf("read advice state: %w", err)
	}
	st.Stale = st.Stale || version != AdviceVersion
	st.AdvisedAt = timeFrom(advised)
	st.AttemptedAt = timeFrom(attempted)
	return st, nil
}

// AdviceCoverage counts how much of somebody's open list the companion can actually place.
//
// Usable advice, not answered-for: an entry whose curve could not be read is one the weighting
// ignores, and counting it would claim an opinion over a list saying the opposite. Two numbers
// that exist only to be compared — neither reaches a response body, per docs/api_design.md.
func (s *Store) AdviceCoverage(ctx context.Context, principalID string) (open, answered int, err error) {
	rows, err := s.main.QueryContext(ctx,
		`SELECT id FROM reminders WHERE principal_id = ? AND binned_at IS NULL`, principalID)
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

	advice, err := s.AdviceFor(ctx, ids)
	if err != nil {
		return 0, 0, err
	}
	for _, a := range advice {
		if a.Curve.Valid() {
			answered++
		}
	}
	return len(ids), answered, nil
}

func (s *Store) MarkAdviceAlerted(ctx context.Context, principalID string) error {
	if _, err := s.derived.ExecContext(ctx,
		`UPDATE advice_state SET alerted = 1 WHERE principal_id = ?`, principalID); err != nil {
		return fmt.Errorf("mark advice alerted: %w", err)
	}
	return nil
}

// ForgetAdvice drops what was said about one reminder. Only on a delete, which is the one end
// that cannot be undone: a binned reminder can come back, and its advice is still true.
func (s *Store) ForgetAdvice(ctx context.Context, reminderID string) error {
	if _, err := s.derived.ExecContext(ctx,
		`DELETE FROM advice WHERE reminder_id = ?`, reminderID); err != nil {
		return fmt.Errorf("forget advice: %w", err)
	}
	return nil
}

func (s *Store) RecordAdvised(ctx context.Context, principalID string, at time.Time) error {
	_, err := s.derived.ExecContext(ctx,
		`INSERT INTO advice_state (principal_id, stale, advised_at, attempted_at, error, limited, alerted, version)
		 VALUES (?, 0, ?, ?, '', 0, 0, ?)
		 ON CONFLICT (principal_id) DO UPDATE SET
		   stale = 0, advised_at = excluded.advised_at,
		   attempted_at = excluded.attempted_at, error = '', limited = 0,
		   -- Lowered by a round that worked, so a key that breaks again months later is
		   -- worth another message.
		   alerted = 0, version = excluded.version`,
		principalID, unix(at), unix(at), AdviceVersion)
	if err != nil {
		return fmt.Errorf("record advised: %w", err)
	}
	return nil
}

// RecordAdviceFailure remembers that asking did not work, and leaves it stale on purpose: a
// failure is not an answer, and clearing the flag would freeze everybody's advice at whatever a
// key said before it stopped working. The loop's interval is the only throttle.
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

// AdviceFor reads what the companion said about a set of reminders. Two queries and no join,
// since main.db and derived.db cannot be spanned by one statement, over a small set.
//
// Only the current version: a row under an older shape is left where it is and read as nothing,
// and the next round replaces it.
func (s *Store) AdviceFor(ctx context.Context, ids []string) (map[string]Advice, error) {
	out := make(map[string]Advice, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	query := `SELECT reminder_id, body, advised_at FROM advice
	           WHERE version = ? AND reminder_id IN (?` + repeatArgs(len(ids)-1) + `)`
	args := make([]any, 0, len(ids)+1)
	args = append(args, AdviceVersion)
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := s.derived.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read advice: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id        string
			body      string
			advisedAt sql.NullInt64
		)
		if err := rows.Scan(&id, &body, &advisedAt); err != nil {
			return nil, fmt.Errorf("read advice row: %w", err)
		}
		var a Advice
		// No advice rather than an error: refusing to nudge somebody at all would be worse.
		if err := json.Unmarshal([]byte(body), &a); err != nil {
			continue
		}
		a.AdvisedAt = timeFrom(advisedAt)
		out[id] = a
	}
	return out, rows.Err()
}

// repeatArgs is the `, ?` tail of an IN list holding n more placeholders.
func repeatArgs(n int) string {
	out := make([]byte, 0, n*3)
	for range n {
		out = append(out, ',', '?')
	}
	return string(out)
}
