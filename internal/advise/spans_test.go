package advise

import (
	"fmt"
	"strings"
	"testing"

	"btw/internal/store"
)

// spans wraps a list of spans as one reminder's answer.
func spans(body string) string {
	return `{"results":[{"id":"r_1","curve":` + body + `}]}`
}

// The requirement fixed buckets could not meet: arbitrary edges, said in the model's own words.
func TestASpanPaintsExactlyTheHoursItNames(t *testing.T) {
	got, _, misshapen := parse(spans(
		`[{"days":"all","from":"10:00","to":"13:00","v":0.9}]`), known("r_1"))
	c := got["r_1"].Curve
	if !c.Valid() {
		t.Fatalf("curve = %+v, misshapen %v, want it read", c, misshapen)
	}

	for d := range store.Days {
		for _, tc := range []struct {
			name   string
			minute int
			want   float64
		}{
			{"just before", 9*60 + 30, neutral},
			{"the first half hour", 10 * 60, 0.9},
			{"the middle of the morning", 11*60 + 30, 0.9},
			{"the last half hour", 12*60 + 30, 0.9},
			{"the end is exclusive", 13 * 60, neutral},
			{"the small hours", 3 * 60, neutral},
		} {
			if v, _ := c.At(d, tc.minute); v != tc.want {
				t.Errorf("day %d, %s = %v, want %v", d, tc.name, v, tc.want)
			}
		}
	}
}

// Anything unmentioned is 0.5, which is a multiplier of exactly 1. That is what makes one line
// a complete answer, and what makes saying nothing cost nothing.
func TestUnmentionedHoursAreNeutral(t *testing.T) {
	got, _, _ := parse(spans(`[{"days":"sat","from":"20:00","to":"23:00","v":1}]`), known("r_1"))
	c := got["r_1"].Curve

	for d := range store.Days {
		for i := range store.Windows {
			v, _ := c.At(d, i*store.WindowMinutes)
			inSpan := d == 5 && i >= 40 && i < 46
			want := neutral
			if inSpan {
				want = 1
			}
			if v != want {
				t.Fatalf("day %d, window %d = %v, want %v", d, i, v, want)
			}
		}
	}
}

// An end at or before its start runs into the next day, which is how somebody who goes to bed
// at four gets an evening that means four hours.
func TestASpanRunsPastMidnightIntoTheNextDay(t *testing.T) {
	got, _, _ := parse(spans(`[{"days":"sat","from":"22:00","to":"02:00","v":0.8}]`), known("r_1"))
	c := got["r_1"].Curve

	for _, tc := range []struct {
		name        string
		day, minute int
		want        float64
	}{
		{"saturday evening", 5, 23 * 60, 0.8},
		{"sunday small hours", 6, 60, 0.8},
		{"sunday at the end", 6, 2 * 60, neutral},
		{"saturday afternoon", 5, 15 * 60, neutral},
		{"friday night", 4, 23 * 60, neutral},
	} {
		if v, _ := c.At(tc.day, tc.minute); v != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, v, tc.want)
		}
	}

	// Sunday is the last day, so its spill lands on Monday rather than an eighth day.
	sunday, _, _ := parse(spans(`[{"days":"sun","from":"23:00","to":"01:00","v":0.8}]`), known("r_1"))
	if v, _ := sunday["r_1"].Curve.At(0, 30); v != 0.8 {
		t.Errorf("monday small hours = %v, want a Sunday night span to reach them", v)
	}
}

// A model writing a broad stroke and then narrowing it is doing the natural thing, and "the
// last word wins" is the only rule that makes that read the way it was written.
func TestALaterSpanWinsWhereTheyOverlap(t *testing.T) {
	got, _, _ := parse(spans(`[
		{"days":"all","from":"09:00","to":"18:00","v":0.2},
		{"days":"all","from":"12:00","to":"13:00","v":0.9}
	]`), known("r_1"))
	c := got["r_1"].Curve

	if v, _ := c.At(0, 10*60); v != 0.2 {
		t.Errorf("the broad stroke = %v, want 0.2", v)
	}
	if v, _ := c.At(0, 12*60+30); v != 0.9 {
		t.Errorf("the narrowing = %v, want 0.9", v)
	}
}

