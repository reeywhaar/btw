// Package rhythm decides whether somebody is due a nudge, and never what with.
//
// Splitting those two questions is the design, not a tidiness. Whether it is time is a fact
// about the clock and a person's waking hours; what to send is a fact about their reminders
// at that instant, and lives in internal/pick. Neither package imports the other.
//
// Everything here is a pure function of a rhythm, two instants and a seed. It opens no
// transaction and reads no clock, which is what makes the interesting behaviour testable
// against a fixed seed rather than against an afternoon.
package rhythm

import (
	"math/rand/v2"
	"time"

	// The zone database, compiled into the binary.
	//
	// About 450KB, and it buys the one thing btw needs local time for: waking hours. A
	// reminder product that pings at four in the morning is uninstalled that morning. The
	// alternative was installing tzdata in the image, which is a property of a base image
	// that a future bump can quietly drop — this is a property of the program.
	//
	// A stored UTC offset instead of an IANA name would be wrong twice a year for weeks at
	// a time, which is worse than wrong all the time.
	_ "time/tzdata"

	"btw/internal/store"
)

// Tick is how often the scheduler asks. It is also the floor on how close two nudges can
// land, since nothing can happen between two ticks.
const Tick = 5 * time.Minute

// Spread is how far past the interval a nudge may be scheduled, as a fraction of it.
//
// The loop asks every Tick, so the moment a wait completes is always a tick boundary.
// Firing there would put every nudge on a five-minute mark; scheduling one a random moment
// later puts it anywhere.
//
// Small on purpose. It is enough that nobody can set a watch by it, and not so much that
// the day stops holding roughly the number somebody asked for — which is not a promise
// anyway. A day that delivers eight when the rhythm says nine behaved correctly.
const Spread = 0.10

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

// Awake reports whether `at` falls inside the person's waking hours.
func Awake(r store.Rhythm, at time.Time) bool {
	if !r.WindowEnabled {
		return true
	}
	local := at.In(Location(r))
	minute := local.Hour()*60 + local.Minute()
	return minute >= r.WakeMinute && minute < r.SleepMinute
}

// Interval is the average time between nudges: the waking day divided by the budget.
//
// Never shorter than a Tick, because nothing can be delivered between two of them and
// promising otherwise would be promising something the loop cannot do.
func Interval(r store.Rhythm) time.Duration {
	if r.Budget <= 0 {
		return 0
	}
	start, end := r.Bounds()
	window := time.Duration(end-start) * time.Minute
	return max(window/time.Duration(r.Budget), Tick)
}

// Due reports whether a nudge is owed, given when the clock started running.
//
// Two rules and nothing else.
//
// **Not while somebody is asleep.** Outside the waking hours the answer is no, whatever the
// arithmetic says.
//
// **Long enough since the last one.** Long enough is the interval, plainly. The randomness
// lives in when the nudge is then scheduled for, not in the threshold — one source of it is
// easier to reason about than two, and a threshold that moved would have to be seeded from
// something stable or the effective wait would be the shortest of many rolls.
func Due(r store.Rhythm, since, now time.Time) bool {
	if r.Budget <= 0 || !Awake(r, now) {
		return false
	}
	if since.IsZero() {
		return true
	}
	interval := Interval(r)
	return interval > 0 && !now.Before(since.Add(interval))
}

// Delay is how long after deciding a nudge should actually go out: somewhere in the first
// Spread of an interval.
func Delay(r store.Rhythm) time.Duration {
	window := time.Duration(float64(Interval(r)) * Spread)
	if window <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(window) + 1))
}

// Since is when the wait for the next nudge started running.
//
// Ordinarily the last nudge. But sleeping does not count towards the wait: measured from a
// nudge the evening before, the answer at nine in the morning is always yes, and somebody
// would be pinged within a minute of waking every day of their life.
//
// So a night that ran past the last nudge starts the clock at waking — less half an
// interval, which the night is credited.
//
// That half is what makes the day hold the number asked for. Starting cleanly at waking, the
// first nudge lands a whole interval in and the last lands exactly at bedtime, where it is
// outside the window and dropped: a day asking for three delivered two. Half an interval of
// credit puts the first one halfway in and the last one half an interval short of bedtime.
func Since(r store.Rhythm, lastNudge, now time.Time) time.Time {
	if !r.WindowEnabled {
		return lastNudge
	}
	local := now.In(Location(r))
	woke := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location()).
		Add(time.Duration(r.WakeMinute) * time.Minute)
	if lastNudge.Before(woke) {
		return woke.Add(-Interval(r) / 2)
	}
	return lastNudge
}
