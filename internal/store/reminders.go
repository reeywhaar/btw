package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"btw/internal/ids"
)

// DefaultMinInterval is the floor a reminder gets when nobody says otherwise: none.
//
// It was a day, which read as a sensible guess and behaved as an instruction — capping the
// day's budget at however many reminders somebody had. A floor is a statement about one
// particular thing ("do not nag me about this more than weekly"), and inheriting one nobody
// made is how a general appetite gets overruled by a preference nobody expressed.
//
// Zero does not mean "repeat freely". The weighting collapses a reminder's chance to nothing
// the moment it is raised and recovers it over a nominal day, the interval between nudges holds
// any two nudges apart, and the no-repeat rule stops the same one arriving twice running.
const DefaultMinInterval = 0

// MaxReminderText is a sentence, not an essay. The whole thing has to be readable on a lock
// screen, and a push payload has a hard ceiling of its own.
const MaxReminderText = 500

// MaxReminderNote is what the sentence could not hold: where the tickets are, which shop,
// what you already tried. It never leaves the app, so a lock screen does not bound it — a
// couple of paragraphs does, because anything longer is a document and belongs somewhere
// that can be searched.
const MaxReminderNote = 2000

// Reminder is the standing thing somebody wrote down.
type Reminder struct {
	ID           string
	PrincipalID  string
	Text         string
	Note         string
	MinInterval  time.Duration
	Priority     int
	CreatedAt    time.Time
	DoneAt       time.Time
	LastNudgedAt time.Time
}

// Done reports whether a person has ended this reminder, by doing it or by dropping it.
func (r Reminder) Done() bool { return !r.DoneAt.IsZero() }