func TestDaysAreReadHoweverTheyAreWritten(t *testing.T) {
	for _, tc := range []struct {
		days string
		want []int
	}{
		{"mon", []int{0}},
		{"Monday", []int{0}},
		{"mon-fri", []int{0, 1, 2, 3, 4}},
		{"sat,sun", []int{5, 6}},
		{"weekends", []int{5, 6}},
		{"weekdays", []int{0, 1, 2, 3, 4}},
		{"all", []int{0, 1, 2, 3, 4, 5, 6}},
		// Wrapping, so a stretch that crosses the end of the week reads as the one it names.
		{"fri-mon", []int{4, 5, 6, 0}},
		// Unsaid means every day: a model writing one line about an evening means every
		// evening, and refusing it over a missing field would refuse the commonest answer.
		{"", []int{0, 1, 2, 3, 4, 5, 6}},
	} {
		t.Run(tc.days, func(t *testing.T) {
			body := fmt.Sprintf(`[{"days":%q,"from":"10:00","to":"11:00","v":1}]`, tc.days)
			got, _, _ := parse(spans(body), known("r_1"))
			c := got["r_1"].Curve
			if !c.Valid() {
				t.Fatalf("curve = %+v, want it read", c)
			}
			painted := map[int]bool{}
			for d := range store.Days {
				if v, _ := c.At(d, 10*60); v == 1 {
					painted[d] = true
				}
			}
			if len(painted) != len(tc.want) {
				t.Fatalf("painted %v, want %v", painted, tc.want)
			}
			for _, d := range tc.want {
				if !painted[d] {
					t.Errorf("day %d was not painted", d)
				}
			}
		})
	}
}

// One unreadable span costs that span. Refusing the whole answer over one would throw away the
// three that were fine, which is the failure this format exists to stop happening.
func TestOneBadSpanDoesNotCostTheGoodOnes(t *testing.T) {
	got, _, _ := parse(spans(`[
		{"days":"all","from":"09:00","to":"11:00","v":0.8},
		{"days":"funday","from":"09:00","to":"11:00","v":0.8},
		{"days":"all","from":"nine","to":"11:00","v":0.8},
		{"days":"all","from":"20:00","to":"22:00","v":0.9}
	]`), known("r_1"))
	c := got["r_1"].Curve

	if v, _ := c.At(0, 10*60); v != 0.8 {
		t.Errorf("morning = %v, want the first span kept", v)
	}
	if v, _ := c.At(0, 21*60); v != 0.9 {
		t.Errorf("evening = %v, want the last span kept", v)
	}
}

// An answer whose every span was unreadable is an answer that failed, not a week of neutrals —
// and saying so is what gets it into the log rather than onto the screen as a flat line.
func TestAnAnswerOfNothingButBadSpansIsRefused(t *testing.T) {
	got, _, misshapen := parse(spans(`[{"days":"funday","from":"nine","to":"ten","v":1}]`), known("r_1"))
	if got["r_1"].Curve.Valid() {
		t.Error("an answer with nothing readable in it was kept")
	}
	if len(misshapen) != 1 {
		t.Errorf("misshapen = %v, want the shape reported", misshapen)
	}
}

// The old shape still reads, because a model answers the question it expected at least as often
// as the one it was given.
func TestAnArrayOfNumbersStillReads(t *testing.T) {
	body := "[" + strings.Repeat(aDay(0.7)+",", store.Days-1) + aDay(0.7) + "]"
	got, _, misshapen := parse(spans(body), known("r_1"))
	if !got["r_1"].Curve.Valid() {
		t.Fatalf("curve = %+v, misshapen %v, want the fallback to read it", got["r_1"].Curve, misshapen)
	}
	if v, _ := got["r_1"].Curve.At(0, 0); v != 0.7 {
		t.Errorf("value = %v, want 0.7", v)
	}
}
