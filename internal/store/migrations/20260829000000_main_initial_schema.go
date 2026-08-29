package migrations

var mainInitialSchema = Migration{
	Name: "20260829000000_main_initial_schema",
	Up: exec(`
CREATE TABLE principals (
  id            TEXT    PRIMARY KEY,
  username      TEXT    NOT NULL,
  password_hash TEXT    NOT NULL,
  role          TEXT    NOT NULL CHECK (role IN ('admin','user')),
  created_at    INTEGER NOT NULL,
  disabled_at   INTEGER
);
-- Case-insensitive, so "Misha" and "misha" cannot both be registered. Enforced by the
-- index rather than by remembering to lower() at every call site.
CREATE UNIQUE INDEX principals_username ON principals(lower(username));

-- token_hash, never the token. A lost invitation link is reissued rather than recovered,
-- which is the same stance as sessions and for the same reason: nothing readable — a
-- backup, a heap dump, a swapped page — should ever contain a replayable credential.
CREATE TABLE invites (
  id           TEXT    PRIMARY KEY,
  token_hash   BLOB    NOT NULL UNIQUE,
  created_by   TEXT    REFERENCES principals(id) ON DELETE SET NULL,
  role         TEXT    NOT NULL CHECK (role IN ('admin','user')),
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  accepted_at  INTEGER,
  principal_id TEXT    REFERENCES principals(id) ON DELETE SET NULL
);

CREATE TABLE tags (
  id           TEXT    PRIMARY KEY,
  principal_id TEXT    NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
  name         TEXT    NOT NULL,
  created_at   INTEGER NOT NULL
);
CREATE UNIQUE INDEX tags_name ON tags(principal_id, lower(name));

-- done_at is the only thing that ever ends a reminder, and only a person sets it. Whether
-- it was done or dropped is a fact about the moment the button was pressed, so it is
-- recorded on the nudge that was answered and not here — nothing in the program reads the
-- distinction back.
--
-- last_nudged_at duplicates what the nudge log in derived.db already knows, and the
-- duplication is the point: the log lives in the file that may be deleted, and the floor
-- must not. Delete derived.db and every reminder still knows when it was last raised, so
-- nothing arrives twice in one morning because a disposable file was disposed of.
--
-- note, priority and min_interval have no interface yet. They are here because a column
-- added now is a line in this migration and a column added later is a migration, a
-- backfill and a release.
CREATE TABLE reminders (
  id             TEXT    PRIMARY KEY,
  principal_id   TEXT    NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
  text           TEXT    NOT NULL,
  note           TEXT    NOT NULL DEFAULT '',
  min_interval   INTEGER NOT NULL DEFAULT 86400 CHECK (min_interval >= 0),
  priority       INTEGER NOT NULL DEFAULT 50    CHECK (priority BETWEEN 0 AND 100),
  created_at     INTEGER NOT NULL,
  done_at        INTEGER,
  last_nudged_at INTEGER
);
-- Partial, because every query that matters asks for the live ones.
CREATE INDEX reminders_live ON reminders(principal_id) WHERE done_at IS NULL;

CREATE TABLE reminder_tags (
  reminder_id TEXT NOT NULL REFERENCES reminders(id) ON DELETE CASCADE,
  tag_id      TEXT NOT NULL REFERENCES tags(id)      ON DELETE CASCADE,
  PRIMARY KEY (reminder_id, tag_id)
) WITHOUT ROWID;
CREATE INDEX reminder_tags_tag ON reminder_tags(tag_id);

-- Minutes since local midnight rather than a time string: an integer needs no parsing and
-- cannot be half-valid. The window must sit inside one local day for now; night owls are
-- a later migration and a harder slot planner.
CREATE TABLE rhythm (
  principal_id TEXT    PRIMARY KEY REFERENCES principals(id) ON DELETE CASCADE,
  timezone     TEXT    NOT NULL DEFAULT 'UTC',
  wake_minute  INTEGER NOT NULL DEFAULT 540,
  sleep_minute INTEGER NOT NULL DEFAULT 1320,
  budget       INTEGER NOT NULL DEFAULT 3,
  min_gap      INTEGER NOT NULL DEFAULT 45,
  CHECK (wake_minute >= 0 AND sleep_minute <= 1440 AND wake_minute < sleep_minute),
  CHECK (budget >= 0 AND min_gap >= 0)
);

-- endpoint is globally unique, not unique per principal, and that is a privacy property
-- rather than tidiness. One browser profile has one push subscription; if somebody signs
-- out and somebody else signs in, the same endpoint is offered again. Scoped per
-- principal, both rows would live and the first person's reminders would arrive on a
-- device the second person is holding. Registering an endpoint takes it from whoever had
-- it.
CREATE TABLE devices (
  id            TEXT    PRIMARY KEY,
  principal_id  TEXT    NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
  endpoint      TEXT    NOT NULL UNIQUE,
  p256dh        TEXT    NOT NULL,
  auth          TEXT    NOT NULL,
  label         TEXT    NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  last_ok_at    INTEGER,
  failure_count INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX devices_principal ON devices(principal_id);

-- One row, enforced, so a second keypair is a constraint violation rather than a quiet
-- question about which one is live.
--
-- Losing this invalidates every subscription on the instance at once, and no amount of
-- re-registering repairs it, because those endpoints were bound to the old key. It is the
-- reason main.db is the file that gets backed up.
CREATE TABLE vapid (
  singleton   INTEGER PRIMARY KEY CHECK (singleton = 1),
  private_key BLOB    NOT NULL,
  public_key  BLOB    NOT NULL,
  created_at  INTEGER NOT NULL
);
`),
}
