package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// The rhythm nobody has changed. Three a day between nine and ten, no closer than
// forty-five minutes.
//
// All three are guesses and are meant to be. They want a fortnight of somebody actually
// carrying a phone before they are defaults rather than placeholders.
const (
	DefaultTimezone = "UTC"
	DefaultWake     = 9 * 60
	DefaultSleep    = 22 * 60
	DefaultBudget   = 3
)

// MaxBudget is the most anybody may ask for in a day.
//
// A plain number now, and nothing else bounds it. The interval is the waking day divided by
// the budget and is floored at one tick, so no budget can ask the loop for something it
// cannot deliver — and there is no second ceiling to keep in agreement with a planner.
const MaxBudget = 24

// Rhythm is one person's answer to how often, and between which hours.
type Rhythm struct {
	PrincipalID string
	Timezone    string

	// WindowEnabled is whether the waking window applies at all. Off means any hour of the
	// day, which is a real thing to want and a real way to be woken at four in the morning.
	//
	// The hours below are kept either way. Unchecking the box would otherwise lose whatever
	// somebody had chosen, and typing them back in is exactly the bookkeeping this product
	// is trying not to have.
	WindowEnabled bool
	WakeMinute    int
	SleepMinute   int

	Budget int

	// Silent means the notification arrives without a sound. The reminder still shows; it
	// simply does not announce itself.
	Silent bool
}

// Window is how long the waking day is, in minutes.
//
// Everything that plans goes through here rather than subtracting the hours itself, so "no
// window" and "a window that crosses midnight" are each one answer in one place instead of a
// condition every caller has to remember.
//
// A window may end at or before it starts, which means it runs into the next day: somebody
// awake from noon until four is awake for sixteen hours, not minus eight. Equal ends mean the
// whole day — the same thing as switching the window off, and the natural way to say so with
// two controls that each name an hour.
func (r Rhythm) Window() int {
	if !r.WindowEnabled {
		return 24 * 60
	}
	length := r.SleepMinute - r.WakeMinute
	if length <= 0 {
		length += 24 * 60
	}
	return length
}

// Rhythm reads one person's, or the defaults if they have never touched it.
//
// A missing row is the defaults rather than an error, and nothing writes a row at account
// creation: a table where most rows equal the defaults is a table that has to be kept in
// step with them, and this way changing a default changes it for everybody who never had
// an opinion.
func (s *Store) Rhythm(ctx context.Context, principalID string) (Rhythm, error) {
	r := Rhythm{
		PrincipalID:   principalID,
		Timezone:      DefaultTimezone,
		WindowEnabled: true,
		WakeMinute:    DefaultWake,
		SleepMinute:   DefaultSleep,
		Budget:        DefaultBudget,
	}
	err := s.main.QueryRowContext(ctx,
		`SELECT timezone, window_enabled, wake_minute, sleep_minute, budget, silent
		   FROM rhythm WHERE principal_id = ?`,
		principalID).Scan(&r.Timezone, &r.WindowEnabled, &r.WakeMinute, &r.SleepMinute,
		&r.Budget, &r.Silent)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return Rhythm{}, fmt.Errorf("read rhythm: %w", err)
	}
	return r, nil
}

// SetRhythm writes one, validating the parts that have to agree with each other.
func (s *Store) SetRhythm(ctx context.Context, r Rhythm) error {
	if _, err := time.LoadLocation(r.Timezone); err != nil {
		// The zone database is imported into the binary — see internal/rhythm — so this
		// is a real answer rather than a property of the base image.
		return Invalid("%q is not a timezone", r.Timezone)
	}
	switch {
	case r.WakeMinute < 0 || r.WakeMinute > 24*60 || r.SleepMinute < 0 || r.SleepMinute > 24*60:
		return Invalid("waking hours have to be inside a day")
	case r.Budget < 0:
		return Invalid("a day cannot hold fewer than no nudges")
	case r.Budget > MaxBudget:
		return Invalid("%d a day is more than btw will send; %d is the most", r.Budget, MaxBudget)
	}

	// Midnight has two names and they mean the same hour. Storing 24:00 as 1440 would make
	// "00:00 to 24:00" a window of zero rather than a whole day, and "24:00 to 09:00" a start
	// no minute of the day is ever past.
	r.WakeMinute %= 24 * 60
	r.SleepMinute %= 24 * 60

	_, err := s.main.ExecContext(ctx,
		`INSERT INTO rhythm (principal_id, timezone, window_enabled, wake_minute, sleep_minute, budget, silent)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (principal_id) DO UPDATE SET
		   timezone = excluded.timezone, window_enabled = excluded.window_enabled,
		   wake_minute = excluded.wake_minute, sleep_minute = excluded.sleep_minute,
		   budget = excluded.budget, silent = excluded.silent`,
		r.PrincipalID, r.Timezone, r.WindowEnabled, r.WakeMinute, r.SleepMinute, r.Budget,
		r.Silent)
	if err != nil {
		return fmt.Errorf("set rhythm: %w", err)
	}
	return nil
}
