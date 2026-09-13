package migrations

// Which service an account's key is for, and one default model per service.
//
// The key decides the service — it works with one of them and not the other — so this is the
// account's, beside the key, rather than the instance's. Existing rows are OpenRouter, which
// is what they were.
//
// companion_default stops being a singleton for the same reason a slug is not portable:
// `minimax/minimax-m3:free` means nothing to the Hugging Face router, so an instance wanting
// a default for both needs two rows.
var mainCompanionProvider = Migration{
	Name: "20260913021845_main_companion_provider",
	Up: exec(`
ALTER TABLE companion ADD COLUMN provider TEXT NOT NULL DEFAULT 'openrouter';

CREATE TABLE companion_default_new (
  provider   TEXT    PRIMARY KEY,
  model      TEXT    NOT NULL,
  updated_at INTEGER NOT NULL
);
INSERT INTO companion_default_new (provider, model, updated_at)
  SELECT 'openrouter', model, updated_at FROM companion_default WHERE model != '';
DROP TABLE companion_default;
ALTER TABLE companion_default_new RENAME TO companion_default;
`),
}
