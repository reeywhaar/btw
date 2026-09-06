package migrations

// The proxy an operator configured, if any.
//
// One row, enforced, for the reason smtp is: a second configuration would be a quiet question
// about which one is live rather than a constraint violation. There is no list and no order to
// try them in — btw reaches one host, a handful of times an hour, so a fallback chain would be
// machinery guarding a cost that does not exist.
//
// In the database rather than the environment, on the same argument main_mail makes about the
// relay: an operator setting one of these up gets it wrong two or three times — a scheme, a
// token pasted with the address still around it, a socks endpoint that turns out to want a
// username — and each correction should be a form field and a test press, not a redeploy.
//
// The token is stored as written, for the reason the relay's password is. Whoever can read
// main.db can already read every password hash in it; a reversible scramble would only make it
// look protected.
//
// `enabled` is separate from the row existing. Switching a proxy off keeps its address and its
// credential, which is the difference between turning something off to find out whether it was
// the problem and deleting it to find out.
var mainProxy = Migration{
	Name: "20260906053301_main_proxy",
	Up: exec(`
CREATE TABLE proxy (
  singleton  INTEGER PRIMARY KEY CHECK (singleton = 1),
  kind       TEXT    NOT NULL CHECK (kind IN ('proxio','socks5')),
  -- The address with no path: how a request is built out of it is the kind's business, and a
  -- path stored here would be a path one kind silently ignores.
  url        TEXT    NOT NULL,
  -- Empty for kinds that authenticate with a secret alone, which is proxio. Not merely
  -- ignored — a name stored against a kind that never reads one is a field somebody will one
  -- day believe is doing something.
  username   TEXT    NOT NULL DEFAULT '',
  token      TEXT    NOT NULL,
  enabled    INTEGER NOT NULL DEFAULT 1,
  updated_at INTEGER NOT NULL
);
`),
}
