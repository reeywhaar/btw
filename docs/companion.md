# Companion

btw runs no model. It puts a question to one an account has a key for, through OpenRouter, and
everything here is about where that key lives and what the model is told.

What it is asked, and what is done with the answer, is below. The configuration and its test
button were built first on purpose: a feature whose credential is wrong fails in a background
loop at three in the morning, and the only way to find that out should be a form field and a
press.

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

## What it is asked

Once per person, not once per reminder. One question carries the whole open list, along with
what they wrote about themselves and the hours they are reachable in, and the answer comes back
as one entry per reminder. Asking separately would be one request each against a quota of
fifty a day, and would also throw away the only context that makes the answers coherent — that
these forty things belong to one week.

The prompt is a **template constant** in `internal/advise`, not string-building. It is the
product here: what the model is told is the whole of what distinguishes good advice from a
guess, so it is written as prose in one block that reads as what the model reads, and a change
to it is a diff somebody can judge without running anything.

### What it asks for

A list of the **stretches of the week it has an opinion about**, each with a weight:

```json
[{"days": "all",     "from": "02:00", "to": "13:00", "v": 0.05},
 {"days": "mon-fri", "from": "13:00", "to": "19:00", "v": 0.3},
 {"days": "all",     "from": "20:00", "to": "01:00", "v": 0.9}]
```

