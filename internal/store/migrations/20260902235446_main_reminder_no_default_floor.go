package migrations

// Reminders stop carrying a floor nobody asked for.
//
// min_interval defaulted to a day, which read as a sensible guess and behaved as a per-
// reminder instruction. It silently capped the budget at the number of reminders somebody
// had: eight reminders at a day apiece cannot fill ten slots however the day is drawn, and
// each floor drifts later with every nudge, so the next morning starts with a smaller pool
// than the evening before. Ten a day quietly became eight, then five.
//
// Nobody could have chosen the old value — there is no interface for it — so every existing
// row is cleared. A floor is now something stated rather than something inherited, and when
// there is one it is obeyed absolutely.
//
// Spacing is not lost with it. The weighting collapses a reminder's chance to nothing the
// moment it is raised and recovers it over a nominal day, the gap between slots holds any
// two nudges apart, and the no-repeat rule stops the same one arriving twice running.
var mainReminderNoDefaultFloor = Migration{
	Name: "20260902235446_main_reminder_no_default_floor",
	Up: exec(`
UPDATE reminders SET min_interval = 0;
`),
}
