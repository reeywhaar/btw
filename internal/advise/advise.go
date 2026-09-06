// Package advise asks each person's companion what it makes of their reminders, and records
// the answer for the weighting to read.
//
// The impure half of the companion, as internal/nudge is the impure half of the scheduling: it
// reads a clock, opens a socket and writes to a database. What it learns is consumed by
// internal/pick, which stays a pure function of what it is handed.
//
// # Why a flag and a loop rather than a write-through
//
// The obvious design asks the moment anything changes. It is wrong twice. Somebody writing
// down six things in a minute would be six questions, and a free key allows fifty in a day —
// so the burst that most deserves one answer is the one that exhausts the quota. And a
// question asked inside somebody's save makes their save as slow as a model's thinking, for a
// result nothing is waiting on.
//
// So every write that could change an answer only says so, and this loop decides when to act.
// The interval is the throttle, exactly as it is for the backup pusher, and it is doing more
// work here: it collapses a burst of edits into one question and bounds the requests a broken
// key can spend.
package advise

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"btw/internal/openrouter"
	"btw/internal/rhythm"
	"btw/internal/store"
)

// Every is how often the loop looks for somebody whose advice is stale.
//
// Half an hour, and the number comes from the quota rather than from taste. One person can
// cost at most one question per pass, so a free key — fifty a day until credit has ever been
// bought — survives a person who edits something in every single window, forty-eight times
// over. At fifteen minutes the same person would exhaust it before the evening.
//
// It is also long enough to be the collapse that matters: somebody sitting down to write out
// their week is one question, half an hour later, holding all of it.
const Every = 30 * time.Minute

// askTimeout bounds one question. Generous, because a reasoning model on a free endpoint
// queues, and an answer that arrives late is still worth having when nothing is waiting on it.
const askTimeout = 3 * time.Minute

// Alerter says something to a person's devices about btw itself.
//
// An interface of one method, satisfied by *nudge.Scheduler, so this package does not import
// the scheduler and the dependency keeps running one way. It is also what lets a test watch
// what would have been sent without a push service on the other end.
type Alerter interface {
	Alert(ctx context.Context, principalID, title, text string) (int, error)
}

// Adviser keeps everybody's advice roughly up to date.
type Adviser struct {
	Store *store.Store
	Log   *slog.Logger

	// Alerts is how somebody finds out their key stopped working without opening settings.
	// Optional: nil sends nothing, which is what a test wants unless it is testing this.
	Alerts Alerter

	// Every defaults to the constant above. A field only so a test can drive the loop without
	// waiting half an hour for it.
	Every time.Duration
}

// New makes an adviser.
func New(st *store.Store, log *slog.Logger, alerts Alerter) *Adviser {
	return &Adviser{Store: st, Log: log, Alerts: alerts}
}

