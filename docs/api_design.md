# API design

## Shape

**JSON in, JSON out, under `/api`.** No version segment: the frontend ships inside the same
image as the backend, so there is no third-party client whose compatibility a version would
protect. If an external client ever appears, that is when `/api/v1` earns its place — and
adding it then is a routing change, not a migration.

**Field names are `snake_case`**, matching the Go struct tags and the SQL columns underneath
them, so a field is the same string from the column to the browser.

**Timestamps are Unix seconds**, always named `*_at`. Rendering in a reader's zone is the
browser's job. The one exception is a rhythm's waking window, which is minutes since local
midnight because it is a wall-clock preference rather than an instant.

**Ids are opaque strings** with a type prefix, never parsed by the client. A malformed one is
refused by `ids.Valid` before it reaches a query, so a typo is a `400` rather than an empty
result set that looks like a `404`.

**Lists come in an envelope**, `{"reminders": [...]}`, never a bare array. The envelope is
what lets a field be added later. It deliberately carries no `total` — see below.

**No pagination.** Nobody has thousands of reminders. Ids sort chronologically, so a cursor is
available whenever it is needed.

## There are no counts

Not on the list, not on the archive link, not in a response body, not in the title, not on the
icon. This is an API rule and not only an interface one, because a count in a payload is a
count somebody will render.

The whole product is the absence of a number that goes up.

## Refusals are honest

btw serves a login page at `/`. It announces what it is by existing, so there is nothing to
disguise and no reason to collapse every refusal into one padded 404.

| situation | status |
| --- | --- |
| Bad input, unparseable body, failed validation | `400` |
| No session, or an expired one | `401` |
| Unknown id, or somebody else's | `404` |
| Known path, wrong method | `405` |
| Duplicate username, an invitation already used | `409` |
| Mutating request whose body is not `application/json` | `415` |
| Rate limited | `429` |

Errors are `{"error": "a sentence"}`. The sentence is written for the person who will read it
in the interface, which is why `internal/store` builds classified errors through
`store.NotFound`, `store.Conflict` and `store.Invalid` rather than
`fmt.Errorf("%w: …", ErrNotFound)` — the latter renders as `not found: no reminder r_1`
wherever it is shown, and these are shown.

**Somebody else's reminder is `404`, not `403`.** Whether a stranger keeps a reminder is not
the caller's business either way, and it makes scoping the lookup and checking the owner one
operation rather than two that can disagree.

## Authentication

A session cookie, `btw_auth`: `HttpOnly`, `SameSite=Lax`, `Path=/`, and `Secure` whenever
`BTW_PUBLIC_URL` is `https`. There is no bearer token, no API key and no header-based auth of
any kind.

