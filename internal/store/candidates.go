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

// Candidates returns the reminders eligible to be sent right now.
//
// The eligibility rules are here, in SQL, and the weighting is in internal/pick. The split
// is deliberate: eligibility is a hard filter that a database index can serve, and
// weighting is arithmetic over a handful of rows that wants to be testable against a fixed
// seed without a database.
//
// Not done, priority above zero, and past its own floor. Zero priority is somebody
// silencing one reminder deliberately — a real setting, and the only way to keep something
// written down without it ever arriving.
func (s *Store) Candidates(ctx context.Context, principalID string, now time.Time) ([]Candidate, error) {
	rows, err := s.main.QueryContext(ctx,
		`SELECT id, text, priority, min_interval, last_nudged_at
		   FROM reminders
		  WHERE principal_id = ?
		    AND done_at IS NULL
		    AND priority > 0
		    AND (last_nudged_at IS NULL OR last_nudged_at + min_interval <= ?)`,
		principalID, unix(now))
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
