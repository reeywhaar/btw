package migrations

// The day's plan goes away.
//
// A plan existed to promise an exact number of nudges at times drawn in advance, and every
// awkward thing around it was in service of that promise: redrawing the day when a setting
// changed, a grace window for slots nobody fired, a ceiling that had to agree with what the
// planner could actually place, and an off-by-one in that agreement.
//
// The promise was not worth its machinery. What matters is roughly this often, at hours
// nobody picked, while awake — which is a question answerable from the last nudge and the
// clock, on a loop, with no state to keep.
var derivedDropSlots = Migration{
	Name: "20260903001211_derived_drop_slots",
	Up: exec(`
DROP TABLE IF EXISTS slots;
`),
}
