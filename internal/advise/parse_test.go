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

// A free model in JSON mode is asked for an object and returns one most of the time. Each of
// these is a shape it actually reaches for, and each is one line to accept and an evening to
// diagnose from a parse error.
func TestTheAnswerIsFoundInWhateverShapeItArrives(t *testing.T) {
	entry := `{"id":"r_1","category":["chores"],"exclusive":false,"slots":[{"day":"sat","start":"10:00","end":"14:00"}]}`

	for _, tc := range []struct{ name, reply string }{
		{"clean", `{"results":[` + entry + `]}`},
		{"fenced", "```json\n{\"results\":[" + entry + "]}\n```"},
		{"fenced without a language", "```\n{\"results\":[" + entry + "]}\n```"},
		{"prefaced with a sentence", "Sure, here you go:\n{\"results\":[" + entry + "]}"},
		{"a bare array", `[` + entry + `]`},
		{"a bare array with prose around it", "Here:\n[" + entry + "]\nHope that helps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := parse(tc.reply, known("r_1"))
			a, ok := got["r_1"]
			if !ok {
				t.Fatalf("parse() found nothing in %q", tc.reply)
			}
			if len(a.Slots) != 1 || a.Slots[0] != (store.Slot{Day: 5, Start: 600, End: 840}) {
				t.Errorf("slots = %+v, want one Saturday window", a.Slots)
			}
		})
	}
}

// Nothing a model says may attach advice to a row it was not asked about. An id it invented,
// or echoed back wrongly, is the one mistake here that could reach somebody else's reminder.
func TestAnUnknownIdIsDropped(t *testing.T) {
	reply := `{"results":[
		{"id":"r_mine","slots":[{"day":"mon","start":"09:00","end":"10:00"}]},
		{"id":"r_theirs","slots":[{"day":"mon","start":"09:00","end":"10:00"}]},
		{"id":"","slots":[]}
	]}`

	got, dropped := parse(reply, known("r_mine"))
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

// Refusing a whole answer over one malformed slot would throw away nineteen good ones and
// leave the account no better off than before it configured a key.
func TestOneBadSlotDoesNotCostTheGoodOnes(t *testing.T) {
	reply := `{"results":[{"id":"r_1","slots":[
		{"day":"mon","start":"09:00","end":"11:00"},
		{"day":"funday","start":"09:00","end":"11:00"},
		{"day":"tue","start":"25:00","end":"11:00"},
		{"day":"wed","start":"09:00","end":"9:70"},
		{"day":"thu","start":"nine","end":"11:00"},
		{"day":"fri","start":"09:00","end":"11:00"}
	]}]}`

	got, _ := parse(reply, known("r_1"))
	if len(got["r_1"].Slots) != 2 {
		t.Errorf("slots = %+v, want the two that were readable", got["r_1"].Slots)
	}
}

// "Monday" and "mon" are the same answer, and 24:00 is what a model reaches for to say "until
// midnight". Refusing either would drop a slot on a technicality.
func TestDaysAndTimesAreReadForgivingly(t *testing.T) {
	reply := `{"results":[{"id":"r_1","slots":[
		{"day":"Monday","start":"09:00","end":"24:00"},
		{"day":" SUN ","start":" 07:05 ","end":"08:00"}
	]}]}`

	got, _ := parse(reply, known("r_1"))
	want := []store.Slot{{Day: 0, Start: 540, End: 1440}, {Day: 6, Start: 425, End: 480}}
	if fmt.Sprint(got["r_1"].Slots) != fmt.Sprint(want) {
		t.Errorf("slots = %+v, want %+v", got["r_1"].Slots, want)
	}
}

// A category the taxonomy does not hold is dropped rather than stored, so that nothing
// downstream has to defend against a word the model made up.
func TestAnInventedCategoryIsDropped(t *testing.T) {
	reply := `{"results":[{"id":"r_1","category":["Chores","vibes","errands"],"slots":[]}]}`

	got, _ := parse(reply, known("r_1"))
	if fmt.Sprint(got["r_1"].Categories) != "[chores errands]" {
		t.Errorf("categories = %v, want the two that exist, lowercased", got["r_1"].Categories)
	}
}

// Answered-with-no-slots and never-answered-for are different states, and the weighting treats
// them oppositely. The map is what carries the difference.
func TestAnEntryWithNoSlotsIsStillAnAnswer(t *testing.T) {
	got, _ := parse(`{"results":[{"id":"r_1","slots":[]},{"id":"r_2","slots":[]}]}`, known("r_1", "r_2", "r_3"))

	a, ok := got["r_1"]
	if !ok {
		t.Fatal("an entry with no slots was not kept")
	}
	if a.Slots == nil {
		t.Error("slots is nil, which cannot be told apart from never having been asked")
	}
	if _, ok := got["r_3"]; ok {
		t.Error("a reminder the model said nothing about was given advice")
	}
}

func TestRubbishIsNoAdviceRatherThanAPanic(t *testing.T) {
	for _, reply := range []string{"", "I'm sorry, I can't help with that.", "{", "null", "[]"} {
		got, _ := parse(reply, known("r_1"))
		if len(got) != 0 {
			t.Errorf("parse(%q) = %v, want nothing", reply, got)
		}
	}
}

// The second opinion is discarded rather than merged. Choosing between two answers about one
// reminder is a decision with nothing to base it on.
func TestASecondOpinionAboutOneReminderIsIgnored(t *testing.T) {
	reply := `{"results":[
		{"id":"r_1","slots":[{"day":"mon","start":"09:00","end":"10:00"}]},
		{"id":"r_1","slots":[{"day":"fri","start":"20:00","end":"22:00"}]}
	]}`

	got, dropped := parse(reply, known("r_1"))
	if len(got["r_1"].Slots) != 1 || got["r_1"].Slots[0].Day != 0 {
		t.Errorf("slots = %+v, want the first answer only", got["r_1"].Slots)
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
		// The complaint that started this: two or three slots for something wanted daily.
		"Be generous with the slots",
		// So a model that finds no good hour says so instead of inventing one.
		"Naming no slots at all is a real answer",
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
