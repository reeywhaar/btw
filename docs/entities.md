# Entities

Two SQLite databases, both WAL, both opened
`journal_mode(WAL) busy_timeout(5000) synchronous(NORMAL) foreign_keys(ON)` with
`SetMaxOpenConns(1)`. The pragmas are **verified after opening** — a pragma in a DSN is a
request, not a guarantee, and a `foreign_keys=OFF` that was silently ignored permits orphaned
rows for the lifetime of the connection.

They are separate handles and are never `ATTACH`ed. SQLite's atomic multi-database commit
does not hold under WAL, so a transaction spanning both would be a transaction only on paper.
No foreign key crosses the boundary either.

## The split is about backups, not derivation

`derived.db` is a leftover name and not quite the right one: almost nothing in it is derived
from anything. The admission test is simpler and stricter.

> **Can this file be deleted between two runs without anybody losing something they wrote?**

Sessions are not recomputable from the accounts they belong to. They go in `derived.db`
anyway, because the whole cost of losing them is that everybody signs in again.

Holding that seam is what keeps `main.db` small and quiet. It is written when somebody types
something and at almost no other time, which is what a file you take snapshots of should look
like — while the tables that move constantly, the nudge waiting to go out and the nudge log,
churn in the file nobody has to keep.

The split is also the backup policy: `main.db` decides when a copy goes out, and `derived.db`
only rides along. `backup_state` lives here for that reason — the question is whether `main.db`
changed, and recording the answer in `main.db` would change the thing being asked about. See
[deploy](deploy.md#backups).

---

## main.db — what a person typed

### `principals`

```
id, username, password_hash, role, created_at, disabled_at
```

`role` is `admin` or `user`. Two, because a third would need a set of permissions and nobody
has wanted one.

A unique index on `lower(username)` rather than a `lower()` at every call site, so "Misha"
and "misha" cannot both be registered.

Passwords are bcrypt at cost 12. Bcrypt truncates at 72 **bytes**, so the input is
length-checked rather than silently cut — a longer password would otherwise authenticate
against any prefix of itself. Bytes and not runes, because the limit is on the encoded form
and an emoji costs four.

`Authenticate` returns the same error for a wrong password and a missing account, and
compares against a real hash when no account matched, so the two take the same time. Without
that, response latency alone is a list of which usernames exist.

### `invites`

```
id, token_hash, created_by, role, created_at, expires_at, accepted_at, principal_id
```

`token_hash`, never the token. It is readable exactly once, when it is minted, so a lost
link is reissued rather than recovered — the same stance as sessions and for the same reason:
nothing readable should ever contain a replayable credential.

Single use, seven days. Accepting carries `WHERE accepted_at IS NULL`, so two requests racing
on one link produce one account and one refusal rather than two accounts.

### `reminders`

```
id, principal_id, text, note, min_interval, priority, created_at, binned_at, last_nudged_at
```

**`binned_at` is the only thing that ever takes a reminder out of the running, and only a
person sets it.** Until then it keeps coming: not fading with age, not stopping because it has
been ignored twenty times, not expiring because a date went past. There is no completed-at
separate from an archived-at, no history to browse, and no recurring/one-shot distinction to
configure — a one-off is one you bin when you are finished with it, a standing one is one you
never bin.

Thirty days in the bin and the row is deleted outright, which is the only delete here that
happens without somebody asking. See [conventions.md](conventions.md#the-bin-is-a-place-not-a-state).

**`min_interval` is one number doing two jobs**, which is why it earns a column. It is a hard
floor — a reminder inside its interval cannot be drawn at all — and it is the denominator that
decides how fast the same reminder becomes interesting again. See
[nudges.md](nudges.md#eligible).

**Zero is the default, and means the reminder states no floor.** It defaulted to a day, which
read as a sensible guess and behaved as an instruction: it capped the day's budget at however
many reminders somebody had, and drifted later with every nudge so each morning began with a
smaller pool than the evening before.

A floor is a statement about one particular thing — *do not raise this more than weekly* — and
inheriting one nobody made is a preference nobody expressed overruling an appetite somebody
did. Where one is stated it is obeyed absolutely, including against a budget that would like
more. Ordering falls back to a nominal day, which decides nothing about eligibility.

**`priority` is `0..100`, default `50`.** Zero means never: the way to keep something written
down without it ever arriving. That is a person silencing one reminder deliberately, which is
a different act from the system deciding to stop on their behalf — and the second is refused
permanently.

**`last_nudged_at` duplicates what the nudge log already knows, and the duplication is the
point.** The log lives in the file that may be deleted; the floor must not. Delete
`derived.db` and every reminder still knows when it was last raised, so nothing arrives twice
in one morning because a disposable file was disposed of. A test asserts exactly this.

`note` is the description: what the sentence could not hold, capped at 2,000 characters
because it never leaves the app and a lock screen does not bound it. It is deliberately not in
the push payload — a notification carries the sentence alone.

`priority` and `min_interval` have no interface yet. They are here because a column
added now is a line in the first migration and a column added later is a migration, a
backfill and a release.

`reminders_live` is a partial index on `principal_id WHERE binned_at IS NULL`, because every
query that matters asks for the live ones.

### `tags`, `reminder_tags`

Flat, per-principal, unique on `lower(name)`. No interface yet.

A hierarchy needs a parent, a cycle check on write, and somewhere for the grouping to be
worth looking at — which means a management screen. A person here has a dozen reminders and
four tags; a forest over that is furniture.

### `rhythm`

```
principal_id, timezone, window_enabled, wake_minute, sleep_minute, budget, silent
```

One row per person, and **a missing row is the defaults rather than an error**. Nothing
writes a row at account creation: a table where most rows equal the defaults is a table that
has to be kept in step with them, and this way changing a default changes it for everybody
who never had an opinion.

`timezone` is an IANA name. `wake_minute` and `sleep_minute` are minutes since local
midnight. `budget` is not a count but an interval — the waking window divided by it — and
`silent` asks for a notification without a sound.

**The window may cross midnight**, and `12:00`–`04:00` is sixteen waking hours rather than
minus eight. Two ends that are equal mean the whole day, which is the natural thing to mean
with two controls that each name an hour and the same as switching the window off. Both
minutes are stored `0..1439` — midnight has two names and storing it as `1440` would make
`00:00`–`24:00` a window of nothing.

**`window_enabled` is off for somebody who wants nudges at any hour**, and it defaults on,
because an account upgraded into this column should not start being woken at four in the
morning. The hours are kept either way rather than being folded into `0..1440`: unchecking
the box would otherwise lose whatever somebody chose, and typing `09:00` and `22:00` back in
is exactly the bookkeeping this product is trying not to have.

Everything that plans goes through `Rhythm.Window()`, which answers how many minutes long the
waking day is — the whole day when there is no window, and the wrapped length when there is
one. So "no window" and "a window through midnight" are each one answer in one place rather
than a condition every caller has to remember.

### `devices`

```
id, principal_id, endpoint, p256dh, auth, label, client_id,
created_at, last_ok_at, failure_count, last_error
```

**`endpoint` is globally unique, not unique per principal, and that is a privacy property
rather than tidiness.** One browser profile has one push subscription. If somebody signs out
and somebody else signs in, the same endpoint is offered again — and scoped per principal,
both rows would live, so the first person's reminders would arrive on a device the second
person is holding. Registering an endpoint takes it from whoever had it, and resets its
failure history, because it is now somebody else's device.

Registration is `ON CONFLICT (endpoint) DO UPDATE` rather than read-then-write, so two tabs
registering at once produce one row.

`last_error` is kept because "this device stopped receiving" without "and here is what the
push service said" sends somebody to logs they do not have.

**`client_id` is what makes one browser one row.** An endpoint identifies a subscription
rather than a browser, and browsers rotate subscriptions unprompted — so upserting on the
endpoint alone left the old row in place, both live, and one nudge went out twice. See
[push.md](push.md#one-browser-one-device).

The endpoint **never leaves the process**. It is a capability: anybody holding it and a VAPID
key can put text on that lock screen. A test asserts it never appears in a response.

### `companion`

```
principal_id, api_key, model, about, updated_at
```

The model an account has given a key for, and what it is told about them. One row per
principal and **not a singleton like `smtp`** — the key spends its owner's credit and `about`
describes one person's life, neither of which is the instance's to hold. `ON DELETE CASCADE`,
so forgetting an account forgets its key with it.

`api_key` is stored as written, for the reason `smtp.password` is: there is no vault here, and
a reversible scramble would only make it look protected. It is never sent back out — the API
carries `key_set` instead.

`model` is **empty when nobody chose one**, and that is a state rather than a gap: it means
"whatever the default is now".

Filling the default in on the way into the row is the tidier-looking alternative, and every row
then names the model it will be asked with. It costs the difference between having chosen and
not. An account is pinned to whichever default it first met, and the form comes back holding a
model name where somebody left a blank — so their next save picks it on their behalf.

The fallback is resolved at the moment of asking, by `Settings.ModelOrDefault`. A model somebody
did type is kept whether or not it equals the default, because typing it is a choice.

`about` is bounded at 2,000 runes, counted in runes rather than bytes because a paragraph is
not four times as long for being written in a four-byte script. The bound is a token bill as
much as a column width: every word of it rides on every request the companion makes. See
[companion.md](companion.md).

### `vapid`

```
singleton, private_key, public_key, created_at
```

One row, enforced by `PRIMARY KEY CHECK (singleton = 1)`, so a second keypair is a constraint
violation rather than a quiet question about which one is live. Generated on first use;
insertion is `DO NOTHING`, so two processes starting together end with one keypair and the
loser reads the winner's rather than failing to start.

**Losing this invalidates every subscription on the instance at once**, and no amount of
re-registering repairs it, because those endpoints were bound to the old key. It is the
single strongest reason `main.db` is the file that gets backed up. Rotation is not a feature.

---

## derived.db — what the running process accumulated

`principal_id` references `main.db` and there is no foreign key, because no constraint can
cross a database. A row pointing at a deleted account is garbage to be collected, not an
inconsistency to be repaired.

### `advice`

```
reminder_id, version, body, advised_at
```

What the [companion](companion.md) made of one reminder. Here rather than in `main.db` because
every row is recomputable from what is there — the reminders, the account's `about`, its rhythm
— and nobody typed a word of it. It is also the half that churns: `main.db` is written when a
person types something, and advice is rewritten whenever anything moves.

**One JSON body under a version, not a column per field.** This is a model's answer, and what is
worth asking for changes whenever the prompt does — the shape has moved more than once inside a
week. A column per field makes each of those a migration, and each migration carries rows
written under an older idea of what the answer was.

A row whose `version` the reader does not recognise **is no advice at all**. That is not a
fallback, it is the point: the row is recomputable, the account is already marked stale, and the
alternative is a reader guessing at a field that meant something else when it was written. The
version is a column rather than a field inside the body so that reading it costs no parsing.

The body holds `curve` — seven days of forty-eight numbers from 0 to 1, one per half hour of the
person's local week — along with `exclusive` and `categories`, **neither of which anything
reads**, and `shape` when the answer could not be read at all. The two unread fields are kept
because asking again is the expensive part, at fifty questions a day on a free key, so
discarding half an answer already paid for would be paid back one request per reminder.

The grid is not what the companion is asked for. It answers with the stretches it has an
opinion about — see [companion.md](companion.md#what-it-asks-for) — and those are expanded here,
with everything unmentioned left at 0.5. Storing the expansion rather than the spans is
deliberate: the weighting reads one half hour, tens of times a day, and should not re-derive a
week to do it.

Replaced **wholesale** for the whole set each time, not row by row: a reminder the companion was
asked about and said nothing for has to lose what it was told last time, and a loop of upserts
leaves exactly that behind.

No foreign key to `reminders`, because no constraint can cross a database. A row for a deleted
reminder is garbage to collect — and is collected, when a reminder is deleted outright rather
than merely finished with.

### `advice_state`

```
principal_id, stale, advised_at, attempted_at, error, limited, alerted, version
```

Whether a person's advice is worth what it says. A **flag rather than a timestamp comparison**:
"has anything changed since we last asked" is a question about writes scattered across two
databases — a reminder added, a note written, an `about` rewritten, a rhythm moved — and the
cheapest true answer is for each of those writes to say so.

**Absence means stale.** A `derived.db` thrown away takes the advice with it, so the state
saying "already asked" must not be the thing that survives, and a fresh instance asking once
for everybody is exactly right.

A **failure leaves it stale** and records the reason. Clearing it would mean a key that stopped
working quietly froze everybody's advice at whatever it last said; the loop's own interval is
the only thing throttling the retry, which is what stops a broken key spending a day's quota in
a minute.

`limited` is whether that failure was a quota rather than a mistake — a column rather than a
prefix on the message, because the message is the gateway's own words and matching on those
breaks the first time OpenRouter rewords a sentence. The two are different states for whoever
reads them: a rejected key wants somebody to fix it, and a quota wants nothing at all.

`alerted` is whether the person has already been told, so a broken key is one notification per
episode rather than one every half hour until they turn notifications off. Lowered by a round
that works, so a key fixed and broken again months later is worth telling them about again.

`advised_at` and `attempted_at` differ exactly when the last attempt failed, which is what lets
the interface say "answered an hour ago, and the try since then failed" rather than picking one
and being wrong half the time.

### The version is what makes a shape change safe

`advice_state.version` records which shape the last answer arrived under, and an account whose
version is not the current one **reads as stale** whatever the flag says.

That is the half of the versioning that is easy to leave out, and leaving it out is silent.
Ignoring rows written under an older shape is obviously right — the reader would otherwise be
guessing at a field that meant something else. But an account already advised keeps `stale = 0`,
so it would never be asked again: its advice ignored, nothing fetching a replacement, and
nothing anywhere saying so. The rows would simply stop counting.

With the version here, changing [store.AdviceVersion] re-asks everybody by itself. **No
migration, and no remembering to write one** — which matters because the shape has already
changed three times, and each of those would otherwise have been a migration whose only job was
one `UPDATE`. The default of `0` matches no real version, so the migration that added the column
needed no statement either.

### `sessions`

```
id_hash, principal_id, created_at, last_seen_at, expires_at
```

Keyed by `sha256` of the cookie value. The hash is what is stored and what is compared, so
nothing readable — a backup, a heap dump, a swapped page — ever contains a replayable
credential, and the lookup is timing-safe without trying to be.

Sliding one-week expiry, refresh throttled to once an hour, swept every ten minutes. The
throttle is not an optimisation: without it a polling SPA rewrites the row and emits a
`Set-Cookie` on every request for a window measured in days. An expired row is deleted on the
way past rather than left for the sweep — the request that found it is already holding the
connection.

**Sessions in the other database is the one place this split costs something.** Changing a
password and ending that account's other sessions cannot be one transaction. The resolution
is ordering rather than atomicity: **end the sessions first, then write the password.** Fail
between the two and somebody has been signed out without their password changing — visible,
harmless, and retryable by pressing the button again. The other order leaves live sessions
behind a changed password, which is a security bug rather than an inconvenience.

Worth being honest that sessions are not where the churn is; `last_seen_at` moves at most
once an hour per session. They are here because they are disposable, which is the only test
this seam applies.

### `pending_nudge`

```
principal_id, at    primary key (principal_id)
```

One row per person, and only while a nudge has been decided on and not yet sent. The loop
asks every five minutes whether somebody is owed one; firing where the answer turns yes would
put every nudge on a tick boundary, so the answer schedules one a random moment later and the
loop leaves it alone while it stands.

This replaced a `slots` table holding a whole day of instants drawn in advance. See
[nudges.md](nudges.md#what-this-replaced) for why the promise it existed to keep was not
worth its machinery.

Claiming is the `DELETE`, so two overlapping ticks cannot both send the same one.

### `nudges`

```
id, principal_id, reminder_id, sent_at, acted_at
```

`acted_at` says whether a nudge was answered, and there is nothing recording *how* — there is
one way now. The column that held `done` or `drop` was the only place that distinction survived
and nothing ever read it, so it went with the second button.

**A row exists only if something actually reached a push service.** The id is minted before
sending — it has to travel inside the encrypted payload, because the notification's buttons
post back to it — but the row is written afterwards. A nudge recorded for a message that
never left would spend the reminder's floor on a notification nobody got, which is the one
failure that looks exactly like the product ignoring you.

Two databases, so recording the nudge and stamping the reminder are two writes with no
transaction across them. The stamp goes second: a log without a stamp lets the reminder be
drawn again inside its floor, which somebody notices; a stamp without a log makes a reminder
wait out its interval for a nudge missing from the history, which nobody does.

Acting twice is not an error and does not move the record. A notification that has sat on a
lock screen since yesterday can be answered after the thing was already ended in the app, and
the person pressing it wanted it ended either way.

### There is no queue

There was going to be a `jobs` table, and it is worth writing down why there is not.

A queue buys retries and backoff. btw's only background work is one POST, and its failure
modes are already settled without one: a missed slot is missed, `TTL: 3600` makes the push
service drop what it could not deliver within the hour, and a `410` deletes the device rather
than retrying it. What was left for a queue to do was retry a `429` — and a nudge worth
retrying twenty minutes later is a nudge worth sending twenty minutes later, which is the
next slot.

So the scheduler sends inline, fanning out across a person's devices concurrently under a
ten-second client timeout. When a second kind of background work appears, the queue appears
with it — and it goes in `derived.db`, which is the opposite of where the usual argument puts
one. That argument is that a queue is not rebuildable from anything and that work lost to a
rebuilt cache is the failure a queue exists to prevent. It holds for work that is still worth
doing an hour later. It does not hold here: a push that outlived the file it was queued in is
a nudge nobody wants delivered.

## Migrations

Go files under `internal/store/migrations/`, one per change, named
`<timestamp>_<database>_<name>.go` and tracked by `PRAGMA user_version`. Each runs in its own
transaction that also stamps the version, so a failure leaves the database at the last version
that fully applied rather than half way through one.

Sorted by name, which begins with a timestamp, so a file added out of order still applies in
the right place. The declared order in `migrations.go` is documentation; the sort is what
decides.

A `user_version` ahead of the build is a startup error rather than a guess. A binary that does
not understand the schema in front of it cannot know which of its statements are still
correct, and downgrading is not supported — saying so beats corrupting data quietly.

Go rather than `.sql` because a migration gets a handle on the *other* database and can move
data across. Nothing has needed to yet; the affordance is why the shape was chosen.

**Never edit a released migration.** Every deployment past it has already recorded it as
applied and will skip the edit forever, so the schema in front of the code silently stops
matching the schema in the file — on those databases and no others, which is the worst kind
of difference to go looking for.
