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
	if atForty > 50*StalenessCap*Neutral {
		t.Errorf("weight %v exceeds the cap", atForty)
	}
}

func TestNeverNudgedIsMaximallyStale(t *testing.T) {
	// A reminder just written down should arrive soon: it is also the fastest way for
	// somebody to find out the thing works at all.
	// Neutral is in there because every weight carries the advice term, and an unasked
	// reminder's is neutral. It cancels across the draw; it does not vanish from the number.
	if got, want := Weight(candidate("r_new", 50, 0), now, whenever), 50*StalenessCap*Neutral; got != want {
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

// What "optional" means at the level of one reminder rather than one account: a reminder
// written a minute ago stands where it stood. Asserted about the draw, not about the number.
func TestAReminderWithNoAdviceIsUnmovedByTheHour(t *testing.T) {
	now := time.Now()
	c := candidate("r", 50, 24*time.Hour)

	if Weight(c, now, at(0, 3*60)) != Weight(c, now, at(4, 15*60)) {
		t.Error("the hour moved the weight of a reminder nothing has been said about")
	}
	if got := Advice(c, at(0, 15*60)); got != Neutral {
		t.Errorf("Advice() with nothing said = %v, want %v", got, Neutral)
	}

	// And an answer that could not be read is the same situation from here.
	unreadable := c
	unreadable.Advised = true
	unreadable.Curve = store.Curve{{0.9}, {0.9}}
	if got := Advice(unreadable, at(0, 15*60)); got != Neutral {
		t.Errorf("Advice() with an unreadable curve = %v, want %v", got, Neutral)
	}
}

// The companion's number is the multiplier, and nothing else happens to it: no threshold, no
// in-or-out, no special case for a reminder wanting full attention.
func TestTheCurveIsTheMultiplier(t *testing.T) {
	c := candidate("r", 50, 24*time.Hour)
	c.Advised = true
	c.Curve = flat(0.2)
	c.Curve[2][20] = 0.5 // Wednesday 10:00
	c.Curve[2][42] = 1.0 // Wednesday 21:00

	for _, tc := range []struct {
		name string
		at   Moment
		want float64
	}{
		{"the half hour it likes most", at(2, 21*60), 1},
		{"one it has no opinion about", at(2, 10*60), Neutral},
		{"one it likes little", at(2, 3*60), 0.2},
		{"the same hour on another day", at(5, 21*60), 0.2},
	} {
		if got := Advice(c, tc.at); got != tc.want {
			t.Errorf("Advice(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	// A companion with no opinion has to land where an unasked reminder does, or the feature
	// would cost something merely by having been asked.
	flatC := candidate("r", 50, 24*time.Hour)
	flatC.Advised = true
	flatC.Curve = flat(Neutral)
	unasked := candidate("r", 50, 24*time.Hour)
	if Advice(flatC, at(3, 9*60)) != Advice(unasked, at(3, 9*60)) {
		t.Error("a flat curve and no curve at all weigh differently")
	}
}

// Half at the middle rather than one is safe only because a weighted draw normalises by the
// total. This is the test that fails if somebody reads the 0.5 as a bug and doubles it back.
func TestAConstantFactorAcrossEveryCandidateChangesNothing(t *testing.T) {
	now := time.Now()

	pool := func(scale float64) []store.Candidate {
		a := candidate("r_a", 50, 24*time.Hour)
		a.Advised = true
		a.Curve = flat(0.9 * scale)
		b := candidate("r_b", 50, 24*time.Hour)
		b.Advised = true
		b.Curve = flat(0.3 * scale)
		return []store.Candidate{a, b}
	}

	const runs = 200
	for i := range runs {
		seed := fmt.Sprintf("s-%d", i)
		full, _ := Pick(pool(1), now, at(0, 9*60), "", seed)
		half, _ := Pick(pool(0.5), now, at(0, 9*60), "", seed)
		if full.ID != half.ID {
			t.Fatalf("seed %s: scaling every weight changed the draw, %q against %q",
				seed, full.ID, half.ID)
		}
	}
}

// A model asked for 0 to 1 answers inside it nearly always. A stray value read literally would
// hand one reminder a multiplier no honest answer can reach — or, worse, a negative weight,
// which would draw it *more* often the more the companion disliked the hour.
func TestAValueOutsideTheScaleIsClamped(t *testing.T) {
	c := candidate("r", 50, 24*time.Hour)
	c.Advised = true
	c.Curve = flat(0.5)
	c.Curve[0][0] = 4.2
	c.Curve[0][2] = -3

	if got := Advice(c, at(0, 0)); got != 1 {
		t.Errorf("Advice(above the scale) = %v, want 1", got)
	}
	if got := Advice(c, at(0, 60)); got != 0 {
		t.Errorf("Advice(below the scale) = %v, want 0", got)
	}
}

// Zero means never, and the draw is not what enforces it: every weight being zero falls through
// to the uniform fallback, which would pick from exactly the reminders meant to be passed over.
func TestAnHourScoredZeroIsNotDrawn(t *testing.T) {
	now := time.Now()

	zeroed := candidate("r_zero", 50, 24*time.Hour)
	zeroed.Advised = true
	zeroed.Curve = flat(0)

	if got := Weight(zeroed, now, at(0, 3*60)); got != 0 {
		t.Errorf("Weight() at an hour scored zero = %v, want 0", got)
	}

	// Alone, it is not sent at all — the fallback must not reach past the filter.
	if _, ok := Pick([]store.Candidate{zeroed}, now, at(0, 3*60), "", "seed"); ok {
		t.Error("Pick() drew a reminder the companion scored zero")
	}

	// Beside one that is wanted, the other is always chosen, whatever the seed.
	wanted := candidate("r_wanted", 50, 24*time.Hour)
	wanted.Advised = true
	wanted.Curve = flat(0.5)
	for i := range 50 {
		got, ok := Pick([]store.Candidate{zeroed, wanted}, now, at(0, 3*60), "", fmt.Sprintf("s-%d", i))
		if !ok || got.ID != "r_wanted" {
			t.Fatalf("Pick() = %q, %v, want the one not scored zero every time", got.ID, ok)
		}
	}

	// And an hour it likes is drawn normally, so the filter is about the hour and not the
	// reminder.
	if _, ok := Pick([]store.Candidate{zeroed}, now, at(0, 3*60), "", "seed"); ok {
		t.Error("still drawn at the zeroed hour")
	}
	liked := zeroed
	liked.Curve = flat(0)
	liked.Curve[0][20] = 0.9
	if _, ok := Pick([]store.Candidate{liked}, now, at(0, 10*60), "", "seed"); !ok {
		t.Error("Pick() found nothing at an hour the companion likes")
	}
}

// The button that proves the chain ignores a reminder's own floor, so everything it offers may
// have been nudged a moment ago and weigh zero for that reason. That is a different zero, and
// it must still send something.
func TestAManualDrawStillSendsWhenNothingIsStale(t *testing.T) {
	now := time.Now()
	justNudged := candidate("r", 50, 24*time.Hour)
	justNudged.LastNudgedAt = now
	justNudged.Advised = true
	justNudged.Curve = flat(0.5)

	if got := Weight(justNudged, now, at(0, 9*60)); got != 0 {
		t.Fatalf("Weight() just after a nudge = %v, want 0", got)
	}
	if _, ok := Pick([]store.Candidate{justNudged}, now, at(0, 9*60), "", "seed"); !ok {
		t.Error("Pick() sent nothing, so the filter reached a zero that was about staleness")
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
	if want := 50 * StalenessCap * 1.0; in != want { // its best hour is a multiplier of 1
		t.Errorf("Weight() in its hours = %v, want %v", in, want)
	}
}

// Advice bends the draw without deciding it. Over many seeds the reminder the companion likes
// has to win most of the time and lose some of it — a rule would do neither.
func TestAdviceShiftsTheDrawWithoutDecidingIt(t *testing.T) {
	now := time.Now()
	evening := at(3, 21*60)

	// Strongly liked against barely liked, not against zero — zero is a switch now, and a
	// reminder holding one is not in the draw at all, which would make this a test of the
	// filter rather than of the weighting.
	liked := candidate("r_liked", 50, 24*time.Hour)
	liked.Advised = true
	liked.Curve = flat(0.9)

	disliked := candidate("r_disliked", 50, 24*time.Hour)
	disliked.Advised = true
	disliked.Curve = flat(0.1)

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
	// 1.8 against 0.2 is nine to one, so about 90%. Wide enough not to be a test of the random
	// number generator, tight enough to fail if the multiplier stopped being applied — and it
	// must not be every draw, because a weighting that decides is a rule.
	if won < runs*8/10 || won >= runs {
		t.Errorf("the liked reminder won %d of %d, want a strong majority rather than all of them", won, runs)
	}
}
