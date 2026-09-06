package advise

import (
	"encoding/json"
	"strconv"
	"strings"

	"btw/internal/store"
)

// A span is one stretch of the week the companion has an opinion about.
//
// This is the shape the question actually asks for, and the array of numbers it replaced is
// kept only as a fallback. The reason is not taste, it is what a model can do reliably:
//
//   - **Nothing to count.** Seven days of forty-eight was asked for and answered as 7×49, 7×24
//     and an object keyed by day, on consecutive attempts. A span names its own hours, so
//     there is no position to lose track of and no length to get wrong.
//   - **Saying nothing costs nothing.** With 336 numbers the lazy answer and the considered one
//     cost the same, so a model under pressure emits 0.5 three hundred and thirty-six times and
//     produces something data-shaped that says nothing. Here an opinion is one line and no
//     opinion is no lines.
//   - **Arbitrary edges.** Fixed buckets could not say "the middle of the morning to the middle
//     of the day". A span says 10:00 to 13:00.
//
// Everything downstream still reads a 7×48 [store.Curve]; spans are expanded into one here.
type span struct {
	Days string
	From string
	To   string
	V    float64
}

// Field names a model reaches for, in the order they are looked up.
//
// **Aliases, not tidiness.** A model told to write "from" writes "start" often enough to
// matter, and the two failures that causes are not equally visible: a missing "from" was
// refused outright and showed on screen, while a weight written as "value" read as the zero
// value — a span the model meant as 0.9 became 0.0, drawn as a confident graph saying the
// opposite of what it said. The silent one is why this list exists.
var (
	dayKeys    = []string{"days", "day"}
	fromKeys   = []string{"from", "start", "begin", "after"}
	toKeys     = []string{"to", "end", "until", "till", "before"}
	weightKeys = []string{"v", "value", "weight", "score", "confidence"}
)

// spanFrom reads one span out of an object, whichever names it used.
//
// The weight is the one field with no default. A span whose weight cannot be read is dropped
// rather than taken as zero, because zero is not an absence — it is the strongest opinion the
// scale can hold, and the wrong one.
func spanFrom(row map[string]json.RawMessage) (span, bool) {
	pick := func(names []string) (json.RawMessage, bool) {
		for _, name := range names {
			if raw, ok := row[name]; ok {
				return raw, true
			}
		}
		return nil, false
	}
	text := func(names []string) string {
		raw, ok := pick(names)
		if !ok {
			return ""
		}
		var out string
		if err := json.Unmarshal(raw, &out); err != nil {
			return ""
		}
		return out
	}

	raw, ok := pick(weightKeys)
	if !ok {
		return span{}, false
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		// Written as a string, which models do. "0.9" is the same answer as 0.9.
		var asText string
		if json.Unmarshal(raw, &asText) != nil {
			return span{}, false
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(asText), 64)
		if err != nil {
			return span{}, false
		}
		v = parsed
	}

	return span{
		Days: text(dayKeys),
		From: text(fromKeys),
		To:   text(toKeys),
		V:    v,
	}, true
}

// neutral is the middle of the scale: no opinion, and a multiplier of exactly 1.
const neutral = 0.5

// neutralWeek is a week with no opinion anywhere.
func neutralWeek() store.Curve {
	week := make(store.Curve, store.Days)
	for d := range week {
		week[d] = make([]float64, store.Windows)
		for i := range week[d] {
			week[d][i] = neutral
		}
	}
	return week
}

