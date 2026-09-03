package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"btw/internal/ids"
)

// What a person pressed. Recorded here and nowhere else: reminders.done_at says a reminder
// ended, and this says which of the two gestures ended it.
//
// Nothing in the program reads the distinction back. It is kept because the two are
// different acts — one is "I did it" and the other is "I do not want this" — and a log
// that flattened them could not answer a question that has not been asked yet.
const (
	ActionDone = "done"
	ActionDrop = "drop"
)

// Nudge is one delivery of one reminder.
type Nudge struct {
	ID          string
	PrincipalID string
	ReminderID  string
	SentAt      time.Time
	ActedAt     time.Time
	Action      string
}

// NewNudgeID mints the id a nudge will have.
//
// Minted by the caller rather than here because the id has to travel *inside* the encrypted
// payload — the notification's buttons post back to it — so it must exist before anything
// is sent, and the row must not exist until something was.
func NewNudgeID() string { return ids.New(ids.Nudge) }

// RecordNudge logs a delivery and stamps the reminder it carried.
//
// Called only after something actually reached a push service. A nudge recorded for a
// message that never left would spend the reminder's floor on a notification nobody got,
// which is the one failure that looks exactly like the product ignoring you.
//
// Two databases, so two writes and no transaction across them. The stamp goes second: a
// nudge logged without its stamp means the reminder can be drawn again sooner than its
// floor allows, which is a nudge somebody gets twice in a morning. A stamp without its log
// means a reminder waits out its interval for a nudge missing from the history. The second
// is the one nobody notices, so it is the one that can happen.
func (s *Store) RecordNudge(ctx context.Context, nudgeID, principalID, reminderID string) (Nudge, error) {
	n := Nudge{
		ID:          nudgeID,
		PrincipalID: principalID,
		ReminderID:  reminderID,
		SentAt:      s.Now().Truncate(time.Second),
	}
	if _, err := s.derived.ExecContext(ctx,
		`INSERT INTO nudges (id, principal_id, reminder_id, sent_at) VALUES (?, ?, ?, ?)`,
		n.ID, n.PrincipalID, n.ReminderID, unix(n.SentAt)); err != nil {
		return Nudge{}, fmt.Errorf("record nudge: %w", err)
	}
	if err := s.StampNudged(ctx, reminderID, n.SentAt); err != nil {
		return Nudge{}, err
	}
	return n, nil
}

// LastNudgeAt is when this account was last nudged, whatever it carried.
//
// The scheduler's entire memory. There is no plan and nothing to claim: how long it has
// been, and whether somebody is awake, is the whole decision.
func (s *Store) LastNudgeAt(ctx context.Context, principalID string) (time.Time, error) {
	var at sql.NullInt64
	if err := s.derived.QueryRowContext(ctx,
		`SELECT max(sent_at) FROM nudges WHERE principal_id = ?`, principalID).Scan(&at); err != nil {
		return time.Time{}, fmt.Errorf("read last nudge: %w", err)
	}
	return timeFrom(at), nil
}

// LastNudgedReminder is the reminder the most recent nudge carried, which is the one the
// next nudge may not carry.
//
// A hard rule rather than a weighting, because a repeat is the one thing a person notices
// immediately and forgives least.
func (s *Store) LastNudgedReminder(ctx context.Context, principalID string) (string, error) {
	var id string
	err := s.derived.QueryRowContext(ctx,
		`SELECT reminder_id FROM nudges WHERE principal_id = ? ORDER BY sent_at DESC, id DESC LIMIT 1`,
		principalID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read last nudge: %w", err)
	}
	return id, nil
}

// CountNudges is how many times one reminder has been raised. For tests and for anything
// that later wants to show a history; nothing in the daemon calls it.
func (s *Store) CountNudges(ctx context.Context, reminderID string) (int, error) {
	var n int
	if err := s.derived.QueryRowContext(ctx,
		`SELECT count(*) FROM nudges WHERE reminder_id = ?`, reminderID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count nudges: %w", err)
	}
	return n, nil
}

// ActOnNudge records which button was pressed and returns the reminder it was about, so
// the caller can end it.
//
// Acting twice is not an error and does not move the record: a notification that has sat
// on a lock screen since yesterday can be answered after the thing was already ended in
// the app, and the person pressing it wanted it ended either way.
func (s *Store) ActOnNudge(ctx context.Context, principalID, nudgeID, action string) (string, error) {
	if action != ActionDone && action != ActionDrop {
		return "", Invalid("%q is not something you can do with a nudge", action)
	}
	var reminderID string
	err := s.derived.QueryRowContext(ctx,
		`SELECT reminder_id FROM nudges WHERE id = ? AND principal_id = ?`, nudgeID, principalID).Scan(&reminderID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", NotFound("no nudge %s", nudgeID)
	}
	if err != nil {
		return "", fmt.Errorf("read nudge: %w", err)
	}
	if _, err := s.derived.ExecContext(ctx,
		`UPDATE nudges SET acted_at = ?, action = ? WHERE id = ? AND acted_at IS NULL`,
		unix(s.Now()), action, nudgeID); err != nil {
		return "", fmt.Errorf("act on nudge: %w", err)
	}
	return reminderID, nil
}
