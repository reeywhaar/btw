# Companion

btw runs no model. It puts a question to one an account has a key for, through a router, and
everything here is about where that key lives and what the model is told.

## Two services

**OpenRouter** and the **Hugging Face router**. Both speak OpenAI's chat completions, so what
differs is small and lives in one type: the address, the model names, and which optional fields
are safe to send.

The choice is the **account's**, beside the key, because a key works with one of them and not
the other — picking the service instance-wide would strand anybody holding the wrong kind. A row
written before there were two reads as OpenRouter, which is what it was.

`response_format`, `seed` and `temperature` go to both. `reasoning: {exclude}` goes only to
OpenRouter, whose models publish `reasoning` among their `supported_parameters`.

### Thinking

Thinking counts against `max_tokens`. A model that thinks and has not been told not to spends
the whole ceiling on it and is cut off before writing any JSON, which arrives as *the answer was
cut off before it finished* and reads like a budget that wants raising.

On OpenRouter it is switched off. On the Hugging Face router it cannot be, and the room is
bought instead — `ThinkingBudget`, sixteen thousand tokens, taken **out of** the ceiling rather
than added on top of it. A model refuses a request for more than it will produce with a `400`
naming itself, so the cap has to be the last thing applied:

```
max_tokens = min(2000 + 1800 × reminders + thinking, 32000)
```

Nothing published says what a given model's cap is — the router's `context_length` is the window
and not this — so 32,000 is a constant chosen to sit under the common 32,768.

The field that would switch it off there, `chat_template_kwargs: {thinking: false}`, is refused
outright by a model whose thinking mode is `required`:

```
`chat_template_kwargs.thinking` conflicts with thinking mode 'required'.
```

That is a `400` for the whole request rather than a field quietly ignored, so it cannot be sent
speculatively — and nothing the router publishes says which models would refuse it. Its
`/v1/models` gives, per provider, `status`, `context_length`, `pricing`, `is_free`,
`supports_tools`, `supports_structured_output`, `first_token_latency_ms` and `throughput`.
Nothing about thinking. OpenRouter's `/api/v1/models` does say, in `supported_parameters`, which
is why only it is asked.

## The companion is an account's, not the instance's

**The one place btw departs from the [relay](mail.md).** A relay is instance-wide and an
administrator's; a companion is not, for two reasons pointing the same way.

The key **spends its owner's credit**. An instance-wide key would mean whoever configured it
paying for everybody's scheduling, and the free models that make this approachable are
rate-limited per key, so one busy account would starve the rest.

The description is **about one person's life**. `about` holds somebody's hours and habits — not
a thing to hold once for an instance, and not a thing an administrator should be able to read.

So: one row per principal, `ON DELETE CASCADE`, every route behind `requireSession`. The
interface sits in settings beside the rhythm, the other answer somebody gives about their days.

## Where the configuration lives

