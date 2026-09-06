package advise

import (
	"fmt"
	"strings"
	"testing"

	"btw/internal/store"
)

func known(ids ...string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// curve is a JSON week of the right shape, saying the same thing about every half hour.
func curve(v float64) string {
	half := make([]string, store.Windows)
	for i := range half {
		half[i] = fmt.Sprint(v)
	}
	day := "[" + strings.Join(half, ",") + "]"
	days := make([]string, store.Days)
	for d := range days {
		days[d] = day
	}
	return "[" + strings.Join(days, ",") + "]"
}

// aDay is one day of the right length, for building a week that is wrong in one place.
func aDay(v float64) string {
	half := make([]string, store.Windows)
	for i := range half {
		half[i] = fmt.Sprint(v)
	}
	return "[" + strings.Join(half, ",") + "]"
}

// A free model in JSON mode is asked for an object and returns one most of the time. Each of
// these is a shape it actually reaches for, and each is one line to accept and an evening to
// diagnose from a parse error.
func TestTheAnswerIsFoundInWhateverShapeItArrives(t *testing.T) {
	entry := `{"id":"r_1","category":["chores"],"exclusive":false,"curve":` + curve(0.7) + `}`

	for _, tc := range []struct{ name, reply string }{
		{"clean", `{"results":[` + entry + `]}`},
		{"fenced", "```json\n{\"results\":[" + entry + "]}\n```"},
		{"fenced without a language", "```\n{\"results\":[" + entry + "]}\n```"},
		{"prefaced with a sentence", "Sure, here you go:\n{\"results\":[" + entry + "]}"},
		{"a bare array", `[` + entry + `]`},
		{"a bare array with prose around it", "Here:\n[" + entry + "]\nHope that helps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := parse(tc.reply, known("r_1"))
			a, ok := got["r_1"]
			if !ok {
				t.Fatalf("parse() found nothing in %q", tc.reply)
			}
			if v, ok := a.Curve.At(0, 0); !ok || v != 0.7 {
				t.Errorf("curve = %+v, want a readable one", a.Curve)
			}
		})
	}
}

// Nothing a model says may attach advice to a row it was not asked about. An id it invented, or
// echoed back wrongly, is the one mistake here that could reach somebody else's reminder.
func TestAnUnknownIdIsDropped(t *testing.T) {
	reply := `{"results":[
		{"id":"r_mine","curve":` + curve(0.6) + `},
		{"id":"r_theirs","curve":` + curve(0.6) + `},
		{"id":"","curve":[]}
	]}`

	got, dropped, _ := parse(reply, known("r_mine"))
	if _, ok := got["r_theirs"]; ok {
		t.Error("advice was attached to a reminder that was never asked about")
	}
	if len(got) != 1 {
		t.Errorf("parse() kept %d entries, want only the one asked about", len(got))
	}
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2", dropped)
	}
}

