// Package pick chooses which reminder a nudge carries, and never when.
//
// It is a pure function of a set of candidates, an instant and a seed. That is what makes
// the interesting behaviour — that the same thing does not arrive twice running, that a
// long-ignored reminder rises, that an empty pool sends nothing — testable against a fixed
// seed instead of against a database and a wall clock.
//
// The choice is made at the moment of sending and never when the slot was drawn. Choosing
// at planning time would mean a reminder ended at lunchtime still arriving at four, and a
// decision about an evening made with the morning's information.
package pick

import (
	"crypto/sha256"
	"encoding/binary"
	"math/rand/v2"
	"time"

	"btw/internal/store"
)

// StalenessCap bounds how overdue a reminder is allowed to count as.
//
// Without it, something written in March with a one-day floor would by June be a thousand
// times likelier than anything else and would take every slot until it was answered.
// Beyond a few multiples of its own interval a reminder is as overdue as it is going to
// get.
const StalenessCap = 4.0

// What the companion's advice does to a weight.
//
// The companion answers with a number from 0 to 1 for each half hour of the day, and it is
// stretched onto this range and multiplied in. Nothing is a special case: no in-or-out, no
// separate rule for a reminder wanting full attention, no threshold anywhere. A curve saying
// 0.5 everywhere is a companion with no opinion, and comes to exactly 1.
//
// **Never zero, and not by rounding — by construction.** The floor is the guarantee: the
// companion moves a reminder around the day, and does not get to remove one. Silencing is a
// person's decision with exactly one expression, priority zero, and a model able to reach the
// same outcome by answering zeroes would be a second and invisible way for something to stop
// arriving.
//
// Three to one between the best half hour and the worst. Gentler than the twelve to one the
// windows it replaced could reach, deliberately: a continuous curve applies its opinion to
// every hour rather than to the handful inside a window, so the same strength per hour adds up
// to far more. Staleness runs to four and keeps climbing underneath either way, so a reminder
// the companion likes nowhere still surfaces — later, and by a route nothing here special-cases.
const (
	// Floor is the multiplier at a confidence of 0, and Ceiling at 1.
	Floor   = 0.5
	Ceiling = 1.5
)

// nominalInterval is the denominator for a reminder that states no floor of its own. It
// decides nothing about eligibility — only how quickly one reminder overtakes another.
const nominalInterval = 24 * time.Hour

// Pick returns the reminder to send, or false when there is nothing to send.
//
// `exclude` is the reminder the last nudge carried. It is refused outright while anything
// else is available — a hard rule rather than a weighting, because a repeat is the one
// thing a person notices immediately and forgives least. When it is the only candidate it
// is allowed through: silence would be the worse answer for somebody with one reminder.
//
// The empty case is not a failure and is not padded. A slot with nothing eligible sends
// nothing at all: reaching for the next-least-ineligible thing, or repeating this
// morning's, is how a notification channel gets turned off for good by somebody who was
// otherwise happy with it.
//
// `local` is where `now` falls in the person's own week. It arrives already converted because
// internal/rhythm is the one place in the program that knows what time it is anywhere; this
// function indexes with integers and stays a pure function of its arguments.
func Pick(candidates []store.Candidate, now time.Time, local Moment, exclude, seed string) (store.Candidate, bool) {
	// Silenced reminders are dropped here and not merely weighted to zero. The store filters
	// them too, but the rule belongs to this function: the uniform fallback below would
	// otherwise draw one, and "zero means never" would hold only as long as every caller
	// remembered to filter first.
	eligible := make([]store.Candidate, 0, len(candidates))
	for _, c := range candidates {
		if c.Priority > 0 {
			eligible = append(eligible, c)
		}
	}
	if exclude != "" {
		filtered := make([]store.Candidate, 0, len(eligible))
		for _, c := range eligible {
			if c.ID != exclude {
				filtered = append(filtered, c)
			}
		}
		// Only when it leaves something. One reminder that keeps coming back is the
		// product working; one reminder that goes quiet is the product broken.
		if len(filtered) > 0 {
			eligible = filtered
		}
	}
	if len(eligible) == 0 {
		return store.Candidate{}, false
	}

	weights := make([]float64, len(eligible))
	var total float64
	for i, c := range eligible {
		weights[i] = Weight(c, now, local)
		total += weights[i]
	}

	// Every weight zero, and yet there is something to send.
	//
	// It cannot happen on a scheduled draw — the floor guarantees a reminder has been
	// waiting at least one interval, so its staleness is at least one. It happens on a
	// manual one, where the floor is ignored and everything offered may have been nudged a
	// moment ago. Weighting has nothing left to say there, so the choice is uniform rather
	// than nothing: somebody pressed a button, and the pool is not empty.
	if total <= 0 {
		return eligible[int(rand.New(rand.NewPCG(seedFrom(seed), 0x9E3779B97F4A7C15)).Uint64()%uint64(len(eligible)))], true
	}

	rng := rand.New(rand.NewPCG(seedFrom(seed), 0x9E3779B97F4A7C15))
	point := rng.Float64() * total
	for i, w := range weights {
		point -= w
		if point < 0 {
			return eligible[i], true
		}
	}
	// Floating-point arithmetic can leave `point` a hair above zero after the last
	// subtraction. The last candidate is the answer in that case, not a panic.
	return eligible[len(eligible)-1], true
}

