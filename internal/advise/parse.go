package advise

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"btw/internal/store"
)

// answer is one entry as the companion returns it.
//
// Times are strings, because that is the shape a model gets right. Asked for minutes since
// midnight it does arithmetic, and arithmetic is the thing it is worst at; asked for "22:30"
// it copies a clock. The conversion belongs on this side, where it is a parse rather than a
// hope.
type answer struct {
	ID        string   `json:"id"`
	Category  []string `json:"category"`
	Exclusive bool     `json:"exclusive"`
	Slots     []struct {
		Day   string `json:"day"`
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"slots"`
}

// parse reads what the companion said, keeping what is usable and discarding the rest.
//
// Lenient throughout, and it has to be: this is a free model answering in JSON mode, which is
// a request rather than a guarantee. The alternative — refusing the whole answer over one
// malformed slot — would throw away nineteen good ones and leave the account no better off
// than before it configured a key.
//
// Nothing here can invent a reminder. `known` is the set that was asked about, and an id
// outside it is dropped: a model that echoes an id back wrongly, or helpfully makes one up,
// must not be able to attach advice to somebody else's row.
//
// The second return is how many entries were thrown away, so a pass can say so without saying
// what they were.
func parse(reply string, known map[string]bool) (map[string]store.Advice, int) {
	out := make(map[string]store.Advice)

	entries, ok := decode(reply)
	if !ok {
		return out, 0
	}

	dropped := 0
	for _, e := range entries {
		if !known[e.ID] || out[e.ID].Slots != nil {
			// Unknown, or a second opinion about a reminder already answered for. The first
			// answer stands: choosing between two is a decision with nothing to base it on.
			dropped++
			continue
		}

		a := store.Advice{Exclusive: e.Exclusive}
		for _, c := range e.Category {
			c = strings.ToLower(strings.TrimSpace(c))
			if slices.ContainsFunc(Categories, func(k struct{ Name, Gloss string }) bool { return k.Name == c }) {
				a.Categories = append(a.Categories, c)
			}
		}
		for _, s := range e.Slots {
			day, okDay := dayOf(s.Day)
			start, okStart := minuteOf(s.Start)
			end, okEnd := minuteOf(s.End)
			if !okDay || !okStart || !okEnd {
				continue
			}
			a.Slots = append(a.Slots, store.Slot{Day: day, Start: start, End: end})
		}
		if a.Slots == nil {
			// Distinguished from "not answered for" by being in the map at all: an entry with
			// no usable slot is still advice, and means no hour suits this better than another.
			a.Slots = []store.Slot{}
		}
		out[e.ID] = a
	}
	return out, dropped
}

// decode finds the array of entries inside whatever came back.
//
// Four attempts, in order of how much they assume. A model in JSON mode usually answers
// cleanly; it sometimes fences the object, sometimes prefaces it with a sentence, and
// sometimes returns the bare array it was asked to wrap. Each of those is one line to accept
// and an evening to diagnose from a parse error.
func decode(reply string) ([]answer, bool) {
	type wrapper struct {
		Results []answer `json:"results"`
	}

	for _, candidate := range []string{reply, unfence(reply), slice(reply, '{', '}'), slice(reply, '[', ']')} {
		if candidate == "" {
			continue
		}
		var w wrapper
		if err := json.Unmarshal([]byte(candidate), &w); err == nil && w.Results != nil {
			return w.Results, true
		}
		var bare []answer
		if err := json.Unmarshal([]byte(candidate), &bare); err == nil {
			return bare, true
		}
	}
	return nil, false
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

// dayOf reads a day name. Forgiving about case and about the full word, since "Monday" and
// "mon" are the same answer and refusing one of them would be refusing it on a technicality.
func dayOf(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for i, d := range days {
		if s == d || strings.HasPrefix(s, d) {
			return i, true
		}
	}
	return 0, false
}

// minuteOf reads "HH:MM" into minutes since midnight.
//
// 24:00 is accepted and means the end of the day, because it is what a model reaches for to
// say "until midnight" and reading it as invalid would drop the slot that runs to bedtime.
func minuteOf(s string) (int, bool) {
	s = strings.TrimSpace(s)
	h, m, found := strings.Cut(s, ":")
	if !found {
		return 0, false
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
