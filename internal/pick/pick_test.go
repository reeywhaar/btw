package pick

import (
	"fmt"
	"testing"
	"time"

	"btw/internal/store"
)

// whenever is the local moment these tests are run at. None of their candidates carries
// advice, so the multiplier is one and the moment cannot reach the weight — naming it says so
// rather than leaving a reader to check.
var whenever = Moment{}

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
	if _, ok := Pick(nil, now, whenever, "", "seed"); ok {
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
		got, ok := Pick(pool, now, whenever, "r_one", fmt.Sprintf("seed-%d", i))
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
	got, ok := Pick(pool, now, whenever, "r_only", "seed")
	if !ok || got.ID != "r_only" {
		t.Fatalf("Pick() = (%q, %v), want the only candidate", got.ID, ok)
	}
}

func TestBeingNudgedCollapsesTheWeight(t *testing.T) {
	fresh := candidate("r_fresh", 50, 0)
	fresh.LastNudgedAt = now
	if w := Weight(fresh, now, whenever); w != 0 {
		t.Errorf("Weight() just after a nudge = %v, want 0", w)
	}
}

func TestStalenessRisesAndThenStops(t *testing.T) {
	atOneInterval := Weight(candidate("r", 50, 24*time.Hour), now, whenever)
	atTwo := Weight(candidate("r", 50, 48*time.Hour), now, whenever)
	atForty := Weight(candidate("r", 50, 40*24*time.Hour), now, whenever)

	if !(atOneInterval < atTwo) {
		t.Errorf("weight did not rise with staleness: %v then %v", atOneInterval, atTwo)
	}
	// Capped, so a reminder written in March cannot take every slot in June.
	if atForty != Weight(candidate("r", 50, 400*24*time.Hour), now, whenever) {
		t.Error("weight is not capped; a very old reminder keeps growing")
	}
	if atForty > float64(50)*StalenessCap {
		t.Errorf("weight %v exceeds the cap", atForty)
	}
}