// Weight is how likely one reminder is to be drawn, relative to the others.
//
//	weight = priority × min(cap, elapsed / min_interval) × advice
//
// The multiplier is what makes this not a loop, and it needs no separate rule to stop one.
// The moment a reminder is nudged its elapsed time is zero, so its weight is zero — and it
// is hard-blocked by its own floor besides. It re-enters the pool as the floor passes and
// then grows likelier, relative to everything else, the longer it goes unmentioned. Two
// reminders at equal priority alternate; a hundred rotate; one arrives at its floor and no
// faster.
//
// Priority is a probability rather than an order. One at 90 arrives more often than one at
// 10 and never silences it, which a sort would fail to give.
func Weight(c store.Candidate, now time.Time, local Moment) float64 {
	if c.Priority <= 0 {
		return 0
	}
	// Never nudged counts as maximally stale: a reminder just written down should arrive
	// soon, which is also the fastest way for somebody to find out the thing works.
	//
	// Advice still applies. It is tempting to let a new reminder skip it and arrive at once,
	// but the first arrival is the one most worth placing well — and maximal staleness already
	// puts it far enough ahead that the damping only decides which hour, not whether.
	if c.LastNudgedAt.IsZero() {
		return float64(c.Priority) * StalenessCap * Advice(c, local)
	}

	// A reminder with no floor of its own is still ordered by how long it has waited — the
	// nominal day decides only how fast one overtakes another, never whether it may be
	// drawn. Without it every unfloored reminder would weigh the same and the draw would be
	// a coin toss between something raised a minute ago and something raised last week.
	denominator := c.MinInterval
	if denominator <= 0 {
		denominator = nominalInterval
	}

	staleness := min(now.Sub(c.LastNudgedAt).Seconds()/denominator.Seconds(), StalenessCap)
	if staleness < 0 {
		staleness = 0
	}
	return float64(c.Priority) * staleness * Advice(c, local)
}

// Moment is where an instant falls in somebody's week.
//
// Day is 0 for Monday through 6 for Sunday, matching [store.Curve]. Minute is since local
// midnight, the same units a rhythm's waking window uses.
type Moment struct {
	Day    int
	Minute int
}

// Advice is what the companion's opinion does to one reminder's weight, right now.
//
// Exactly 1 when it has said nothing, which is what makes the whole feature optional at the
// level of one reminder rather than one account: a reminder written a minute ago, before the
// next round of questions, weighs precisely what it weighed before any of this existed. Also
// exactly 1 for an answer that could not be read, which is the same situation from here.
func Advice(c store.Candidate, local Moment) float64 {
	if !c.Advised {
		return 1
	}
	confidence, ok := c.Curve.At(local.Day, local.Minute)
	if !ok {
		return 1
	}
	// Clamped rather than trusted. A model asked for 0 to 1 mostly answers inside it, and a
	// stray 1.4 read literally would hand one reminder a multiplier no honest answer can reach.
	confidence = min(max(confidence, 0), 1)
	return Floor + confidence*(Ceiling-Floor)
}

func seedFrom(s string) uint64 {
	sum := sha256.Sum256([]byte(s))
	return binary.BigEndian.Uint64(sum[:8])
}