// Run works until the context is done.
//
// The first pass is immediate rather than one interval in, for the reason the backup pusher's
// is: a process that has just started may be running against a derived.db that was thrown
// away, and everybody's advice with it. Waiting half an hour to find that out is half an hour
// of weighting nothing.
func (a *Adviser) Run(ctx context.Context) {
	if a.Every <= 0 {
		a.Every = Every
	}
	a.Log.Info("advising", "at_most_every", a.Every)

	for {
		a.Once(ctx)

		timer := time.NewTimer(a.Every)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Once is one pass over everybody who has a companion.
//
// Exported so a test can drive a single pass without waiting on a clock.
func (a *Adviser) Once(ctx context.Context) {
	people, err := a.Store.PrincipalsWithCompanion(ctx)
	if err != nil {
		a.Log.Error("could not list companions", "err", err)
		return
	}
	for _, id := range people {
		select {
		case <-ctx.Done():
			return
		default:
		}

		a.Look(ctx, id)
	}
}

// Look asks about one person, when their advice wants asking about, and reports what went
// wrong if anything did.
//
// Exported because a person pressing "ask again" waits for the answer. That press used to hand
// the work to the loop and return before anything had happened, which is defensible for a
// background job and poor for a button: somebody who has just rewritten what they say about
// themselves wants to see the difference, not a note telling them to look again later.
//
// The error is returned as well as recorded, so a press can show it. A pass ignores what comes
// back — by then it has already been logged, alerted on, and written against the account.
func (a *Adviser) Look(ctx context.Context, id string) error {
	state, err := a.Store.Advice(ctx, id)
	if err != nil {
		a.Log.Error("could not read advice state", "principal", id, "err", err)
		return err
	}
	if !state.Stale {
		return nil
	}

	err = a.advise(ctx, id)
	if err == nil {
		return nil
	}

	// Recorded against the account as well as logged, so that somebody whose key stopped
	// working can be told rather than left wondering why nothing improves. The flag stays
	// set, so the next pass tries again.
	limited := errors.Is(err, openrouter.ErrRateLimited)
	a.Log.Warn("could not advise", "principal", id, "limited", limited, "err", err)
	if err := a.Store.RecordAdviceFailure(ctx, id, a.Store.Now(), err.Error(), limited); err != nil {
		a.Log.Error("could not record an advice failure", "principal", id, "err", err)
	}
	if !limited {
		// A quota is not worth waking somebody for. It resolves itself, nothing they could
		// do would help, and a notification saying so is one that trains them to ignore
		// the next one.
		a.tell(ctx, id, state, err)
	}

	// Nothing is throttled and the pass is not abandoned, because **a quota is per key and
	// every account brings its own**. One person's key having run out says nothing about
	// the next person's, and pacing between two accounts would be spreading requests
	// across quotas that were never shared.
	//
	// So a rate limit is only worth telling apart from every other failure, which the
	// sentinel does: it wants a wait rather than a person, and the next pass is half an
	// hour away — which is the wait. One account cannot reach the limit alone, since it
	// costs at most one question per pass.
	return err
}

// advise is one question about one person.
func (a *Adviser) advise(ctx context.Context, principalID string) error {
	set, err := a.Store.Companion(ctx, principalID)
	if err != nil {
		return err
	}
	if !set.Configured() {
		return nil
	}

	reminders, err := a.Store.Reminders(ctx, principalID, false)
	if err != nil {
		return err
	}
	if len(reminders) == 0 {
		// Nothing to ask about, and asking anyway would spend a request to be told so. Marked
		// answered rather than left stale, or an account with no reminders would be revisited
		// every single pass forever.
		return a.Store.RecordAdvised(ctx, principalID, a.Store.Now())
	}

	rh, err := a.Store.Rhythm(ctx, principalID)
	if err != nil {
		return err
	}
	// Instance-wide, unlike the key: how this machine reaches the internet is one fact about
	// the machine, not one per account.
	via, err := a.Store.Proxy(ctx)
	if err != nil {
		return err
	}
	question, err := User(set.About, rh, reminders)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()

	reply, res, err := openrouter.Ask(ctx, set, via, System(), question, budget(len(reminders)))
	if err != nil {
		return err
	}

	ids := make([]string, len(reminders))
	known := make(map[string]bool, len(reminders))
	for i, r := range reminders {
		ids[i] = r.ID
		known[r.ID] = true
	}

	advice, dropped, misshapen := parse(reply, known)

	// What a round could not read is not thrown away — the answer before it stands.
	//
	// A model that mangles one reminder out of forty used to cost that reminder everything it
	// had, leaving it weighed at exactly 1 until some later round happened to get it right.
	// Stale advice is worth more than none: it was true when it was written, and the reminder
	// it is about did not change — the *answer* failed, not the question.
	//
	// A deliberate "no shape" still overwrites, because an empty list of spans reads as a
	// whole week of neutrals rather than as a failure. So the only thing carried forward is an
	// answer that could not be understood or did not arrive.
	previous, err := a.Store.AdviceFor(ctx, ids)
	if err != nil {
		return err
	}
	carried := 0
	for id, old := range previous {
		if !old.Curve.Valid() {
			continue
		}
		fresh, answered := advice[id]
		if answered && fresh.Curve.Valid() {
			continue
		}
		// Whatever this round did manage to say about it is kept — the categories may be
		// readable when the curve is not — and only the curve comes from before.
		fresh.Curve = old.Curve
		advice[id] = fresh
		carried++
	}

	if err := a.Store.SetAdvice(ctx, ids, advice); err != nil {
		return err
	}
	if err := a.Store.RecordAdvised(ctx, principalID, a.Store.Now()); err != nil {
		return err
	}

	// The reminder text is not logged and neither is what was said about it — the same rule
	// the nudge log follows, and for the same reason. The counts are what an operator needs.
	a.Log.Info("advised", "principal", principalID, "model", res.Model,
		"reminders", len(reminders), "answered", len(advice), "dropped", dropped,
		"carried", carried,
		// The shapes and not the answers: "7x24" says what to change about the question, and
		// says nothing about anybody's reminders.
		"misshapen", misshapen, "tokens", res.Tokens)
	return nil
}

// tell lets somebody know their companion has stopped working, at most once per episode.
//
// Settings already says so, which is where somebody looks once they already suspect. This is
// for the case that makes the whole feature fail quietly: a key revoked in March, noticed in
// June, with three months of nudges that were never weighted and no sign anything was wrong.
//
// Two rules keep it from becoming the thing people turn notifications off over.
//
// **Once per episode.** The loop runs every half hour and a broken key fails every pass, so
// without the flag this would be a notification twice an hour for as long as it stayed broken.
// The flag is lowered by a round that succeeds, so a key fixed and later broken again is worth
// a second message.
//
// **Not while they are asleep.** btw refuses to nudge outside somebody's waking hours, and a
// message about an API key has less claim on four in the morning than a reminder does. Asleep
// means it is not marked told either, so it goes out on the first pass after they wake.
func (a *Adviser) tell(ctx context.Context, principalID string, was store.AdviceState, reason error) {
	if a.Alerts == nil || was.Alerted {
		return
	}

	rh, err := a.Store.Rhythm(ctx, principalID)
	if err != nil {
		a.Log.Error("could not read a rhythm", "principal", principalID, "err", err)
		return
	}
	if !rhythm.Awake(rh, a.Store.Now()) {
		return
	}

	delivered, err := a.Alerts.Alert(ctx, principalID,
		"btw has stopped advising you",
		// The gateway's own words, trimmed to what a lock screen can hold. What went wrong
		// matters more than that something did: a rejected key and a retired model send
		// somebody to two different fields.
		sentence(reason.Error()))
	if err != nil {
		a.Log.Error("could not send an alert", "principal", principalID, "err", err)
		return
	}
	if delivered <= 0 {
		// Nothing reached a device, so nothing is recorded: the next pass tries again rather
		// than marking somebody told about a message they never got.
		return
	}
	if err := a.Store.MarkAdviceAlerted(ctx, principalID); err != nil {
		a.Log.Error("could not record an alert", "principal", principalID, "err", err)
	}
}

// sentence caps a gateway's error at something a lock screen can hold.
func sentence(s string) string {
	const limit = 140
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// budget is how much room the answer is given.
//
// Scaled by the question, because the answer is one object per reminder and a fixed ceiling is
// either wasteful for somebody with three or a truncation for somebody with forty. The floor
// covers a reasoning model's thinking, which counts against the same number even though it is
// excluded from what comes back.
//
// The per-reminder figure is almost entirely the curve: seven days of forty-eight numbers at
// one decimal place is well over a thousand tokens on its own, before anything around it. A
// truncated answer is a torn-off JSON object rather than a short one, so the room is worth more
// than the tokens it costs.
//
// The ceiling is where this stops being free. Somebody with a very long list is asking for more
// output than most models will produce in one go, and the honest answer is that the question
// wants splitting rather than the ceiling raising — which is a thing to build when somebody
// meets it, not before.
func budget(reminders int) int {
	return min(2000+1800*reminders, 32000)
}
