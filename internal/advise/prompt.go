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
const systemPrompt = `You pick the hours of the week at which somebody's reminders would land well.

btw shows one reminder at a time, at hours nobody chose, a few times a day. Your answer does not decide whether a reminder is shown — it makes a reminder likelier during the hours you name and less likely outside them. There is no due date and nothing is overdue.

Some reminders want announcing a while before the thing they are about, so the person can prepare. Others want to arrive at the moment they could be acted on. Decide which from the reminder and from what you are told about the person.

Answer with a JSON object of the form {"results": [...]}, holding one entry per reminder, in the order given:
- id: the id of the reminder this entry is for, echoed back.
- category: an array of one or more of the following. Many reminders earn more than one.
{{range .Categories}}  - {{.Name}}: {{.Gloss}}
{{end}}- exclusive: true when it wants the person's full attention and nothing else should be scheduled against it, false when it can run alongside something else. Judge it from the reminder and from what the person says about doing things in parallel.
- slots: when it would land well, each {day, start, end}.
  - day is one of {{.Days}}. The week starts on monday.
  - start and end are 24-hour HH:MM. An end at or before its start runs past midnight into the next day.

Be generous with the slots. A reminder the person could pick up on any given day belongs on every one of those days, not on a token two or three of them. Narrow the list only when the reminder is tied to particular days, the way an appointment is, or when the person's own routine rules a day out. A reminder that is not exclusive has even less reason to be scarce, because its hours can overlap other reminders.

Naming no slots at all is a real answer and means there is no hour that suits it better than any other. It does not silence anything.

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

var templates = template.Must(template.New("advise").Parse(
	`{{define "system"}}` + systemPrompt + `{{end}}` +
		`{{define "user"}}` + userPrompt + `{{end}}` +
		`{{define "hoursWindow"}}` + hoursWindow + `{{end}}` +
		`{{define "hoursAnyTime"}}` + hoursAnyTime + `{{end}}`))

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

// days is the week as the companion is asked to name it, Monday first. The index is
// [store.Slot].Day, so this slice is the definition of that origin rather than a restatement
// of it.
var days = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// System is the standing half of the question: what the answer must look like.
//
// Exported, and it takes no arguments, so that reading the exact text a model is sent is a
// call rather than an exercise in following string concatenation.
func System() string {
	return render("system", map[string]any{
		"Categories": Categories,
		"Days":       strings.Join(days, ", "),
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

	return render("user", map[string]any{
		"About":     strings.TrimSpace(about),
		"Hours":     hours(r),
		"Reminders": string(encoded),
	}), nil
}

func hours(r store.Rhythm) string {
	if !r.WindowEnabled {
		return render("hoursAnyTime", map[string]any{"Timezone": r.Timezone})
	}
	return render("hoursWindow", map[string]any{
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
func render(name string, data any) string {
	var b strings.Builder
	templates.ExecuteTemplate(&b, name, data)
	return b.String()
}

func clock(minute int) string {
	return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
}
