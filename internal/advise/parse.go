package advise

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"btw/internal/store"
)

// answer is one entry as the companion returns it.
//
// Everything but the id is raw, and decoded one field at a time below. Typed fields here read
// better and cost far too much: a model that answered `"curve": ["morning","evening"]` for one
// reminder would fail the whole document, and a round covering forty reminders would come back
// with nothing at all. Isolating a bad field to the field is the entire reason this is lenient.
type answer struct {
	ID        string          `json:"id"`
	Category  json.RawMessage `json:"category"`
	Exclusive json.RawMessage `json:"exclusive"`
	Curve     json.RawMessage `json:"curve"`
}

// soft decodes one field, and treats anything it cannot read as absent.
//
// The zero value on failure, and **not what encoding/json left behind**. It fills a slice
// before it fails on an element, so a flat list of 336 numbers read as [][]float64 comes back
// as 336 empty days rather than as nothing — which reads as a shape rather than as a failure,
// and sends the caller down the wrong branch.
func soft[T any](raw json.RawMessage) T {
	var out T
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		var zero T
		return zero
	}
	return out
}

// parse reads what the companion said, keeping what is usable and discarding the rest.
//
// Lenient throughout, and it has to be: this is a free model answering in JSON mode, which is a
// request rather than a guarantee. The alternative — refusing the whole answer over one bad
// entry — would throw away nineteen good ones and leave the account no better off than before
// it configured a key.
//
// Nothing here can invent a reminder. `known` is the set that was asked about, and an id
// outside it is dropped: a model that echoes an id back wrongly, or helpfully makes one up,
// must not be able to attach advice to somebody else's row.
//
// The second return is how many entries were thrown away, and the third the shapes of the
// curves that could not be read — "7x24", "336" — so a pass can say what went wrong without
// saying anything about somebody's reminders.
func parse(reply string, known map[string]bool) (map[string]store.Advice, int, []string) {
	out := make(map[string]store.Advice)

	entries, ok := decode(reply)
	if !ok {
		return out, 0, nil
	}

	dropped := 0
	var misshapen []string
	for _, e := range entries {
		if !known[e.ID] {
			dropped++
			continue
		}
		if _, already := out[e.ID]; already {
			// A second opinion about a reminder already answered for. The first stands:
			// choosing between two is a decision with nothing to base it on.
			dropped++
			continue
		}

		a := store.Advice{Exclusive: soft[bool](e.Exclusive)}
		for _, c := range soft[[]string](e.Category) {
			c = strings.ToLower(strings.TrimSpace(c))
			if slices.ContainsFunc(Categories, func(k struct{ Name, Gloss string }) bool { return k.Name == c }) {
				a.Categories = append(a.Categories, c)
			}
		}

		if week, shape := reshape(e.Curve); week != nil {
			a.Curve = week
		} else if len(e.Curve) != 0 {
			dropped++
			// Kept on the entry as well as reported, so the screen can say which shape it
			// refused rather than leaving somebody to find it in a log.
			a.Shape = shape
			misshapen = append(misshapen, shape)
		}
		out[e.ID] = a
	}
	return out, dropped, misshapen
}