**In the database, set from settings, not in the environment** — the same argument
[mail.md](mail.md#where-the-configuration-lives) makes, and more so. The environment is right
for what must be true before the process starts; a key belonging to one of several accounts,
rotated whenever that person likes, is not.

The key is **stored as written**, for the reason the relay's password is: there is no vault
here, and whoever can read `main.db` can already read every password hash in it.

It is **never sent back out**. `GET /api/companion` carries `key_set` and no key, and saving
with an empty key keeps the stored one — so a model can be changed without retyping a
credential. `about` **does** come back out; it is a text field somebody edits.

## What is not validated

**The shape of the key.** A `sk-or-` prefix rule would refuse a valid key the day OpenRouter
changes the format, and only using it settles the question.

**Whether the model exists.** There are hundreds and the list moves weekly.

Both are the same decision: refuse nothing a single request would answer, and make that request
cheap to run.

## The default model

One per service, because a slug belongs to one: `minimax/minimax-m3:free` on OpenRouter,
`deepseek-ai/DeepSeek-V4.1-Flash` on Hugging Face. An administrator can replace either — see
[the escape hatch](#an-administrators-default), which exists because routers retire slugs.

OpenRouter's `:free` variants accept **far fewer parameters** than the paid slug of the same
name — `response_format` but not `structured_outputs`, no `stop`, no `top_k`, none of the
penalties. Anything asking for a strict schema has to check rather than assume, or it gets prose
back from a request that looked like it demanded otherwise.

Free OpenRouter keys allow **20 requests a minute and 50 a day**, or 1000 once $10 of credit has
been bought. That is the ceiling on how often anything here may run, and the reason a `429` is
reported as its own kind of refusal.

### An administrator's default

`/admin` holds one model per service, and an account that never chose one follows it. Empty
means the model the build ships with.

It is not checked, and could not be: the instance has no key of its own, and the only thing that
settles whether a slug works is an account using it. The first companion to try reports what the
gateway said.

## Trying it

`POST /api/companion/test` puts **the real question** to the model and reads the answer back.

Not `GET /api/v1/key`, which proves a key is live and nothing about the model. Not a one-word
completion either: that proves a key and a slug, and the model worth catching is the one that
answers cheerfully and cannot hold a JSON object together, or writes a week in a shape the
parser refuses. That failure is otherwise invisible until the loop has been quietly producing
nothing for a week.

**With a made-up list and a made-up description**, not the account's own. A press has to work
before anything has been written down, and the answer to "does this model work" must not change
with what somebody happens to have on their list. Four reminders, chosen to need every part of
the answer: one bound to a clock, one to opening hours, one to being awake, one that suits most
of a week.

The reply is parsed exactly as a real round's is, and the press reports how much of it could be
read — `all`, `some` or `none`, with the shapes that arrived instead for the rest. No counts,
per [api_design.md](api_design.md#companion).

Nothing is stored. The press reconciles unsaved form values, so an answer earned under settings
nobody has saved has no row it belongs to. It shares the refresh's ceiling of four a minute,
because it now costs the same.

**Against what is in the form, not what was last saved.** The button sits beside the fields it
is about, and a button beside a value somebody has just corrected has to mean that correction.
It reconciles under the same rule a save follows — an empty key means the stored one — so what
was tried is what saving would store. Trying stores nothing itself.

It reports **which model answered**, which is not always the one asked for: OpenRouter falls
back between providers and a slug can resolve to a variant.

A refusal is a **`502` carrying the gateway's own words**, for the reason
[mail.md](mail.md#sending) gives: everything on this side worked and something upstream did not.
A rejected key, a model with no credit, a missing slug and a rate limit are four different
afternoons.

### The status code is not the whole story

OpenRouter answers **`200` with the fault in the body** when a provider dies partway through, so
the fault is looked for at the top level and inside a choice before the status is consulted.

## Tested against a gateway, not a mock

`internal/openrouter` starts a real HTTP server on a loopback port, as `internal/mail` starts an
SMTP one. A mocked `http.Client` would assert that `net/http` was called; what is worth
asserting is that the key travels as a bearer token, that a fault inside a `200` is a failure,
that the model which answered is the one reported, and that a proxy's HTML error page survives.

## What it is asked

In batches, not once per reminder: a question carries up to ten of them, along with what the
person wrote about themselves and the hours they are reachable in. Asking one at a time would be
a request each against fifty a day, and would throw away the context that makes the answers
cohere.

**Ten is a ceiling on the answer, not a preference.** Output is roughly 1,800 tokens a reminder,
almost all of it the curve, and a model refuses a request over its own limit outright rather
than answering shorter — so the batch is sized to what fits under `MaxOutputTokens`:

| | fits | batch | fold |
| --- | --- | --- | --- |
| OpenRouter | 16 | 10 | 5 |
| Hugging Face | 7 | 4 | 2 |

Fewer on Hugging Face because thinking cannot be switched off there and comes out of the same
ceiling — so 45 reminders is four questions on one service and eleven on the other. That is the
price of a model that must think, and the number to revisit is `ThinkingBudget` rather than the
batch.

**A small remainder folds into the batch before it** rather than becoming a question of its own.
A last batch of one spends a whole request, against a quota of fifty a day, to ask about a
single reminder — and the answer is no better for being alone. The fold is half the batch, so on
OpenRouter 15 is one question, 16 is `10 + 6`, 45 is `10 10 10 15` and 46 is `10 10 10 10 6`.

**A failed batch stops the round.** The failure is almost always the key or the quota, and both
are answers about every remaining batch, so working through them spends the quota to be told the
same thing four more times. What earlier batches answered is still written, and the round is not
marked answered — so the flag stays up and the next pass finishes it.

The prompt is a **template constant** in `internal/advise`, not string-building. It is the
product here, so it is written as prose in one block that reads as what the model reads.

### What it asks for

A list of the **stretches of the week it has an opinion about**, each with a weight:

```json
[
  { "days": "all", "from": "02:00", "to": "13:00", "v": 0.05 },
  { "days": "mon-fri", "from": "13:00", "to": "19:00", "v": 0.3 },
  { "days": "all", "from": "20:00", "to": "01:00", "v": 0.9 }
]
```

Anything unmentioned is 0.5, so one line is a complete answer and an empty list is a real one.
What the weight does is in
[nudges.md](nudges.md#the-companions-advice-is-the-last-term-and-only-a-multiplier).

Two other shapes are obvious and both cost more than they look.

**Windows** — hours with no weight — make the model answer _when_ and _how strongly_ at once,
and it is bad at the second: every answer comes out in-or-out.

**A number per half hour** — seven arrays of forty-eight — fixes that and breaks two things
that matter more. _It cannot be counted_: there are no landmarks in 336 numbers, so a model that
loses its place cannot notice and neither can the parser, and asked for 7×48 a free model
answers 7×24, or an object keyed by day, or 7×49. _Saying nothing costs the same as saying
something_: the considered answer and the lazy one are the same length, so a model under
pressure writes `0.5` three hundred and thirty-six times and produces something data-shaped that
draws as a flat grey week.

Spans have neither problem: a span names its own hours, and the format is sparse, so effort and
length rise together.

**Fixed buckets** solve the counting and are refused for a different reason — their edges are
somebody else's. "The middle of the morning to the middle of the day" is `10:00` to `13:00`.

Five things in the question are load-bearing and have tests asserting they are still said.

- **That the answer does not decide whether a reminder is shown.** Without it a model reads the
  job as "when is this due", the one question btw exists to refuse.
- **Say only what you have an opinion about**, or a model describes the whole week out of
  politeness and the incentive that makes the format work is gone.
- **That almost everything has some shape.** Washing up is worse at four in the morning.
  Without this the empty list becomes the default rather than the exception.
- **That a later stretch wins where two overlap**, which is what lets a model say the broad
  thing and then narrow it.
- **Two worked examples.** The single most effective thing in the prompt.

The categories are **glossed rather than listed**: _errands_ is "bound by opening hours" and
_chores_ is "bound by nothing but being awake", which is the reason they are two words.

That is also the bar for adding one, in `internal/advise/categories.go`: a category earns its
place by implying *when*. _medication_ is bound to the clock where a hospital appointment is
bound to opening hours, which is why they are not both _health_. A word saying only what a
reminder is about buys nothing and costs a line of every request.

### The answer is read leniently

A `:free` model in JSON mode is a request, not a guarantee. The parser accepts a bare array as
well as the `{"results": []}` wrapper, strips markdown fences, and finds the object inside a
sentence of preamble.

One malformed field costs that field, one span that span, one entry that entry. Each is decoded
on its own, because a typed struct fails the _whole document_ over one bad value — so a round
covering forty reminders would come back empty and look like a model that said nothing.

Within a span, `days` reads `mon`, `Monday`, `mon-fri`, `sat,sun`, `weekends`, `all`, and wraps,
so `fri-mon` is the stretch it names. Times read `9`, `09:00`, `24:00`. A `to` at or before its
`from` runs past midnight. An absent `days` means every day: a model writing one line about an
evening means every evening.

**Every field has aliases** — `start`/`end` beside `from`/`to`, `value`/`weight`/`score` beside
`v` — and that is not tidiness. A model told to write `from` writes `start` often enough to
matter, and the two failures are not equally visible: a missing `from` is refused and says so,
while a weight written as `value` reads as **the zero value**, and a span meant as 0.9 becomes
0.0 and draws as a confident graph saying the opposite. That is also why a span whose weight
cannot be read is dropped rather than taken as zero — zero is not an absence, it is the
strongest opinion on the scale.

**An empty list is a real answer** — no opinion anywhere — and not a failure.

### What one round cannot read, the round before it keeps

Stale advice is worth more than none: it was true when it was written, and the reminder did not
change — the _answer_ failed, not the question. So an entry whose curve could not be read, or
that never arrived, keeps the curve it had. Whatever the round did manage to say is still taken,
since categories can be readable when a curve is not.

A deliberate "no shape" still overwrites, because an empty list of spans is a week of neutrals
rather than a failure. **The only thing carried forward is an answer that could not be
understood or did not come.** A pass says how many it carried, beside how many it answered and
dropped.

**The array shapes are still read**, because a model answers the question it expected at least
as often as the one it was given:

| what arrives                | how it is read                                                 |
| --------------------------- | -------------------------------------------------------------- |
| 7×48                        | the week, half-hourly                                          |
| 336 flat                    | the same numbers in the same order                             |
| 7×24                        | the week by the hour; each value covers both of its half hours |
| an object keyed by day name | Monday first, all seven or none                                |
| one day of 48 or 24         | that day, all week                                             |
| a day one value out         | trimmed or held at the end                                     |

None of those invents a number. The **one out** case is a tolerance rather than a repair,
allowed because the question asks for broad stretches and says a curve swinging between
neighbouring half hours describes precision the model does not have. Two out is not a slip.

What is refused is anything **ragged** — six days, or seven with one short. There the values
after the mistake belong to hours nobody can identify, and a curve confidently wrong about which
hour is which is worse than no curve.

A refused curve keeps the shape it arrived in, `7x24` or `obj:6`, which the screen shows and the
log records: it says whether the prompt or the parser wants changing, and nothing about
anybody's reminders.

**An id that was not asked about is dropped.** It is the one mistake here that could reach
another person's row.

## Whether it is working

The companion block reports on the last round: when it happened, whether there is an opinion
about all of the open list or only part of it, and the gateway's own words when the last attempt
failed. Without it the two states somebody most needs to tell apart look identical — a key that
stopped working, and a companion that simply has little to say.

**No counts**, which this is the one screen tempted to show: "5 of 7 reminders" is a workload,
and the absence of a number that goes up is the product. See
[api_design.md](api_design.md#companion).

A quota reads differently from a mistake. `limited` says the key is out of requests and will try
again on its own, which is the difference between waiting and going to look for a broken key
that is fine.

### A key that stops working says so

Settings is where somebody looks once they already suspect. The case this fails on is the other
one: a key revoked in March and noticed in June. So a failure is pushed, on its own channel —
see [push.md](push.md#two-channels) for why a notification about btw cannot share a topic or a
tag with one carrying a reminder.

Three rules keep it from becoming the thing people turn notifications off over.

**Once per episode.** The loop runs every half hour and a broken key fails every pass. The flag
is lowered by a round that succeeds, so a key fixed and broken again is worth a second message.
An undelivered message is not counted as told.

**Never while they are asleep.** A message about an API key has less claim on four in the
morning than a reminder does. It is held rather than dropped, and goes out on the first pass
after they wake.

**A quota is not pushed at all.** It resolves itself, nothing somebody could do would help, and
a notification saying so trains them to ignore the next one.

### Seeing what it said

_What it thinks_ draws the open list with each reminder's week: one row per day, one bar per
half hour, tall where the companion thinks it fits better, against a dashed rule at 0.5.

Drawn rather than listed, because 336 numbers per reminder is not something anybody reads. What
somebody wants is the shape — whether the evenings are lifted, whether a weekend differs from a
Tuesday — and a week of bars answers that at a glance. The exact number is on hovering a bar,
since a height can be compared but not read.

Bars with gaps rather than a filled outline, because the answer _is_ buckets. HTML rather than
SVG for a duller reason: filling the available width means `preserveAspectRatio="none"`, which
scales x and y differently and turns a one-pixel gap into a variable one.

It shows **only what the weighting reads**. A reminder nothing has been said about says so; an
answer in a shape the program ignores says which shape it was; and a week that is flat all
through says _no opinion_ in words rather than drawing a band that looks like data.

_Ask again_ asks the companion there and then and **waits for the answer**, which arrives as the
redrawn week. Answering with a 202 would make somebody who has just rewritten their `about`
judge a change they cannot see. It marks the advice stale first, because a look declines when
nothing has changed — right for the loop, wrong for a press meaning "ask anyway".

That press spends one of a small daily quota, so it has two ceilings: twenty seconds on the
button and four a minute at the server. The second is not redundant — a cooldown in one screen
is not a ceiling on a screen that is not the one being used.

## When it is asked

Every write that could change an answer sets a flag, and a loop decides when to act.

Marked stale by: a reminder written, described, ended, revived or deleted; an account's `about`
rewritten; its rhythm moved, because the waking window bounds which hours a slot can be
delivered in. Not by a nudge going out — that changes when a reminder was last raised, not what
it is about.

Asking the moment anything changes is wrong twice. Somebody writing down six things in a minute
would be six questions against fifty a day, so the burst that most deserves a single answer is
the one that exhausts the quota. And a question asked inside somebody's save makes their save as
slow as a model's thinking, for a result nothing is waiting on.

**Half an hour**, and the number comes from the quota rather than from taste. One person costs
at most one question per pass, so fifty a day survives somebody who edits something in every
single window. At fifteen minutes the same person would exhaust it before the evening.

Nothing paces the pass and a rate limit does not stop it, because **a quota is per key and every
account brings its own**. One person's key having run out says nothing about the next person's.
What is worth doing is telling a quota apart from a mistake, since only one of them wants a
person.

## What the answer does

It is the last term in the weight, and it _is_ the confidence: 0.9 multiplies by 0.9, an hour
nobody mentioned by 0.5, and **0 by 0**. The numbers are in
[nudges.md](nudges.md#the-companions-advice-is-the-last-term-and-only-a-multiplier).

**A reminder nothing has been said about weighs exactly what it always did**, which is what
makes this optional at the level of one reminder rather than one account.

**A stretch scored zero is not raised in those hours at all.** It is the one number here that is
a switch, and the one place a model's answer can stop something arriving rather than move it.
The prompt says so plainly, because a model that reaches for zero out of habit would silence
things nobody meant to silence.

## What this does not do yet

**Nothing reads the categories.** They are stored because the question that produced them is the
rate-limited part. What they are for is a filter or a label, and neither exists.

**A person is never told which hours were chosen for a particular reminder** outside _what it
thinks_. That is defensible while it is only a weighting — btw shows no schedule — but the
remedy for a wrong answer is rewriting `about` and waiting.

**Nothing verifies the advice was any good.** A curve that is confidently wrong looks exactly
like one that is right.
