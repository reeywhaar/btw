package migrations

// The model an account gets when it has not chosen one.
//
// A compile-time constant cannot survive OpenRouter retiring a slug: every account that never
// chose would break at once, with no way to fix it short of a redeploy. A singleton row is the
// escape hatch, and empty means the constant.
var mainCompanionDefault = Migration{
	Name: "20260910234317_main_companion_default",
	Up: exec(`
CREATE TABLE companion_default (
  singleton  INTEGER PRIMARY KEY CHECK (singleton = 1),
  model      TEXT    NOT NULL,
  updated_at INTEGER NOT NULL
);
`),
}