// A model asked for seven arrays of forty-eight reliably sends something else, and most of
// those somethings mean exactly one thing. Refusing them was the bug: the screen said "not in a
// shape that could be read" for answers that were perfectly readable.
func TestTheShapesAModelActuallySendsAreRead(t *testing.T) {
	hourly := func(v float64) string {
		parts := make([]string, store.Windows/2)
		for i := range parts {
			parts[i] = fmt.Sprint(v)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	repeat := func(day string, n int) string {
		days := make([]string, n)
		for i := range days {
			days[i] = day
		}
		return "[" + strings.Join(days, ",") + "]"
	}
	flat := func(v float64, n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprint(v)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}

	for _, tc := range []struct{ name, body string }{
		{"seven days of forty-eight, as asked", curve(0.7)},
		{"seven days by the hour", repeat(hourly(0.7), store.Days)},
		{"one day of forty-eight", repeat(aDay(0.7), 1)},
		{"one day by the hour", repeat(hourly(0.7), 1)},
		{"the whole week flat", flat(0.7, store.Days*store.Windows)},
		{"one day flat", flat(0.7, store.Windows)},
		{"one day flat by the hour", flat(0.7, store.Windows/2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, misshapen := parse(`{"results":[{"id":"r_1","curve":`+tc.body+`}]}`, known("r_1"))
			c := got["r_1"].Curve
			if !c.Valid() {
				t.Fatalf("curve = %+v, misshapen %v, want it read", c, misshapen)
			}
			// Every cell, because an expansion that lands values on the wrong hour is the
			// failure worth catching and a spot check would miss it.
			for d := range store.Days {
				for i := range store.Windows {
					if v, _ := c.At(d, i*store.WindowMinutes); v != 0.7 {
						t.Fatalf("day %d, window %d = %v, want 0.7", d, i, v)
					}
				}
			}
		})
	}
}

// The shape the prompt itself invites: "one for each day of the week" reads as an object keyed
// by day at least as naturally as an array, and refusing it was refusing a better answer than
// the one asked for.
func TestAWeekKeyedByDayNameIsRead(t *testing.T) {
	full := make([]string, store.Days)
	abbrev := make([]string, store.Days)
	for i, name := range dayNames {
		full[i] = fmt.Sprintf("%q:%s", name, aDay(0.7))
		abbrev[i] = fmt.Sprintf("%q:%s", name[:3], aDay(0.7))
	}

	for _, tc := range []struct{ name, body string }{
		{"full names", "{" + strings.Join(full, ",") + "}"},
		{"abbreviated", "{" + strings.Join(abbrev, ",") + "}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, misshapen := parse(`{"results":[{"id":"r_1","curve":`+tc.body+`}]}`, known("r_1"))
			if !got["r_1"].Curve.Valid() {
				t.Fatalf("curve = %+v, misshapen %v, want it read", got["r_1"].Curve, misshapen)
			}
			for d := range store.Days {
				if v, _ := got["r_1"].Curve.At(d, 0); v != 0.7 {
					t.Errorf("day %d = %v, want 0.7", d, v)
				}
			}
		})
	}

	// Six named days is one left out rather than a six-day week, and filling the gap would be
	// inventing an opinion the model did not offer.
	six := "{" + strings.Join(full[:store.Days-1], ",") + "}"
	got, _, misshapen := parse(`{"results":[{"id":"r_1","curve":`+six+`}]}`, known("r_1"))
	if got["r_1"].Curve.Valid() {
		t.Error("a week missing a day was filled in rather than refused")
	}
	if len(misshapen) != 1 || misshapen[0] != "obj:6" {
		t.Errorf("misshapen = %v, want [obj:6]", misshapen)
	}
}

// "0.5" is the same answer as 0.5. Refusing it would be refusing on a technicality.
func TestNumbersWrittenAsStringsAreRead(t *testing.T) {
	parts := make([]string, store.Windows)
	for i := range parts {
		parts[i] = `"0.7"`
	}
	body := "[" + strings.Join(parts, ",") + "]"

	got, _, misshapen := parse(`{"results":[{"id":"r_1","curve":`+body+`}]}`, known("r_1"))
	if !got["r_1"].Curve.Valid() {
		t.Fatalf("curve = %+v, misshapen %v, want it read", got["r_1"].Curve, misshapen)
	}
	if v, _ := got["r_1"].Curve.At(0, 0); v != 0.7 {
		t.Errorf("value = %v, want 0.7", v)
	}
}

// The screen says "not in a shape that could be read", and has to be able to say which shape.
func TestARefusedCurveKeepsTheShapeItArrivedIn(t *testing.T) {
	sixDays := "[" + strings.Repeat(aDay(0.5)+",", store.Days-2) + aDay(0.5) + "]"
	got, _, _ := parse(`{"results":[{"id":"r_1","curve":`+sixDays+`}]}`, known("r_1"))

	if got["r_1"].Shape != "6x48" {
		t.Errorf("shape = %q, want it kept for the screen to show", got["r_1"].Shape)
	}

	// And a curve that was read carries no shape, so nothing renders a complaint about one
	// that worked.
	good, _, _ := parse(`{"results":[{"id":"r_1","curve":`+curve(0.5)+`}]}`, known("r_1"))
	if good["r_1"].Shape != "" {
		t.Errorf("shape = %q, want it empty for a curve that was read", good["r_1"].Shape)
	}
}

// An hourly value covers both of its half hours. That is what hourly means, and it is the one
// expansion here that could silently land values on the wrong hour.
func TestAnHourlyCurveIsSpreadAcrossBothHalfHours(t *testing.T) {
	parts := make([]string, store.Windows/2)
	for i := range parts {
		parts[i] = fmt.Sprint(float64(i) / 100)
	}
	body := "[" + strings.Join(parts, ",") + "]"

	got, _, _ := parse(`{"results":[{"id":"r_1","curve":`+body+`}]}`, known("r_1"))
	c := got["r_1"].Curve
	if !c.Valid() {
		t.Fatalf("curve = %+v, want it read", c)
	}
	for hour := range store.Windows / 2 {
		want := float64(hour) / 100
		for _, minute := range []int{hour * 60, hour*60 + 30} {
			if v, _ := c.At(0, minute); v != want {
				t.Errorf("minute %d = %v, want %v", minute, v, want)
			}
		}
	}
}

// What is still refused, and rightly: the values after the mistake belong to hours nobody can
// identify, and a curve confidently wrong about which hour is which is worse than none.
func TestARaggedCurveIsStillRefusedAndNamed(t *testing.T) {
	sixDays := "[" + strings.Repeat(aDay(0.5)+",", store.Days-2) + aDay(0.5) + "]"
	shortDay := "[" + strings.Repeat(aDay(0.5)+",", store.Days-1) + "[0.5,0.5]]"

	for _, tc := range []struct{ name, body, shape string }{
		{"six days", sixDays, "6x48"},
		{"one short day among seven", shortDay, "7x2"},
		{"a flat list of no known length", "[0.5,0.5,0.5]", "3"},
		{"not numbers", `["morning","evening"]`, "list:2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, misshapen := parse(`{"results":[{"id":"r_1","curve":`+tc.body+`}]}`, known("r_1"))
			a, ok := got["r_1"]
			if !ok {
				// Kept as an entry either way: the reminder was answered for, and simply
				// weighs what it did before.
				t.Fatal("the entry was dropped along with its unreadable curve")
			}
			if a.Curve.Valid() {
				t.Errorf("curve = %+v, want it refused rather than repaired", a.Curve)
			}
			// The shape is what says whether the prompt or the parser wants changing, and it
			// says nothing about anybody's reminders.
			if len(misshapen) != 1 || misshapen[0] != tc.shape {
				t.Errorf("misshapen = %v, want [%s]", misshapen, tc.shape)
			}
		})
	}
}

