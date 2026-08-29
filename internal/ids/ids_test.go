package ids

import (
	"testing"
	"time"
)

func TestIdsSortChronologically(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	earlier := newAt(Reminder, base)
	later := newAt(Reminder, base.Add(time.Millisecond))

	if earlier >= later {
		t.Fatalf("ids do not sort by time: %q >= %q", earlier, later)
	}
}

func TestNewIsUnique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for range 1000 {
		id := New(Nudge)
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestValid(t *testing.T) {
	id := New(Principal)
	if !Valid(Principal, id) {
		t.Errorf("Valid(Principal, %q) = false, want true", id)
	}
	// The prefix is part of the shape: a reminder id is not a principal id.
	if Valid(Reminder, id) {
		t.Errorf("Valid(Reminder, %q) = true, want false", id)
	}
	for _, bad := range []string{"", "p_", "p_short", id + "X", "p_IIIIIIIIIIIIIIIIIIIIIIIIII"} {
		if Valid(Principal, bad) {
			t.Errorf("Valid(Principal, %q) = true, want false", bad)
		}
	}
}
