package advise

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"btw/internal/store"
)

// The prompt is the product here, so it is one block of prose rather than something assembled
// by Fprintf: it reads as what the model reads, and a change to it is a diff somebody can judge
// without running anything.
//
// text/template, never html/template — HTML escaping would turn an apostrophe in somebody's
// description of themselves into `&#39;`.
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

**Most of a week should go unmentioned.** Every hour you leave out counts as 0.5 — no opinion, and no change to anything. One or two stretches is a normal answer and a reminder with no natural hour gets an empty list. You are not describing a week; you are naming the exceptions to it.

- days is "mon", "mon-fri", "sat,sun", "weekends", or "all". Leaving it out means every day.
- from and to are 24-hour times with any minutes you like: "10:00" to "13:00" says the middle of the morning to the middle of the day. A "to" at or before its "from" runs past midnight into the next day, so "22:00" to "02:00" is four hours of an evening.
- v is how timely the reminder is in that stretch. 0.5 is no opinion, 0.7 is a timely moment, 0.3 is an untimely one, and 1 is the best you can say for it — which guarantees nothing.
- **0 is very untimely, and it is the one number that is a switch rather than a point on the scale: the reminder is not raised in those hours at all.** Use it where being reminded would be no use whatsoever — while they are asleep, or where the hours belong to the world and the world is shut. Anything you would still take grudgingly is 0.1.
- Where two stretches overlap, the later one wins. Say the broad thing first and narrow it after.

**A low number means "raising it then would be a waste", not "the thing cannot be done then".** A reminder is a prompt to think about something, not an order to do it that instant. Somebody reminded at lunchtime about an evening out can act on it — buy the tickets, tell the other person, decide not to go. Mark an hour down when being reminded then would be untimely, and mark it 0 only when you would rather they were not reminded at all.

**Almost everything has some shape.** Washing up is worse at four in the morning. Anything needing a shop is worse when shops are shut. Anything involving another person is worse when that person is asleep. Say that much at least.

For "wash dishes", from somebody who works weekdays and stays up late:

    [{"days": "all", "from": "20:00", "to": "01:00", "v": 0.8}]

One line, because there is one thing worth saying: after the evening meal is when it gets done. Every other hour is left alone rather than scored, including the ones they are asleep for — those are already handled elsewhere.

For "buy stamps", where the hours belong to the world rather than to the person:

    [{"days": "mon-fri", "from": "09:00", "to": "17:00", "v": 0.8},
     {"days": "all", "from": "01:00", "to": "07:00", "v": 0.1}]

The second line is 0.1 rather than 0: five in the morning is untimely rather than useless, and somebody awake then could still write a note to themselves. The evenings and the weekend are left at 0.5, because a reminder to buy stamps is still worth having then even though the shop is shut — it is something to plan, not only something to do.

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

// One template each, rather than `{{define}}` blocks concatenated into one source: these
// constants contain balanced `{{end}}`s of their own, so an outer block would be closed by
// whichever the parser reached first, and a template that parses into something nobody wrote is
// not a failure anybody would look for in a paragraph of English.
//
// Somebody's own words reach a template as data, which text/template never re-parses.
var (
	systemTemplate       = template.Must(template.New("system").Parse(systemPrompt))
	userTemplate         = template.Must(template.New("user").Parse(userPrompt))
	hoursWindowTemplate  = template.Must(template.New("hoursWindow").Parse(hoursWindow))
	hoursAnyTimeTemplate = template.Must(template.New("hoursAnyTime").Parse(hoursAnyTime))
)

// Categories are what the companion may call a reminder, each with the gloss it is given.
//
// Glossed because a bare word loses the distinction that matters: "errands" is bound by opening
// hours and "chores" by nothing but being awake, which is the scheduling fact that makes them
// two words rather than one.
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