// A model asked for 0 to 1 answers inside it nearly always, and the odd 1.2 plainly meant the
// top of the scale.
func TestValuesOutsideTheScaleAreClamped(t *testing.T) {
	odd := "[" + strings.Repeat("0.5,", store.Windows-2) + "1.4,-0.2]"
	body := "[" + strings.Repeat(aDay(0.5)+",", store.Days-1) + odd + "]"
	got, _, _ := parse(`{"results":[{"id":"r_1","curve":`+body+`}]}`, known("r_1"))

	c := got["r_1"].Curve
	if !c.Valid() {
		t.Fatalf("curve = %+v, want it kept", c)
	}
	last := c[store.Days-1]
	if last[store.Windows-2] != 1 || last[store.Windows-1] != 0 {
		t.Errorf("ends = %v, %v, want them clamped to 1 and 0", last[store.Windows-2], last[store.Windows-1])
	}
}

// A category the taxonomy does not hold is dropped rather than stored, so that nothing
// downstream has to defend against a word the model made up.
func TestAnInventedCategoryIsDropped(t *testing.T) {
	reply := `{"results":[{"id":"r_1","category":["Chores","vibes","errands"],"curve":` + curve(0.5) + `}]}`

	got, _, _ := parse(reply, known("r_1"))
	if fmt.Sprint(got["r_1"].Categories) != "[chores errands]" {
		t.Errorf("categories = %v, want the two that exist, lowercased", got["r_1"].Categories)
	}
}

