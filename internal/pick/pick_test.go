package pick

import (
	"fmt"
	"testing"
	"time"

	"btw/internal/store"
)

// whenever is the moment in the local week these tests are run at. None of their candidates
// carries advice, so the multiplier is one and the moment cannot reach the weight — naming it
// says so rather than leaving a reader to check.
var whenever = Moment{}

// flat is a curve saying the same thing about every half hour of every day.
func flat(v float64) store.Curve {
	c := make(store.Curve, store.Days)
	for d := range c {
		c[d] = make([]float64, store.Windows)
		for i := range c[d] {
			c[d][i] = v
		}
	}
	return c
}

// at is a moment in the week, for the tests that care which.
func at(day, minute int) Moment { return Moment{Day: day, Minute: minute} }

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

	if Weight(c, now, at(0, 3*60)) != Weight(c, now, at(4, 15*60)) {
		t.Error("the hour moved the weight of a reminder nothing has been said about")
	}
	if got := Advice(c, at(0, 15*60)); got != 1 {
		t.Errorf("Advice() with nothing said = %v, want 1", got)
	}

	// And an answer that could not be read is the same situation from here.
	unreadable := c
	unreadable.Advised = true
	unreadable.Curve = store.Curve{{0.9}, {0.9}}
	if got := Advice(unreadable, at(0, 15*60)); got != 1 {
		t.Errorf("Advice() with an unreadable curve = %v, want 1", got)
	}
}

// The curve is stretched onto the multiplier's range and nothing else happens to it: no
// threshold, no in-or-out, no special case for a reminder wanting full attention.
func TestTheCurveIsStretchedOntoTheRange(t *testing.T) {
	c := candidate("r", 50, 24*time.Hour)
	c.Advised = true
	c.Curve = flat(0)
	c.Curve[2][20] = 0.5 // Wednesday 10:00
	c.Curve[2][42] = 1.0 // Wednesday 21:00

	for _, tc := range []struct {
		name string
		at   Moment
		want float64
	}{
		{"the half hour it likes most", at(2, 21*60), Ceiling},
		{"one it has no opinion about", at(2, 10*60), 1},
		{"one it likes least", at(2, 3*60), Floor},
		{"the same hour on another day", at(5, 21*60), Floor},
	} {
		if got := Advice(c, tc.at); got != tc.want {
			t.Errorf("Advice(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	// Exactly 1 in the middle is what makes a companion with no opinion cost nothing.
	flatC := candidate("r", 50, 24*time.Hour)
	flatC.Advised = true
	flatC.Curve = flat(0.5)
	if got := Advice(flatC, at(3, 9*60)); got != 1 {
		t.Errorf("Advice(a flat 0.5) = %v, want exactly 1", got)
	}
}

// A model asked for 0 to 1 answers inside it nearly always. A stray value read literally would
// hand one reminder a multiplier no honest answer can reach — or, worse, a negative weight.
func TestAValueOutsideTheScaleIsClamped(t *testing.T) {
	c := candidate("r", 50, 24*time.Hour)
	c.Advised = true
	c.Curve = flat(0)
	c.Curve[0][0] = 4.2
	c.Curve[0][2] = -3

	if got := Advice(c, at(0, 0)); got != Ceiling {
		t.Errorf("Advice(above the scale) = %v, want %v", got, Ceiling)
	}
	if got := Advice(c, at(0, 60)); got != Floor {
		t.Errorf("Advice(below the scale) = %v, want %v", got, Floor)
	}
}

// The floor is a guarantee rather than a rounding: the companion moves a reminder around the
// day and does not get to remove one. Silencing is a person's decision, priority zero.
func TestAdviceCanNeverSilenceAReminder(t *testing.T) {
	now := time.Now()
	c := candidate("r", 50, 24*time.Hour)
	c.Advised = true
	c.Curve = flat(0)

	if got := Weight(c, now, at(0, 3*60)); got <= 0 {
		t.Errorf("Weight() with the worst possible advice = %v, want something above zero", got)
	}
	if Floor <= 0 {
		t.Errorf("Floor = %v, which lets a curve of zeroes silence a reminder", Floor)
	}
	if _, ok := Pick([]store.Candidate{c}, now, at(0, 3*60), "", "seed"); !ok {
		t.Error("Pick() found nothing, so advice silenced the only reminder there was")
	}
}

// The first arrival is the one most worth placing well, so the never-nudged shortcut must not
// step around the multiplier.
func TestANeverNudgedReminderStillObeysItsCurve(t *testing.T) {
	now := time.Now()
	c := candidate("r_new", 50, 0)
	c.Advised = true
	c.Curve = flat(0)
	for i := 18; i < 34; i++ {
		c.Curve[1][i] = 1 // Tuesday, 09:00 to 17:00
	}

	in := Weight(c, now, at(1, 10*60))
	out := Weight(c, now, at(1, 4*60))
	if in <= out {
		t.Errorf("in its hours = %v, outside them = %v, want the first larger", in, out)
	}
	if want := 50 * StalenessCap * Ceiling; in != want {
		t.Errorf("Weight() in its hours = %v, want %v", in, want)
	}
}

// Advice bends the draw without deciding it. Over many seeds the reminder the companion likes
// has to win most of the time and lose some of it — a rule would do neither.
func TestAdviceShiftsTheDrawWithoutDecidingIt(t *testing.T) {
	now := time.Now()
	evening := at(3, 21*60)

	liked := candidate("r_liked", 50, 24*time.Hour)
	liked.Advised = true
	liked.Curve = flat(1)

	disliked := candidate("r_disliked", 50, 24*time.Hour)
	disliked.Advised = true
	disliked.Curve = flat(0)

	pool := []store.Candidate{liked, disliked}
	var won int
	const runs = 400
	for i := range runs {
		got, ok := Pick(pool, now, evening, "", fmt.Sprintf("seed-%d", i))
		if !ok {
			t.Fatal("Pick() found nothing")
		}
		if got.ID == "r_liked" {
			won++
		}
	}
	// 1.5 against 0.5 is three to one, so about 75%. Wide enough not to be a test of the random
	// number generator, tight enough to fail if the multiplier stopped being applied.
	if won < runs*6/10 || won > runs*9/10 {
		t.Errorf("the liked reminder won %d of %d, want a majority rather than every draw", won, runs)
	}
}
