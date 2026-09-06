package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AdviceVersion is the shape [Advice] is in.
//
// Bumped whenever what the companion is asked for changes. A stored row in any other version is
// read as no advice at all — it is recomputable, and a reader guessing at a field that meant
// something else when it was written is worse than one round of questions.
const AdviceVersion = 4

// The shape of a week, as the companion is asked to describe it.
//
// Half an hour is as fine as the answer can honestly be: the scheduler wakes on a five-minute
// tick and a person's sense of "the evening" has no edges sharper than this. Finer would be
// asking a model for precision it does not have and paying tokens for it.
const (
	Days          = 7
	Windows       = 48
	WindowMinutes = 24 * 60 / Windows
)

// Curve is how well each half hour of somebody's week suits one reminder, from 0 to 1.
//
// Seven days of forty-eight, Monday first — **seven arrays rather than one of 336**, and that
// is a fact about what a model can do rather than about the data. Asked for one long list it
// loses count somewhere in the middle and every value after that names the wrong hour, which is
// worse than no answer because it is confidently wrong. Asked for a day at a time it has a
// short list and a name for it.
//
// Read against the person's own clock. Nothing here converts anything: the day and the minute
// are worked out once, in internal/rhythm, and this is two indexes.
type Curve [][]float64

// Valid reports whether a curve is the right shape to be read.
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

// At is how well the half hour containing a local moment suits this reminder.
//
// `day` is 0 for Monday through 6 for Sunday. The bool is false for a curve that is not the
// right shape, which is a model's answer that could not be understood rather than one saying
// "no hour suits this" — and the two want opposite treatment.
func (c Curve) At(day, minute int) (float64, bool) {
	if !c.Valid() {
		return 0, false
	}
	if minute < 0 {
		minute = 0
	}
	// Modulo rather than bounds checks, so a minute past the end of a day — which is what an
	// hour written as 24:00 comes to — wraps rather than being refused.
	return c[((day%Days)+Days)%Days][(minute/WindowMinutes)%Windows], true
}

// Advice is what the companion made of one reminder.
//
// Stored as one JSON body under [AdviceVersion] rather than a column per field: this is a
// model's answer, and what is worth asking for changes whenever the prompt does.
type Advice struct {
	// Exclusive is whether it wants somebody's full attention. Nothing reads it since the
	// curve replaced the windows it used to sharpen — see docs/nudges.md.
	Exclusive bool `json:"exclusive"`

	// Categories is what it called the reminder. Nothing reads this either.
	Categories []string `json:"categories"`

	Curve Curve `json:"curve"`

	// Shape is what arrived when the curve could not be read — "7x24", "obj:7", "336".
	//
	// Kept because the alternative is a screen saying "not in a shape that could be read" and
	// leaving somebody to guess which shape, with the only answer in a log they may not have.
	// It says nothing about the reminder it belongs to.
	Shape string `json:"shape,omitempty"`

	AdvisedAt time.Time `json:"-"`
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
	//
	// So is an answer that arrived under a shape the reader no longer understands, which is
	// what makes changing [AdviceVersion] enough on its own: without it, an account already
	// advised would keep this false while every row of its advice was being ignored.
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
// **Usable advice, not answered-for.** An entry whose curve could not be read is an entry the
// weighting ignores, so counting it made the settings block say "it has an opinion about all
// your reminders" over a list where every single one said the opposite. What a person is being
// told is whether it is working, and an answer nothing reads is not working.
//
// Two numbers that exist only to be compared. Neither reaches a response body — see the rule
// in docs/api_design.md about counts — and what the interface is told is "all of them", "some
// of them" or "none", which carries no number that goes up.
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

// AdviceFor reads what the companion said about a set of reminders.
//
// Two queries and no join, because the reminders are in main.db and this is derived.db and no
// statement may span them. The set is one person's open reminders, so it is small.
//
// **Only the current version.** A row written under an older shape is left where it is and read
// as nothing: the account is already marked stale, so the next round replaces it, and a reader
// half-understanding an older answer is the failure this version column exists to prevent.
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
		// A body that will not decode is treated as no advice rather than as an error. It can
		// only come from a version that claimed this number and wrote something else, and
		// refusing to nudge somebody at all over it would be the wrong trade.
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
