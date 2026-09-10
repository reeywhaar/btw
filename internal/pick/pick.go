// Package pick chooses which reminder a nudge carries, and never when.
//
// A pure function of a set of candidates, an instant and a seed, so its behaviour is testable
// against a fixed seed rather than a database and a wall clock. It runs at the moment of
// sending, so a reminder ended at lunchtime does not still arrive at four.
package pick

import (
	"crypto/sha256"
	"encoding/binary"
	"math/rand/v2"
	"time"

	"btw/internal/store"
)

// StalenessCap bounds how overdue a reminder counts as. Without it something written in March
// would by June take every slot until it was answered.
const StalenessCap = 4.0

// Neutral is what an hour nobody has an opinion about is worth. The companion's number is the
// multiplier itself; half rather than one because a weighted draw normalises by the total.
// Zero excludes rather than damps.
const Neutral = 0.5

// nominalInterval is the denominator for a reminder with no floor: it decides nothing about
// eligibility, only how quickly one overtakes another.
const nominalInterval = 24 * time.Hour

// Pick returns the reminder to send, or false when there is nothing to send.
//
// `exclude` is what the last nudge carried, refused outright while anything else is available,
// and allowed through when it is the only candidate. Nothing eligible sends nothing: padding a
// slot is how a channel gets turned off for good.
//
// `local` is where `now` falls in the person's own week, converted by internal/rhythm so this
// stays a pure function of integers.
func Pick(candidates []store.Candidate, now time.Time, local Moment, exclude, seed string) (store.Candidate, bool) {
	// Every weight being zero falls through to the uniform fallback below, which would then pick
	// from exactly the reminders meant to be passed over.
	eligible := make([]store.Candidate, 0, len(candidates))
	for _, c := range candidates {
		if c.Priority > 0 && Advice(c, local) > 0 {
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
		// Only when it leaves something: one reminder coming back beats one going quiet.
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

	// Only staleness reaches here, zero advice having been filtered out above, and only on a
	// manual draw — which ignores a floor, so everything offered may have been nudged a moment
	// ago. A button was pressed and the pool is not empty, so the choice is uniform.
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
	// Floating point can leave `point` a hair above zero after the last subtraction.
	return eligible[len(eligible)-1], true
}

// Weight is how likely one reminder is to be drawn, relative to the others.
//
//	weight = priority × min(cap, elapsed / min_interval) × advice
//
// The staleness term needs no separate anti-repeat rule: a reminder just nudged has elapsed
// zero. Priority is a probability rather than an order — 90 arrives more often than 10 and
// never silences it, which a sort would fail to give.
func Weight(c store.Candidate, now time.Time, local Moment) float64 {
	if c.Priority <= 0 {
		return 0
	}
	// Never nudged counts as maximally stale, so something just written down arrives soon.
	// Advice still applies: the first arrival is the one most worth placing well.
	if c.LastNudgedAt.IsZero() {
		return float64(c.Priority) * StalenessCap * Advice(c, local)
	}

	// Without a denominator every unfloored reminder would weigh the same, making the draw a
	// coin toss between something raised a minute ago and something raised last week.
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

// Moment is where an instant falls in somebody's week: Day 0 is Monday, matching [store.Curve],
// and Minute is since local midnight.
type Moment struct {
	Day    int
	Minute int
}

// Advice is what the companion's opinion does to one reminder's weight, right now. [Neutral]
// when it has said nothing, which is what makes this optional per reminder rather than per
// account.
func Advice(c store.Candidate, local Moment) float64 {
	if !c.Advised {
		return Neutral
	}
	confidence, ok := c.Curve.At(local.Day, local.Minute)
	if !ok {
		return Neutral
	}
	// A stray 1.4 read literally would be a multiplier no honest answer can reach.
	return min(max(confidence, 0), 1)
}

func seedFrom(s string) uint64 {
	sum := sha256.Sum256([]byte(s))
	return binary.BigEndian.Uint64(sum[:8])
}
