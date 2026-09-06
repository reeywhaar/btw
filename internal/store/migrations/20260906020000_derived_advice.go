package migrations

// What the companion made of somebody's reminders, and whether it needs asking again.
//
// In derived.db, and it passes the admission test rather than merely fitting: every row here
// is recomputable from what is in main.db — the reminders, the account's `about`, its rhythm —
// and nobody typed a word of it. Losing the file costs one round of questions, not a sentence
// somebody wrote.
//
// It is also the file that is allowed to churn. main.db is written when a person types
// something and at almost no other time, which is what a file being snapshotted should look
// like; advice is rewritten whenever anything moves, and putting it there would mean a backup
// every time a model changed its mind about a Tuesday.
//
// No foreign key to reminders, because no constraint can cross a database. A row for a
// reminder that has been deleted is garbage to collect, not an inconsistency to repair — and
// the weighting reads advice by joining from the reminders it already has, so an orphan is
// never consulted in the meantime.
var derivedAdvice = Migration{
	Name: "20260906020000_derived_advice",
	Up: exec(`
-- One row per reminder. Replaced wholesale each time the companion is asked, because a
-- partial answer about one reminder is not something to merge into an older one.
CREATE TABLE advice (
  reminder_id TEXT    PRIMARY KEY,
  -- Whether it wants somebody's full attention. Read by the weighting, which damps a reminder
  -- outside its hours harder when the answer is yes: a show can be suggested at a bad moment
  -- and cost nothing, and a thing needing an hour of concentration cannot.
  exclusive   INTEGER NOT NULL,
  -- JSON, and read by nothing yet. Kept because asking again is the expensive part — a free
  -- key allows fifty questions a day — so throwing away half of an answer already paid for,
  -- to save a column, would be paid back one request per reminder.
  categories  TEXT    NOT NULL,
  -- JSON: [{"day":0-6 from monday,"start":minutes,"end":minutes}] in the person's local time,
  -- the same units and the same origin as a rhythm's waking window. An end at or before its
  -- start runs past midnight into the next day.
  slots       TEXT    NOT NULL,
  advised_at  INTEGER NOT NULL
) WITHOUT ROWID;

-- Whether a person's advice is worth what it says.
--
-- A flag rather than a timestamp comparison. "Has anything changed since we last asked" is a
-- question about writes scattered over two databases — a reminder added, a description
-- written, an about rewritten, a rhythm moved — and the cheapest true answer is for each of
-- those writes to say so.
--
-- Absence means stale. A derived.db thrown away takes the advice with it, so the state that
-- says "already asked" must not be the thing that survives — and a fresh instance asking once
-- for everybody is exactly right.
CREATE TABLE advice_state (
  principal_id TEXT    PRIMARY KEY,
  stale        INTEGER NOT NULL,
  -- When the last answer arrived, and what went wrong if none did. The error is kept so an
  -- operator reading a log is not the only way to find out a key stopped working.
  advised_at   INTEGER,
  attempted_at INTEGER,
  error        TEXT    NOT NULL DEFAULT ''
) WITHOUT ROWID;
`),
}
