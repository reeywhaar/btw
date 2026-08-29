# btw

A place to put a thought down and stop carrying it.

Some of the things you mean to do have no *when*. "Go to the circus." When? No idea — not
today, not never, sometime. Every reminder application asks that question before it will
hold the thought, and because there is no answer the thought either never gets written down
or it goes into a list and rots there. Both endings leave you still carrying it.

btw takes the sentence and asks nothing else. A few times a day, at hours nobody picked, one
of them arrives as a notification. Sometimes it lands at a moment that makes it obvious and
you go; sometimes you read it and find you do not want it any more, and you drop it. Both
are wins. Both end with the thing out of your head.

There is no inbox, no due date, no overdue, no streak, and no count anywhere. A nudge nobody
answers costs nothing and comes round again.

## Running it

```sh
docker run -d --name btw \
  -e BTW_PUBLIC_URL=https://btw.example.com \
  -v btw-data:/data \
  -p 8080:80 \
  ghcr.io/reeywhaar/btw:latest
```

On first run it prints an invitation link built from `BTW_PUBLIC_URL`. Open it, choose a
name and a password, and that is the administrator. No default password exists at any point.
`docker exec btw invite` prints another when the first one scrolls out of a log.

| variable | default | meaning |
| --- | --- | --- |
| `BTW_PUBLIC_URL` | *required* | The address you open in a browser |
| `BTW_DATA_DIR` | `/data` | Where `main.db` and `derived.db` live |
| `BTW_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

**`BTW_PUBLIC_URL` must be https in practice.** No browser registers a service worker
against an insecure origin, so on plain http nothing can subscribe and no nudge can ever
arrive. It is also the origin every push subscription is bound to: moving btw to a different
address silently kills every device already registered, and they have to be added again.

Mount `/data`. `main.db` is the file worth backing up — accounts, reminders, devices, and the
VAPID keypair, which cannot be regenerated without invalidating every subscription at once.
`derived.db` is sessions, the day's plan and the nudge log, and is designed to be deletable.

## Turning on notifications

Open btw, go to settings, and press **Turn on nudges**. Then press **Send one now**, which
walks the whole chain — permission, subscription, VAPID, encryption, service worker — and
tells you whether one arrived.

**On iPhone and iPad, add btw to your Home Screen first.** Safari delivers push only to an
installed web app, and btw says so rather than offering a button that cannot work there.

## Building from a checkout

```sh
go test ./...          # passes with no frontend build present
cd web && npm ci && npm run build
docker build -t btw .
```

The listen port is `:80` inside the container and is not configurable; remap it with `-p`.

## How it works

Two decisions, kept apart on purpose.

**When** is `internal/rhythm`: a person's waking window is cut into as many blocks as their
daily budget, and one instant is drawn uniformly inside each — unpredictable within its
block, never three in an hour, and always terminating. Seeded by person and date, so a day's
plan is reproducible when somebody asks why it went off at that hour. A slot more than ten
minutes late is dropped rather than fired: three notifications arriving together, all about
moments that have passed, is what teaches somebody to swipe the channel away.

**What** is `internal/pick`, decided at the instant of sending and never in advance:

```
weight = priority × min(4, (now − last_nudged_at) / min_interval)
```

Being nudged sets the elapsed time to zero, so a reminder's weight collapses and it is
hard-blocked by its own floor besides. It re-enters the pool as the floor passes and grows
likelier the longer it goes unmentioned. Two reminders alternate; a hundred rotate. Nothing
eligible sends nothing at all — never a repeat, never padding.

Delivery is Web Push against RFC 8030, 8291 and 8292, written on the standard library and
checked against RFC 8291's own worked example. The push service sees a length and a time,
never a word.

## Documentation

[docs/](docs/README.md) settles the decisions so they do not have to be re-argued.

| | |
| --- | --- |
| [nudges.md](docs/nudges.md) | When somebody is nudged, and what with |
| [push.md](docs/push.md) | Web Push: the encryption, the headers, and the iOS problem |
| [entities.md](docs/entities.md) | The two databases, every table, every invariant |
| [api_design.md](docs/api_design.md) | HTTP conventions and the endpoint reference |
| [backend.md](docs/backend.md) · [frontend.md](docs/frontend.md) | Package layout, islands, the API layer |
| [stack.md](docs/stack.md) · [conventions.md](docs/conventions.md) · [deploy.md](docs/deploy.md) | Dependencies, naming, shipping |
