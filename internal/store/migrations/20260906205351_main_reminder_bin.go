package migrations

// A reminder is binned rather than finished.
//
// btw is not a to-do list and had borrowed a to-do list's vocabulary anyway: *done*, and beside
// it *drop* for the case where done would be a lie. Two buttons, one outcome, and the second
// existed only so that ending something you never did did not require claiming you had.
//
// A bin claims nothing. One gesture covers both, and it is the truer word for what already
// happened — the row was never finished with, it was put somewhere out of the way and kept.
//
// A rename rather than a new column, because it is the same fact under a better name and two
// would be one to keep in step with the other. The partial index goes with it: an index over a
// renamed column keeps the old name in its definition, which reads as a different column
// entirely to whoever meets it next.
var mainReminderBin = Migration{
	Name: "20260906205351_main_reminder_bin",
	Up: exec(`
DROP INDEX reminders_live;
ALTER TABLE reminders RENAME COLUMN done_at TO binned_at;
CREATE INDEX reminders_live ON reminders(principal_id) WHERE binned_at IS NULL;
`),
}
