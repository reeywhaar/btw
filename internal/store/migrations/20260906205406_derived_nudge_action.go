package migrations

// The action a nudge was answered with goes away, because there is only one now.
//
// It existed to record which of two buttons was pressed, and it was already the only place
// that distinction survived — nothing ever read it back. With a single gesture there is nothing
// left to tell apart, and a column holding one value for every row is a column somebody will
// one day believe means something.
//
// `acted_at` stays. Whether a nudge was answered at all is a different question from which way,
// and it is the one worth being able to ask.
//
// Rebuilt rather than altered, because the column is named in a table CHECK.
var derivedNudgeAction = Migration{
	Name: "20260906205406_derived_nudge_action",
	Up: exec(`
CREATE TABLE nudges_new (
  id           TEXT    PRIMARY KEY,
  principal_id TEXT    NOT NULL,
  reminder_id  TEXT    NOT NULL,
  sent_at      INTEGER NOT NULL,
  acted_at     INTEGER
);
INSERT INTO nudges_new (id, principal_id, reminder_id, sent_at, acted_at)
  SELECT id, principal_id, reminder_id, sent_at, acted_at FROM nudges;
DROP TABLE nudges;
ALTER TABLE nudges_new RENAME TO nudges;
CREATE INDEX nudges_principal_sent ON nudges(principal_id, sent_at DESC);
`),
}
