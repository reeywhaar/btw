package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// open a store against a temporary file rather than :memory:. WAL behaves differently in
// memory, and WAL is the thing being relied on.
func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testPrincipal(t *testing.T, s *Store) Principal {
	t.Helper()
	p, err := s.CreatePrincipal(context.Background(), "misha", "a-good-password", RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePrincipal(): %v", err)
	}
	return p
}

func TestMigrationsApply(t *testing.T) {
	s := testStore(t)
	main, derived, err := s.SchemaVersions(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersions(): %v", err)
	}
	if main == 0 || derived == 0 {
		t.Errorf("versions = (%d, %d), want both non-zero", main, derived)
	}
}

func TestUsernamesAreCaseInsensitivelyUnique(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	testPrincipal(t, s)

	_, err := s.CreatePrincipal(ctx, "MISHA", "another-password", RoleUser)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreatePrincipal(MISHA) error = %v, want ErrConflict", err)
	}
}

func TestAuthenticateRefusesTheSameWayBothTimes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	testPrincipal(t, s)

	_, wrongPassword := s.Authenticate(ctx, "misha", "not-the-password")
	_, noSuchUser := s.Authenticate(ctx, "nobody", "not-the-password")

	// Which half was wrong is not information a caller should be able to extract.
	if wrongPassword == nil || noSuchUser == nil {
		t.Fatal("Authenticate() accepted a bad credential")
	}
	if wrongPassword.Error() != noSuchUser.Error() {
		t.Errorf("refusals differ: %q vs %q", wrongPassword, noSuchUser)
	}
}

func TestSessionSlidesButThrottles(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })

	token := NewSessionToken()
	if err := s.CreateSession(ctx, token, p.ID); err != nil {
		t.Fatalf("CreateSession(): %v", err)
	}

	// Inside the throttle: resolved, not rewritten.
	if _, moved, err := s.Session(ctx, token); err != nil || moved {
		t.Fatalf("Session() immediately after = (moved %v, %v), want (false, nil)", moved, err)
	}

	now = now.Add(SessionRefresh + time.Minute)
	if _, moved, err := s.Session(ctx, token); err != nil || !moved {
		t.Fatalf("Session() past the throttle = (moved %v, %v), want (true, nil)", moved, err)
	}

	now = now.Add(SessionLifetime + time.Minute)
	if _, _, err := s.Session(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Session() past expiry error = %v, want ErrNotFound", err)
	}
}

func TestChangingAPasswordEndsSessions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	token := NewSessionToken()
	if err := s.CreateSession(ctx, token, p.ID); err != nil {
		t.Fatalf("CreateSession(): %v", err)
	}
	if err := s.SetPassword(ctx, p.ID, "a-different-password"); err != nil {
		t.Fatalf("SetPassword(): %v", err)
	}
	if _, _, err := s.Session(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session survived a password change: %v", err)
	}
}

func TestInviteIsSingleUse(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	_, token, err := s.CreateInvite(ctx, "", RoleAdmin)
	if err != nil {
		t.Fatalf("CreateInvite(): %v", err)
	}
	if _, err := s.AcceptInvite(ctx, token, "misha", "a-good-password"); err != nil {
		t.Fatalf("AcceptInvite(): %v", err)
	}
	if _, err := s.AcceptInvite(ctx, token, "someone", "a-good-password"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second AcceptInvite() error = %v, want ErrConflict", err)
	}
}

func TestCandidatesRespectTheFloor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })

	r, err := s.CreateReminder(ctx, p.ID, "go to the circus")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}

	// Never nudged: eligible.
	if got, _ := s.Candidates(ctx, p.ID, now); len(got) != 1 {
		t.Fatalf("Candidates() before any nudge = %d, want 1", len(got))
	}

	if _, err := s.RecordNudge(ctx, NewNudgeID(), p.ID, r.ID); err != nil {
		t.Fatalf("RecordNudge(): %v", err)
	}
	// Just nudged: inside its own floor, so not offered again.
	if got, _ := s.Candidates(ctx, p.ID, now); len(got) != 0 {
		t.Fatalf("Candidates() inside the floor = %d, want 0", len(got))
	}
	// A day later the floor has passed.
	if got, _ := s.Candidates(ctx, p.ID, now.Add(DefaultMinInterval)); len(got) != 1 {
		t.Fatalf("Candidates() past the floor = %d, want 1", len(got))
	}
}