// Answered-with-no-opinion and never-answered-for are different states, and the weighting
// treats them the same only by coincidence. The map is what carries the difference.
func TestAnEntryWithAFlatCurveIsStillAnAnswer(t *testing.T) {
	reply := `{"results":[{"id":"r_1","curve":` + curve(0.5) + `},{"id":"r_2","curve":` + curve(0.5) + `}]}`
	got, _, _ := parse(reply, known("r_1", "r_2", "r_3"))

	if _, ok := got["r_1"]; !ok {
		t.Fatal("an entry with a flat curve was not kept")
	}
	if _, ok := got["r_3"]; ok {
		t.Error("a reminder the model said nothing about was given advice")
	}
}

func TestRubbishIsNoAdviceRatherThanAPanic(t *testing.T) {
	for _, reply := range []string{"", "I'm sorry, I can't help with that.", "{", "null", "[]"} {
		got, _, _ := parse(reply, known("r_1"))
		if len(got) != 0 {
			t.Errorf("parse(%q) = %v, want nothing", reply, got)
		}
	}
}

// The second opinion is discarded rather than merged. Choosing between two answers about one
// reminder is a decision with nothing to base it on.
func TestASecondOpinionAboutOneReminderIsIgnored(t *testing.T) {
	reply := `{"results":[
		{"id":"r_1","curve":` + curve(0.9) + `},
		{"id":"r_1","curve":` + curve(0.1) + `}
	]}`

	got, dropped, _ := parse(reply, known("r_1"))
	if v, _ := got["r_1"].Curve.At(0, 0); v != 0.9 {
		t.Errorf("curve = %v, want the first answer only", v)
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
}

// The prompt is the product here, so the things it must not stop saying are worth asserting.
func TestTheQuestionSaysWhatTheAnswerCannotDo(t *testing.T) {
	q := System()
	for _, must := range []string{
		// Without this the model reads the job as "when is this due", which is the one
		// question btw exists to refuse.
		"does not decide whether a reminder is shown",
		// Without this a model takes the cheapest path and scores every half hour 0.5, which
		// is data-shaped and says nothing — and costs the same 336 numbers as an opinion.
		"Almost everything has some shape",
		// A worked example is the single most effective thing in here: it is the difference
		// between a model knowing what a shape looks like and guessing at one.
		"For \"wash dishes\"",
		// Sparseness is the whole point of the format. Without it a model describes all
		// twenty-four hours out of politeness, at the ends of the scale, and the week comes back
		// decisive about hours it was never asked to have an opinion on.
		"Most of a week should go unmentioned",
		// The rule that lets a model write a broad stroke and then narrow it.
		"the later one wins",
		// What separates a low score from silence, and the thing the first examples taught
		// wrongly: being reminded at a useless hour is the cost, not being unable to act.
		"raising it then would be a waste",
		// Arbitrary edges, which is the requirement that ruled out fixed buckets.
		"the middle of the morning to the middle of the day",
		// The distinction that carries actual scheduling meaning.
		"bound by opening hours",
	} {
		if !strings.Contains(q, must) {
			t.Errorf("the question no longer says %q", must)
		}
	}
	for _, c := range Categories {
		if !strings.Contains(q, "  - "+c.Name+": ") {
			t.Errorf("the question does not gloss %q", c.Name)
		}
	}
}

// The failure that would otherwise be invisible: one reminder answered with the wrong type must
// not cost the other thirty-nine. A typed struct fails the whole document over one field, so a
// round covering a full list would come back empty and look like a model that said nothing.
func TestOneMalformedEntryDoesNotCostTheRound(t *testing.T) {
	reply := `{"results":[
		{"id":"r_1","curve":` + curve(0.9) + `},
		{"id":"r_2","curve":["morning","evening"],"category":"chores","exclusive":"yes"},
		"this is not an entry at all",
		{"id":"r_3","curve":` + curve(0.3) + `}
	]}`

	got, _, _ := parse(reply, known("r_1", "r_2", "r_3"))

	for _, id := range []string{"r_1", "r_3"} {
		if _, ok := got[id]; !ok {
			t.Errorf("%s was lost to another entry's bad field", id)
		}
	}
	if v, _ := got["r_1"].Curve.At(0, 0); v != 0.9 {
		t.Errorf("r_1 curve = %v, want it read normally", v)
	}

	// The one with wrong types is still an entry — it was answered for — and simply carries
	// nothing readable.
	a, ok := got["r_2"]
	if !ok {
		t.Fatal("an entry with unreadable fields was dropped entirely")
	}
	if a.Curve.Valid() || a.Exclusive || len(a.Categories) != 0 {
		t.Errorf("r_2 = %+v, want its unreadable fields treated as absent", a)
	}
}

// The mistake a model actually makes, seen in the wild as "7x49": one value too many in every
// day. Refusing it threw away an answer that was right about every hour but one.
func TestADayThatIsOneValueOutIsFittedRatherThanRefused(t *testing.T) {
	long := "[" + strings.Repeat("0.7,", store.Windows) + "0.7]"    // 49
	short := "[" + strings.Repeat("0.7,", store.Windows-2) + "0.7]" // 47
	longHour := "[" + strings.Repeat("0.7,", store.Windows/2) + "0.7]"

	for _, tc := range []struct{ name, day string }{
		{"one too many", long},
		{"one too few", short},
		{"one too many, by the hour", longHour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "[" + strings.Repeat(tc.day+",", store.Days-1) + tc.day + "]"
			got, _, misshapen := parse(`{"results":[{"id":"r_1","curve":`+body+`}]}`, known("r_1"))
			c := got["r_1"].Curve
			if !c.Valid() {
				t.Fatalf("curve = %+v, misshapen %v, want it fitted", c, misshapen)
			}
			for d := range store.Days {
				for i := range store.Windows {
					if v, _ := c.At(d, i*store.WindowMinutes); v != 0.7 {
						t.Fatalf("day %d, window %d = %v, want 0.7", d, i, v)
					}
				}
			}
		})
	}

	// Strictly one. Two out is no longer a slip, and past that the values land on hours
	// nobody can identify — which is the thing worth refusing.
	twoOut := "[" + strings.Repeat("0.7,", store.Windows+1) + "0.7]" // 50
	body := "[" + strings.Repeat(twoOut+",", store.Days-1) + twoOut + "]"
	got, _, misshapen := parse(`{"results":[{"id":"r_1","curve":`+body+`}]}`, known("r_1"))
	if got["r_1"].Curve.Valid() {
		t.Error("a day two values out was fitted, which lands values on hours nobody can name")
	}
	if len(misshapen) != 1 || misshapen[0] != "7x50" {
		t.Errorf("misshapen = %v, want [7x50]", misshapen)
	}
}

