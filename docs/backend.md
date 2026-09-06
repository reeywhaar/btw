# Backend

Go 1.27. Standard library where it reaches; see [stack.md](stack.md) for the three
dependencies that go beyond it.

## Layout

```
main.go              ten lines
internal/
  app/               what this program is: name, version, project, listen address
  cli/               cobra commands
  config/            the environment, parsed once
  store/             both databases, migrations, every SQL statement
  ids/               prefixed, sortable identifiers
  rhythm/            when somebody is nudged — pure
  pick/              what the nudge carries — pure
  webpush/           VAPID, RFC 8291 encryption, one POST
  openrouter/        one question, put to the model an account has a key for
  advise/            asks the companion about somebody's reminders — the impure half
  nudge/             the scheduler: the impure half
  backup/            snapshots the databases and posts them to a backup agent
  api/               HTTP handlers, middleware, SPA serving
web/
  dist/              the built bundle; read from disk at startup, not compiled in
```

`main.go` does two things and then gets out of the way:

```go
func main() {
	time.Local = time.UTC
	os.Exit(cli.Execute())
}
```

### `internal/app` against `internal/config`

`app` is what the binary **is**: its name, the version stamped into it at link time, the
project it comes from, the port it listens on. `config` is what it was **told**: the public
URL, the data directory, the log level.

The line is whether an operator can change it without rebuilding. They can move the port with
`-p`; they cannot make btw listen on anything but `:80` inside the container, any more than
they can make it a different project.

`app` deliberately does not become a home for every constant in the program. The default
rhythm, the session cookie's name and the staleness cap are domain rules and belong beside the
code that enforces them — a `constants` package is a package with no subject, and the first
thing anybody does with one is stop reading it.

The version is a variable rather than a constant for one reason:

```
-ldflags "-X btw/internal/app.Version=$(git rev-parse --short HEAD)"
```

### Dependency direction

```
cli → api → nudge → pick, rhythm, webpush → store
  ↓     ↘                                     ↗
  ↓      ─────────────── store ──────────────
  └→ backup ──────────────────────────────────↗
config is read by cli and passed down; nothing imports it upward.
app imports nothing and is imported freely — that is what a package of three facts is for.
```

`internal/backup` hangs off `cli` rather than `api` because nothing about it is a request:
it wakes on a timer, snapshots the databases and posts them outward. It is the only package
that makes an outgoing request other than `webpush`.

`internal/api` does **not** import `internal/nudge`. It declares a one-method `Nudger`
interface and takes it, which is what keeps the direction clean and what lets a test drive the
"send me one" button without a push service on the other end.

Nothing under `internal/` imports `web`. `internal/api` takes an `fs.FS`, which is both what
keeps that clean and what lets its tests drive an `fstest.MapFS` instead of whatever the real
bundle happens to contain.

**The bundle is read from disk, not embedded.** `serve` hands `api.NewSPA` an `os.DirFS` over
`BTW_WEB_DIR`. `NewSPA` walks it *once*, at startup, and copies every file into a map — so the
bundle is in memory because of what `NewSPA` does, not because of where it came from. What
embedding would cost is the build: it makes every stylesheet an input to the Go compiler, so a
one-line CSS change invalidates the layer that compiles and relinks the binary. The Node and
Go stages of the image do not depend on each other.

## The two pure packages

`internal/rhythm` and `internal/pick` are where the product is, and neither opens a
transaction, reads a clock, or touches the network. Each takes values and a seed and returns
values.

That is not tidiness. It is what makes "a long-ignored reminder rises", "the same thing does
not arrive twice running" and "an empty pool sends nothing" testable against a fixed seed
instead of against a database at four in the afternoon. `pick_test.go` runs four thousand
draws to assert that priority behaves like a probability rather than a sort; that test takes
milliseconds because nothing in it touches SQLite.

They also do not import each other. *When* and *what* are two questions and the whole design
is keeping them apart — see [nudges.md](nudges.md).

## `internal/store`

The only package that writes SQL. Handlers do not build queries, and `nudge` does not reach
past it into the database.

Two handles, `main` and `derived`, never `ATTACH`ed. Full schema and every invariant in
[entities.md](entities.md).

