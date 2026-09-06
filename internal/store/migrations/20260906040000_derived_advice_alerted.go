package migrations

// Whether the person has already been told their companion stopped working.
//
// The loop runs every half hour and a broken key fails every single pass. Without this, being
// told would mean a notification every thirty minutes for as long as the key stayed broken —
// which is the fastest way to have notifications turned off altogether, and would cost btw the
// channel it actually exists to use.
//
// So the flag makes it once per episode: raised when the message goes out, lowered whenever a
// round succeeds. A key that breaks, is fixed, and breaks again is two messages, months apart,
// which is right.
var derivedAdviceAlerted = Migration{
	Name: "20260906040000_derived_advice_alerted",
	Up: exec(`
ALTER TABLE advice_state ADD COLUMN alerted INTEGER NOT NULL DEFAULT 0;
`),
}
