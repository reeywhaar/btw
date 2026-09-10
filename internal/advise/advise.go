// Package advise asks each person's companion what it makes of their reminders, and records
// the answer for the weighting to read.
//
// The impure half of the companion, as internal/nudge is of the scheduling: it reads a clock,
// opens a socket and writes to a database, and internal/pick consumes what it learns.
//
// # Why a flag and a loop rather than a write-through
//
// Asking the moment anything changes is wrong twice. Six things written down in a minute would
// be six questions against fifty a day, so the burst that most deserves one answer exhausts the
// quota; and a question inside somebody's save makes the save as slow as a model's thinking.
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
// The number comes from the quota, not from taste: one person costs at most one question per
// pass, so fifty a day survives somebody who edits in all forty-eight windows. At fifteen
// minutes they would exhaust it before the evening.
const Every = 30 * time.Minute

// askTimeout bounds one question. Generous: a reasoning model on a free endpoint queues, and
// nothing is waiting on the answer.
const askTimeout = 3 * time.Minute

// Alerter says something to a person's devices about btw itself. One method, so this package
// does not import the scheduler and a test can watch what would have been sent.
type Alerter interface {
	Alert(ctx context.Context, principalID, title, text string) (int, error)
}

// Adviser keeps everybody's advice roughly up to date.
type Adviser struct {
	Store *store.Store
	Log   *slog.Logger

	// Optional: nil sends nothing.
	Alerts Alerter

	// Every defaults to the constant above; a field so a test need not wait half an hour.
	Every time.Duration
}

func New(st *store.Store, log *slog.Logger, alerts Alerter) *Adviser {
	return &Adviser{Store: st, Log: log, Alerts: alerts}
}

// Run works until the context is done. The first pass is immediate: a process that has just
// started may be running against a derived.db that was thrown away, advice and all.
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

// Once is one pass over everybody who has a companion. Exported so a test can drive one
// without waiting on a clock.
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

// Look asks about one person, when their advice wants asking about.
//
// Exported because "ask again" waits for the answer, and the error is returned as well as
// recorded so that press can show it. A pass ignores it, having already logged and alerted.
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

	// Recorded against the account as well as logged, so somebody whose key stopped working can
	// be told. The flag stays set, so the next pass tries again.
	limited := errors.Is(err, openrouter.ErrRateLimited)
	a.Log.Warn("could not advise", "principal", id, "limited", limited, "err", err)
	if err := a.Store.RecordAdviceFailure(ctx, id, a.Store.Now(), err.Error(), limited); err != nil {
		a.Log.Error("could not record an advice failure", "principal", id, "err", err)
	}
	if !limited {
		// A quota resolves itself and nothing they could do would help.
		a.tell(ctx, id, state, err)
	}

	// Nothing is throttled and the pass is not abandoned: a quota is per key, so one person's
	// having run out says nothing about the next person's. A rate limit is only worth telling
	// apart from every other failure, since it wants a wait, and the next pass is the wait.
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
		// Asking would spend a request to be told there is nothing to say. Marked answered, or
		// the account is revisited every pass forever.
		return a.Store.RecordAdvised(ctx, principalID, a.Store.Now())
	}

	rh, err := a.Store.Rhythm(ctx, principalID)
	if err != nil {
		return err
	}
	// Instance-wide, unlike the key: how the machine reaches the internet is one fact.
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

	// Stale advice is worth more than none: the *answer* failed, not the question. A deliberate
	// "no shape" still overwrites, so the only thing carried forward is an answer that could not
	// be understood or did not arrive.
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
		// Categories can be readable when the curve is not, so only the curve comes from before.
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

	// No reminder text and nothing said about one, the same rule the nudge log follows.
	a.Log.Info("advised", "principal", principalID, "model", res.Model,
		"reminders", len(reminders), "answered", len(advice), "dropped", dropped,
		"carried", carried,
		// The shapes and not the answers: "7x24" says what to change about the question.
		"misshapen", misshapen, "tokens", res.Tokens)
	return nil
}

// tell lets somebody know their companion has stopped working, at most once per episode.
//
// For the case that makes the feature fail quietly: a key revoked in March, noticed in June.
// Once per episode, because a broken key fails every pass; the flag is lowered by a round that
// succeeds. Not while they are asleep, and not marked told either, so it goes out on the first
// pass after they wake.
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
		// The gateway's own words: a rejected key and a retired model are two different fields.
		sentence(reason.Error()))
	if err != nil {
		a.Log.Error("could not send an alert", "principal", principalID, "err", err)
		return
	}
	if delivered <= 0 {
		// Nothing reached a device, so nobody is marked told.
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
