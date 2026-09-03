package migrations

// The one nudge that has been decided on and not yet sent.
//
// The loop asks every few minutes whether somebody is owed a nudge. Firing the moment the
// answer turns yes would put every nudge on a tick boundary, so the answer instead schedules
// one a random moment later and the loop bails while that stands.
//
// One row per person and no history: this is a decision in flight, not a record. What was
// sent is in nudges.
var derivedPendingNudge = Migration{
	Name: "20260903001519_derived_pending_nudge",
	Up: exec(`
CREATE TABLE pending_nudge (
  principal_id TEXT    PRIMARY KEY,
  at           INTEGER NOT NULL
) WITHOUT ROWID;
`),
}
