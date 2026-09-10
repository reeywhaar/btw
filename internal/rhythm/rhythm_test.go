package rhythm

import (
	"fmt"
	"testing"
	"time"

	"btw/internal/store"
)

func defaults() store.Rhythm {
	return store.Rhythm{
		PrincipalID: "p_test", Timezone: "Europe/Belgrade", WindowEnabled: true,
		WakeMinute: store.DefaultWake, SleepMinute: store.DefaultSleep,
		Budget: store.DefaultBudget,
	}
}

func at(r store.Rhythm, day int, hour, minute int) time.Time {
	loc := Location(r)
	return time.Date(2026, 9, day, hour, minute, 0, 0, loc).UTC()
}

func TestNobodyIsNudgedWhileAsleep(t *testing.T) {
	r := defaults()
	for _, hour := range []int{0, 3, 7, 8, 22, 23} {
		when := at(r, 3, hour, 0)
		if Awake(r, when) {
			t.Errorf("%02d:00 counts as awake", hour)
		}
		// However long it has been, the answer outside waking hours is no.
		if Due(r, when.Add(-30*time.Hour), when) {
			t.Errorf("a nudge was due at %02d:00", hour)
		}
	}
	for _, hour := range []int{9, 13, 21} {
		if !Awake(r, at(r, 3, hour, 0)) {
			t.Errorf("%02d:00 counts as asleep", hour)
		}
	}
}

func TestSleepingDoesNotCountTowardsTheWait(t *testing.T) {
	r := defaults()
	lastNight := at(r, 2, 21, 30)
	morning := at(r, 3, 9, 0)

	// Measured from last night, eleven hours have passed and every threshold is met — so a
	// nudge would land at the same minute somebody woke up, every day of their life.
	// Waking, less the half interval the night is credited — which is what keeps the last
	// nudge of the day from landing exactly at bedtime and being dropped.
	since := Since(r, lastNight, morning)
	if want := at(r, 3, 9, 0).Add(-Interval(r) / 2); !since.Equal(want) {
		t.Fatalf("Since() = %s, want %s", since, want)
	}
	if Due(r, since, morning) {
		t.Error("a nudge was due at the exact minute of waking")
	}
	// It arrives partway into the morning instead.
	later := morning.Add(Interval(r))
	if !Due(r, since, later) {
		t.Errorf("nothing was due %s after waking", Interval(r)*2)
	}
}

func TestTheDelayLandsInsideTheFirstTenthOfAnInterval(t *testing.T) {
	r := defaults()
	r.Budget = 13 // an hour apiece
	most := time.Duration(float64(Interval(r)) * Spread)

	seen := map[time.Duration]bool{}
	for range 400 {
		d := Delay(r)
		if d < 0 || d > most {
			t.Fatalf("Delay() = %s, outside 0..%s", d, most)
		}
		seen[d.Truncate(time.Minute)] = true
	}
	// A nudge scheduled the instant a wait completes lands on a tick boundary every time,
	// which is a five-minute mark anybody would notice. The delay is what moves it.
	if len(seen) < 3 {
		t.Errorf("the delay only ever took %d values", len(seen))
	}
}

func TestABudgetOfNoneIsNeverDue(t *testing.T) {
	r := defaults()
	r.Budget = 0
	if Due(r, at(r, 3, 9, 0), at(r, 3, 20, 0)) {
		t.Error("a nudge was due with no budget")
	}
}

func TestTheIntervalIsTheWakingDayOverTheBudget(t *testing.T) {
	r := defaults()
	r.Budget = 13
	// Thirteen waking hours, thirteen nudges.
	if got := Interval(r); got != time.Hour {
		t.Errorf("Interval() = %s, want an hour", got)
	}

	// With no window the day is the window, which is where "eighteen a day" is one every
	// eighty minutes.
	r.WindowEnabled = false
	r.Budget = 18
	if got := Interval(r); got != 80*time.Minute {
		t.Errorf("Interval() = %s, want 80m", got)
	}

	// Never shorter than a tick: nothing can be delivered between two of them.
	r.WindowEnabled = true
	r.Budget = store.MaxBudget
	if got := Interval(r); got < Tick {
		t.Errorf("Interval() = %s, shorter than the loop can deliver", got)
	}
}

