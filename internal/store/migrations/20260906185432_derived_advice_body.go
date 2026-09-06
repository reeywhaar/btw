package migrations

// What the companion said, stored as one JSON body beside the version of the shape it is in.
//
// Columns per field was the first version of this table and it was the wrong instinct. This is
// a model's answer: what is worth asking for changes whenever the prompt does — a list of
// weekly windows became a curve over the week within days of the first one — and a column per
// field means a migration per prompt tweak, each carrying data written under an older idea of
// what the answer was.
//
// So the shape lives in the code and the version says which shape a row is in. **A row whose
// version the reader does not recognise is no advice at all**, which is exactly right: it is
// recomputable, and the alternative is a reader guessing at a field that meant something else
// when it was written.
//
// `advice_state.version` is the other half of that, and the half that is easy to forget.
// Ignoring old rows is not enough on its own: an account already advised keeps `stale = 0` and
// would never be asked again, so its advice would be gone with nothing to fetch it back.
// Recording which version an answer arrived under makes a bump re-ask everybody by itself —
// `0` matches no real version, so this migration needs no UPDATE, and no future bump needs a
// migration at all.
//
// The table is dropped rather than migrated for the same reason it can be: every row here is
// recomputable, which is the whole admission test for derived.db, so the cost is one round of
// questions.
var derivedAdviceBody = Migration{
	Name: "20260906185432_derived_advice_body",
	Up: exec(`
DROP TABLE advice;

CREATE TABLE advice (
  reminder_id TEXT    PRIMARY KEY,
  -- Which shape body is in. A column rather than a field inside it, so that reading the
  -- version costs no parsing and a query can filter on it.
  version     INTEGER NOT NULL,
  body        TEXT    NOT NULL,
  advised_at  INTEGER NOT NULL
) WITHOUT ROWID;

-- Which shape the last answer arrived under. Zero is no shape at all, so every account that
-- has ever been advised is stale from here without a statement saying so.
ALTER TABLE advice_state ADD COLUMN version INTEGER NOT NULL DEFAULT 0;
`),
}
