package migrations

var derivedInitialSchema = Migration{
	Name: "20260829000000_derived_initial_schema",
	Up: exec(`
-- principal_id references main.db; there is no foreign key, because no constraint can
-- cross a database and no transaction may span the two. A row pointing at a deleted
-- account is garbage to be collected, not an inconsistency to be repaired.

-- Keyed by sha256 of the cookie value, so nothing readable ever holds a replayable
-- credential and the lookup is timing-safe without trying to be.
--
-- Sessions are not recomputable from the accounts they belong to, but they are throwaway
-- all the same: the whole cost of losing them is that everybody signs in again. That is
-- the test this database applies, and the price is that ending sessions and changing a
-- password cannot be one transaction — see store.SetPassword, which orders them so the
-- survivable failure is the one that can happen.
CREATE TABLE sessions (
  id_hash      BLOB    PRIMARY KEY,
  principal_id TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX sessions_principal ON sessions(principal_id);
CREATE INDEX sessions_expires   ON sessions(expires_at);

-- A day's plan: which minutes a person is due to be nudged at. Stored because a restart
-- that re-rolled the day could fire a slot twice or lose an afternoon.
--
-- Never shown. A person who can see that the next nudge is at 14:32 is a person waiting
-- for 14:32, and the surprise is the entire mechanism. No endpoint returns these.
--
-- local_date is the person's own date as 'YYYY-MM-DD', which is what makes the plan one
-- per waking day rather than one per UTC day.
CREATE TABLE slots (
  principal_id TEXT    NOT NULL,
  local_date   TEXT    NOT NULL,
  idx          INTEGER NOT NULL,
  at           INTEGER NOT NULL,
  fired_at     INTEGER,
  PRIMARY KEY (principal_id, local_date, idx)
) WITHOUT ROWID;
CREATE INDEX slots_due ON slots(at) WHERE fired_at IS NULL;

-- One delivery of one reminder. action records which button was pressed, which is the
-- only place that distinction is kept; reminders.done_at records merely that it ended.
CREATE TABLE nudges (
  id           TEXT    PRIMARY KEY,
  principal_id TEXT    NOT NULL,
  reminder_id  TEXT    NOT NULL,
  sent_at      INTEGER NOT NULL,
  acted_at     INTEGER,
  action       TEXT    CHECK (action IN ('done','drop'))
);
CREATE INDEX nudges_principal_sent ON nudges(principal_id, sent_at DESC);
`),
}
