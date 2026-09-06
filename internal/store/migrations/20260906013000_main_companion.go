package migrations

// The model an account has given a key for, and what it is told about them.
//
// One row per account and not a singleton like smtp. A relay is the instance's — an operator
// sets it up once and everybody sends through it — and a companion is not: the key is
// somebody's own, spends their credit, and reads a description of their life that nobody else
// on the instance has any business being scheduled against.
//
// The key is stored as written, for the reason main_mail gives about the relay's password.
// There is no vault here to seal it under, and a reversible scramble would only make it look
// protected: whoever can read main.db can already read every password hash in it.
//
// In main.db because somebody typed it. It survives a derived.db thrown away, which about
// especially has to — it is prose about a person, and asking them to write it again because
// a process restarted is exactly the bookkeeping this product refuses to have.
var mainCompanion = Migration{
	Name: "20260906013000_main_companion",
	Up: exec(`
CREATE TABLE companion (
  principal_id TEXT    PRIMARY KEY REFERENCES principals(id) ON DELETE CASCADE,
  api_key      TEXT    NOT NULL,
  model        TEXT    NOT NULL,
  about        TEXT    NOT NULL DEFAULT '',
  updated_at   INTEGER NOT NULL
);
`),
}