// reshape reads a curve out of whatever a model actually sent, and names the shape when it
// cannot.
//
// **Only readings that are unambiguous.** A model asked for seven arrays of forty-eight
// reliably sends something else, and most of those somethings mean exactly one thing:
//
//   - 7×48, as asked.
//   - an object keyed by day name, which is what "one for each day of the week" invites and
//     is arguably a better answer than the one asked for.
//   - 336 in a flat list, which is the same numbers in the same order.
//   - 7×24, which is the week by the hour — every model's favourite substitution.
//   - one day of 48 or 24, which the prompt itself says is right for a reminder that does not
//     differ across the week.
//
// Numbers written as strings are read too. `"0.5"` is the same answer as `0.5` and refusing it
// would be refusing on a technicality.
//
// Each of those is expanded rather than guessed at: an hourly value covers both of its half
// hours, and one day covers all seven. Nothing invents a number that was not sent.
//
// What is still refused is anything ragged — six days, or seven with one short. There the
// values after the mistake belong to hours nobody can identify, and a curve confidently wrong
// about which hour is which is worse than no curve at all.
func reshape(raw json.RawMessage) (store.Curve, string) {
	// What the question now asks for. Everything below it is a fallback for a model that
	// answered the question it expected rather than the one it was given — which they do, and
	// which is worth reading rather than refusing.
	if week, ok := spansToCurve(raw); ok {
		return week, ""
	}
	if nested := nestedDays(raw); nested != nil {
		return fromDays(nested)
	}
	if keyed, ok := keyedDays(raw); ok {
		return fromDays(keyed)
	}
	flat := numbers(raw)
	switch len(flat) {
	case store.Days * store.Windows:
		days := make([][]float64, store.Days)
		for d := range days {
			days[d] = flat[d*store.Windows : (d+1)*store.Windows]
		}
		return fromDays(days)
	case store.Windows, store.Windows / 2:
		return fromDays([][]float64{flat})
	}
	if len(flat) > 0 {
		return nil, fmt.Sprint(len(flat))
	}
	return nil, shapeOf(raw)
}

// nestedDays reads an array of arrays, or nil.
func nestedDays(raw json.RawMessage) [][]float64 {
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return nil
	}
	out := make([][]float64, 0, len(rows))
	for _, row := range rows {
		day := numbers(row)
		if day == nil {
			return nil
		}
		out = append(out, day)
	}
	return out
}

// keyedDays reads an object keyed by day name into Monday-first order.
//
// All seven or nothing. A model that named six days has left one out rather than described a
// six-day week, and filling the gap would be inventing an opinion it did not offer.
func keyedDays(raw json.RawMessage) ([][]float64, bool) {
	var byName map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byName); err != nil || len(byName) == 0 {
		return nil, false
	}

	out := make([][]float64, store.Days)
	for name, row := range byName {
		d, ok := dayIndex(name)
		if !ok {
			return nil, false
		}
		day := numbers(row)
		if day == nil || out[d] != nil {
			return nil, false
		}
		out[d] = day
	}
	for _, day := range out {
		if day == nil {
			return nil, false
		}
	}
	return out, true
}

// dayNames is the week Monday first, in the forms a model writes it.
var dayNames = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

func dayIndex(name string) (int, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, full := range dayNames {
		if name == full || name == full[:3] {
			return i, true
		}
	}
	return 0, false
}

// numbers reads a list of numbers, accepting ones written as strings.
//
// Not json.Unmarshal into []float64 directly: that fills the slice before failing on an
// element, so a caller checking the length is handed a shape rather than a failure.
func numbers(raw json.RawMessage) []float64 {
	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := make([]float64, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case float64:
			out = append(out, v)
		case string:
			// "0.5" is the same answer as 0.5, and refusing it would be a technicality.
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil
			}
			out = append(out, f)
		default:
			return nil
		}
	}
	return out
}

// shapeOf names what arrived when nothing above could read it.
func shapeOf(raw json.RawMessage) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		return fmt.Sprintf("obj:%d", len(object))
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err == nil {
		return fmt.Sprintf("list:%d", len(rows))
	}
	return "unreadable"
}

