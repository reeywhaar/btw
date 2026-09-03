package migrations

// The minimum gap goes away with the plan that needed it.
//
// It existed because slots were drawn independently and could land on top of each other. A
// wait measured from the last nudge cannot: the interval *is* the spacing, and a second
// number saying the same thing is a second number to keep in agreement with the first.
//
// Rebuilt rather than dropped, because the column is named in a table CHECK.
var mainRhythmDropMinGap = Migration{
	Name: "20260903001211_main_rhythm_drop_min_gap",
	Up: exec(`
CREATE TABLE rhythm_new (
  principal_id   TEXT    PRIMARY KEY REFERENCES principals(id) ON DELETE CASCADE,
  timezone       TEXT    NOT NULL DEFAULT 'UTC',
  window_enabled INTEGER NOT NULL DEFAULT 1,
  wake_minute    INTEGER NOT NULL DEFAULT 540,
  sleep_minute   INTEGER NOT NULL DEFAULT 1320,
  budget         INTEGER NOT NULL DEFAULT 3,
  silent         INTEGER NOT NULL DEFAULT 0,
  CHECK (wake_minute >= 0 AND sleep_minute <= 1440 AND wake_minute < sleep_minute),
  CHECK (budget >= 0)
);
INSERT INTO rhythm_new (principal_id, timezone, window_enabled, wake_minute, sleep_minute, budget, silent)
  SELECT principal_id, timezone, window_enabled, wake_minute, sleep_minute, budget, silent FROM rhythm;
DROP TABLE rhythm;
ALTER TABLE rhythm_new RENAME TO rhythm;
`),
}
