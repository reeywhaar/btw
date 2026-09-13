package advise

import (
	"context"
	"errors"

	"btw/internal/gateway"
	"btw/internal/proxy"
	"btw/internal/store"
)

// The question is put with a set made up here rather than with the account's own.
//
// A test press has to work before anything has been written down, and it must not depend on
// what somebody happens to have on their list — the answer to "does this model work" would
// otherwise change with their reminders. These four are chosen to need every part of the
// answer: one bound to a clock, one to opening hours, one to being awake, and one that suits
// most of a week.
var (
	tryAbout = "I sleep until about midnight and get up at eight. " +
		"I work weekdays from ten, and I would rather do anything domestic in the evening. " +
		"I can only do one thing at a time."

	tryRhythm = store.Rhythm{
		Timezone:      "UTC",
		WindowEnabled: true,
		WakeMinute:    8 * 60,
		SleepMinute:   24 * 60,
		Budget:        6,
	}

	tryReminders = []store.Reminder{
		{ID: "rem_probe_medication", Text: "take the tablets", Note: "with breakfast"},
		{ID: "rem_probe_bins", Text: "put the bins out", Note: "collected on Wednesday morning"},
		{ID: "rem_probe_stamps", Text: "buy stamps"},
		{ID: "rem_probe_stretch", Text: "stretch for ten minutes"},
	}
)

// Trial is how a test press went.
type Trial struct {
	// Model is what actually served it, which is not always what was asked for.
	Model  string
	Tokens int

	// Asked and Answered are the made-up reminders put to it, and how many came back with a
	// curve the weighting could read. They are the whole point of the press: a model can say
	// "ok" and still answer this in a shape nothing can use.
	Asked    int
	Answered int

	// Shapes names what arrived instead, for the ones that could not be read.
	Shapes []string
}

// Try puts the real question to a model and reads the answer back, so a press proves the thing
// the companion actually does.
//
// A one-word completion proves a key and a slug and nothing else. The model that fails here is
// the one that answers cheerfully and cannot hold a JSON object together, or writes a week in
// a shape the parser refuses — which is invisible until a loop has been quietly producing
// nothing for a week.
//
// Nothing is stored. The press reconciles unsaved form values, so an answer earned under
// settings nobody has saved has no row it belongs to.
func Try(ctx context.Context, set gateway.Settings, via proxy.Settings) (Trial, error) {
	if !set.Configured() {
		return Trial{}, errors.New("there is no companion to ask")
	}

	question, err := User(tryAbout, tryRhythm, tryReminders)
	if err != nil {
		return Trial{}, err
	}

	reply, res, err := gateway.Ask(ctx, set, via, System(), question, budget(len(tryReminders)))
	if err != nil {
		return Trial{}, err
	}

	known := make(map[string]bool, len(tryReminders))
	for _, r := range tryReminders {
		known[r.ID] = true
	}
	advice, _, misshapen := parse(reply, known)

	trial := Trial{
		Model:  res.Model,
		Tokens: res.Tokens,
		Asked:  len(tryReminders),
		Shapes: misshapen,
	}
	for _, a := range advice {
		if a.Curve.Valid() {
			trial.Answered++
		}
	}
	return trial, nil
}