// spansToCurve expands a list of spans into a week, or reports nil when it is not a list of
// spans at all.
//
// Later spans win where they overlap. A model writing a broad stroke and then narrowing it is
// doing the natural thing, and "the last word wins" is the only rule that makes that read the
// way it was written.
func spansToCurve(raw json.RawMessage) (store.Curve, bool) {
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, false
	}
	// An empty list is a real answer and a common one: no opinion anywhere. It reads the same
	// as an empty array of numbers would, and both mean the same thing, so there is nothing to
	// tell apart — and answering neutral rather than refusing is what lets a deliberate "no
	// shape" overwrite an older answer instead of being carried forward as a failure.
	if len(rows) == 0 {
		return neutralWeek(), true
	}

	// Neutral everywhere, then painted over. Unmentioned means no opinion, which is what makes
	// a single line a complete answer.
	week := neutralWeek()

	painted := false
	for _, row := range rows {
		s, ok := spanFrom(row)
		if !ok {
			continue
		}
		if paint(week, s) {
			painted = true
		}
	}
	// A list of spans none of which could be read is not an answer of neutrals — it is an
	// answer that failed, and saying so is what gets the shape into the log.
	return week, painted
}

// paint applies one span, and reports whether it could be read at all.
func paint(week store.Curve, s span) bool {
	days := daysOf(s.Days)
	from, okFrom := minuteOf(s.From)
	to, okTo := minuteOf(s.To)
	if len(days) == 0 || !okFrom || !okTo {
		return false
	}
	v := min(max(s.V, 0), 1)

	for _, d := range days {
		// A span whose end is at or before its start runs past midnight into the next day,
		// which is how somebody who goes to bed at four gets an evening that means four hours.
		length := to - from
		if length <= 0 {
			length += 24 * 60
		}
		for offset := 0; offset < length; offset += store.WindowMinutes {
			minute := from + offset
			day := (d + minute/(24*60)) % store.Days
			week[day][(minute%(24*60))/store.WindowMinutes] = v
		}
	}
	return true
}

// dayWords are the names a model reaches for, and what each covers.
var dayWords = map[string][]int{
	"all": {0, 1, 2, 3, 4, 5, 6}, "every day": {0, 1, 2, 3, 4, 5, 6}, "daily": {0, 1, 2, 3, 4, 5, 6},
	"weekday": {0, 1, 2, 3, 4}, "weekdays": {0, 1, 2, 3, 4},
	"weekend": {5, 6}, "weekends": {5, 6},
}

// daysOf reads whichever way a model wrote a set of days: "mon", "mon-fri", "sat,sun",
// "weekends", "all". Empty when none of it could be read.
func daysOf(raw string) []int {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		// Unsaid means every day. A model writing one line about an evening means every
		// evening, and refusing it over a missing field would refuse the commonest answer.
		return dayWords["all"]
	}
	if set, ok := dayWords[raw]; ok {
		return set
	}

	seen := map[int]bool{}
	var out []int
	add := func(d int) {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}

	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if set, ok := dayWords[part]; ok {
			for _, d := range set {
				add(d)
			}
			continue
		}
		if lo, hi, found := strings.Cut(part, "-"); found {
			a, okA := dayIndex(lo)
			b, okB := dayIndex(hi)
			if !okA || !okB {
				continue
			}
			// Wrapping, so "sat-sun" and "fri-mon" both read as the stretch they name.
			for d := a; ; d = (d + 1) % store.Days {
				add(d)
				if d == b {
					break
				}
			}
			continue
		}
		if d, ok := dayIndex(part); ok {
			add(d)
		}
	}
	return out
}

// minuteOf reads "HH:MM" into minutes since midnight.
//
// 24:00 is accepted and means the end of the day, because it is what a model reaches for to say
// "until midnight" and reading it as invalid would drop the span that runs to bedtime.
func minuteOf(s string) (int, bool) {
	s = strings.TrimSpace(s)
	h, m, found := strings.Cut(s, ":")
	if !found {
		// A bare hour is unambiguous and models write them.
		hour, err := strconv.Atoi(s)
		if err != nil || hour < 0 || hour > 24 {
			return 0, false
		}
		return hour * 60, true
	}
	hour, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || hour < 0 || hour > 24 {
		return 0, false
	}
	minute, err := strconv.Atoi(strings.TrimSpace(m))
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	if hour == 24 && minute != 0 {
		return 0, false
	}
	return hour*60 + minute, true
}