// fit forces a day that is one value out onto the grid.
//
// The extra is dropped from the end and a missing one is held from it, because the end is where
// a miscount shows and because a day's last half hour is the one nobody is awake for. It is a
// tolerance and not a repair: nothing here knows where the mistake actually was.
func fit(day []float64) []float64 {
	scale := 1
	if abs(len(day)-store.Windows/2) == 1 {
		scale = 2
	}
	want := store.Windows / scale

	out := make([]float64, want)
	for i := range out {
		if i < len(day) {
			out[i] = day[i]
		} else {
			out[i] = day[len(day)-1]
		}
	}
	if scale == 1 {
		return out
	}
	hours := make([]float64, store.Windows)
	for i, v := range out {
		hours[2*i], hours[2*i+1] = v, v
	}
	return hours
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// fromDays expands a list of days into a full week, or names its shape.
func fromDays(days [][]float64) (store.Curve, string) {
	if len(days) == 0 {
		return nil, "0"
	}
	if len(days) != store.Days && len(days) != 1 {
		return nil, fmt.Sprintf("%dx%d", len(days), len(days[0]))
	}

	out := make(store.Curve, 0, store.Days)
	for _, day := range days {
		var hours []float64
		switch {
		case len(day) == store.Windows:
			hours = day
		case len(day) == store.Windows/2:
			// By the hour. Each value covers both of its half hours, which is what "hourly"
			// means rather than a guess about the gap between them.
			hours = make([]float64, store.Windows)
			for i, v := range day {
				hours[2*i], hours[2*i+1] = v, v
			}
		case abs(len(day)-store.Windows) == 1, abs(len(day)-store.Windows/2) == 1:
			// One out, which is the mistake a model actually makes — 49 values for a day, or
			// 23. Trimmed or held rather than refused, and the reason is in what the answer
			// claims to be: the question asks for broad stretches and says in as many words
			// that a curve swinging between neighbouring half hours is describing precision
			// the model does not have. An answer whose neighbours are meant to be alike
			// cannot be ruined by a half hour of misalignment.
			//
			// Strictly one. Two out is no longer a slip, and past that the values are landing
			// on hours nobody can identify — which is the thing worth refusing.
			hours = fit(day)
		default:
			return nil, fmt.Sprintf("%dx%d", len(days), len(day))
		}

		clamped := make([]float64, store.Windows)
		for i, v := range hours {
			// Clamped rather than refused: a model asked for 0 to 1 answers inside it nearly
			// always, and the odd 1.2 plainly meant the top of the scale.
			clamped[i] = min(max(v, 0), 1)
		}
		out = append(out, clamped)
	}

	// One day means the same day all week, which the prompt says is the right answer whenever
	// a reminder does not differ across it.
	for len(out) < store.Days {
		out = append(out, out[0])
	}
	return out, ""
}

// decode finds the array of entries inside whatever came back.
//
// Four attempts, in order of how much they assume. A model in JSON mode usually answers
// cleanly; it sometimes fences the object, sometimes prefaces it with a sentence, and
// sometimes returns the bare array it was asked to wrap. Each of those is one line to accept
// and an evening to diagnose from a parse error.
func decode(reply string) ([]answer, bool) {
	type wrapper struct {
		Results []json.RawMessage `json:"results"`
	}

	for _, candidate := range []string{reply, unfence(reply), slice(reply, '{', '}'), slice(reply, '[', ']')} {
		if candidate == "" {
			continue
		}
		var w wrapper
		if err := json.Unmarshal([]byte(candidate), &w); err == nil && w.Results != nil {
			return entries(w.Results), true
		}
		var bare []json.RawMessage
		if err := json.Unmarshal([]byte(candidate), &bare); err == nil {
			return entries(bare), true
		}
	}
	return nil, false
}

// entries reads each one on its own, so an entry that will not decode costs that entry rather
// than the round it arrived in.
func entries(raw []json.RawMessage) []answer {
	out := make([]answer, 0, len(raw))
	for _, one := range raw {
		var a answer
		if err := json.Unmarshal(one, &a); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}

func unfence(s string) string {
	start := strings.Index(s, "```")
	if start < 0 {
		return ""
	}
	rest := s[start+3:]
	rest = strings.TrimPrefix(rest, "json")
	end := strings.Index(rest, "```")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func slice(s string, open, close byte) string {
	start := strings.IndexByte(s, open)
	end := strings.LastIndexByte(s, close)
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}