// CreateReminder writes one down. Text is the only thing asked for; everything else has a
// default that is deliberately invisible.
func (s *Store) CreateReminder(ctx context.Context, principalID, text string) (Reminder, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return Reminder{}, Invalid("a reminder needs something written in it")
	case len([]rune(text)) > MaxReminderText:
		return Reminder{}, Invalid("that is too long for a notification; %d characters at most", MaxReminderText)
	}

	r := Reminder{
		ID:          ids.New(ids.Reminder),
		PrincipalID: principalID,
		Text:        text,
		MinInterval: DefaultMinInterval,
		Priority:    50,
		CreatedAt:   s.Now().Truncate(time.Second),
	}
	_, err := s.main.ExecContext(ctx,
		`INSERT INTO reminders (id, principal_id, text, min_interval, priority, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, r.PrincipalID, r.Text, int64(r.MinInterval.Seconds()), r.Priority, unix(r.CreatedAt))
	if err != nil {
		return Reminder{}, fmt.Errorf("create reminder: %w", err)
	}
	return r, nil
}

// UpdateReminder changes what a reminder says.
//
// Text and note only. Ending one is its own act with its own route, and folding it in here
// would make "save this wording" and "I am done with this" the same request.
func (s *Store) UpdateReminder(ctx context.Context, principalID, id, text, note string) (Reminder, error) {
	text = strings.TrimSpace(text)
	note = strings.TrimSpace(note)
	switch {
	case text == "":
		return Reminder{}, Invalid("a reminder needs something written in it")
	case len([]rune(text)) > MaxReminderText:
		return Reminder{}, Invalid("that is too long for a notification; %d characters at most", MaxReminderText)
	case len([]rune(note)) > MaxReminderNote:
		return Reminder{}, Invalid("that description is too long; %d characters at most", MaxReminderNote)
	}

	res, err := s.main.ExecContext(ctx,
		`UPDATE reminders SET text = ?, note = ? WHERE id = ? AND principal_id = ?`,
		text, note, id, principalID)
	if err != nil {
		return Reminder{}, fmt.Errorf("update reminder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Reminder{}, NotFound("no reminder %s", id)
	}
	return s.Reminder(ctx, principalID, id)
}

// Reminders lists one person's, newest first.
//
// Live ones and ended ones are separate calls rather than one call with a filter, because
// they are two different screens and the ended list is the one nobody looks at.
func (s *Store) Reminders(ctx context.Context, principalID string, done bool) ([]Reminder, error) {
	clause := "done_at IS NULL"
	if done {
		clause = "done_at IS NOT NULL"
	}
	rows, err := s.main.QueryContext(ctx,
		`SELECT id, principal_id, text, note, min_interval, priority, created_at, done_at, last_nudged_at
		   FROM reminders WHERE principal_id = ? AND `+clause+`
		  ORDER BY id DESC`, principalID)
	if err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}
	defer rows.Close()

	var out []Reminder
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Reminder reads one, scoped to its owner. Somebody else's answers "no such reminder"
// rather than "not yours": whether a stranger keeps a reminder is not the caller's
// business either way, and scoping the lookup and checking the owner become one operation.
func (s *Store) Reminder(ctx context.Context, principalID, id string) (Reminder, error) {
	row := s.main.QueryRowContext(ctx,
		`SELECT id, principal_id, text, note, min_interval, priority, created_at, done_at, last_nudged_at
		   FROM reminders WHERE id = ? AND principal_id = ?`, id, principalID)
	r, err := scanReminder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Reminder{}, NotFound("no reminder %s", id)
	}
	return r, err
}

// EndReminder marks one done. Whether the button said Done or Drop is recorded on the
// nudge that was answered, not here — nothing in the program reads the distinction back,
// and a column nothing reads is a column that goes stale.
func (s *Store) EndReminder(ctx context.Context, principalID, id string) error {
	res, err := s.main.ExecContext(ctx,
		`UPDATE reminders SET done_at = ? WHERE id = ? AND principal_id = ? AND done_at IS NULL`,
		unix(s.Now()), id, principalID)
	if err != nil {
		return fmt.Errorf("end reminder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Already ended is not an error. Pressing Done on a notification that has been
		// sitting on a lock screen since yesterday, after ending the thing in the app,
		// should not produce a failure — the caller wanted it ended and it is ended.
		return s.assertReminderExists(ctx, principalID, id)
	}
	return nil
}

// ReviveReminder undoes an ending, for the one that was pressed by mistake.
func (s *Store) ReviveReminder(ctx context.Context, principalID, id string) error {
	res, err := s.main.ExecContext(ctx,
		`UPDATE reminders SET done_at = NULL WHERE id = ? AND principal_id = ?`, id, principalID)
	if err != nil {
		return fmt.Errorf("revive reminder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("no reminder %s", id)
	}
	return nil
}

// DeleteReminder removes one outright. Ending is the ordinary gesture; this is for the
// typo, and for somebody who wants it gone rather than filed.
func (s *Store) DeleteReminder(ctx context.Context, principalID, id string) error {
	res, err := s.main.ExecContext(ctx,
		`DELETE FROM reminders WHERE id = ? AND principal_id = ?`, id, principalID)
	if err != nil {
		return fmt.Errorf("delete reminder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("no reminder %s", id)
	}
	return nil
}

// SetMinInterval states how often a reminder may be raised at most.
//
// No interface reaches this yet. It exists because a floor is meant to be *stated* — the
// default is none — and the behaviour of a stated one is worth being able to exercise.
func (s *Store) SetMinInterval(ctx context.Context, principalID, id string, d time.Duration) error {
	if d < 0 {
		return Invalid("an interval cannot be negative")
	}
	res, err := s.main.ExecContext(ctx,
		`UPDATE reminders SET min_interval = ? WHERE id = ? AND principal_id = ?`,
		int64(d.Seconds()), id, principalID)
	if err != nil {
		return fmt.Errorf("set min interval: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("no reminder %s", id)
	}
	return nil
}

// StampNudged records that a reminder was just raised, which is what its own floor is
// measured from. Written in the same statement that the nudge is logged by its caller —
// see nudges.go for why this lives in main.db beside the reminder rather than being read
// back out of the log.
func (s *Store) StampNudged(ctx context.Context, id string, at time.Time) error {
	if _, err := s.main.ExecContext(ctx,
		`UPDATE reminders SET last_nudged_at = ? WHERE id = ?`, unix(at), id); err != nil {
		return fmt.Errorf("stamp nudged: %w", err)
	}
	return nil
}

func (s *Store) assertReminderExists(ctx context.Context, principalID, id string) error {
	var n int
	err := s.main.QueryRowContext(ctx,
		`SELECT count(*) FROM reminders WHERE id = ? AND principal_id = ?`, id, principalID).Scan(&n)
	if err != nil {
		return fmt.Errorf("read reminder: %w", err)
	}
	if n == 0 {
		return NotFound("no reminder %s", id)
	}
	return nil
}

// scanner is what Row and Rows have in common, so one scan serves both.
type scanner interface{ Scan(...any) error }

func scanReminder(sc scanner) (Reminder, error) {
	var (
		r        Reminder
		interval int64
		cre      int64
		done     sql.NullInt64
		nudged   sql.NullInt64
	)
	err := sc.Scan(&r.ID, &r.PrincipalID, &r.Text, &r.Note, &interval, &r.Priority, &cre, &done, &nudged)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Reminder{}, err
		}
		return Reminder{}, fmt.Errorf("read reminder: %w", err)
	}
	r.MinInterval = time.Duration(interval) * time.Second
	r.CreatedAt = time.Unix(cre, 0).UTC()
	r.DoneAt = timeFrom(done)
	r.LastNudgedAt = timeFrom(nudged)
	return r, nil
}
