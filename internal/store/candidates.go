package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Candidate is a reminder as the selection sees it: the text to send, and the three
// numbers that decide whether and how likely it is to be sent.
//
// A narrow struct rather than a Reminder, because internal/pick is a pure function and
// should not be handed columns it has no business reading. What it needs is what is here.
type Candidate struct {
	ID           string
	Text         string
	Priority     int
	MinInterval  time.Duration
	LastNudgedAt time.Time
}

// Floor says whether a reminder's own minimum interval applies to this draw.
type Floor bool

const (
	// RespectFloor is what a scheduled nudge uses. A reminder inside its own interval is
	// not offered, which is most of what stops the same thing arriving twice in a morning.
	RespectFloor Floor = true

	// IgnoreFloor is what "send one now" uses.
	//
	// The floor governs when the *scheduler* may raise something. Somebody pressing a
	// button has asked for a nudge, and answering "that was raised too recently" is
	// refusing a request nobody made on their behalf — the button exists to prove the chain
	// works, and a button that usually declines proves nothing.
	IgnoreFloor Floor = false
)

// Candidates returns the reminders that could be sent right now.
//
// The eligibility rules are here, in SQL, and the weighting is in internal/pick. The split
// is deliberate: eligibility is a hard filter that a database index can serve, and
// weighting is arithmetic over a handful of rows that wants to be testable against a fixed
// seed without a database.
//
// Not done and priority above zero, always. Zero priority is somebody silencing one
// reminder deliberately — a real setting, and the only way to keep something written down
// without it ever arriving — so it holds even for a manual send, which is the difference
// between "not now" and "not ever".
func (s *Store) Candidates(ctx context.Context, principalID string, now time.Time, floor Floor) ([]Candidate, error) {
	query := `SELECT id, text, priority, min_interval, last_nudged_at
		   FROM reminders
		  WHERE principal_id = ?
		    AND done_at IS NULL
		    AND priority > 0`
	args := []any{principalID}
	if floor == RespectFloor {
		query += ` AND (last_nudged_at IS NULL OR last_nudged_at + min_interval <= ?)`
		args = append(args, unix(now))
	}

	rows, err := s.main.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read candidates: %w", err)
	}
	defer rows.Close()

	var out []Candidate
	for rows.Next() {
		var (
			c        Candidate
			interval int64
			nudged   sql.NullInt64
		)
		if err := rows.Scan(&c.ID, &c.Text, &c.Priority, &interval, &nudged); err != nil {
			return nil, fmt.Errorf("read candidate: %w", err)
		}
		c.MinInterval = time.Duration(interval) * time.Second
		c.LastNudgedAt = timeFrom(nudged)
		out = append(out, c)
	}
	return out, rows.Err()
}