func TestADayHoldsRoughlyWhatWasAskedFor(t *testing.T) {
	r := defaults()
	for _, budget := range []int{3, 9, 18, 24} {
		r.Budget = budget
		total := 0
		for day := 1; day <= 14; day++ {
			last := time.Time{}
			for now := at(r, day, 0, 0); now.Before(at(r, day+1, 0, 0)); now = now.Add(Tick) {
				if Due(r, Since(r, last, now), now) {
					last = now
					total++
				}
			}
		}
		average := float64(total) / 14
		// The count is not a promise — it is what falls out of an interval and a waking
		// window — but it should land near what somebody asked for rather than half of it.
		if average < float64(budget)*0.7 || average > float64(budget)*1.1 {
			t.Errorf("budget %d averaged %.1f a day", budget, average)
		}
		fmt.Printf("budget %2d -> %.1f a day (interval %s)\n", budget, average, Interval(r))
	}
}

func TestAnUnknownZoneStillNudges(t *testing.T) {
	r := defaults()
	r.Timezone = "Mars/Olympus_Mons"
	// Wrong hours are visible and correctable. Silence looks like the product not working.
	if !Awake(r, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)) {
		t.Error("noon UTC counts as asleep under a zone that will not load")
	}
}

// A waking day spanning two local dates: somebody awake from noon until four is awake at one in
// the morning.
func TestAWakingWindowThatCrossesMidnight(t *testing.T) {
	r := store.Rhythm{
		Timezone: "UTC", WindowEnabled: true,
		WakeMinute: 12 * 60, SleepMinute: 4 * 60, Budget: 4,
	}
	day := func(hour, minute int) time.Time {
		return time.Date(2026, 9, 7, hour, minute, 0, 0, time.UTC)
	}

	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"the small hours", day(1, 0), true},
		{"just before bed", day(3, 59), true},
		{"asleep at four", day(4, 0), false},
		{"asleep mid-morning", day(9, 0), false},
		{"awake at noon", day(12, 0), true},
		{"the evening", day(22, 0), true},
		{"a minute to midnight", day(23, 59), true},
	} {
		if got := Awake(r, tc.at); got != tc.want {
			t.Errorf("Awake(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	// Sixteen hours, not minus eight — which is what decides how far apart nudges are.
	if got, want := Interval(r), 4*time.Hour; got != want {
		t.Errorf("Interval() = %v, want %v", got, want)
	}
}

// At one in the morning, for somebody awake from noon, today's waking hour is eleven hours
// away — so reading Since as that hour puts every nudge in the future and silences the small
// hours for exactly the people who asked to be awake in them.
func TestTheWakingDayBeganYesterdayInTheSmallHours(t *testing.T) {
	r := store.Rhythm{
		Timezone: "UTC", WindowEnabled: true,
		WakeMinute: 12 * 60, SleepMinute: 4 * 60, Budget: 4,
	}
	now := time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC)

	since := Since(r, time.Time{}, now)
	if !since.Before(now) {
		t.Errorf("Since() = %v, which is not before %v — a nudge would never come due", since, now)
	}
	// Yesterday's noon, less half an interval.
	if want := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC).Add(-Interval(r) / 2); !since.Equal(want) {
		t.Errorf("Since() = %v, want %v", since, want)
	}

	// And a nudge is actually owed, which is the whole point.
	if !Due(r, since, now) {
		t.Error("nothing is due at one in the morning for somebody who is awake then")
	}
}

// Equal ends are the whole day: the natural thing to mean with two controls that each name an
// hour, and the same as switching the window off.
func TestEqualEndsAreTheWholeDay(t *testing.T) {
	r := store.Rhythm{
		Timezone: "UTC", WindowEnabled: true,
		WakeMinute: 13 * 60, SleepMinute: 13 * 60, Budget: 4,
	}
	for hour := range 24 {
		at := time.Date(2026, 9, 7, hour, 30, 0, 0, time.UTC)
		if !Awake(r, at) {
			t.Errorf("Awake(%02d:30) = false, want the whole day", hour)
		}
	}
	if got, want := Interval(r), 6*time.Hour; got != want {
		t.Errorf("Interval() = %v, want %v", got, want)
	}
}
