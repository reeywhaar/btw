# Companion

btw runs no model. It puts a question to one an account has a key for, through OpenRouter, and
everything here is about where that key lives and what the model is told.

Nothing asks a question yet. What exists is the configuration and a button that proves it
works — built first on purpose, because a feature whose credential is wrong fails in a
background loop at three in the morning, and the only way to find that out should be a form
field and a press.

## The companion is an account's, not the instance's

**This is the one place btw departs from the [relay](mail.md).** A relay is instance-wide and
an administrator's: an operator sets it up once and everybody sends through it. A companion is
not, for two reasons that point the same way.

The key **spends its owner's credit**. An instance-wide key would mean whoever configured it
paying for everybody's scheduling, with no way to see who spent what — and the free models
that make this feature approachable are rate-limited per key, so one busy account would starve
the rest.

The description is **about one person's life**. `about` holds somebody's hours, their habits
and what they can stand doing at once. That is not a thing to hold once for an instance and
apply to everybody in it, and it is not a thing an administrator should be able to read.

So: one row per principal, `ON DELETE CASCADE`, and every route behind `requireSession` rather
than `requireAdmin`. The interface lives in settings beside the rhythm, which is the other
answer somebody gives about their own days.

## Where the configuration lives

**In the database, set from settings, not in the environment** — the same argument
[mail.md](mail.md#where-the-configuration-lives) makes about the relay, and more so. The
environment is right for what must be true before the process starts. A key belonging to one
of several accounts, changed whenever that person rotates it, is not one of those.

The key is **stored as written**, for the reason the relay's password is. There is no vault
here to seal it under, and a reversible scramble would only make it look protected: whoever can
read `main.db` can already read every password hash in it. The file is the boundary either way.

It is **never sent back out**. `GET /api/companion` carries `key_set` and no key. Saving with
an empty key keeps the stored one, which is what lets somebody change their model or rewrite
their description without retyping a credential the form was never given.

`about` **does come back out**, and has to: it is a text field somebody edits, and a form that
could not show what it already said would be a form that asks for it again every time.

## What is not validated

**The shape of the key.** A `sk-or-` prefix rule would refuse a valid key the day OpenRouter
changes the format, and the only thing that settles whether a key works is using it. That is
what the test button is for.

**Whether the model exists.** There are hundreds and the list moves weekly. A slug is a string
until it is asked, and asking is one press away.

Both are the same decision: refuse nothing that a single request would answer definitively, and
make that request cheap to run.

## The default model

`minimax/minimax-m3:free`. Free, so somebody who has just found out this feature exists can try
it without a balance.

The `:free` variants of a model accept **far fewer parameters** than the paid slug of the same
name — `minimax/minimax-m3:free` advertises `response_format` but not `structured_outputs`,
along with no `stop`, no `top_k` and none of the penalties. Anything that asks for a strict
JSON schema has to check rather than assume, or it gets prose back from a request that looked
like it demanded otherwise. The endpoint's own `supported_parameters` is the list worth
believing.

Free keys are limited to **20 requests a minute and 50 a day**, or 1000 a day once $10 of
credit has ever been bought. That is a ceiling on how often anything here may run, and the
reason a `429` is reported as its own kind of refusal rather than folded into a general failure.

## Trying it

`POST /api/companion/test` sends one completion and reports what answered.

**A real completion rather than `GET /api/v1/key`**, which would prove the key is live and
nothing about the model — and the model is the half somebody is likelier to get wrong. One
completion proves both, and on the default model it costs nothing.

**Against what is in the form, not what was last saved.** The button lives inside the edit
dialog, beside the fields it is about, and a button beside a value somebody has just corrected
has to mean that correction — otherwise pressing it teaches them the wrong thing about the key
they are looking at. It was a separate dialog over the saved settings first, which is where the
relay's still is; the relay has no second copy of its password to disagree with.

It reconciles under **the same rule a save follows**: an empty key means the stored one, an
empty model means the stored model or the default. So what was tried is what saving would
store, which is the whole of what keeps the shortcut honest — and trying stores nothing itself,
so a key that turns out not to work is not left behind by having been tested.

It reports **which model answered**, which is not always the one asked for: OpenRouter falls
back between providers and a slug can resolve to a variant. "You asked for X and Y answered" is
worth knowing before trusting anything the companion says.

A refusal is a **`502` carrying the gateway's own words**, for the reason
[mail.md](mail.md#sending) gives about a refused send: everything on this side worked and
something upstream did not, and a `500` sends somebody through the wrong logs. A rejected key, a
model with no credit, a slug that does not exist and a rate limit are four different afternoons.

### The status code is not the whole story

OpenRouter answers **`200` with the fault in the body** when a provider dies after generating
part of an answer. A `resp.StatusCode` check alone reports that as a success, so the fault is
looked for in two places — the top level and inside a choice — before the status is consulted
at all. A test covers both, because this is exactly the case that looks like it works.

## Tested against a gateway, not a mock

`internal/openrouter` starts a real HTTP server on a loopback port, the same way
`internal/mail` starts a real SMTP one and for the same reason. A mocked `http.Client` would
assert that `net/http` was called. What is worth asserting is that the key travels as a bearer
token, that a fault inside a `200` is still a failure, that the model which answered is
reported rather than the one asked for, and that a proxy's HTML error page survives to the
caller instead of being summarised into "bad gateway".

## What this does not do yet

**Nothing asks the model anything.** The next step is the metadata: what the companion says
about a reminder — when it would land well, what it is about, whether it wants somebody's full
attention — and how that becomes a multiplier on the weight
[nudges.md](nudges.md) already computes. It multiplies rather than replaces, so a reminder with
no metadata weighs exactly what it weighs today and the feature stays optional at the level of
one reminder rather than one account.

**Nothing recrawls.** When it does, it will be on a flag rather than on every write: a
`needs_recrawl` set true when the account's `about` changes, when a reminder is added, removed
or given a description, and when the rhythm changes — with a background loop checking it
periodically and doing the work in one pass. The same argument the
[backup pusher](deploy.md#backups) makes about its interval applies here and costs more:
somebody writing down six things in a minute must be one crawl, not six, because each one is a
request against a key with fifty a day in it.

That flag belongs in `derived.db`, not beside the key. It is process state rather than
something a person typed, and it passes the admission test in
[entities.md](entities.md#the-split-is-about-backups-not-derivation) — a lost flag costs one
unnecessary crawl, and its absence meaning "crawl" is the safe default for a `derived.db`
thrown away along with the metadata it was tracking.
