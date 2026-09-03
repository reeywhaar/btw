package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"btw/internal/mail"
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

// withFloor states a floor on a reminder, which is now the only way one exists — reminders
// no longer inherit a day's floor nobody asked for.
func withFloor(t *testing.T, s *Store, id string, d time.Duration) {
	t.Helper()
	if _, err := s.main.Exec(`UPDATE reminders SET min_interval = ? WHERE id = ?`,
		int64(d.Seconds()), id); err != nil {
		t.Fatalf("set floor: %v", err)
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
	withFloor(t, s, r.ID, 24*time.Hour)

	// Never nudged: eligible.
	if got, _ := s.Candidates(ctx, p.ID, now, RespectFloor); len(got) != 1 {
		t.Fatalf("Candidates() before any nudge = %d, want 1", len(got))
	}

	if _, err := s.RecordNudge(ctx, NewNudgeID(), p.ID, r.ID); err != nil {
		t.Fatalf("RecordNudge(): %v", err)
	}
	// Just nudged: inside its own floor, so not offered again.
	if got, _ := s.Candidates(ctx, p.ID, now, RespectFloor); len(got) != 0 {
		t.Fatalf("Candidates() inside the floor = %d, want 0", len(got))
	}
	// A day later the floor has passed.
	if got, _ := s.Candidates(ctx, p.ID, now.Add(24*time.Hour), RespectFloor); len(got) != 1 {
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
	if got, _ := s.Candidates(ctx, p.ID, s.Now(), RespectFloor); len(got) != 0 {
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
	withFloor(t, s, r.ID, 24*time.Hour)
	if _, err := s.RecordNudge(ctx, NewNudgeID(), p.ID, r.ID); err != nil {
		t.Fatalf("RecordNudge(): %v", err)
	}

	// derived.db is designed to be deletable. The floor lives in main.db precisely so
	// that deleting it cannot make everything arrive again at once.
	if _, err := s.derived.ExecContext(ctx, `DELETE FROM nudges`); err != nil {
		t.Fatalf("clear log: %v", err)
	}
	if got, _ := s.Candidates(ctx, p.ID, s.Now(), RespectFloor); len(got) != 0 {
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
	if _, err := s.RegisterDevice(ctx, a.ID, endpoint, "k", "s", "phone", ""); err != nil {
		t.Fatalf("RegisterDevice(a): %v", err)
	}
	if _, err := s.RegisterDevice(ctx, b.ID, endpoint, "k", "s", "phone", ""); err != nil {
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

func TestARelayIsRefusedWithoutEncryptionOrHalfCredentials(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	good := mail.Settings{
		Host: "smtp.example.com", Port: 587, TLS: mail.StartTLS,
		Username: "postmaster", Password: "hunter2", FromAddress: "btw@example.com",
	}
	if err := s.SetSMTP(ctx, good); err != nil {
		t.Fatalf("SetSMTP(): %v", err)
	}
	got, err := s.SMTP(ctx)
	if err != nil || got.Host != "smtp.example.com" || got.TLS != mail.StartTLS {
		t.Fatalf("SMTP() = %+v, %v", got, err)
	}

	for name, bad := range map[string]mail.Settings{
		"no encryption":        {Host: "h", Port: 25, TLS: "none", FromAddress: "btw@example.com"},
		"username no password": {Host: "h", Port: 587, TLS: mail.StartTLS, Username: "u", FromAddress: "btw@example.com"},
		"password no username": {Host: "h", Port: 587, TLS: mail.StartTLS, Password: "p", FromAddress: "btw@example.com"},
		"unparseable from":     {Host: "h", Port: 587, TLS: mail.StartTLS, FromAddress: "not an address"},
		"no host":              {Port: 587, TLS: mail.StartTLS, FromAddress: "btw@example.com"},
	} {
		if err := s.SetSMTP(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("SetSMTP(%s) = %v, want ErrInvalid", name, err)
		}
	}
}

func TestAnAddressIsOnlyProvedByItsCode(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	code, err := s.StartRecovery(ctx, p.ID, "misha@example.com")
	if err != nil {
		t.Fatalf("StartRecovery(): %v", err)
	}

	// Until the code comes back the account has no recovery address at all — not a
	// provisional one — so a flow abandoned anywhere leaves what was there before.
	if got, _, _ := s.RecoveryAddress(ctx, p.ID); got != "" {
		t.Fatalf("RecoveryAddress() = %q before confirming, want empty", got)
	}
	if got, _ := s.PendingRecovery(ctx, p.ID); got != "misha@example.com" {
		t.Errorf("PendingRecovery() = %q", got)
	}

	if _, err := s.ConfirmRecovery(ctx, p.ID, "WRONGCOD"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ConfirmRecovery(wrong) = %v, want ErrInvalid", err)
	}
	if _, err := s.ConfirmRecovery(ctx, p.ID, code); err != nil {
		t.Fatalf("ConfirmRecovery(): %v", err)
	}
	if got, _, _ := s.RecoveryAddress(ctx, p.ID); got != "misha@example.com" {
		t.Errorf("RecoveryAddress() = %q after confirming", got)
	}
	// The attempt is spent.
	if got, _ := s.PendingRecovery(ctx, p.ID); got != "" {
		t.Errorf("PendingRecovery() = %q after confirming, want empty", got)
	}
}

func TestACodeIsForgivingAboutHowItIsTyped(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	code, _ := s.StartRecovery(ctx, p.ID, "misha@example.com")
	// Read off one screen and typed into another: lower case, a stray space, and the
	// letters Crockford leaves out because they look like digits.
	typed := strings.ToLower(code[:4] + " " + code[4:])
	if _, err := s.ConfirmRecovery(ctx, p.ID, typed); err != nil {
		t.Fatalf("ConfirmRecovery(%q) for code %q: %v", typed, code, err)
	}
}

func TestFiveWrongAnswersThrowTheAttemptAway(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	code, _ := s.StartRecovery(ctx, p.ID, "misha@example.com")
	for range RecoveryCodeAttempts {
		s.ConfirmRecovery(ctx, p.ID, "00000000")
	}
	// A lockout is a state somebody has to wait out; starting again is faster and no weaker.
	if _, err := s.ConfirmRecovery(ctx, p.ID, code); !errors.Is(err, ErrInvalid) {
		t.Fatalf("the right code still worked after %d wrong ones", RecoveryCodeAttempts)
	}
	if got, _ := s.PendingRecovery(ctx, p.ID); got != "" {
		t.Errorf("the attempt survived being exhausted: %q", got)
	}
}

func TestACodeExpires(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })

	code, _ := s.StartRecovery(ctx, p.ID, "misha@example.com")
	now = now.Add(RecoveryCodeLifetime + time.Minute)
	if _, err := s.ConfirmRecovery(ctx, p.ID, code); !errors.Is(err, ErrInvalid) {
		t.Fatal("an expired code was accepted")
	}
}

func TestStartingAgainReplacesTheAttempt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	first, _ := s.StartRecovery(ctx, p.ID, "misha@example.com")
	second, _ := s.StartRecovery(ctx, p.ID, "misha@example.com")

	// Two live codes for one account is two chances at the same guess.
	if _, err := s.ConfirmRecovery(ctx, p.ID, first); !errors.Is(err, ErrInvalid) {
		t.Error("the superseded code still worked")
	}
	if _, err := s.ConfirmRecovery(ctx, p.ID, second); err != nil {
		t.Errorf("the current code did not work: %v", err)
	}
}

func TestAnAddressBelongsToWhoeverProvedItLast(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	first := testPrincipal(t, s)
	second, err := s.CreatePrincipal(ctx, "someone", "a-good-password", RoleUser)
	if err != nil {
		t.Fatalf("CreatePrincipal(): %v", err)
	}

	code, _ := s.StartRecovery(ctx, first.ID, "shared@example.com")
	if _, err := s.ConfirmRecovery(ctx, first.ID, code); err != nil {
		t.Fatalf("ConfirmRecovery(first): %v", err)
	}
	code, _ = s.StartRecovery(ctx, second.ID, "shared@example.com")
	if _, err := s.ConfirmRecovery(ctx, second.ID, code); err != nil {
		t.Fatalf("ConfirmRecovery(second): %v", err)
	}

	// Whoever can read that inbox today is who recovery through it would actually reach.
	if got, _, _ := s.RecoveryAddress(ctx, first.ID); got != "" {
		t.Errorf("the first account kept the address: %q", got)
	}
	if got, _, _ := s.RecoveryAddress(ctx, second.ID); got != "shared@example.com" {
		t.Errorf("the second account did not take it: %q", got)
	}
}

func TestAFailedChangeLeavesTheAddressThatWorked(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	code, _ := s.StartRecovery(ctx, p.ID, "old@example.com")
	if _, err := s.ConfirmRecovery(ctx, p.ID, code); err != nil {
		t.Fatalf("ConfirmRecovery(): %v", err)
	}

	// A code that could not be sent leaves nothing waiting — but must not take the address
	// that already worked with it.
	if _, err := s.StartRecovery(ctx, p.ID, "new@example.com"); err != nil {
		t.Fatalf("StartRecovery(): %v", err)
	}
	if err := s.DropRecovery(ctx, p.ID); err != nil {
		t.Fatalf("DropRecovery(): %v", err)
	}
	if got, _, _ := s.RecoveryAddress(ctx, p.ID); got != "old@example.com" {
		t.Errorf("RecoveryAddress() = %q, want the address that still worked", got)
	}
	if got, _ := s.PendingRecovery(ctx, p.ID); got != "" {
		t.Errorf("the abandoned attempt survived: %q", got)
	}
}

func TestTheFloorHoldsForTheSchedulerAndNotForAButton(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	r, err := s.CreateReminder(ctx, p.ID, "go to the circus")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}
	withFloor(t, s, r.ID, 24*time.Hour)
	if _, err := s.RecordNudge(ctx, NewNudgeID(), p.ID, r.ID); err != nil {
		t.Fatalf("RecordNudge(): %v", err)
	}

	now := s.Now()
	if got, _ := s.Candidates(ctx, p.ID, now, RespectFloor); len(got) != 0 {
		t.Errorf("a scheduled draw offered %d inside the floor, want 0", len(got))
	}
	// Somebody pressing a button has asked for a nudge; "that was raised too recently" is
	// refusing a request nobody made on their behalf.
	if got, _ := s.Candidates(ctx, p.ID, now, IgnoreFloor); len(got) != 1 {
		t.Errorf("a manual draw offered %d, want the reminder", len(got))
	}
}

func TestSilencingSurvivesEvenAManualDraw(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	r, _ := s.CreateReminder(ctx, p.ID, "never mention this")
	if _, err := s.main.ExecContext(ctx, `UPDATE reminders SET priority = 0 WHERE id = ?`, r.ID); err != nil {
		t.Fatalf("silence: %v", err)
	}
	// Zero is the difference between "not now" and "not ever", so it holds either way.
	for _, floor := range []Floor{RespectFloor, IgnoreFloor} {
		if got, _ := s.Candidates(ctx, p.ID, s.Now(), floor); len(got) != 0 {
			t.Errorf("floor=%v offered a silenced reminder", floor)
		}
	}
}

func TestADoneReminderIsNeverDrawnEitherWay(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	r, _ := s.CreateReminder(ctx, p.ID, "ring the dentist")
	if err := s.EndReminder(ctx, p.ID, r.ID); err != nil {
		t.Fatalf("EndReminder(): %v", err)
	}
	for _, floor := range []Floor{RespectFloor, IgnoreFloor} {
		if got, _ := s.Candidates(ctx, p.ID, s.Now(), floor); len(got) != 0 {
			t.Errorf("floor=%v offered a finished reminder", floor)
		}
	}
}

func TestOneBrowserKeepsOneDeviceThroughARotatedSubscription(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	// A browser replaces its subscription on its own — after a permission is re-granted,
	// after site data is cleared, after pushsubscriptionchange. Upserting on the endpoint
	// alone left the old row in place, both stayed live at the push service, and one press
	// of "send one now" sent two pushes.
	if _, err := s.RegisterDevice(ctx, p.ID, "https://push.example.com/first", "k", "a", "Chrome on Mac", "c_one"); err != nil {
		t.Fatalf("RegisterDevice(first): %v", err)
	}
	if _, err := s.RegisterDevice(ctx, p.ID, "https://push.example.com/second", "k", "a", "Chrome on Mac", "c_one"); err != nil {
		t.Fatalf("RegisterDevice(second): %v", err)
	}

	got, err := s.Devices(ctx, p.ID)
	if err != nil {
		t.Fatalf("Devices(): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("one browser holds %d devices, want 1", len(got))
	}
	if got[0].Endpoint != "https://push.example.com/second" {
		t.Errorf("kept %q, want the current subscription", got[0].Endpoint)
	}
}

func TestTwoBrowsersKeepTwoDevices(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	s.RegisterDevice(ctx, p.ID, "https://push.example.com/laptop", "k", "a", "Chrome on Mac", "c_laptop")
	s.RegisterDevice(ctx, p.ID, "https://push.example.com/phone", "k", "a", "Safari on iPhone", "c_phone")

	if got, _ := s.Devices(ctx, p.ID); len(got) != 2 {
		t.Fatalf("two browsers hold %d devices, want 2", len(got))
	}
}

func TestARowWithNoBrowserIdentityCollapsesNothing(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	// Rows predating the client id have none, and an unknown browser must not be treated as
	// the same unknown browser as another.
	s.RegisterDevice(ctx, p.ID, "https://push.example.com/old-a", "k", "a", "a browser", "")
	s.RegisterDevice(ctx, p.ID, "https://push.example.com/old-b", "k", "a", "a browser", "")

	if got, _ := s.Devices(ctx, p.ID); len(got) != 2 {
		t.Fatalf("empty client ids collapsed to %d rows, want 2 left alone", len(got))
	}
}

func TestAReminderInheritsNoFloor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	r, err := s.CreateReminder(ctx, p.ID, "go to the circus")
	if err != nil {
		t.Fatalf("CreateReminder(): %v", err)
	}
	if r.MinInterval != 0 {
		t.Errorf("a new reminder carries a floor of %s, want none", r.MinInterval)
	}

	// A day apiece was the old default, and it capped the day's budget at however many
	// reminders somebody had — eight reminders could never fill ten slots.
	if _, err := s.RecordNudge(ctx, NewNudgeID(), p.ID, r.ID); err != nil {
		t.Fatalf("RecordNudge(): %v", err)
	}
	if got, _ := s.Candidates(ctx, p.ID, s.Now(), RespectFloor); len(got) != 1 {
		t.Errorf("a just-nudged reminder with no floor is not drawable; the budget is capped again")
	}
}

func TestAStatedFloorIsStillObeyed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testPrincipal(t, s)

	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })

	r, _ := s.CreateReminder(ctx, p.ID, "ring the dentist")
	withFloor(t, s, r.ID, 7*24*time.Hour)
	if _, err := s.RecordNudge(ctx, NewNudgeID(), p.ID, r.ID); err != nil {
		t.Fatalf("RecordNudge(): %v", err)
	}

	// A floor somebody stated is an instruction about that particular thing, and outranks a
	// general appetite for more nudges.
	if got, _ := s.Candidates(ctx, p.ID, now.AddDate(0, 0, 3), RespectFloor); len(got) != 0 {
		t.Error("a weekly reminder was offered three days later")
	}
	if got, _ := s.Candidates(ctx, p.ID, now.AddDate(0, 0, 8), RespectFloor); len(got) != 1 {
		t.Error("a weekly reminder was not offered after eight days")
	}
}

func TestABudgetIsBoundedOnlyByTheCeiling(t *testing.T) {
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

	// Nothing about the window bounds it any more: the interval is the waking day over the
	// budget and is floored at one tick, so there is no second ceiling to keep in agreement
	// with a planner that no longer exists.
	r.Budget = MaxBudget
	if err := s.SetRhythm(ctx, r); err != nil {
		t.Fatalf("SetRhythm(%d): %v", MaxBudget, err)
	}
	r.Budget = MaxBudget + 1
	if err := s.SetRhythm(ctx, r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetRhythm(%d) = %v, want ErrInvalid", MaxBudget+1, err)
	}
}
