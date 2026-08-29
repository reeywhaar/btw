// Package rhythm decides when somebody is nudged, and never what with.
//
// Splitting those two questions is the design, not a tidiness. When is a fact about a
// person's day and can be planned hours ahead; what is a fact about their reminders at one
// instant and must not be — see internal/pick. Neither package imports the other.
//
// Everything here is a pure function of a rhythm, a date and a seed. It opens no
// transaction and reads no clock, which is what makes the interesting behaviour testable
// against a fixed seed rather than against a database at four in the afternoon.
package rhythm

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"time"

	// The zone database, compiled into the binary.
	//
	// About 450KB, and it buys the one thing btw needs local time for: quiet hours. A
	// reminder product that pings at four in the morning is uninstalled that morning. The
	// alternative was `apk add tzdata` in the image, which is a property of a base image
	// that a future bump can quietly drop — this is a property of the program.
	//
	// A stored UTC offset instead of an IANA name would have been wrong twice a year for
	// weeks at a time, which is worse than wrong all the time.
	_ "time/tzdata"

	"btw/internal/store"
)

// Grace is how late a nudge may be before it is dropped instead of sent.
//
// A missed slot is missed. An instance that was down for three hours does not catch up on
// restart: three notifications arriving together, every one of them about a moment that
// has passed, is indistinguishable from a broken app and is what teaches somebody to swipe
// the channel away for good.
const Grace = 10 * time.Minute

// Location resolves a rhythm's timezone, falling back to UTC.
//
// A zone that will not load is not a reason to stop nudging somebody: it is a reason to
// nudge them on the wrong hours until they fix it, which they can see and correct, whereas
// silence looks like the product not working.
func Location(r store.Rhythm) *time.Location {
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// LocalDate is the person's own date at that instant, as YYYY-MM-DD.
//
// The plan is keyed by this rather than by a UTC date, which is what makes it one plan per
// waking day rather than one per rotation of the earth measured from Greenwich.
func LocalDate(r store.Rhythm, at time.Time) string {
	return at.In(Location(r)).Format(time.DateOnly)
}

// PlanDay chooses the instants a person will be nudged at on one of their local days.
//
// Stratified rather than rejection-sampled: the window is cut into `budget` equal blocks
// and one instant is drawn uniformly inside each. That terminates — always, on the first
// try — and gives both properties that matter. Unpredictable inside its block, so nobody
// can wait for it; never three in an hour, because each block holds exactly one.
//
// Uniform rejection sampling with a minimum-gap constraint would give a slightly nicer
// distribution and can loop for a long time on a short window, which is a bad trade for a
// function that runs inside a scheduler tick.
//
// The seed is the person and the date, so a day's plan is reproducible. "Why did it go off
// at 04:12" is a question worth being able to answer without having been watching.
func PlanDay(r store.Rhythm, date string, seed string) ([]time.Time, error) {
	if r.Budget <= 0 {
		return nil, nil
	}
	loc := Location(r)
	day, err := time.ParseInLocation(time.DateOnly, date, loc)
	if err != nil {
		return nil, fmt.Errorf("plan %s: %w", date, err)
	}

	// Bounds rather than the hours directly, so "no window" is one answer in one place —
	// the whole local day — instead of a condition the planner has to remember.
	//
	// Added as minutes rather than by constructing a time from wall-clock fields, so a day
	// on which the clocks change is a shorter or longer window rather than an invalid or
	// ambiguous local time that the parser has to guess at.
	from, to := r.Bounds()
	start := day.Add(time.Duration(from) * time.Minute)
	end := day.Add(time.Duration(to) * time.Minute)
	window := end.Sub(start)
	if window <= 0 {
		return nil, nil
	}

	rng := rand.New(rand.NewPCG(seedFrom(seed+"/"+date), 0x9E3779B97F4A7C15))
	gap := time.Duration(r.MinGap) * time.Minute
	block := window / time.Duration(r.Budget)

	out := make([]time.Time, 0, r.Budget)
	var last time.Time
	for i := range r.Budget {
		blockStart := start.Add(block * time.Duration(i))
		at := blockStart.Add(time.Duration(rng.Int64N(int64(block))))

		// Two adjacent blocks can place their instants at the end of one and the start of
		// the next. Pushing the later one forward biases a few slots slightly later, which
		// is worth stating rather than hiding — the alternative is resampling, and a loop
		// that can fail to terminate does not belong in a scheduler.
		if !last.IsZero() && at.Sub(last) < gap {
			at = last.Add(gap)
		}
		if !at.Before(end) {
			// The window ran out. A short day is the honest outcome; padding it would put
			// two nudges on top of each other at bedtime.
			break
		}
		out = append(out, at.UTC())
		last = at
	}
	return out, nil
}

// seedFrom turns a string into a reproducible 64-bit seed.
func seedFrom(s string) uint64 {
	sum := sha256.Sum256([]byte(s))
	return binary.BigEndian.Uint64(sum[:8])
}
