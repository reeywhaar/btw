package migrations

// The relay an operator configured, and the addresses accounts can be recovered through.
var mainMail = Migration{
	Name: "20260829022502_main_mail",
	Up: exec(`
-- One row, enforced, so a second configuration is a constraint violation rather than a
-- quiet question about which one is live.
--
-- In the database rather than the environment because an operator setting up mail gets it
-- wrong two or three times — a port, a username that turns out to be an address, a From the
-- relay will not accept — and each correction should be a form field and a test send, not a
-- redeploy. The environment is the right place for what must be true before the process
-- starts. This is not one of those.
--
-- The password is stored as written. There is no vault here to seal it under, and a
-- reversible scramble would only make it look protected: whoever can read main.db can
-- already read every password hash in it. The file is the boundary either way, and
-- pretending otherwise is worse than saying so.
CREATE TABLE smtp (
  singleton    INTEGER PRIMARY KEY CHECK (singleton = 1),
  host         TEXT    NOT NULL,
  port         INTEGER NOT NULL,
  tls          TEXT    NOT NULL CHECK (tls IN ('starttls','implicit')),
  username     TEXT    NOT NULL,
  password     TEXT    NOT NULL,
  from_address TEXT    NOT NULL,
  sender_name  TEXT    NOT NULL DEFAULT '',
  updated_at   INTEGER NOT NULL
);

-- Two tables, not a "proved" column on one.
--
-- A nullable flag is one forgotten WHERE clause away from an unproved address being treated
-- as proved, and an unproved address is one somebody typed — possibly somebody holding a
-- borrowed session, pointing recovery at an inbox of their own. Confirming is a move from
-- one table to the other, so the only address anything can recover through is one that was
-- proved.
CREATE TABLE user_recovery (
  principal_id TEXT    PRIMARY KEY REFERENCES principals(id) ON DELETE CASCADE,
  email        TEXT    NOT NULL,
  proved_at    INTEGER NOT NULL
);
-- One address, one account, held by whoever proved it last. Whoever can read that inbox
-- today is who recovery through it would actually reach.
CREATE UNIQUE INDEX user_recovery_email ON user_recovery(lower(email));

-- Keyed by principal, so starting again replaces the attempt rather than adding to it. Two
-- live codes for one account is two chances at the same guess, and starting again is what
-- somebody does when the mail did not arrive.
CREATE TABLE recovery_pending (
  principal_id TEXT    PRIMARY KEY REFERENCES principals(id) ON DELETE CASCADE,
  email        TEXT    NOT NULL,
  code_hash    BLOB    NOT NULL,
  attempts     INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL
);
`),
}