Sliding expiry of one week since last use, refresh throttled to once an hour. See
[entities.md](entities.md#sessions).

An unauthenticated request to `/api/*` gets `401` and a JSON body. **The server never issues a
redirect for an API call** — a `302` to an HTML page is the least useful thing a `fetch` can
receive. The island reads the `401` and sends somebody to `/login` itself.

### CSRF

Three parts, all cheap:

1. `SameSite=Lax` on the cookie.
2. A mutating request carrying `Sec-Fetch-Site: cross-site` is refused with `403`. A browser
   sets this and a script cannot forge it.
3. A mutating request with a body must declare `application/json`, or `415`. Checked only when
   a body is actually present — a `DELETE` legitimately carries none, and demanding a content
   type for an absent body is a rule that only ever catches our own client.

A service worker on this origin sends `Sec-Fetch-Site: same-origin`, which is what lets the
notification buttons post back with no exception carved into the guard.

### There is no CORS middleware

Its absence is load-bearing, not an oversight. The browser only ever talks to this origin;
adding `Access-Control-Allow-Origin` would weaken two of the three defences above. A test
asserts the header is never emitted, on any route, including the SPA and the 404.

### Rate limits

Two buckets on login, with two different jobs. The **global** one bounds bcrypt: at cost 12 on
an unauthenticated endpoint it is a CPU exhaustion vector before it is an authentication one.
The **per-username** one stops somebody working through a password list against one account,
which the global limit alone would not — it would only make them share the budget with
everybody else.

`POST /api/nudges` is limited per principal: it makes an outbound request on the caller's
behalf.

Limiters live on the `Server` rather than at package level. Two instances in one process —
which is what a test suite is — would otherwise share one budget and lock each other out.

## Endpoints

### Auth

```
POST   /api/auth/login                   {username, password} → 204 + Set-Cookie
POST   /api/auth/logout                  → 204
GET    /api/auth/me                      {id, username, role, created_at}
GET    /api/auth/invites/{token}         {role, expires_at} — validity only
POST   /api/auth/invites/{token}/accept  {username, password} → 204 + Set-Cookie
```

**Everything about proving who you are is under one root.** These were five paths at the top
level — `/api/login`, `/api/me`, `/api/invites/…` — sitting beside the resources they are
not. An invitation belongs here rather than under a resource of its own, because accepting
one is how an account starts; it is authentication, not a thing somebody keeps.

`GET /api/auth/invites/{token}` tells the acceptance page whether a link is live before
somebody types a password into it. It reveals nothing but its own validity.

Accepting signs you in immediately. Being shown a login form straight afterwards is asking
somebody to prove something they just proved.

`GET /api/push/key` is public because the page needs it before there is any question of a
session, and because it is a public key. What it reveals is that this instance sends push
notifications, which it announces by existing.

### Reminders

```
GET    /api/reminders[?done=true]        {reminders: [...]}
POST   /api/reminders                    {text} → the reminder
PATCH  /api/reminders/{id}               {text?, note?} → the reminder
POST   /api/reminders/{id}/done          → 204
POST   /api/reminders/{id}/revive        → 204
DELETE /api/reminders/{id}               → 204
```

**`POST` takes one field.** Typing a sentence is the entire path to a reminder existing;
everything else has a default that is deliberately invisible.

**Live and finished are two calls, not one call with a filter**, because they are two different
screens and the finished list is the one nobody looks at.

`PATCH` takes pointers, so **absent leaves a field alone and empty clears it** — which is how
a description is deleted without also retyping the sentence. It changes wording only: ending a
reminder has its own route, and folding it in would make "fix this" and "I am finished with
this" the same request.

A reminder carries `id`, `text`, `note`, `created_at` and `done_at`. It deliberately does **not** carry
`last_nudged_at`: that is how the selection works, not something a person is meant to reason
about, and showing it invites exactly the arithmetic this product exists to avoid.

There is one *done* route and not a `done` and a `drop`, because both end a reminder
identically. Which button was pressed is recorded on the nudge — see below — and only when
there was a nudge to record it against.

`revive` is for the one pressed by mistake. `DELETE` is for the typo.

**Ending an already-ended reminder is `204`, not an error.** A notification that has sat on a
lock screen since yesterday can be answered after the thing was already ended in the app, and
the person pressing it wanted it ended either way.

### Nudges

```
POST   /api/nudges                       → {sent: bool} — send one now
POST   /api/nudges/{id}/done             → 204
POST   /api/nudges/{id}/drop             → 204
```

**`POST /api/nudges` creates one**, which is what the button does and what the path now says.
It was `/api/nudge` — a singular root beside a plural one, for one subject.

The other two are what the service worker calls. `{id}` is a **nudge** id rather than a reminder id, so acting
on a notification cannot act on the wrong thing after the list has been edited, and so the log
records that this arrival is the one that was answered.

The verb is the last path segment and one handler serves both, so they cannot drift apart.

### Rhythm

```
GET    /api/rhythm                       {timezone, window_enabled, wake_minute, sleep_minute,
                                          budget, silent, max_budget}
PATCH  /api/rhythm                       any of the above, all optional
```

Fields are pointers in the request struct, so **absent and zero are different**: a budget of
`0` is somebody switching nudges off, and a missing budget is a request about something else.

`max_budget` is the most anybody may ask for, and is a plain number: the budget is an
interval rather than a count — the waking window divided by it — so nothing about the window
bounds it.

**There is no `next_nudge_at`, and there never will be.** A person who can see that the next
nudge is at 14:32 is a person waiting for 14:32, and the surprise is the mechanism. A test
asserts this response leaks no scheduling detail.

A change drops any nudge already scheduled, because it was decided under the old answer — at
an interval that has changed, or for a moment that may now be the middle of the night. The
next tick works the whole thing out again, which is all a rhythm change has to do now that
there is no plan to redraw.

### Devices

```
GET    /api/devices                      {devices: [{id, label, created_at, last_ok_at, failure_count, last_error}]}
POST   /api/devices                      {endpoint, p256dh, auth, label} → {id, label}
DELETE /api/devices/{id}                 → 204
```

`POST /api/devices` is **idempotent on the endpoint**: a browser re-registering an unchanged
subscription updates the row it already has rather than growing the list every time somebody
opens the app. Registering an endpoint that belongs to another account moves it — see
[entities.md](entities.md#devices).

**The endpoint never comes back out.** It is a capability: anybody holding it and a VAPID key
can put text on that lock screen. A test asserts it never appears in a response.

`POST /api/nudges` is the button that proves the chain — permission, subscription, VAPID,
encryption, service worker, notification — in one press, without waiting hours for a slot. It
stays in the product after it has served its purpose in development, because setting up a new
phone raises exactly the same question.

It goes through the identical path a scheduled nudge takes. A test button that takes a
shortcut tests the shortcut.

It answers `200` with an `outcome` of `sent`, `nothing` or `undelivered` — none of them an
error, and the last two are different problems. It also ignores each reminder's own interval,
which the scheduled path does not. Both in
[nudges.md](nudges.md#the-floor-is-the-schedulers-rule-not-the-buttons).

## Handler conventions

- One file per resource in `internal/api`, one function per endpoint.
- Decode into a request struct with a size-limited body and `DisallowUnknownFields`. Never into
  a map: a map accepts anything and moves every validation into the handler, one forgotten
  check at a time.
- Validate before touching the store; the store's `ErrInvalid` is the backstop, not the first
  line.
- Store errors map to status codes in exactly one place, `Server.fail`.
- Authorisation is decided at **registration**, not inside a handler. A handler cannot forget
  to check, because a handler registered without `requireSession` is visibly registered without
  it.
- `/api/` has a catch-all returning a JSON `404`, so a mistyped API path never falls through to
  the SPA and reaches a `fetch` as an HTML document it cannot parse.

## Client naming

Actions are named mechanically from the route — `<method><PathSegmentsInPascalCase>`, with
`By<Param>` for a path parameter — so the mapping is reversible and nobody has to guess.

```
getReminders                  GET    /api/reminders
postReminders                 POST   /api/reminders
postRemindersByIdDone         POST   /api/reminders/{id}/done
deleteDevicesById             DELETE /api/devices/{id}
postAuthInvitesByTokenAccept  POST   /api/auth/invites/{token}/accept
patchRhythm                   PATCH  /api/rhythm
```

**One module per root**, under `web/src/api/actions/` — `auth`, `reminders`, `rhythm`,
`devices`, `nudges`, `push`. A single `actions.ts` holding all of them meant every component
importing from one file that knew about every endpoint in the product, and the file only ever
grows. There is no barrel re-exporting them: an import that names the root it came from says
where to go and looking for it.