// The person's own words reach the template as data, and text/template never re-parses a value.
// Somebody whose description of themselves contains template syntax gets those characters in
// the prompt, not a template that does something.
//
// Worth a test rather than a comment: it is a property of how the prompt is *built*, and a
// refactor that started concatenating the description into the source instead of passing it
// would look tidier and be a hole.
func TestWhatSomebodyWritesAboutThemselvesIsNeverATemplate(t *testing.T) {
	hostile := `{{end}}{{define "system"}}HIJACKED{{end}}{{.Secret}}{{template "system"}}`

	got, err := User(hostile, store.Rhythm{Timezone: "UTC"}, []store.Reminder{
		{ID: "r_1", Text: `{{end}} and a "quote"`},
	})
	if err != nil {
		t.Fatalf("User(): %v", err)
	}
	if !strings.Contains(got, hostile) {
		t.Errorf("the description was not carried through literally:\n%s", got)
	}
	// The proof that nothing executed. `.Secret` is not a field of anything here, so an
	// executed `{{.Secret}}` renders as "<no value>" — its surviving verbatim is the
	// difference between data being printed and data being run.
	if strings.Contains(got, "<no value>") {
		t.Error("template syntax in the description was executed")
	}

	// And the standing half is untouched by any of it.
	if !strings.Contains(System(), "Most of a week should go unmentioned") {
		t.Error("the system prompt was affected by what somebody wrote about themselves")
	}

	// Reminder text goes in as JSON, so a quote or a newline cannot end the block it sits in.
	if !strings.Contains(got, `{{end}} and a \"quote\"`) {
		t.Errorf("reminder text was not escaped into its JSON block:\n%s", got)
	}
}
