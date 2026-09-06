package advise

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"btw/internal/openrouter"
	"btw/internal/store"
)

// gateway stands in for OpenRouter over a real loopback socket, for the reason
// internal/openrouter's own tests use one: what is worth asserting is the conversation.
type gateway struct {
	asked  int
	prompt string
	reply  string
	status int
}

func (g *gateway) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		g.asked++
		g.prompt = string(body)
		w.Header().Set("Content-Type", "application/json")
		if g.status != 0 && g.status != http.StatusOK {
			w.WriteHeader(g.status)
			// The code in the body matches the status, as OpenRouter's does. They are read
			// separately on purpose — the body wins — so a fixture that disagreed with itself
			// would be testing the disagreement rather than the status it meant to send.
			fmt.Fprintf(w, `{"error":{"code":%d,"message":"%s"}}`, g.status, http.StatusText(g.status))
			return
		}
		io.WriteString(w, `{"model":"minimax/minimax-m3:free","usage":{"total_tokens":120},
			"choices":[{"finish_reason":"stop","message":{"content":`+quote(g.reply)+`}}]}`)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(openrouter.SetEndpoint(srv.URL))
}

func quote(s string) string {
	// Tabs and carriage returns as well as quotes and newlines: a literal control character
	// inside a JSON string is invalid, and the decoder under test is right to refuse one.
	return `"` + strings.NewReplacer(
		`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\r", `\r`,
	).Replace(s) + `"`
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func adviser(st *store.Store) *Adviser {
	return &Adviser{Store: st, Log: slog.New(slog.DiscardHandler)}
}

func person(t *testing.T, st *store.Store, name string) store.Principal {
	t.Helper()
	p, err := st.CreatePrincipal(context.Background(), name, "a-good-password", store.RoleUser)
	if err != nil {
		t.Fatalf("CreatePrincipal(): %v", err)
	}
	return p
}

func TestOneQuestionCoversEverybodysReminders(t *testing.T) {
	g := &gateway{}
	g.start(t)

	st := newStore(t)
	ctx := context.Background()
	p := person(t, st, "misha")
	if err := st.SetCompanion(ctx, p.ID, openrouter.Settings{APIKey: "k", About: "I sleep until noon"}); err != nil {
		t.Fatalf("SetCompanion(): %v", err)
	}
	show, _ := st.CreateReminder(ctx, p.ID, "watch rick and morty")
	cv, _ := st.CreateReminder(ctx, p.ID, "update cv")

	g.reply = `{"results":[
		{"id":"` + show.ID + `","category":["entertainment"],"exclusive":false,
		 "slots":[{"day":"mon","start":"22:00","end":"02:00"},{"day":"tue","start":"22:00","end":"02:00"}]},
		{"id":"` + cv.ID + `","category":["work","admin"],"exclusive":true,
		 "slots":[{"day":"sat","start":"14:00","end":"18:00"}]}
	]}`

	adviser(st).Once(ctx)

	// One question for the pair, not one each. This is the whole reason the loop exists.
	if g.asked != 1 {
		t.Errorf("asked %d times, want once for the whole set", g.asked)
	}
	// What the person wrote about themselves has to reach the model, or the advice is guessed
	// from the reminder text alone.
	if !strings.Contains(g.prompt, "I sleep until noon") {
		t.Error("the description of the person did not reach the model")
	}

	got, err := st.Candidates(ctx, p.ID, st.Now(), store.IgnoreFloor)
	if err != nil {
		t.Fatalf("Candidates(): %v", err)
	}
	for _, c := range got {
		if !c.Advised {
			t.Fatalf("%q came back unadvised", c.Text)
		}
		switch c.ID {
		case show.ID:
			if c.Exclusive || len(c.Slots) != 2 {
				t.Errorf("the show = %+v, want two windows and not exclusive", c)
			}
		case cv.ID:
			if !c.Exclusive || len(c.Slots) != 1 {
				t.Errorf("the cv = %+v, want one window and exclusive", c)
			}
		}
	}

	// And it is not asked again until something changes.
	adviser(st).Once(ctx)
	if g.asked != 1 {
		t.Errorf("asked %d times, want the fresh answer left alone", g.asked)
	}
}

// Every write that could change an answer says so, and nothing else does. Nudging a reminder
// changes when it was last raised and not what it is about.
func TestOnlyAChangeWorthAskingAboutMakesItStale(t *testing.T) {
	g := &gateway{reply: `{"results":[]}`}
	g.start(t)

	st := newStore(t)
	ctx := context.Background()
	p := person(t, st, "misha")
	st.SetCompanion(ctx, p.ID, openrouter.Settings{APIKey: "k"})
	rem, _ := st.CreateReminder(ctx, p.ID, "water the plants")

	adviser(st).Once(ctx)
	if g.asked != 1 {
		t.Fatalf("asked %d times on the first pass, want once", g.asked)
	}

	if err := st.StampNudged(ctx, rem.ID, st.Now()); err != nil {
		t.Fatalf("StampNudged(): %v", err)
	}
	adviser(st).Once(ctx)
	if g.asked != 1 {
		t.Error("being nudged made the advice stale, which it does not change")
	}

	if err := st.MarkAdviceStale(ctx, p.ID); err != nil {
		t.Fatalf("MarkAdviceStale(): %v", err)
	}
	adviser(st).Once(ctx)
	if g.asked != 2 {
		t.Error("a change did not lead to a new question")
	}
}

func TestNobodyIsAskedOnBehalfOfAnAccountWithNoCompanion(t *testing.T) {
	g := &gateway{reply: `{"results":[]}`}
	g.start(t)

	st := newStore(t)
	ctx := context.Background()
	p := person(t, st, "misha")
	st.CreateReminder(ctx, p.ID, "water the plants")

	adviser(st).Once(ctx)
	if g.asked != 0 {
		t.Errorf("asked %d times for an account with no key", g.asked)
	}
}

// Asking would spend one of fifty daily requests to be told there is nothing to say. Marked
// answered rather than left stale, or the account is revisited every pass forever.
func TestAnAccountWithNoRemindersCostsNoQuestion(t *testing.T) {
	g := &gateway{reply: `{"results":[]}`}
	g.start(t)

	st := newStore(t)
	ctx := context.Background()
	p := person(t, st, "misha")
	st.SetCompanion(ctx, p.ID, openrouter.Settings{APIKey: "k"})

	adviser(st).Once(ctx)
	adviser(st).Once(ctx)
	if g.asked != 0 {
		t.Errorf("asked %d times with nothing to ask about", g.asked)
	}
	if state, _ := st.Advice(ctx, p.ID); state.Stale {
		t.Error("an account with no reminders is revisited every pass")
	}
}

// A failure is not an answer. Clearing the flag would mean a key that stopped working quietly
// froze the advice at whatever it last said, with nothing to show for it.
func TestAFailedQuestionIsRememberedAndTriedAgain(t *testing.T) {
	g := &gateway{status: http.StatusUnauthorized}
	g.start(t)

	st := newStore(t)
	ctx := context.Background()
	p := person(t, st, "misha")
	st.SetCompanion(ctx, p.ID, openrouter.Settings{APIKey: "wrong"})
	st.CreateReminder(ctx, p.ID, "water the plants")

	adviser(st).Once(ctx)
	if state, _ := st.Advice(ctx, p.ID); !state.Stale {
		t.Error("a failure was recorded as an answer")
	}

	adviser(st).Once(ctx)
	if g.asked != 2 {
		t.Errorf("asked %d times, want the next pass to try again", g.asked)
	}
}

// The reminder text is the one thing in this database somebody would mind being read. It goes
// to the model because that is the point, and it must not also end up anywhere else.
func TestAdviceNeverSilencesAnythingItCannotPlace(t *testing.T) {
	g := &gateway{}
	g.start(t)

	st := newStore(t)
	ctx := context.Background()
	p := person(t, st, "misha")
	st.SetCompanion(ctx, p.ID, openrouter.Settings{APIKey: "k"})
	rem, _ := st.CreateReminder(ctx, p.ID, "the model has no idea when to do this")
	g.reply = `{"results":[{"id":"` + rem.ID + `","category":[],"exclusive":true,"slots":[]}]}`

	adviser(st).Once(ctx)

	got, err := st.Candidates(ctx, p.ID, st.Now(), store.IgnoreFloor)
	if err != nil {
		t.Fatalf("Candidates(): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Candidates() = %d, want the reminder still there", len(got))
	}
	if !got[0].Advised || len(got[0].Slots) != 0 {
		t.Errorf("candidate = %+v, want it answered for with no hours", got[0])
	}
}

func TestTheQuestionCarriesTheWakingHoursAndTheNote(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	p := person(t, st, "misha")
	rem, _ := st.CreateReminder(ctx, p.ID, "update cv")
	if _, err := st.UpdateReminder(ctx, p.ID, rem.ID, "update cv", "the one for the agency"); err != nil {
		t.Fatalf("UpdateReminder(): %v", err)
	}
	reminders, _ := st.Reminders(ctx, p.ID, false)

	q, err := User("", store.Rhythm{
		Timezone: "Asia/Tbilisi", WindowEnabled: true, WakeMinute: 13 * 60, SleepMinute: 4 * 60,
	}, reminders)
	if err != nil {
		t.Fatalf("User(): %v", err)
	}

	// A slot outside these hours can never be delivered in, so a companion that does not know
	// them wastes half its answer.
	for _, want := range []string{"13:00", "04:00", "Asia/Tbilisi", "the one for the agency"} {
		if !strings.Contains(q, want) {
			t.Errorf("the question does not carry %q", want)
		}
	}
	// A model handed an empty section invents a person to fill it.
	if !strings.Contains(q, "have not written anything") {
		t.Error("an empty description is left blank rather than said")
	}
}

// A quota is not a mistake, and it is recorded as its own kind so the interface can say "it
// will try again" instead of sending somebody to check a key that is fine.
func TestARateLimitIsRememberedAsAQuotaAndNotAsABrokenKey(t *testing.T) {
	g := &gateway{status: http.StatusTooManyRequests}
	g.start(t)

	st := newStore(t)
	ctx := context.Background()
	p := person(t, st, "misha")
	st.SetCompanion(ctx, p.ID, openrouter.Settings{APIKey: "k"})
	st.CreateReminder(ctx, p.ID, "water the plants")

	adviser(st).Once(ctx)

	state, err := st.Advice(ctx, p.ID)
	if err != nil {
		t.Fatalf("Advice(): %v", err)
	}
	if !state.Limited {
		t.Error("a 429 was recorded the way a rejected key is")
	}
	if !state.Stale {
		t.Error("a rate limit was treated as an answer")
	}
	if state.Error == "" {
		t.Error("the gateway's own words were not kept")
	}
}

// Keys are per account, so one person's exhausted quota says nothing about the next person's.
// Abandoning the pass would punish everybody for one key.
func TestOneExhaustedKeyDoesNotStopTheRest(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		seen = append(seen, key)
		w.Header().Set("Content-Type", "application/json")
		if key == "spent" {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"code":429,"message":"Rate limit exceeded"}}`)
			return
		}
		io.WriteString(w, `{"model":"m","choices":[{"finish_reason":"stop","message":{"content":"{\"results\":[]}"}}]}`)
	}))
	defer srv.Close()
	defer openrouter.SetEndpoint(srv.URL)()

	st := newStore(t)
	ctx := context.Background()
	for _, who := range []struct{ name, key string }{{"aaa", "spent"}, {"zzz", "fine"}} {
		p := person(t, st, who.name)
		st.SetCompanion(ctx, p.ID, openrouter.Settings{APIKey: who.key})
		st.CreateReminder(ctx, p.ID, "water the plants")
	}

	adviser(st).Once(ctx)

	if len(seen) != 2 {
		t.Fatalf("asked with %v, want both keys tried", seen)
	}
	if seen[1] != "fine" {
		t.Errorf("second question used %q, want the other account's own key", seen[1])
	}
}

// Deleting a reminder outright is the one ending that cannot be undone, so it is the one that
// takes the advice with it. Finishing with a reminder does not: it can be revived, and what
// was said about it is still true.
func TestAdviceOutlivesADoneReminderAndNotADeletedOne(t *testing.T) {
	g := &gateway{}
	g.start(t)

	st := newStore(t)
	ctx := context.Background()
	p := person(t, st, "misha")
	st.SetCompanion(ctx, p.ID, openrouter.Settings{APIKey: "k"})
	kept, _ := st.CreateReminder(ctx, p.ID, "water the plants")
	gone, _ := st.CreateReminder(ctx, p.ID, "call the dentist")

	g.reply = `{"results":[
		{"id":"` + kept.ID + `","slots":[{"day":"mon","start":"09:00","end":"10:00"}]},
		{"id":"` + gone.ID + `","slots":[{"day":"tue","start":"09:00","end":"10:00"}]}
	]}`
	adviser(st).Once(ctx)

	if err := st.EndReminder(ctx, p.ID, kept.ID); err != nil {
		t.Fatalf("EndReminder(): %v", err)
	}
	if err := st.DeleteReminder(ctx, p.ID, gone.ID); err != nil {
		t.Fatalf("DeleteReminder(): %v", err)
	}
	if err := st.ForgetAdvice(ctx, gone.ID); err != nil {
		t.Fatalf("ForgetAdvice(): %v", err)
	}

	// Reviving restores a reminder that still has its advice, without waiting for a new round.
	if err := st.ReviveReminder(ctx, p.ID, kept.ID); err != nil {
		t.Fatalf("ReviveReminder(): %v", err)
	}
	got, err := st.Candidates(ctx, p.ID, st.Now(), store.IgnoreFloor)
	if err != nil {
		t.Fatalf("Candidates(): %v", err)
	}
	if len(got) != 1 || !got[0].Advised {
		t.Errorf("candidates = %+v, want the revived reminder still carrying its advice", got)
	}
}

// alerts records what would have gone to somebody's devices.
type alerts struct {
	sent []string
	// nobodyTakes makes every send land nowhere. A bool rather than a count, because a count
	// wants a zero value meaning "one device took it" and that is exactly the reading that
	// hid the bug this tests for.
	nobodyTakes bool
}

func (a *alerts) Alert(_ context.Context, principalID, title, text string) (int, error) {
	a.sent = append(a.sent, principalID+": "+title+" — "+text)
	if a.nobodyTakes {
		return 0, nil
	}
	return 1, nil
}

func broken(t *testing.T, status int) (*store.Store, store.Principal, *alerts) {
	t.Helper()
	g := &gateway{status: status}
	g.start(t)

	st := newStore(t)
	ctx := context.Background()
	p := person(t, st, "misha")
	st.SetCompanion(ctx, p.ID, openrouter.Settings{APIKey: "k"})
	st.CreateReminder(ctx, p.ID, "water the plants")
	// Midday, inside the default waking window. Pinned rather than left to the wall clock:
	// alerting is refused outside somebody's waking hours, so an unpinned clock makes every
	// test here pass or fail depending on the hour it is run at.
	st.SetClock(func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) })
	return st, p, &alerts{}
}

// The case the whole feature exists for: a key revoked in March and noticed in June, with
// three months of nudges that were never weighted and no sign anything was wrong.
func TestABrokenKeyIsPushedOnceAndNotEveryPass(t *testing.T) {
	st, p, sent := broken(t, http.StatusUnauthorized)
	ctx := context.Background()
	a := adviser(st)
	a.Alerts = sent

	a.Once(ctx)
	if len(sent.sent) != 1 {
		t.Fatalf("sent %v, want one message", sent.sent)
	}
	if !strings.Contains(sent.sent[0], "Unauthorized") {
		t.Errorf("message = %q, want the gateway's own words", sent.sent[0])
	}

	// The loop runs every half hour and a broken key fails every pass. Without the flag this
	// is a notification twice an hour until somebody turns notifications off for good.
	a.Once(ctx)
	a.Once(ctx)
	if len(sent.sent) != 1 {
		t.Errorf("sent %d messages, want the person told once per episode", len(sent.sent))
	}

	// A round that works lowers the flag, so a key fixed and broken again months later is
	// worth telling somebody about a second time.
	if err := st.RecordAdvised(ctx, p.ID, st.Now()); err != nil {
		t.Fatalf("RecordAdvised(): %v", err)
	}
	if err := st.MarkAdviceStale(ctx, p.ID); err != nil {
		t.Fatalf("MarkAdviceStale(): %v", err)
	}
	a.Once(ctx)
	if len(sent.sent) != 2 {
		t.Errorf("sent %d messages, want a second episode to be worth one", len(sent.sent))
	}
}

// A quota resolves itself and nothing somebody could do would help. A notification saying so
// is a notification that trains them to ignore the next one.
func TestAQuotaIsNotWorthANotification(t *testing.T) {
	st, _, sent := broken(t, http.StatusTooManyRequests)
	a := adviser(st)
	a.Alerts = sent

	a.Once(context.Background())
	if len(sent.sent) != 0 {
		t.Errorf("sent %v, want a quota to stay in settings", sent.sent)
	}
}

// btw refuses to nudge outside somebody's waking hours, and a message about an API key has
// less claim on four in the morning than a reminder does.
func TestNobodyIsWokenToBeToldAboutAKey(t *testing.T) {
	st, p, sent := broken(t, http.StatusUnauthorized)
	ctx := context.Background()

	// Awake from 09:00 to 22:00, and it is the small hours.
	if err := st.SetRhythm(ctx, store.Rhythm{
		PrincipalID: p.ID, Timezone: "UTC", WindowEnabled: true,
		WakeMinute: 9 * 60, SleepMinute: 22 * 60, Budget: 3,
	}); err != nil {
		t.Fatalf("SetRhythm(): %v", err)
	}
	night := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	st.SetClock(func() time.Time { return night })

	a := adviser(st)
	a.Alerts = sent
	a.Once(ctx)
	if len(sent.sent) != 0 {
		t.Fatalf("sent %v at four in the morning", sent.sent)
	}

	// And it is not recorded as told, so it goes out on the first pass after they wake.
	st.SetClock(func() time.Time { return night.Add(8 * time.Hour) })
	a.Once(ctx)
	if len(sent.sent) != 1 {
		t.Errorf("sent %v, want it held until waking hours rather than dropped", sent.sent)
	}
}

// Marking somebody told about a message no device took would be the one way to lose the
// notification entirely.
func TestAnUndeliveredAlertIsSentAgain(t *testing.T) {
	st, _, sent := broken(t, http.StatusUnauthorized)
	sent.nobodyTakes = true

	a := adviser(st)
	a.Alerts = sent
	a.Once(context.Background())
	a.Once(context.Background())
	if len(sent.sent) != 2 {
		t.Errorf("tried %d times, want an undelivered message tried again", len(sent.sent))
	}
}
