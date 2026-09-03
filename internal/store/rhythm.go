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
	DefaultMinGap   = 45
)

// MaxBudget is a ceiling on top of what the window can hold, so a control cannot ask for a
// nudge every four minutes even in a very long waking day.
//
// The window and the gap still bind first, and usually well below this: nine to ten at
// forty-five minutes apart holds seventeen. Twenty-four is reachable with the window
// switched off, which is somebody asking to be nudged roughly once an hour, all day.
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
	MinGap int

	// Silent means the notification arrives without a sound. The reminder still shows; it
	// simply does not announce itself.
	Silent bool
}

// Bounds is the window a day is actually planned inside, in minutes since local midnight.
//
// Everything that plans or validates goes through here rather than reading WakeMinute
// directly, so "no window" is one answer in one place instead of a condition every caller
// has to remember.
func (r Rhythm) Bounds() (start, end int) {
	if !r.WindowEnabled {
		return 0, 24 * 60
	}
	return r.WakeMinute, r.SleepMinute
}

// Window is how long the window a day is planned inside is.
func (r Rhythm) Window() time.Duration {
	start, end := r.Bounds()
	return time.Duration(end-start) * time.Minute
}

// MaxBudgetForWindow is how many nudges this window can hold at this spacing.
//
// A budget the window cannot hold is refused rather than accepted and quietly squashed:
// asking for eight and receiving six, with no explanation, is worse than being told the
// window is too short.
func (r Rhythm) MaxBudgetForWindow() int {
	if r.MinGap <= 0 {
		return MaxBudget
	}
	start, end := r.Bounds()

	// N nudges need N-1 gaps between them, not N. Dividing the window by the gap counts
	// gaps and then reports that as a number of nudges, which is one short — thirteen
	// waking hours at forty-five minutes apart were offered as seventeen when eighteen fit
	// with a quarter of an hour to spare.
	//
	// The minus one on the window is the strict inequality the planner enforces: a slot has
	// to land *before* the window closes, so a window of exactly one gap holds one nudge
	// rather than two.
	n := (end-start-1)/r.MinGap + 1
	return min(max(n, 1), MaxBudget)
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
		MinGap:        DefaultMinGap,
	}
	err := s.main.QueryRowContext(ctx,
		`SELECT timezone, window_enabled, wake_minute, sleep_minute, budget, min_gap, silent
		   FROM rhythm WHERE principal_id = ?`,
		principalID).Scan(&r.Timezone, &r.WindowEnabled, &r.WakeMinute, &r.SleepMinute,
		&r.Budget, &r.MinGap, &r.Silent)
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
	case r.WakeMinute < 0 || r.SleepMinute > 24*60:
		return Invalid("waking hours have to be inside a day")
	case r.WakeMinute >= r.SleepMinute:
		// Validated even when the window is switched off, because the hours are kept and
		// have to still be a window when somebody switches it back on. A stored 22:00–09:00
		// would be a save that fails much later, for a reason nobody would connect to this.
		//
		// Night owls want 22:00–02:00 and cannot have it yet: a window crossing midnight
		// means slots belonging to two local dates, which the planner does not model.
		return Invalid("for now the waking window has to start and end on the same day")
	case r.MinGap < 0:
		return Invalid("the gap between nudges cannot be negative")
	case r.Budget < 0:
		return Invalid("a day cannot hold fewer than no nudges")
	case r.Budget > r.MaxBudgetForWindow():
		start, end := r.Bounds()
		if !r.WindowEnabled {
			return Invalid("%d a day will not fit into a day at %d minutes apart; %d is the most",
				r.Budget, r.MinGap, r.MaxBudgetForWindow())
		}
		return Invalid("%d a day will not fit between %s and %s at %d minutes apart; %d is the most",
			r.Budget, clock(start), clock(end), r.MinGap, r.MaxBudgetForWindow())
	}

	_, err := s.main.ExecContext(ctx,
		`INSERT INTO rhythm (principal_id, timezone, window_enabled, wake_minute, sleep_minute, budget, min_gap, silent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (principal_id) DO UPDATE SET
		   timezone = excluded.timezone, window_enabled = excluded.window_enabled,
		   wake_minute = excluded.wake_minute, sleep_minute = excluded.sleep_minute,
		   budget = excluded.budget, min_gap = excluded.min_gap, silent = excluded.silent`,
		r.PrincipalID, r.Timezone, r.WindowEnabled, r.WakeMinute, r.SleepMinute, r.Budget,
		r.MinGap, r.Silent)
	if err != nil {
		return fmt.Errorf("set rhythm: %w", err)
	}
	return nil
}

// clock renders minutes-since-midnight for a message somebody reads.
func clock(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }
