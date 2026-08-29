package rhythm

import (
	"testing"
	"time"

	"btw/internal/store"
)

func defaults() store.Rhythm {
	return store.Rhythm{
		PrincipalID:   "p_test",
		Timezone:      "Europe/Belgrade",
		WindowEnabled: true,
		WakeMinute:    store.DefaultWake,
		SleepMinute:   store.DefaultSleep,
		Budget:        store.DefaultBudget,
		MinGap:        store.DefaultMinGap,
	}
}

func TestPlanIsInsideTheWakingWindow(t *testing.T) {
	r := defaults()
	loc := Location(r)
	at, err := PlanDay(r, "2026-08-29", r.PrincipalID)
	if err != nil {
		t.Fatalf("PlanDay(): %v", err)
	}
	if len(at) != r.Budget {
		t.Fatalf("PlanDay() gave %d slots, want %d", len(at), r.Budget)
	}
	for _, t0 := range at {
		local := t0.In(loc)
		m := local.Hour()*60 + local.Minute()
		if m < r.WakeMinute || m >= r.SleepMinute {
			t.Errorf("slot at %s is outside %02d:00–%02d:00", local.Format(time.TimeOnly), r.WakeMinute/60, r.SleepMinute/60)
		}
	}
}

func TestPlanRespectsTheMinimumGap(t *testing.T) {
	r := defaults()
	gap := time.Duration(r.MinGap) * time.Minute
	// Many days, because the failure this guards against is a rare draw rather than a
	// systematic one: two adjacent blocks can each place their instant near the boundary.
	for d := 1; d <= 60; d++ {
		date := time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
		at, err := PlanDay(r, date, r.PrincipalID)
		if err != nil {
			t.Fatalf("PlanDay(%s): %v", date, err)
		}
		for i := 1; i < len(at); i++ {
			if got := at[i].Sub(at[i-1]); got < gap {
				t.Fatalf("%s: slots %d and %d are %s apart, want at least %s", date, i-1, i, got, gap)
			}
		}
	}
}

func TestPlanIsReproducible(t *testing.T) {
	r := defaults()
	first, _ := PlanDay(r, "2026-08-29", r.PrincipalID)
	second, _ := PlanDay(r, "2026-08-29", r.PrincipalID)
	if len(first) != len(second) {
		t.Fatalf("two plans for one day differ in length: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if !first[i].Equal(second[i]) {
			t.Fatalf("slot %d differs between runs: %s vs %s", i, first[i], second[i])
		}
	}
}

func TestPlanDiffersBetweenDaysAndBetweenPeople(t *testing.T) {
	r := defaults()
	monday, _ := PlanDay(r, "2026-08-31", r.PrincipalID)
	tuesday, _ := PlanDay(r, "2026-09-01", r.PrincipalID)
	if len(monday) == 0 || monday[0].Equal(tuesday[0].AddDate(0, 0, -1)) {
		t.Error("two days produced the same time of day; the plan is not varying")
	}

	other, _ := PlanDay(r, "2026-08-31", "p_somebody_else")
	if monday[0].Equal(other[0]) {
		t.Error("two people got the same minute; the seed does not include who")
	}
}

func TestPlanIsEmptyWhenNobodyWantsNudges(t *testing.T) {
	r := defaults()
	r.Budget = 0
	at, err := PlanDay(r, "2026-08-29", r.PrincipalID)
	if err != nil || len(at) != 0 {
		t.Fatalf("PlanDay() with no budget = %d slots, %v; want 0, nil", len(at), err)
	}
}

func TestLocalDateIsTheirDateNotOurs(t *testing.T) {
	r := defaults()
	r.Timezone = "Pacific/Auckland"
	// Late on the 29th in UTC is already the 30th in Auckland, and the plan belongs to
	// their day rather than to Greenwich's.
	at := time.Date(2026, 8, 29, 22, 0, 0, 0, time.UTC)
	if got := LocalDate(r, at); got != "2026-08-30" {
		t.Errorf("LocalDate() = %q, want 2026-08-30", got)
	}
}

func TestAnUnknownZoneStillPlansSomething(t *testing.T) {
	r := defaults()
	r.Timezone = "Mars/Olympus_Mons"
	at, err := PlanDay(r, "2026-08-29", r.PrincipalID)
	if err != nil {
		t.Fatalf("PlanDay(): %v", err)
	}
	// Wrong hours are visible and correctable. Silence looks like the product not working.
	if len(at) != r.Budget {
		t.Errorf("PlanDay() with an unloadable zone gave %d slots, want %d", len(at), r.Budget)
	}
}

func TestWithoutAWindowTheWholeDayIsAvailable(t *testing.T) {
	r := defaults()
	r.WindowEnabled = false
	loc := Location(r)

	// Somebody who switches the window off has asked to be nudged at any hour, which
	// includes the ones they are asleep in. The planner does what it was told; the
	// interface is what says so out loud.
	seen := map[int]bool{}
	for d := 1; d <= 40; d++ {
		date := time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
		at, err := PlanDay(r, date, r.PrincipalID)
		if err != nil {
			t.Fatalf("PlanDay(%s): %v", date, err)
		}
		if len(at) != r.Budget {
			t.Fatalf("%s: %d slots, want %d", date, len(at), r.Budget)
		}
		for _, t0 := range at {
			seen[t0.In(loc).Hour()] = true
		}
	}
	// Blocks of eight hours across forty days should reach well outside 09:00–22:00.
	var outside int
	for hour := range seen {
		if hour < store.DefaultWake/60 || hour >= store.DefaultSleep/60 {
			outside++
		}
	}
	if outside == 0 {
		t.Error("no slot ever landed outside the old window; the whole day is not being used")
	}
}

func TestSwitchingTheWindowOffDoesNotForgetTheHours(t *testing.T) {
	r := defaults()
	r.WindowEnabled = false

	// The hours are kept so that switching it back on restores what somebody chose, rather
	// than making them type 09:00 and 22:00 in again.
	if r.WakeMinute != store.DefaultWake || r.SleepMinute != store.DefaultSleep {
		t.Fatal("the hours were cleared")
	}
	if from, to := r.Bounds(); from != 0 || to != 24*60 {
		t.Errorf("Bounds() = %d..%d, want the whole day", from, to)
	}

	r.WindowEnabled = true
	if from, to := r.Bounds(); from != store.DefaultWake || to != store.DefaultSleep {
		t.Errorf("Bounds() = %d..%d, want the hours back", from, to)
	}
}