func TestEndingAReminderTakesItOutOfTheRunning(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	r, err := s.CreateReminder(ctx, p.ID, "ring the dentist")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}
	if err := s.EndReminder(ctx, p.ID, r.ID); err != nil {
		t.Fatalf("EndReminder(): %v", err)
	}
	if got, _ := s.Candidates(ctx, p.ID, s.Now()); len(got) != 0 {
		t.Fatalf("Candidates() after ending = %d, want 0", len(got))
	}
	// Ending twice is not an error: a notification answered after the app already ended it
	// wanted the same outcome, and it has it.
	if err := s.EndReminder(ctx, p.ID, r.ID); err != nil {
		t.Errorf("EndReminder() twice = %v, want nil", err)
	}
}

func TestARemindersFloorSurvivesLosingTheLog(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	r, _ := s.CreateReminder(ctx, p.ID, "water the plants")
	if _, err := s.RecordNudge(ctx, NewNudgeID(), p.ID, r.ID); err != nil {
		t.Fatalf("RecordNudge(): %v", err)
	}

	// derived.db is designed to be deletable. The floor lives in main.db precisely so
	// that deleting it cannot make everything arrive again at once.
	if _, err := s.derived.ExecContext(ctx, `DELETE FROM nudges`); err != nil {
		t.Fatalf("clear log: %v", err)
	}
	if got, _ := s.Candidates(ctx, p.ID, s.Now()); len(got) != 0 {
		t.Fatalf("Candidates() after losing the log = %d, want 0", len(got))
	}
}

func TestRegisteringAnEndpointTakesItFromWhoeverHadIt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testPrincipal(t, s)
	b, err := s.CreatePrincipal(ctx, "someone", "a-good-password", RoleUser)
	if err != nil {
		t.Fatalf("CreatePrincipal(): %v", err)
	}

	const endpoint = "https://push.example.com/abc"
	if _, err := s.RegisterDevice(ctx, a.ID, endpoint, "k", "s", "phone"); err != nil {
		t.Fatalf("RegisterDevice(a): %v", err)
	}
	if _, err := s.RegisterDevice(ctx, b.ID, endpoint, "k", "s", "phone"); err != nil {
		t.Fatalf("RegisterDevice(b): %v", err)
	}

	// One browser profile, one subscription. If it stayed on both accounts, the first
	// person's reminders would arrive on a device the second person is holding.
	if got, _ := s.Devices(ctx, a.ID); len(got) != 0 {
		t.Errorf("first owner still has %d devices, want 0", len(got))
	}
	if got, _ := s.Devices(ctx, b.ID); len(got) != 1 {
		t.Errorf("second owner has %d devices, want 1", len(got))
	}
}

func TestRhythmRefusesABudgetTheWindowCannotHold(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	r, err := s.Rhythm(ctx, p.ID)
	if err != nil {
		t.Fatalf("Rhythm(): %v", err)
	}
	if r.Budget != DefaultBudget {
		t.Errorf("Budget = %d, want the default %d", r.Budget, DefaultBudget)
	}

	// Thirteen waking hours hold twelve nudges at forty-five minutes apart, so make the
	// gap the binding constraint instead: two hours apart leaves room for six.
	r.MinGap = 120
	r.Budget = 12
	if err := s.SetRhythm(ctx, r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetRhythm() with an impossible budget = %v, want ErrInvalid", err)
	}

	r.Budget = 4
	if err := s.SetRhythm(ctx, r); err != nil {
		t.Fatalf("SetRhythm(): %v", err)
	}
	if got, _ := s.Rhythm(ctx, p.ID); got.Budget != 4 {
		t.Errorf("Budget = %d, want 4", got.Budget)
	}
}

