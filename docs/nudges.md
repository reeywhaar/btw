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
is awake is not the right moment; it is the *absence of a wrong one*, and it is what makes the
question **when?** unnecessary, which is what lets a half-formed want be written down at all.

So the randomness is not a gimmick and the surprise is not the point. It is a floor. The one
direction it could honestly rise is in [what is still open](#still-open).

## When a nudge happens

A person's rhythm is a timezone, a waking window, how many nudges a day, and how close two may
fall. Defaults are `09:00`–`22:00`, three a day, forty-five minutes apart — guesses that want a
fortnight of somebody carrying a phone before they are defaults rather than placeholders.

The ceiling is twenty-four, and the window and gap bind first and usually well below it: nine
to ten at forty-five minutes apart holds eighteen. Twenty-four needs the window switched off,
which is asking to be nudged about once an hour, all day. A budget the effective window cannot
hold is refused with a sentence naming the most it will take.

**N nudges need N−1 gaps.** The ceiling divided the window by the gap, which counts gaps and
reports the answer as a number of nudges — one short, every time. Seventeen gaps of
forty-five minutes is 765 of a 780-minute window, so eighteen fit with a quarter of an hour
spare.

Nought is not on the control either. It was offered as "none", which is a way of switching
nudges off hidden inside a count of them.

The waking window is the only thing btw actually knows about somebody's day, so it is the only
thing allowed to shape the draw.

**It is optional.** Switched off, the day is the window and a nudge can land at any hour. That
is a real thing to want — shift work, or simply not minding — and it is not a state to fall
into by accident, so the interface says out loud what unchecking the box means rather than
leaving somebody to discover it at four in the morning.

### Stratified, not rejection-sampled

The window is cut into `budget` equal blocks and one instant is drawn uniformly inside each.

That terminates — always, on the first try — and gives both properties that matter:
unpredictable inside its block, so nobody can wait for it; never three in an hour, because
each block holds exactly one.

Uniform sampling across the whole window with a minimum-gap constraint would give a slightly
nicer distribution and can loop for a long time on a short window, which is a bad trade for a
function that runs inside a scheduler tick.

Two adjacent blocks can still place their instants either side of a boundary, so a slot too
close to its predecessor is pushed forward. That biases a few slots slightly later, and it is
worth stating rather than hiding — the alternative is resampling, and a loop that can fail to
terminate does not belong in a scheduler.

Each slot also **reserves room for the ones after it**: it may not be drawn so late that the
remainder have nowhere left to go. Without that reservation a day pushed gradually later ran
out of window and dropped its last few, so asking for the most that fits quietly delivered
fewer — and the ceiling had to be set below what actually packs to hide it. A property test
holds the two together: at every window and gap, the number the interface offers is the number
the planner draws, every gap holds, and nothing lands after the window closes.

The seed is the person and the local date. A day's plan is reproducible, so *why did it go off
at 04:12* is a question answerable without having been watching.

### The timezone is real

Quiet hours are the only reason btw needs to know what time it is where somebody is, and they
are reason enough — a reminder product that pings at four in the morning is uninstalled that
morning.

A stored UTC offset would be wrong twice a year, for weeks at a time, which is worse than
wrong all the time. So the column holds an IANA name, captured from
`Intl.DateTimeFormat().resolvedOptions().timeZone` and offered to the person rather than
imposed.

`time.LoadLocation` needs a zone database, and an Alpine image has none. btw imports
`time/tzdata`: about 450KB of binary, no image change, and no `apk add tzdata` that a future
base-image bump can quietly drop. It makes the zone database a property of the program rather
than of what the runtime happens to contain.

A zone that will not load falls back to UTC rather than refusing to plan. Wrong hours are
visible and correctable; silence looks like the product not working.

### A change takes effect today

Changing the budget, the window or the timezone redraws the rest of the day immediately.

It did not, on the theory that having the afternoon jump is a surprise. That was the wrong
surprise to optimise for: asking for twelve a day and receiving two is a plan drawn yesterday
behaving perfectly correctly, and from the outside it is indistinguishable from the setting
being broken.

Slots that already fired stay as they are — they happened, and rewriting them would be a lie
about a notification somebody already saw. Only instants still ahead are replaced, and only
future ones are written: a day redrawn at half past nine in the evening honestly yields a
short evening rather than a morning that fires all at once or is swept as missed.

The redraw is never fatal to the save. The rhythm is stored either way and tomorrow is planned
from it, so a failure costs the rest of one day rather than the change.

### Planning is lazy

When the scheduler ticks and finds somebody has no plan for the local date they are currently
in, it makes one. No cron per timezone, nothing planned for a disabled account, and nothing
planned for somebody with no device — a plan for somebody nothing can be delivered to is a
table filling up with rows that will only ever be dropped.

A budget of zero writes nothing, so `HasPlan` says the day is unplanned and it is re-planned
once a minute for nothing. That is cheap enough to prefer over a marker row that would have to
be kept in step.

### A missed slot is missed

The scheduler ticks every minute. A slot whose time has come fires; a slot more than **ten
minutes** late is marked fired and dropped, with a line in the log.

An instance that was down for three hours does not catch up on restart. Three notifications
arriving together, every one of them about a moment that has passed, is indistinguishable from
a broken app and is exactly what teaches somebody to swipe the channel away for good.

The rule is enforced in two places, because there are two ways of being late. `rhythm.Grace`
covers this side: a slot nobody got to in ten minutes is dropped, whether the process was down
or merely busy. `TTL: 3600` covers the other side, asking the push service to drop what it
could not hand to a device within the hour — a phone that was off is not something this process
can see.

The two numbers differ on purpose. Ten minutes is how late a nudge may be *chosen*; an hour is
how long it may sit in a queue somewhere else. Tightening the second to ten minutes would throw
away nudges to a phone that was briefly in a tunnel.

## Which reminder it carries

Decided at the instant of sending, never when the slot was drawn. Choosing at planning time
would mean a reminder ended at lunchtime still arriving at four, and a decision about an
evening made with the morning's information.

### Eligible

Not done, `priority > 0`, and past whatever floor the reminder states. That is a hard filter
an index can serve, and it lives in SQL.

**A reminder states no floor by default.** It used to inherit one of a day, which read as a
sensible guess and behaved as an instruction — and silently capped the day at however many
reminders somebody had. Eight reminders at a day apiece cannot fill ten slots however the day
is drawn, and each floor drifts later with every nudge, so the next morning starts with a
smaller pool than the evening before: ten a day became eight, then five.

A floor is a statement about one particular thing — *do not raise this more than weekly* — and
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
weight = priority × min(4, (now − last_nudged_at) / min_interval)
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
thing arriving twice in a morning *unasked*, which is a different thing entirely.

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

| outcome | what happened |
| --- | --- |
| `sent` | at least one device took it |
| `nothing` | the pool was empty — everything is finished or silenced |
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
for. A thought written down without a *when* has not been decided yet, and the arrival is where
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

Doing nothing already *means* later: the reminder becomes eligible again when its interval
passes. A snooze would be a third control doing a job that ignoring it and `min_interval`
already do between them, and it is the control that turns an arrival into a decision somebody
has to make.

This is the piece most likely to come back, and if it does it needs an answer to a question
this section does not have — see below.

## Still open

- **Three a day is a guess.** So are forty-five minutes and nine-to-ten.
- **What "Later" would mean, if it existed.** The obvious button, and the reason it is not
  built is that its behaviour is genuinely ambiguous. Ignoring already defers by
  `min_interval` — a day by default — so *Later* has to mean either **sooner** ("yes, I want
  this, ask again in a few hours") or **not today** ("skip past the next one"). Those are
  opposite behaviours behind one word, and picking wrong makes the button worse than absent.
  Note also that `Notification.maxActions` is 2, so Later cannot simply be added; it displaces
  Done or Drop.
- **Shaping the hours from when somebody actually answers.** The honest way to close part of
  the gap between the floor and the right moment. Pressing Done or Drop is a real event,
  reliably reported, and a few weeks of them say which hours a person is *receptive* in — not
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

Inferring *you did not mean it* from silence, and then quietly raising something less often, is
the product deciding on somebody's behalf that a thing they wrote down does not matter.

The line this draws is between the system giving up and a person changing their mind. The
second is what expiry and priority are, and they are welcome.
