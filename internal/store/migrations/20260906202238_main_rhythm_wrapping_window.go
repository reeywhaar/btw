package migrations

// The waking window may cross midnight.
//
// It could not, and the CHECK said so: `wake_minute < sleep_minute`. That refused the hours a
// good number of people actually keep — noon until four is sixteen waking hours, and the old
// rule read it as minus eight — and the refusal came from a limitation elsewhere rather than
// from anything about the data. What could not model it was Since, which took "the start of
// today's waking day" to be today's waking hour; at one in the morning, for somebody awake
// from noon, that hour is still eleven hours away.
//
// Equal ends are now the whole day, which is the natural thing to mean with two controls that
// each name an hour, and the same as switching the window off.
//
// Rebuilt rather than altered, because a table CHECK cannot be dropped in place. The bound on
// the hours stays: a minute outside a day is a mistake in a way that a window crossing midnight
// never was.
var mainRhythmWrappingWindow = Migration{
	Name: "20260906202238_main_rhythm_wrapping_window",
	Up: exec(`
CREATE TABLE rhythm_new (
  principal_id   TEXT    PRIMARY KEY REFERENCES principals(id) ON DELETE CASCADE,
  timezone       TEXT    NOT NULL DEFAULT 'UTC',
  window_enabled INTEGER NOT NULL DEFAULT 1,
  wake_minute    INTEGER NOT NULL DEFAULT 540,
  sleep_minute   INTEGER NOT NULL DEFAULT 1320,
  budget         INTEGER NOT NULL DEFAULT 3,
  silent         INTEGER NOT NULL DEFAULT 0,
  CHECK (wake_minute >= 0 AND wake_minute < 1440 AND sleep_minute >= 0 AND sleep_minute < 1440),
  CHECK (budget >= 0)
);
INSERT INTO rhythm_new (principal_id, timezone, window_enabled, wake_minute, sleep_minute, budget, silent)
  SELECT principal_id, timezone, window_enabled,
         wake_minute % 1440, sleep_minute % 1440, budget, silent
    FROM rhythm;
DROP TABLE rhythm;
ALTER TABLE rhythm_new RENAME TO rhythm;
`),
}
