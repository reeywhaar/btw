# Conventions

## Naming things in the product

Two words carry the whole design and are easy to run together.

A **reminder** is the standing thing somebody wrote down: its text, how often it is willing
to be raised. It keeps arriving until the person ends it.

A **nudge** is one delivery of one reminder — a notification that arrived at a particular
minute on a particular device. Reminders persist; nudges happen.

| word | meaning |
| --- | --- |
| **reminder** | The standing thing. `reminders` in the database, `Reminder` in Go |
| **nudge** | One delivery of one reminder. Both a noun and the verb for sending one |
| **slot** | A minute, drawn at random ahead of time, when a nudge is due |
| **rhythm** | One person's answer to how often, and between which hours |
| **device** | A browser that has agreed to receive nudges. What a person sees in settings |
| **principal** | An account, admin or user |
| **done** | Finished with, because it was done |
| **drop** | Finished with, because it turned out not to be wanted |

Never "task", never "todo", never "item". Never "notification" in code — that is the
browser's word for what a nudge becomes once it is on screen, and keeping the two apart is
what stops `sendNotification` and `sendNudge` both existing.

### done and drop

Both set `reminders.done_at` and neither is a delete. They are two words for one outcome
because they are two different acts — "I did it" and "I do not want this" — and a product
with only *Done* makes ending something you never did feel like a small lie, which is enough
friction that people stop pressing anything.

Which one was pressed is recorded on the **nudge**, not on the reminder, and nothing in the
program reads it back. It is kept because the two are genuinely different and a log that
flattened them could not answer a question that has not been asked yet.

## Commit messages

**One line. No body, no trailers, ever.**

A full declarative sentence saying what the change accomplishes. Capitalized, no trailing
period, no prefix, no conventional-commit tag, no ticket number, and no `Co-Authored-By`.

```
Let a reminder that was dropped by mistake be put back
Say why nudges are silent rather than offering a button that leads nowhere
Bind a nudge's id into the payload, so a notification answers for its own delivery
Stop a reminder written in March taking every slot in June
```

Two clauses joined by "so" or "and" are common and welcome — the second says why the first
was worth doing. What the message must not be is a label: not `fix: push`, not
`update store`, not `wip`.

The single line is not a length limit fighting the explanation; it is where the explanation
goes. If a change needs three paragraphs of justification, those paragraphs belong in a
comment beside the code they justify, where somebody reading that code will actually meet
them — a commit body is read once, by whoever runs `git log`, and never again.

A change that genuinely cannot be said in one sentence is usually two changes.

## Comments

Comments explain **why**, never what. A comment restating the line under it is noise; a
comment recording the reason a line is written the way it is prevents somebody "simplifying"
it back into a bug.

A comment is as long as the surprise it explains and no longer. Most lines need none; the
ones that earn a paragraph are the ones where the obvious version is wrong, and the
paragraph is what stops it being written back.

Say it once. The same reason repeated in a component, its test and its caller is three
copies to keep true, and the two that fall behind are the ones somebody will read.

When a decision has a real alternative, say what the alternative was and what it cost. That
is the sentence that is impossible to reconstruct later.

Package doc comments are expected and are the right place for the argument a package exists
to make — not what the package contains, which is readable, but why it is a package.

## Identifiers

Prefix plus 26 characters of Crockford base32 over 16 bytes: a 6-byte big-endian millisecond
timestamp then 10 random bytes. ULID's layout, so ids sort chronologically and `ORDER BY id`
is a time order. Crockford's alphabet omits `I`, `L`, `O` and `U`, so an id cannot be misread
between similar glyphs or accidentally spell something.

| prefix | entity |
| --- | --- |
| `p_` | principal |
| `i_` | invite |
| `t_` | tag |
| `r_` | reminder |
| `n_` | nudge |
| `d_` | device |

Ids are opaque and never parsed back. The prefix is for the human reading a log line, and
for `ids.Valid`, which refuses a malformed id before it reaches a query — so a typo comes
back as a `400` rather than an empty result set that looks like a `404`.

## Time

- `main.go` pins `time.Local = time.UTC`. Everything stored and everything logged is UTC.
- Stored as **Unix seconds in an `INTEGER` column**. Not text, not milliseconds.
- btw is the one project here that genuinely needs local time, and it exists at exactly one
  boundary: `internal/rhythm` turns a person's waking window into instants, against an IANA
  name held per account. Nothing else in the program knows what time it is anywhere.
- Minutes-since-local-midnight, as an integer, is how a waking window is stored. An integer
  needs no parsing and cannot be half-valid.

## Go

- `gofmt` clean; CI fails on anything it would rewrite.
- Tests beside sources as `*_test.go`. No `tests/` directory.
- Errors wrap with `%w` and name what was being done: `fmt.Errorf("plan %s: %w", date, err)`.
- One error vocabulary, in `internal/store`, that handlers map onto status codes:
  `ErrNotFound`, `ErrConflict`, `ErrInvalid`. Built through `store.NotFound`,
  `store.Conflict` and `store.Invalid` so the message is a sentence written for the person
  who will read it, not `not found: no reminder r_1`.
- `context.Context` first parameter on anything that can block.
- Injectable clocks: `store.SetClock` takes a `func() time.Time` so tests drive expiry
  without sleeping. `webpush.Sender.SetClient` is the same idea for the network.
- A function that returns "this succeeded, and also something happened" returns a bool, not
  a sentinel error. `store.Session` returns `(Session, bool, error)` because the first
  version folded "the cookie wants re-issuing" into the error, and the obvious
  `if err != nil { return 401 }` would have signed out every session it had just extended.

## TypeScript

- Prettier, default settings, `npm run format`.
- Components are `PascalCase.tsx`; everything else is `camelCase.ts`.
- Imports use the `@app/*` alias, never `../../`. An import that names where a module *is*
  stops depending on where the importer sits.
- `strict`, `noUncheckedIndexedAccess`, `noUnusedLocals`, `noUnusedParameters`,
  `verbatimModuleSyntax`.