Anything unmentioned is 0.5, which is a multiplier of exactly 1 — so one line is a complete
answer and an empty list is a real one. What the weight does is in
[nudges.md](nudges.md#the-companions-advice-is-the-last-term-and-only-a-multiplier).

This is the third shape the question has had, and the two it replaced were both wrong in ways
worth writing down, because they are not obvious and they cost a day each to find.

**Windows** — a list of hours with no weight — made the model answer two questions at once,
*when* and *how strongly*, and it was bad at the second: every answer was in-or-out, so a
reminder was boosted hard or damped hard with nothing between.

**A number per half hour** — seven arrays of forty-eight — fixed that and broke something
worse. Two things, in fact.

*It cannot be counted.* Asked for 7×48, a free model sent 7×24, then an object keyed by day
name, then 7×49 — three attempts, three shapes. There are no landmarks in a list of numbers, so
a model that loses its place cannot notice and neither can the parser.

*Saying nothing costs the same as saying something.* This is the one that actually decided it.
With 336 numbers the considered answer and the lazy one are the same length, so a model under
pressure writes `0.5` three hundred and thirty-six times and produces something data-shaped
that says nothing at all. It did exactly that, for every reminder, and the screen drew it as a
flat grey week. With spans an opinion is one line and no opinion is no lines.

Spans have neither problem. Nothing is counted — a span names its own hours — and the format
is sparse, so effort and length rise together.

**Fixed buckets** would have solved the counting too, and are the obvious alternative. They
were refused because their edges are somebody else's: "the middle of the morning to the middle
of the day" is `10:00` to `13:00`, and no set of named buckets says that.

Five things in the question are load-bearing and have tests asserting they are still said.

**That the answer does not decide whether a reminder is shown.** Without that sentence a model
reads the job as "when is this due", which is the one question btw exists to refuse.

**Say only what you have an opinion about.** The sparseness has to be stated or a model
describes the whole week out of politeness, and the incentive that makes this format work is
gone.

**That almost everything has some shape.** Washing up is worse at four in the morning; anything
needing a shop is worse when shops are shut. Without this the empty list becomes the default
rather than the exception.

**That a later stretch wins where two overlap.** It is what lets a model say the broad thing
and then narrow it, which is how anybody describes a week.

**Two worked examples**, for "wash dishes" and "buy stamps". The single most effective thing in
the prompt: it is the difference between a model knowing what an answer looks like and guessing.

The categories are **glossed rather than listed**, and the glosses carry scheduling meaning a
bare noun loses: *errands* is "bound by opening hours" and *chores* is "bound by nothing but
being awake", which is the whole reason they are two words.

### The answer is read leniently

A `:free` model in JSON mode is a request, not a guarantee. The parser accepts a bare array as
well as the `{"results": []}` wrapper, strips markdown fences, and finds the object inside a
sentence of preamble.

One malformed field costs that field, one malformed span costs that span, and one malformed
entry costs that entry. Each is decoded on its own: a typed struct fails the *whole document*
over one bad value, so a round covering forty reminders would come back with nothing and look
exactly like a model that had said nothing at all.

Within a span, `days` reads `mon`, `Monday`, `mon-fri`, `sat,sun`, `weekends`, `all`, and
wraps, so `fri-mon` is the stretch it names. Times read `9`, `09:00` and `24:00`. A `to` at or
before its `from` runs past midnight. An absent `days` means every day, because a model writing
one line about an evening means every evening and refusing it over a missing field would refuse
the commonest answer.

**Every field has aliases** — `start`/`end` beside `from`/`to`, `value`/`weight`/`score` beside
`v` — and that is not tidiness. A model told to write `from` writes `start` often enough to
matter, and the two failures that causes are not equally visible: a missing `from` was refused
outright and said so on screen, while a weight written as `value` read as **the zero value**. A
span the model meant as 0.9 became 0.0 and was drawn as a confident graph saying the opposite.
The silent one is the reason the aliases exist, and why a span whose weight cannot be read is
dropped rather than taken as zero: zero is not an absence, it is the strongest opinion on the
scale and the wrong one.

**An empty list is a real answer** — no opinion anywhere — and not a failure. That distinction
carries more weight than it looks, because of what happens to a round that fails.

### What one round cannot read, the round before it keeps

A model that mangles one reminder out of forty used to cost that reminder everything it had,
leaving it weighed at exactly 1 until some later round happened to get it right. Stale advice is
worth more than none: it was true when it was written, and the reminder it is about did not
change — the *answer* failed, not the question.

So an entry whose curve could not be read, or that never arrived, keeps the curve it had.
Whatever the round *did* manage to say about it is still taken, since the categories can be
readable when the curve is not; only the curve comes from before.

A deliberate "no shape" still overwrites, because an empty list of spans reads as a whole week
of neutrals rather than as a failure. That is the whole of the distinction: **the only thing
carried forward is an answer that could not be understood or did not come.**

A pass says how many it carried, beside how many it answered and dropped.

**The array shapes are still read**, because a model answers the question it expected at least
as often as the one it was given:

| what arrives | how it is read |
| --- | --- |
| 7×48 | the week, half-hourly |
| 336 flat | the same numbers in the same order |
| 7×24 | the week by the hour; each value covers both of its half hours |
| an object keyed by day name | Monday first, all seven or none |
| one day of 48 or 24 | that day, all week |
| a day one value out | trimmed or held at the end — see below |

None of those invents a number. The **one out** case is a tolerance rather than a repair, and
it is allowed for a reason particular to this data: the question asks for broad stretches and
says a curve swinging between neighbouring half hours is describing precision the model does
not have. An answer whose neighbours are meant to be alike cannot be ruined by half an hour of
misalignment. Two out is no longer a slip and is refused.

What is refused is anything **ragged** — six days, or seven with one short. There the values
after the mistake belong to hours nobody can identify, and a curve confidently wrong about which
hour is which is worse than no curve.

A refused curve keeps the shape it arrived in, `7x24` or `obj:6`, which the screen shows and the
log records. It says whether the prompt or the parser wants changing, and nothing about
anybody's reminders.

**An id that was not asked about is dropped.** It is the one mistake here that could reach
another person's row, and a model that echoes an id back wrongly — or helpfully invents one —
must not be able to attach advice to it.

## Whether it is working

The companion block reports on the last round: when it happened, whether the companion has an
opinion about all of the open list or only part of it, and the gateway's own words when the
last attempt failed.

It exists because without it the two states somebody most needs to tell apart look identical —
a key that stopped working, and a companion that simply has little to say. The failure was
already being recorded; showing it is the whole reason it was worth recording.

**No counts**, which this is the one screen tempted to show: "5 of 7 reminders" is a workload,
and the absence of a number that goes up is the product. "Some of your reminders" says what
somebody needs. See [api_design.md](api_design.md#companion).

A quota reads differently from a mistake. `limited` says the key is out of requests and will
try again on its own; nothing needs doing, and saying so is the difference between waiting and
going to look for a broken key that is fine.

### A key that stops working says so

Settings is where somebody looks once they already suspect. The case this feature actually
fails on is the other one: a key revoked in March and noticed in June, with three months of
nudges that were never weighted and nothing anywhere saying why.

So a failure is pushed, on its own channel — see [push.md](push.md#two-channels) for why a
notification about btw cannot share a topic or a tag with one carrying a reminder.

Two rules keep it from becoming the thing people turn notifications off over.

**Once per episode.** The loop runs every half hour and a broken key fails every pass, so
without a flag this would be a notification twice an hour for as long as it stayed broken. The
flag is lowered by a round that succeeds, so a key fixed and later broken again is worth a
second message. An undelivered message is not counted as told.

**Never while they are asleep.** btw refuses to nudge outside somebody's waking hours, and a
message about an API key has less claim on four in the morning than a reminder does. Asleep
means it is held rather than dropped: it goes out on the first pass after they wake.

**A quota is not pushed at all.** It resolves itself, nothing somebody could do would help, and
a notification saying so is one that trains them to ignore the next one. `limited` stays in
settings, where it costs nobody anything.

### Seeing what it said

*What it thinks* draws the open list with each reminder's week: one row per day, one bar per
half hour, tall where the companion thinks it fits better, against a dashed rule at 0.5.

Drawn rather than listed, because 336 numbers per reminder is not something anybody reads. What
somebody wants to know is the shape — whether the evenings are lifted, whether a weekend differs
from a Tuesday, whether the model understood them at all — and a week of bars answers that at a
glance where a column of decimals does not. The exact number is on hovering a bar, since a
height can be compared but not read.

Bars with gaps rather than one filled outline, because the answer *is* buckets: two neighbours
agreeing is a fact worth seeing rather than a slab to smooth over. It is HTML rather than SVG
for a duller reason — filling the available width means `preserveAspectRatio="none"`, which
scales x and y by different factors and turns a one-pixel gap into a variable one and a rounded
corner into an ellipse.

It shows **only what the weighting reads**. A reminder nothing has been said about says so; an
answer in a shape the program ignores says which shape it was; and a week that is flat all
through says *no opinion* in words rather than drawing a uniform band that looks like data.

*Ask again* asks the companion there and then and **waits for the answer**, which arrives as the
redrawn week. It marks the advice stale first, because a look declines when nothing has changed
— right for the loop, and wrong for a press that means "ask anyway".

That press spends one of a small daily quota, so it has two ceilings: twenty seconds on the
button, and four a minute at the server. The second is not redundant. A cooldown in one screen
is not a ceiling on a screen that is not the one being used.

It was 202-and-come-back-later first, which is right for a background job and wrong for a
button: somebody who has just rewritten what they say about themselves presses this to see the
difference, and being told to look again in a while makes them judge a change they cannot see.

## When it is asked

Every write that could change an answer sets a flag, and a loop decides when to act.

Marked stale by: a reminder written, described, ended, revived or deleted; an account's `about`
rewritten; its rhythm moved, because the waking window bounds which hours a slot can ever be
delivered in. Not by a nudge going out — that changes when a reminder was last raised, not what
it is about.

The obvious alternative, asking the moment anything changes, is wrong twice. Somebody writing
down six things in a minute would be six questions, and a free key allows fifty in a day — so
the burst that most deserves a single answer is the one that exhausts the quota. And a question
asked inside somebody's save makes their save as slow as a model's thinking, for a result
nothing is waiting on.

**Half an hour**, and the number comes from the quota rather than from taste. One person costs
at most one question per pass, so fifty a day survives somebody who edits something in every
single window, forty-eight times over. At fifteen minutes the same person would exhaust it
before the evening.

Nothing paces the pass and a rate limit does not stop it, because **a quota is per key and
every account brings its own**. One person's key having run out says nothing about the next
person's, and spreading requests between two accounts would be spreading them across quotas
that were never shared. An earlier version did both, on the assumption of a shared ceiling that
does not exist. What is left is telling a quota apart from a mistake, which is worth doing
because only one of them wants a person.

## What the answer does

It is the last term in the weight, and only a multiplier. The numbers, and why it can never be
zero, are in [nudges.md](nudges.md#the-companions-advice-is-the-last-term-and-only-a-multiplier).

The short of it: a reminder inside the hours it was given is five times as likely as one
outside them, and **a reminder nothing has been said about weighs exactly what it always did**.
That is what makes this optional at the level of one reminder rather than one account — a
reminder written a minute ago, before the next round of questions, is unaffected.

## What this does not do yet

**Nothing reads the categories.** They are stored because the question that produced them is
the rate-limited part, and re-deriving them later would cost one request per reminder. What
they are for is a filter or a label in the interface, and neither exists.

**A person is never told what the companion said about a particular reminder.** The settings
block says whether it has an opinion and when it last had one; it does not say which hours it
chose. That is defensible while it is only a weighting — btw deliberately shows no schedule —
but a wrong answer is still something somebody can feel more easily than see, and the remedy is
rewriting `about` and waiting.

**Nothing verifies the advice was any good.** *What it thinks* draws the week so somebody can
see whether a model understood them, but nothing checks it, and a curve that is confidently
wrong looks exactly like one that is right.
