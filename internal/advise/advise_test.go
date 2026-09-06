package advise

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
			io.WriteString(w, `{"error":{"code":401,"message":"No auth credentials found"}}`)
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
	if stale, _ := st.AdviceIsStale(ctx, p.ID); stale {
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
	if stale, _ := st.AdviceIsStale(ctx, p.ID); !stale {
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