`Candidates` is the one place the split between the packages shows: eligibility is a hard
filter an index can serve, so it is SQL and lives here; weighting is arithmetic over a handful
of rows that wants a fixed seed, so it is `internal/pick`. The struct handed across is
`store.Candidate` — four fields — rather than a `Reminder`, because a pure function should not
be handed columns it has no business reading.

### The error vocabulary

```go
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
	ErrInvalid  = errors.New("invalid")
)
```

Three sentinels, wrapped with a written sentence at the point they are returned, mapped to
status codes in exactly one place in `internal/api`. A handler that switches on a driver error
is a handler that will disagree with another handler eventually.

## `internal/webpush`

Everything about handing one encrypted message to one push service. See [push.md](push.md).

It knows nothing about reminders, nudges or accounts: it takes a `Subscription` and a byte
slice. That is what lets its tests be RFC vectors rather than fixtures.

## `internal/openrouter`

One question, put to the gateway an account has a key for. See
[companion.md](companion.md).

The same seam as `internal/mail`: the store decides what the companion *is* and holds its key,
and this is the half that opens a socket. Nothing here touches the database, and nothing in the
store touches the network.

Its tests hold a real conversation with a server started on a loopback port, for the reason
`internal/mail`'s do. A mocked `http.Client` would assert that `net/http` was called; what is
worth asserting is that a fault arriving inside a `200` is still a failure.

## `internal/advise`

Asks each person's companion what it makes of their reminders, and records the answer for the
weighting to read. See [companion.md](companion.md).

The impure half of the companion, as `internal/nudge` is the impure half of the scheduling. Its
own loop rather than a second job on the scheduler's tick: the two run on different clocks for
different reasons — one is a person's rhythm, the other is a quota — and a slow answer from a
model must never delay a nudge.

The prompt is a **template constant**, not string-building. It is the product here, so it reads
as what the model reads and a change to it is a diff somebody can judge without running
anything.

## `internal/nudge`

The scheduler, and the only thing in the program that reads a clock and talks to the network
in the same function.

A **ticker**, not a chain that reschedules itself after each pass. The spacing is identical,
but a chain has no owner — lose the goroutine between finishing one pass and queueing the next
and the work stops with nothing to notice.

One pass: plan whatever is unplanned, then fire whatever is due. Claiming a slot is the
`UPDATE` itself, so two overlapping passes cannot both send for one slot.

`NudgeNow` and the scheduled path are the same function. A test button that takes a shortcut
tests the shortcut.

Sends fan out across a person's devices concurrently: a phone whose push service is slow must
not delay the laptop's, and other people's slots are waiting behind this pass.

## Testing

- Tests beside sources. `go test ./...` must pass with **no frontend build present**. That is
  free because the bundle is read at startup: a missing directory is the same as an empty one,
  and `NewSPA` falls back to its placeholder page.
- The store's tests run against a temporary file, not `:memory:`. WAL behaves differently in
  memory, and WAL is the thing being relied on.
- Constructors take an injectable clock, so expiry is driven rather than slept through.
- `pick` and `rhythm` are tested against fixed seeds: same seed, same answer.
- `webpush` is tested against RFC 8291's own worked example. Why that matters rather than a
  round trip is in [push.md](push.md#the-test-vector-is-the-point).
- `nudge` is tested end to end against a fake push service holding a real subscription
  keypair, so "a reminder arrives" is asserted by decrypting one.
- `openrouter` is tested against a real server on a loopback port, because the case that looks
  most like success — a fault delivered inside a `200` — is the one a mock would never catch.
- One test asserts no `Access-Control-Allow-Origin` is ever emitted, because that absence is a
  security property.
- One test asserts the rhythm endpoint leaks no scheduling detail, because that absence is a
  product property.

## Logging

`log/slog`, level from `BTW_LOG_LEVEL`.

Log what an operator needs and nothing else: a nudge sent, a nudge that reached nobody, a
device that has gone, a push that failed and why, a login refused, an account created. Never a
token, a cookie value, a push endpoint, or a password — hashed or otherwise.

Reminder text is not logged either. It is the one thing in this database somebody would mind
being read, and an operator has no reason to see it. Nor is a companion's `about`, for the same
reason and more strongly: it is prose about a person's life, written for a model and not for
whoever runs the server.
