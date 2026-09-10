# Nudges

The part with the product in it.

Two decisions, kept apart on purpose. **When** somebody is nudged is a fact about their day
and can be planned hours ahead. **What** the nudge carries is a fact about their reminders at
one instant and must not be. They live in `internal/rhythm` and `internal/pick`, neither
imports the other, and both are pure functions of their inputs and a seed — which is what
makes the interesting behaviour testable against a fixed seed rather than against a database
at four in the afternoon.

`internal/nudge` is the impure half: it reads the clock, talks to the network, and
orchestrates the two.

## The ideal is the right moment; random is the honest floor

The best nudge arrives when you were nearly expecting it — at the hour you would have thought
of the thing yourself, when you are in the mood to be asked.

Nothing can read a mind, and btw does not pretend to. A uniform draw across the hours somebody
is awake is not the right moment; it is the _absence of a wrong one_, and it is what makes the
question **when?** unnecessary, which is what lets a half-formed want be written down at all.

So the randomness is not a gimmick and the surprise is not the point. It is a floor. The one
direction it could honestly rise is in [what is still open](#still-open).

## When a nudge happens

A person's rhythm is a timezone, a waking window, and how many nudges a day. Defaults are
`09:00`–`22:00` and three a day — guesses that want a fortnight of somebody carrying a phone
before they are defaults rather than placeholders. The ceiling is twenty-four.

**The budget is not a count, it is an interval.** Eighteen a day across a thirteen-hour
waking window is one about every forty-three minutes; across a whole day it is one about
every eighty. That is the only thing the number means, and the interval is floored at one
tick because nothing can be delivered between two of them.

The exact number of nudges in a day is therefore **not a promise**. A day that delivers nine
when the rhythm says ten behaved correctly, and nobody counts.

### Three states, and no plan

Every five minutes the loop asks, for each person with a device:

1. **A nudge is waiting and its moment has come.** Send it. Claiming it is the delete, so two
   overlapping ticks cannot both send the same one.
2. **A nudge is waiting and its moment has not.** Leave it alone. Deciding again while one
   stands would move it, and a nudge that keeps being rescheduled never arrives.
3. **Nothing is waiting.** Work out whether one is owed — awake, and longer than an interval
   since the last — and if so schedule one a random moment inside the next tenth of an
   interval.

That last step is why deciding and firing are separate. The loop only wakes on a tick, so
firing where the answer turns yes would put every nudge on a five-minute mark, which is a
pattern anybody would eventually notice.

### What this replaced

A day's worth of instants, drawn in advance and stored. It existed to promise an exact number
of nudges at times chosen a day ahead, and everything awkward nearby was in service of that
promise: redrawing the day when a setting changed, a grace window for slots nobody fired, a
ceiling that had to agree with what the planner could actually place, and an off-by-one in
that agreement.

The promise was not worth its machinery, and the machinery kept being wrong in ways that
looked like the product ignoring somebody. What matters is _roughly this often, at hours
nobody picked, while awake_ — which is answerable from the clock and the last nudge, with one
timestamp of state.

Everything else fell out. A rhythm change needs no announcing: the next tick works the answer
out afresh, and the only thing a change does is drop a nudge that was scheduled under the old
answer. An instance down for three hours sends one nudge on restart rather than discovering a
queue of them. There is no `min_gap`, because the interval **is** the spacing and a second
number saying so is a second number to keep in agreement with the first.

### The timezone is real

Waking hours are the only reason btw needs to know what time it is where somebody is, and
they are reason enough — a reminder product that pings at four in the morning is uninstalled
that morning.

A stored UTC offset would be wrong twice a year, for weeks at a time, which is worse than
wrong all the time. So the column holds an IANA name, captured from
`Intl.DateTimeFormat().resolvedOptions().timeZone` and offered to the person rather than
imposed. `time/tzdata` is imported into the binary — about 450KB — which makes the zone
database a property of the program rather than of whatever the base image happens to carry.

A zone that will not load falls back to UTC rather than refusing to nudge. Wrong hours are
visible and correctable; silence looks like the product not working.

### Sleeping does not count towards the wait

Measured from a nudge the evening before, the answer at nine in the morning is always yes —
and somebody would be pinged within a minute of waking, every day of their life. So a night
that ran past the last nudge starts the clock at waking instead.

Less half an interval, which the night is credited. That half is what makes the day hold the
number asked for: starting cleanly at waking, the first nudge lands a whole interval in and
the last lands exactly at bedtime, where it is outside the window and dropped — a day asking
for three delivered two.

A nudge scheduled while awake that comes due after bedtime is dropped rather than sent late.

#### The waking day may have begun yesterday

Somebody awake from noon until four is, at one in the morning, in a day that started thirteen
hours earlier. So "when did this waking day begin" is today's waking hour, or **yesterday's
when today's has not arrived yet**.

That one branch is the whole of what a window through midnight needed, and its absence is why
the hours were refused outright until now. Reading the start as today's noon — still hours away
— makes every nudge look scheduled for the future, so nothing is ever due: the small hours
would go silent for exactly the people who asked to be awake in them, and the setting that
caused it is the one they would least suspect.

## Which reminder it carries

Decided at the instant of sending, never when the slot was drawn. Choosing at planning time
would mean a reminder ended at lunchtime still arriving at four, and a decision about an
evening made with the morning's information.

### Eligible

Not done, `priority > 0`, and past whatever floor the reminder states. That is a hard filter
an index can serve, and it lives in SQL.

**A reminder states no floor by default.** An inherited floor of a day reads as a sensible
guess and behaves as an instruction: eight reminders at a day apiece cannot fill ten slots
however the day is drawn, and each floor drifts later with every nudge, so the next morning
starts with a smaller pool than the evening before.

A floor is a statement about one particular thing — _do not raise this more than weekly_ — and
inheriting one nobody made is a preference nobody expressed overruling an appetite somebody
did. Where one is stated it is obeyed absolutely, including against a budget that would like
more.

Nothing is lost by dropping the default. The weighting collapses a reminder's chance to
nothing the moment it is raised and recovers it over a nominal day; the gap between slots
holds any two nudges apart; and the no-repeat rule stops the same one arriving twice running.
Spacing was never the floor's job alone.

And, when anything else is eligible, **not the reminder the last nudge carried**. A hard rule
rather than a weighting, because a repeat is the one thing a person notices immediately and
forgives least. When it is the only candidate it is allowed through: one reminder that keeps
coming back is the product working, one that goes quiet is the product broken.

### Weighted

```
weight = priority × min(4, (now − last_nudged_at) / min_interval) × advice
```

The multiplier is what makes this not a loop, and it needs no separate rule to stop one. The
moment a reminder is nudged its elapsed time is zero, so its weight is zero — and it is
hard-blocked by its own floor besides. It re-enters the pool as the floor passes and then
grows likelier, relative to everything else, the longer it goes unmentioned.

Two reminders at equal priority alternate. A hundred rotate. One arrives at its floor and no
faster.

The **cap at 4** is what stops something written in March with a one-day floor being a
thousand times likelier than anything else by June and taking every slot until it is
answered. Beyond a few multiples of its own interval, a reminder is as overdue as it is going
to get.

Never nudged counts as maximally stale, so a reminder just written down arrives soon — which
is also the fastest way for somebody to find out the thing works at all.

A reminder with no floor of its own is still ordered by how long it has waited, against a
nominal day. That denominator decides nothing about eligibility — only how quickly one
reminder overtakes another — and without it every unfloored reminder would weigh the same and
the draw would be a coin toss between something raised a minute ago and something raised last
week.

Priority is a probability, not an order. One at 90 arrives more often than one at 10 and never
silences it, which a sort would fail to give.

#### The companion's advice is the last term, and only a multiplier

`advice` is **exactly 1** unless a [companion](companion.md) has been asked about this
reminder. That is what makes the whole feature optional at the level of one reminder rather
than one account: something written a minute ago, before the next round of questions, weighs
precisely what it would have weighed before any of this existed.

When there is advice it is a **curve** over the person's week: seven days of forty-eight half
hours, held as numbers from 0 to 1 and read against their own local clock — converted once in
`internal/rhythm`, and two integer indexes everywhere after. The number for the current half
hour is stretched onto a range and multiplied in:

```
advice = confidence              0 → 0,  0.5 → 0.5,  1 → 1
```

The companion's number **is** the multiplier. An unanswered hour counts as 0.5, and so does a
reminder nothing has been said about.

The companion is not _asked_ for 336 numbers — it is asked for the stretches it has an opinion
about, and those are expanded into the grid on arrival. Why, in
[companion.md](companion.md#what-it-asks-for). What matters here is the consequence: anything
the companion did not mention arrives as 0.5, so a reminder it said one thing about is neutral
for the rest of the week rather than damped by omission.

**Nothing is a special case, with one exception.** No threshold, no in-or-out, no separate rule
for a reminder wanting somebody's full attention. A curve of 0.5 throughout lands exactly where
never having been asked lands, and that is the property the whole design hangs on.

It lands there at **half** rather than at one. A term meaning "no opinion" looks like it ought
to multiply by 1, and scaling it to do so buys nothing: a weighted draw normalises by the total,
so a factor applied to every candidate alike cancels. Halving a reminder nothing has been said
about only halves it against reminders that are halved too.

What is left is the number the model gave, unmodified, which is worth more than the tidiness of
a 1 — one fewer place for the code and the prompt to disagree. A test pins the equivalence,
because 0.5 reads as a bug to anybody meeting it cold.

The exception is zero.

##### Zero means never

The alternative is a floor — never quite reaching zero, so that the companion **moves** a
reminder around the week but cannot remove one, silencing staying a person's decision with
priority zero its single expression. It costs the hours a model is certain about: a reminder
merely damped at four in the morning still arrives at four in the morning sometimes.

**The weighted draw is not what enforces it.** A draw already never lands on a weight of zero,
but _every_ weight being zero is the one case that falls through to the uniform fallback below
— which would then pick from exactly the reminders that were meant to be passed over. So a
reminder the companion scored zero for the current half hour is filtered out before the draw
sees it, beside the ones somebody silenced with priority zero.

That leaves the fallback covering only a zero that came from **staleness**, which is the manual
button: it ignores a reminder's own floor, so everything it offers may have been nudged a moment
ago. Two different zeros, and only one of them means never.

What zero costs instead is that a model answering it casually stops a reminder arriving, and
says nothing. The prompt holds that line: it says plainly that zero is a switch rather than a
point on the scale, and that anything still worth taking grudgingly is 0.1. A reminder zeroed
across the week does not arrive until somebody opens _what it thinks_ and sees why.

Nine to one between a stretch scored 0.9 and one scored 0.1. Staleness
runs to four and keeps climbing underneath, so a reminder the companion merely dislikes still
surfaces — later, and by a route nothing here has to special-case. Only an outright zero stops
it.

The advice applies to a **never-nudged** reminder too, rather than being skipped along with the
staleness arithmetic. It is tempting to let something just written down arrive at once, but the
first arrival is the one most worth placing well, and maximal staleness already puts it far
enough ahead that the multiplier decides which hour rather than whether.

### Nothing eligible sends nothing at all

Not the next-least-ineligible reminder. Not a repeat of this morning's. Nothing.

Sending something rather than nothing is how a notification channel gets turned off for good
by somebody who was otherwise happy with it.

### The floor is the scheduler's rule, not the button's

`min_interval` governs when the **scheduler** may raise something. `POST /api/nudges` — the
"send one now" button — ignores it.

It did not, and refusing there was nonsense: the button exists to prove the chain works, and a
button that answers "that was raised too recently" to somebody who just pressed it proves
nothing and looks broken. Somebody pressing it has asked for a nudge. The floor stops the same
thing arriving twice in a morning _unasked_, which is a different thing entirely.

Two rules still hold on that path, because they are not about timing. A finished reminder is
not sent, and neither is a silenced one — `priority = 0` is the difference between "not now"
and "not ever", and only the first is the button's business.

One consequence reaches `internal/pick`: with the floor ignored, every candidate may have been
nudged a moment ago, so every weight can be zero. Weighting has nothing left to say there, so
the choice falls back to uniform rather than to nothing — somebody pressed a button and the
pool is not empty. Silenced reminders are dropped before that fallback rather than merely
weighted to zero, so "never" survives it.

### Three answers, because two of the failures are different afternoons

`POST /api/nudges` answers `200` with an `outcome`, and none of the three is an error:

| outcome       | what happened                                           |
| ------------- | ------------------------------------------------------- |
| `sent`        | at least one device took it                             |
| `nothing`     | the pool was empty — everything is finished or silenced |
| `undelivered` | a reminder was chosen and no push service would take it |

The last two shared a sentence once, and it sent people to the wrong place. An empty list is
something to fix by writing something down; a device that will not take a push is something to
fix by re-registering it. A button that gives the first explanation for the second problem is a
button nobody trusts twice.

## What happens when it arrives

Three responses. Two are buttons and the third is the common one.

- **Done** — you did the thing. It stops coming.
- **Drop** — you no longer want the thing. It stops coming.
- **Nothing at all** — the default, the majority, and explicitly not a failure. It records no
  miss, carries no state, and shows nowhere. The reminder becomes eligible again when its
  interval passes.

**Drop is not a smaller Done and it is not a delete.** It is the second half of what a nudge is
for. A thought written down without a _when_ has not been decided yet, and the arrival is where
deciding happens — reading "go to the circus" on a Tuesday evening either makes you want to go
or makes you realise you do not, and the second answer is worth exactly as much as the first.

Which is also how the list stays short with nobody tidying it. **The nudge is the garbage
collector.** No review, no weekly triage, no cleanup screen: the things nobody wants any more
leave one at a time, at the moment somebody is already looking at them, and that is the only
moment they can honestly be judged.

That third row is the promise. Every todo application in existence is built on the premise
that an unanswered item is a debt, and the count that follows is the thing being avoided here.
**Until a person ends it, the reminder keeps arriving** — not fading with age, not stopping
after a run of nudges nobody answered, not expiring because a date went past.

### Arriving quietly

A rhythm can ask for notifications without a sound. The reminder still shows; it simply does
not announce itself — which is what somebody working beside a phone wants, and the difference
between a nudge and an interruption.

It travels in the payload rather than being read at display time, because a service worker
cannot read a rhythm. `renotify` is dropped when it is on: one asks to re-alert and the other
asks for quiet, and asking for both is asking for nothing in particular.

### There is no snooze

Doing nothing already _means_ later: the reminder becomes eligible again when its interval
passes. A snooze would be a third control doing a job that ignoring it and `min_interval`
already do between them, and it is the control that turns an arrival into a decision somebody
has to make.

This is the piece most likely to come back, and if it does it needs an answer to a question
this section does not have — see below.

## Still open

- **Three a day is a guess.** So are forty-five minutes and nine-to-ten.
- **What "Later" would mean, if it existed.** The obvious button, and the reason it is not
  built is that its behaviour is genuinely ambiguous. Ignoring already defers by
  `min_interval` — a day by default — so _Later_ has to mean either **sooner** ("yes, I want
  this, ask again in a few hours") or **not today** ("skip past the next one"). Those are
  opposite behaviours behind one word, and picking wrong makes the button worse than absent.
  Note also that `Notification.maxActions` is 2, so Later cannot simply be added; it displaces
  Done or Drop.
- **Shaping the hours from when somebody actually answers.** The honest way to close part of
  the gap between the floor and the right moment. Pressing Done or Drop is a real event,
  reliably reported, and a few weeks of them say which hours a person is _receptive_ in — not
  what they want, only when they are willing to be asked. It may move **when** a nudge lands
  and never **whether** one does, which is what keeps it clear of what is refused below. It
  needs months of log before it has anything to say.
- **Night owls.** The window must currently sit inside one local day.
- **Expiration and finer control.** Wanted, and deferred rather than refused: a reminder that
  stops mattering after a date, a priority somebody can set, an interval per reminder. All
  three are somebody stating something about their own reminder.

## Refused, permanently

**The system never gives up on somebody's behalf.** It was tempting to back a reminder off
after a run of nudges nobody answered, the way anything polling backs off a source that keeps
failing. But a source that stopped answering is a fact, and an unanswered nudge is not.
"Ignored" is not something a browser reports honestly: `notificationclose` is uneven across engines and
a phone face-down on a table reports nothing at all, so it would be inferred from silence.

Inferring _you did not mean it_ from silence, and then quietly raising something less often, is
the product deciding on somebody's behalf that a thing they wrote down does not matter.

The line this draws is between the system giving up and a person changing their mind. The
second is what expiry and priority are, and they are welcome.