func TestNeverNudgedIsMaximallyStale(t *testing.T) {
	// A reminder just written down should arrive soon: it is also the fastest way for
	// somebody to find out the thing works at all.
	if got, want := Weight(candidate("r_new", 50, 0), now, whenever), 50*StalenessCap; got != want {
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
		got, ok := Pick(pool, now, whenever, "", fmt.Sprintf("seed-%d", i))
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
	if _, ok := Pick(pool, now, whenever, "", "seed"); ok {
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
		got, ok := Pick(pool, now, whenever, last, fmt.Sprintf("seed-%d", i))
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
	first, _ := Pick(pool, now, whenever, "", "n_01JABC")
	second, _ := Pick(pool, now, whenever, "", "n_01JABC")
	if first.ID != second.ID {
		t.Errorf("one seed gave two answers: %q then %q", first.ID, second.ID)
	}
}

func TestSomethingIsAlwaysDrawnWhenThePoolIsNotEmpty(t *testing.T) {
	// What "send one now" hands over: everything live, floor ignored, so every candidate
	// may have been nudged a moment ago and every weight is zero.
	justNudged := candidate("r_one", 50, 0)
	justNudged.LastNudgedAt = now
	other := candidate("r_two", 50, 0)
	other.LastNudgedAt = now

	got, ok := Pick([]store.Candidate{justNudged, other}, now, whenever, "", "seed")
	if !ok {
		t.Fatal("Pick() found nothing to send from a pool of two")
	}
	if got.ID != "r_one" && got.ID != "r_two" {
		t.Fatalf("Pick() returned %q, which is neither candidate", got.ID)
	}
}

func TestSilencedRemindersAreNeverDrawnEvenWithNothingElse(t *testing.T) {
	silenced := candidate("r_silenced", 0, 0)
	silenced.LastNudgedAt = now

	// The uniform fallback must not reach past the one rule that means "not ever".
	if _, ok := Pick([]store.Candidate{silenced}, now, whenever, "", "seed"); ok {
		t.Error("Pick() drew a silenced reminder through the fallback")
	}
}

// The reminder written a minute ago, before the companion has been asked again, has to weigh
// exactly what it weighed before any of this existed. This is what "optional" means at the
// level of one reminder rather than one account.
func TestAReminderWithNoAdviceWeighsExactlyWhatItDidBefore(t *testing.T) {
	now := time.Now()
	c := candidate("r", 50, 24*time.Hour)

	inside := Moment{Day: 2, Minute: 15 * 60}
	outside := Moment{Day: 5, Minute: 3 * 60}
	if Weight(c, now, inside) != Weight(c, now, outside) {
		t.Error("the moment moved the weight of a reminder nothing has been said about")
	}
	if got, want := Advice(c, inside), 1.0; got != want {
		t.Errorf("Advice() with nothing said = %v, want %v", got, want)
	}
}

func TestAdviceLiftsAReminderInsideItsHoursAndDampsItOutside(t *testing.T) {
	c := candidate("r", 50, 24*time.Hour)
	c.Advised = true
	c.Slots = []store.Slot{{Day: 1, Start: 20 * 60, End: 23 * 60}}

	for _, tc := range []struct {
		name string
		at   Moment
		want float64
	}{
		{"inside", Moment{Day: 1, Minute: 21 * 60}, InSlot},
		{"at the start", Moment{Day: 1, Minute: 20 * 60}, InSlot},
		{"at the end is outside", Moment{Day: 1, Minute: 23 * 60}, OutOfSlot},
		{"an hour early", Moment{Day: 1, Minute: 19 * 60}, OutOfSlot},
		{"the wrong day", Moment{Day: 2, Minute: 21 * 60}, OutOfSlot},
	} {
		if got := Advice(c, tc.at); got != tc.want {
			t.Errorf("Advice(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	// Something wanting full attention is damped harder out of hours. A show suggested at a
	// bad moment is ignored for free; an hour of concentration is the notification people
	// turn off.
	c.Exclusive = true
	if got := Advice(c, Moment{Day: 2, Minute: 21 * 60}); got != OutOfSlotExclusive {
		t.Errorf("Advice(exclusive, out of hours) = %v, want %v", got, OutOfSlotExclusive)
	}
}

// The companion moves a reminder around the week. It does not get to remove one — silencing is
// a person's decision and has exactly one expression, priority zero.
func TestAdviceCanNeverSilenceAReminder(t *testing.T) {
	now := time.Now()
	c := candidate("r", 50, 24*time.Hour)
	c.Advised = true
	c.Exclusive = true
	c.Slots = nil // asked, and no hour suits it

	if got := Weight(c, now, Moment{Day: 3, Minute: 9 * 60}); got <= 0 {
		t.Errorf("Weight() with the worst possible advice = %v, want something above zero", got)
	}

	// And it is still drawn when it is all there is.
	if _, ok := Pick([]store.Candidate{c}, now, Moment{Day: 3, Minute: 9 * 60}, "", "seed"); !ok {
		t.Error("Pick() found nothing, so advice silenced the only reminder there was")
	}
}

// The first arrival is the one most worth placing well, so the never-nudged shortcut must not
// step around the multiplier.
func TestANeverNudgedReminderStillObeysItsHours(t *testing.T) {
	now := time.Now()
	c := candidate("r_new", 50, 0)
	c.Advised = true
	c.Slots = []store.Slot{{Day: 0, Start: 9 * 60, End: 17 * 60}}

	in := Weight(c, now, Moment{Day: 0, Minute: 10 * 60})
	out := Weight(c, now, Moment{Day: 4, Minute: 10 * 60})
	if in <= out {
		t.Errorf("in hours = %v, out of hours = %v, want the first larger", in, out)
	}
	if want := 50 * StalenessCap * InSlot; in != want {
		t.Errorf("Weight() in hours = %v, want %v", in, want)
	}
}

// Advice bends the draw without deciding it. Over many seeds the reminder in its hours has to
// win most of the time and lose some of it — a rule would do neither.
func TestAdviceShiftsTheDrawWithoutDecidingIt(t *testing.T) {
	now := time.Now()
	at := Moment{Day: 1, Minute: 21 * 60}

	inHours := candidate("r_in", 50, 24*time.Hour)
	inHours.Advised = true
	inHours.Slots = []store.Slot{{Day: 1, Start: 20 * 60, End: 23 * 60}}

	outOfHours := candidate("r_out", 50, 24*time.Hour)
	outOfHours.Advised = true
	outOfHours.Slots = []store.Slot{{Day: 4, Start: 9 * 60, End: 12 * 60}}

	pool := []store.Candidate{inHours, outOfHours}
	var won int
	const runs = 400
	for i := range runs {
		got, ok := Pick(pool, now, at, "", fmt.Sprintf("seed-%d", i))
		if !ok {
			t.Fatal("Pick() found nothing")
		}
		if got.ID == "r_in" {
			won++
		}
	}
	// 3.0 against 0.6 is five to one, so about 83%. The bounds are wide enough not to be a
	// test of the random number generator and tight enough to fail if the multiplier stopped
	// being applied at all.
	if won < runs*7/10 || won > runs*95/100 {
		t.Errorf("the reminder in its hours won %d of %d, want a strong but not total majority", won, runs)
	}
}
