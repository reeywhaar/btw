package advise

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"btw/internal/store"
)

// The prompt is the product here, so it is written as prose in one block rather than
// assembled out of string concatenation.
//
// Two consequences worth having. It reads as what the model reads, so a change to it is a
// diff somebody can judge without running anything — and it is a constant, so `go doc` and a
// grep both find the whole of it in one place. Building it with Fprintf hid the argument
// inside the machinery, and the argument is the part that matters.
//
// text/template, never html/template: this is prose going to a model, and HTML escaping would
// silently turn an apostrophe in somebody's description of themselves into `&#39;`.
const systemPrompt = `You say how well each half hour of the day suits each of somebody's reminders.

btw shows one reminder at a time, at hours nobody chose, a few times a day. Your answer does not decide whether a reminder is shown — it makes a reminder likelier during the half hours you score highly and less likely during the ones you score low. There is no due date and nothing is overdue.

Some reminders want raising a while before the thing they are about, so the person can prepare. Others want to arrive at the moment they could be acted on. Decide which from the reminder and from what you are told about the person.

Answer with a JSON object of the form {"results": [...]}, holding one entry per reminder, in the order given:
- id: the id of the reminder this entry is for, echoed back.
- category: an array of one or more of the following. Many reminders earn more than one.
{{range .Categories}}  - {{.Name}}: {{.Gloss}}
{{end}}- exclusive: true when it wants the person's full attention and nothing else should be scheduled against it, false when it can run alongside something else.
- curve: a list of the stretches of the week you have an opinion about. Each is {"days": …, "from": "HH:MM", "to": "HH:MM", "v": a number between 0 and 1}.

The curve is the answer. Everything else is context.

**Say only what you have an opinion about.** Every hour you do not mention counts as 0.5, which means no opinion and changes nothing. There is no need to describe a whole week, and a reminder with no natural hour gets an empty list.

- days is "mon", "mon-fri", "sat,sun", "weekends", or "all". Leaving it out means every day.
- from and to are 24-hour times with any minutes you like: "10:00" to "13:00" says the middle of the morning to the middle of the day. A "to" at or before its "from" runs past midnight into the next day, so "22:00" to "02:00" is four hours of an evening.
- v above 0.5 says this stretch is a better moment than usual; below says worse. 0 does not silence anything and 1 does not guarantee anything — they are the ends of a scale, not switches.
- Where two stretches overlap, the later one wins. Say the broad thing first and narrow it after.

**Almost everything has some shape.** Washing up is worse at four in the morning. Anything needing a shop is worse when shops are shut. Anything involving another person is worse when that person is asleep. Anything that takes an hour of quiet is worse in the middle of a working day. Say that much at least.

Two or three stretches is a good answer. A dozen is describing precision you do not have — a person does not experience 14:30 differently from 15:00.

For example, for "wash dishes" from somebody who sleeps until noon and works weekdays:

    [{"days": "all", "from": "02:00", "to": "13:00", "v": 0.05},
     {"days": "mon-fri", "from": "13:00", "to": "19:00", "v": 0.3},
     {"days": "all", "from": "20:00", "to": "01:00", "v": 0.9}]

And for "buy stamps":

    [{"days": "all", "from": "00:00", "to": "09:00", "v": 0.0},
     {"days": "mon-fri", "from": "09:00", "to": "17:30", "v": 0.8},
     {"days": "sat", "from": "09:00", "to": "13:00", "v": 0.6},
     {"days": "all", "from": "17:30", "to": "24:00", "v": 0.0}]

Reply with the JSON object only, with no prose and no markdown fences around it.`

// userPrompt is the half about one person.
//
// The empty description is said rather than left blank: a model handed an empty section
// invents a person to fill it, and one told the section is empty falls back on the hours,
// which are real.
const userPrompt = `About the person, in their own words:
{{if .About}}{{.About}}{{else}}(they have not written anything about themselves){{end}}

{{.Hours}}

Their reminders, as JSON ({id, text, note?}[]):
{{.Reminders}}`

// hoursWindow and hoursAnyTime say when the person can be reached at all.
//
// Worth stating even when they have written a description of themselves: a slot outside these
// hours can never be delivered in, so a companion that does not know them wastes half its
// answer on windows nothing will ever fire in.
const (
	hoursWindow  = `They are reachable between {{.Wake}} and {{.Sleep}}, in {{.Timezone}}, and never outside those hours. All times below are their local time.`
	hoursAnyTime = `They are reachable at any hour, in {{.Timezone}}. All times below are their local time.`
)

