package migrations

// A waking window is now something somebody opts into rather than something everybody has.
//
// Defaulting to on, because every existing account was created under a window and switching
// them all to "any hour" on upgrade would mean nudges at four in the morning for people who
// never asked for that.
//
// The hours stay in their own columns rather than being folded into 0..1440 when the window
// is off. Unchecking the box would otherwise lose whatever somebody had chosen, and typing
// 09:00 and 22:00 back in is exactly the bookkeeping this product is trying not to have.
var mainRhythmWindowEnabled = Migration{
	Name: "20260829010000_main_rhythm_window_enabled",
	Up: exec(`
ALTER TABLE rhythm ADD COLUMN window_enabled INTEGER NOT NULL DEFAULT 1;
`),
}