func TestSlotIsClaimedOnce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	if err := s.PlanDay(ctx, p.ID, "2026-08-29", []time.Time{now}); err != nil {
		t.Fatalf("PlanDay(): %v", err)
	}
	due, err := s.DueSlots(ctx, now, 10*time.Minute)
	if err != nil || len(due) != 1 {
		t.Fatalf("DueSlots() = %d, %v; want 1, nil", len(due), err)
	}

	// Two overlapping ticks must not both send for one slot.
	first, err := s.ClaimSlot(ctx, due[0], now)
	if err != nil || !first {
		t.Fatalf("ClaimSlot() first = %v, %v; want true, nil", first, err)
	}
	second, err := s.ClaimSlot(ctx, due[0], now)
	if err != nil || second {
		t.Fatalf("ClaimSlot() second = %v, %v; want false, nil", second, err)
	}
}

func TestAMissedSlotIsMissed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	at := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	if err := s.PlanDay(ctx, p.ID, "2026-08-29", []time.Time{at}); err != nil {
		t.Fatalf("PlanDay(): %v", err)
	}
	// Three hours later, which is what a restart after an outage looks like. Nothing is
	// due: three nudges at once about moments that have passed is the failure this
	// prevents.
	if due, _ := s.DueSlots(ctx, at.Add(3*time.Hour), 10*time.Minute); len(due) != 0 {
		t.Fatalf("DueSlots() long after the slot = %d, want 0", len(due))
	}
}

func TestTheWakingWindowIsOptionalAndItsHoursSurvive(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	r, err := s.Rhythm(ctx, p.ID)
	if err != nil {
		t.Fatalf("Rhythm(): %v", err)
	}
	// On by default, because an account upgraded into this should not start being nudged
	// at four in the morning.
	if !r.WindowEnabled {
		t.Fatal("a fresh rhythm has no waking window")
	}

	r.WakeMinute = 8 * 60
	r.SleepMinute = 20 * 60
	if err := s.SetRhythm(ctx, r); err != nil {
		t.Fatalf("SetRhythm(): %v", err)
	}

	r.WindowEnabled = false
	if err := s.SetRhythm(ctx, r); err != nil {
		t.Fatalf("SetRhythm(off): %v", err)
	}

	got, err := s.Rhythm(ctx, p.ID)
	if err != nil {
		t.Fatalf("Rhythm(): %v", err)
	}
	if got.WindowEnabled {
		t.Error("the window is still on")
	}
	// The hours are kept, so switching it back on restores what somebody chose.
	if got.WakeMinute != 8*60 || got.SleepMinute != 20*60 {
		t.Errorf("hours = %d..%d, want them remembered", got.WakeMinute, got.SleepMinute)
	}
	if from, to := got.Bounds(); from != 0 || to != 24*60 {
		t.Errorf("Bounds() = %d..%d, want the whole day", from, to)
	}
}

func TestABudgetIsCheckedAgainstTheWindowThatWillActuallyBeUsed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	r, _ := s.Rhythm(ctx, p.ID)
	r.WakeMinute = 9 * 60
	r.SleepMinute = 11 * 60 // two hours
	r.MinGap = 45
	r.Budget = 6

	// Two hours at forty-five minutes apart holds two, so six is refused with a sentence
	// naming the most it will take.
	if err := s.SetRhythm(ctx, r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetRhythm() into a two-hour window = %v, want ErrInvalid", err)
	}

	// The same six fits once the window is off, because then the day is the window.
	r.WindowEnabled = false
	if err := s.SetRhythm(ctx, r); err != nil {
		t.Fatalf("SetRhythm() with no window = %v, want it to fit", err)
	}
}