// One template each, rather than four `{{define}}` blocks concatenated into one source.
//
// Nothing was ever injectable here — every piece is a compile-time constant, and the person's
// own words reach the template as *data*, which text/template never re-parses. Somebody whose
// description of themselves contains `{{end}}` gets those five characters in the prompt, and a
// test below holds that.
//
// The concatenation went anyway, because it was fragile for a duller reason. Both of these
// constants already contain a balanced `{{end}}` of their own, so wrapping each in a
// `{{define}}` meant the outer block was closed by whichever `{{end}}` the parser reached
// first. It happened to be the right one. Editing the prose near an `{{if}}` could have made
// it the wrong one, and the failure — a template that parses into something nobody wrote —
// is one nobody would look for in a paragraph of English.
var (
	systemTemplate       = template.Must(template.New("system").Parse(systemPrompt))
	userTemplate         = template.Must(template.New("user").Parse(userPrompt))
	hoursWindowTemplate  = template.Must(template.New("hoursWindow").Parse(hoursWindow))
	hoursAnyTimeTemplate = template.Must(template.New("hoursAnyTime").Parse(hoursAnyTime))
)

// Categories are what the companion may call a reminder, each with the gloss it is given.
//
// Glossed rather than a bare list, because the distinctions that matter here are the ones a
// bare word loses. "Errands" is bound by opening hours and "chores" is bound by nothing but
// being awake — which is a scheduling fact, and the whole reason the two are separate words
// rather than one. A model handed sixteen unexplained nouns guesses; handed sixteen sentences
// it does not have to.
var Categories = []struct{ Name, Gloss string }{
	{"work", "the job and whatever it demands"},
	{"personal", "the person's own business, where nothing more specific fits"},
	{"family", "partner, children, parents, relatives"},
	{"social", "friends, meeting people, calls and replies that are not work"},
	{"health", "appointments, medication, anything medical"},
	{"fitness", "exercise and sport"},
	{"finance", "bills, payments, budgeting, taxes"},
	{"admin", "paperwork, renewals, bureaucracy, accounts"},
	{"shopping", "buying things, in a shop or online"},
	{"errands", "short tasks that have to happen out of the house, and so are bound by opening hours"},
	{"chores", "housework and maintenance at home, bound by nothing but being awake"},
	{"learning", "study, courses, reading to learn something"},
	{"creative", "making things, writing, music, art"},
	{"entertainment", "watching, playing, listening, going out for fun"},
	{"travel", "trips, packing, bookings, getting somewhere"},
	{"pets", "feeding, walking, the vet"},
}

// System is the standing half of the question: what the answer must look like.
//
// Exported, and it takes no arguments, so that reading the exact text a model is sent is a
// call rather than an exercise in following string concatenation.
func System() string {
	return render(systemTemplate, map[string]any{
		"Categories": Categories,
		"Days":       store.Days,
		"Windows":    store.Windows,
	})
}

// User is the half about this person: who they are, when they are awake, and what they wrote
// down.
func User(about string, r store.Rhythm, reminders []store.Reminder) (string, error) {
	type line struct {
		ID   string `json:"id"`
		Text string `json:"text"`
		Note string `json:"note,omitempty"`
	}
	lines := make([]line, 0, len(reminders))
	for _, rem := range reminders {
		lines = append(lines, line{ID: rem.ID, Text: rem.Text, Note: rem.Note})
	}
	encoded, err := json.Marshal(lines)
	if err != nil {
		return "", fmt.Errorf("encode reminders: %w", err)
	}

	return render(userTemplate, map[string]any{
		"About":     strings.TrimSpace(about),
		"Hours":     hours(r),
		"Reminders": string(encoded),
	}), nil
}

func hours(r store.Rhythm) string {
	if !r.WindowEnabled {
		return render(hoursAnyTimeTemplate, map[string]any{"Timezone": r.Timezone})
	}
	return render(hoursWindowTemplate, map[string]any{
		"Wake":     clock(r.WakeMinute),
		"Sleep":    clock(r.SleepMinute),
		"Timezone": r.Timezone,
	})
}

// render executes one of the templates above.
//
// The error is dropped rather than returned, and it is the one place here that is allowed to:
// these templates are constants parsed at startup by template.Must, so the only failure left
// is a write to a strings.Builder, which does not fail. Threading an impossible error through
// every caller would cost more than it says.
func render(t *template.Template, data any) string {
	var b strings.Builder
	t.Execute(&b, data)
	return b.String()
}

func clock(minute int) string {
	return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
}
