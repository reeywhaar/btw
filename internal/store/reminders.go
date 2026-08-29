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

// DefaultMinInterval is the floor a reminder gets when nobody says otherwise: a day.
const DefaultMinInterval = 24 * time.Hour

// MaxReminderText is a sentence, not an essay. The whole thing has to be readable on a
// lock screen, and a push payload has a hard ceiling of its own.
const MaxReminderText = 500

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
