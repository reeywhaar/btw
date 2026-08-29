package pick

import (
	"fmt"
	"testing"
	"time"

	"btw/internal/store"
)

var now = time.Date(2026, 8, 29, 16, 20, 0, 0, time.UTC)

func candidate(id string, priority int, lastNudged time.Duration) store.Candidate {
	c := store.Candidate{
		ID:          id,
		Text:        id,
		Priority:    priority,
		MinInterval: 24 * time.Hour,
	}
	if lastNudged > 0 {
		c.LastNudgedAt = now.Add(-lastNudged)
	}
	return c
}

func TestNothingEligibleSendsNothing(t *testing.T) {
	if _, ok := Pick(nil, now, "", "seed"); ok {
		t.Error("Pick() found something to send in an empty pool")
	}
}

func TestTheSameThingDoesNotArriveTwiceRunning(t *testing.T) {
	pool := []store.Candidate{
		candidate("r_one", 50, 48*time.Hour),
		candidate("r_two", 50, 48*time.Hour),
	}
	// Whatever the seed, the reminder the last nudge carried is not the answer while
	// anything else is available.
	for i := range 200 {
		got, ok := Pick(pool, now, "r_one", fmt.Sprintf("seed-%d", i))
		if !ok {
			t.Fatal("Pick() found nothing with two candidates")
		}
		if got.ID == "r_one" {
			t.Fatalf("seed %d repeated the last reminder", i)
		}
	}
}

func TestOneReminderStillArrives(t *testing.T) {
	pool := []store.Candidate{candidate("r_only", 50, 48*time.Hour)}
	// Silence would be the worse answer: one reminder that keeps coming back is the
	// product working, one that goes quiet is the product broken.
	got, ok := Pick(pool, now, "r_only", "seed")
	if !ok || got.ID != "r_only" {
		t.Fatalf("Pick() = (%q, %v), want the only candidate", got.ID, ok)
	}
}

func TestBeingNudgedCollapsesTheWeight(t *testing.T) {
	fresh := candidate("r_fresh", 50, 0)
	fresh.LastNudgedAt = now
	if w := Weight(fresh, now); w != 0 {
		t.Errorf("Weight() just after a nudge = %v, want 0", w)
	}
}

func TestStalenessRisesAndThenStops(t *testing.T) {
	atOneInterval := Weight(candidate("r", 50, 24*time.Hour), now)
	atTwo := Weight(candidate("r", 50, 48*time.Hour), now)
	atForty := Weight(candidate("r", 50, 40*24*time.Hour), now)

	if !(atOneInterval < atTwo) {
		t.Errorf("weight did not rise with staleness: %v then %v", atOneInterval, atTwo)
	}
	// Capped, so a reminder written in March cannot take every slot in June.
	if atForty != Weight(candidate("r", 50, 400*24*time.Hour), now) {
		t.Error("weight is not capped; a very old reminder keeps growing")
	}
	if atForty > float64(50)*StalenessCap {
		t.Errorf("weight %v exceeds the cap", atForty)
	}
}

func TestNeverNudgedIsMaximallyStale(t *testing.T) {
	// A reminder just written down should arrive soon: it is also the fastest way for
	// somebody to find out the thing works at all.
	if got, want := Weight(candidate("r_new", 50, 0), now), 50*StalenessCap; got != want {
		t.Errorf("Weight() of a never-nudged reminder = %v, want %v", got, want)
	}
}

func TestPriorityIsAProbabilityNotAnOrder(t *testing.T) {
	pool := []store.Candidate{
		candidate("r_loud", 90, 48*time.Hour),
		candidate("r_quiet", 10, 48*time.Hour),
	}
	counts := map[string]int{}
	for i := range 4000 {
		got, ok := Pick(pool, now, "", fmt.Sprintf("seed-%d", i))
		if !ok {
			t.Fatal("Pick() found nothing")
		}
		counts[got.ID]++
	}
	// The quiet one is drawn less often and is never silenced, which is what a sort would
	// fail to give.
	if counts["r_quiet"] == 0 {
		t.Error("the low-priority reminder never arrived; priority is acting as an order")
	}
	if counts["r_loud"] <= counts["r_quiet"] {
		t.Errorf("priority had no effect: loud %d, quiet %d", counts["r_loud"], counts["r_quiet"])
	}
}

func TestZeroPriorityIsNever(t *testing.T) {
	pool := []store.Candidate{candidate("r_silenced", 0, 48*time.Hour)}
	if _, ok := Pick(pool, now, "", "seed"); ok {
		t.Error("Pick() drew a reminder somebody silenced")
	}
}

func TestEqualRemindersAlternateRatherThanLoop(t *testing.T) {
	pool := []store.Candidate{
		candidate("r_one", 50, 48*time.Hour),
		candidate("r_two", 50, 48*time.Hour),
		candidate("r_three", 50, 48*time.Hour),
	}
	seen := map[string]bool{}
	last := ""
	for i := range 60 {
		got, ok := Pick(pool, now, last, fmt.Sprintf("seed-%d", i))
		if !ok {
			t.Fatal("Pick() found nothing")
		}
		seen[got.ID] = true
		last = got.ID
	}
	if len(seen) != len(pool) {
		t.Errorf("only %d of %d reminders were ever drawn", len(seen), len(pool))
	}
}

func TestPickIsReproducible(t *testing.T) {
	pool := []store.Candidate{
		candidate("r_one", 50, 48*time.Hour),
		candidate("r_two", 70, 30*time.Hour),
	}
	first, _ := Pick(pool, now, "", "n_01JABC")
	second, _ := Pick(pool, now, "", "n_01JABC")
	if first.ID != second.ID {
		t.Errorf("one seed gave two answers: %q then %q", first.ID, second.ID)
	}
}
