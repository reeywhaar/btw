package migrations

// Whether the last failure was a quota rather than a mistake.
//
// A column rather than a prefix on the message already stored, because the message is the
// gateway's own words and matching on those would break the first time OpenRouter rewords a
// sentence. The two are genuinely different states for the person reading them: a rejected key
// wants somebody to go and fix it, and this wants nothing at all — the next pass is half an
// hour away, which is the wait.
//
// A new migration rather than an edit to the one that made the table. Every deployment past
// that one has recorded it as applied and would skip the edit forever.
var derivedAdviceLimited = Migration{
	Name: "20260906030000_derived_advice_limited",
	Up: exec(`
ALTER TABLE advice_state ADD COLUMN limited INTEGER NOT NULL DEFAULT 0;
`),
}
