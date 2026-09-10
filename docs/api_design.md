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
GET    /api/reminders                    {reminders: [...]}
POST   /api/reminders                    {text} → the reminder
PATCH  /api/reminders/{id}               {text?, note?} → the reminder
POST   /api/reminders/{id}/bin           → 204
POST   /api/reminders/{id}/restore       → 204
DELETE /api/reminders/{id}               → 204
```

**`POST` takes one field.** Typing a sentence is the entire path to a reminder existing;
everything else has a default that is deliberately invisible.

**The bin has a route of its own**, below, rather than `?binned=true` on this one. A parameter
says "the same collection, different rows", and the bin is a place: its own screen, its own
life, its own sweep.

`PATCH` takes pointers, so **absent leaves a field alone and empty clears it** — which is how
a description is deleted without also retyping the sentence. It changes wording only: ending a
reminder has its own route, and folding it in would make "fix this" and "I am finished with
this" the same request.

A reminder carries `id`, `text`, `note`, `created_at` and `binned_at`. It deliberately does
**not** carry `last_nudged_at`: that is how the selection works, not something a person is meant
to reason about, and showing it invites exactly the arithmetic this product exists to avoid.

**One route, where there were two.** `done` and `drop` ended a reminder identically and differed
only in the word beside them — see [conventions.md](conventions.md#the-bin-is-a-place-not-a-state).
`restore` is for the press that was a mistake, `DELETE` is for the typo and for emptying the bin
by hand, and anything left in the bin thirty days is deleted by a sweep.

**Binning an already-binned reminder is `204`, not an error.** A notification that has sat on a
lock screen since yesterday can be answered after the thing was already binned in the app, and
the person pressing it wanted it gone either way.

### Bin

```
GET    /api/bin                          {reminders: [...]}
DELETE /api/bin                          → 204
```

What has been binned and not yet thrown away. A reminder gets there through
`POST /api/reminders/{id}/bin` and comes back through `/restore`; `DELETE /api/reminders/{id}`
takes one out for good, and `DELETE /api/bin` takes all of them — the same end the thirty-day
sweep reaches on its own, asked for now.

**No confirmation server-side.** The interface asks, and a second refusal here would guard
against a request nobody can make by accident: it takes a session, a same-origin fetch and a
deliberate `DELETE`.

### Nudges

```
POST   /api/nudges                       → {sent: bool} — send one now
POST   /api/nudges/{id}/bin              → 204
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

### Proxy

```
GET    /api/admin/proxy                  {configured, kind, url, username, token_set, enabled}
PUT    /api/admin/proxy                  {kind, url, username, token} → the above
PATCH  /api/admin/proxy                  {enabled} → the above
DELETE /api/admin/proxy                  → 204
POST   /api/admin/proxy/test             {} → {reached, took_ms}
GET    /api/admin/companion              {model, fallback_model, model_limit}
PUT    /api/admin/companion              {model} → the above
```

An administrator's, unlike the companion below it: how this machine reaches the internet is one
fact about the machine, not one per account.

**The token never comes back out**, the way the relay's password does not. An empty `token` on
save keeps the stored one — **but only while `kind` and `url` are unchanged**, because a
credential belongs to an endpoint and carrying one to a different host would send a secret
somewhere it was never meant for. A save with neither is a `400`.

`PATCH` is its own route rather than an `enabled` field on the save, and **saving switches the
proxy on**. Both in [proxies.md](proxies.md#on-and-off-is-not-the-same-as-gone).

`POST /api/admin/proxy/test` fetches the gateway through the saved proxy, whether or not it is
switched on — the press means "would this work". A refusal is a `502` carrying whatever failed,
with any address scrubbed out of it first, since a transport error names what it dialled and for
proxio that ends in the token.

### Companion

```
GET    /api/companion                    {configured, model, key_set, about, default_model,
                                          about_limit, advice?}
PUT    /api/companion                    {api_key, model, about} → the above
DELETE /api/companion                    → 204
POST   /api/companion/test               {} → {model, tokens}
GET    /api/companion/advice             {reminders: [{id, text, advised, categories?,
                                          exclusive?, curve?, advised_at?}], days, windows}
POST   /api/companion/advice/refresh     {} → the above, once it has asked
```

`GET /api/companion/advice` is the open list with what the companion said about each, and
carries a `curve` **only when it is the shape the weighting actually reads** — an answer the
program ignores is not something to draw. When it is not, `shape` names what arrived instead,
so a screen can say *it sent 7x24* rather than leaving somebody to find it in a log. `days` and
`windows` come with it so the screen cannot disagree with the server about the size of a week,
and `stale`, `advised_at` and `error` say whether an answer is still owed.

`POST /api/companion/advice/refresh` **asks the companion and waits**, answering with the advice
as it then stands. It is the longest request in the product by some way — as long as a model
takes — and that is the point: somebody presses it to see a change, and a 202 telling them to
come back makes them judge one they cannot see.

It marks the advice stale first, because a look declines when nothing has changed. Rate limited
at four a minute, since it makes an outbound request on the caller's behalf against a quota with
fifty a day in it; the screen holds the button for twenty seconds as well, which is the cooldown
for the screen being used rather than a ceiling. A failure is a `502` carrying the gateway's own
words.

Behind `requireSession` and never `requireAdmin`: a companion is one account's, unlike the
relay. Why, in [companion.md](companion.md#the-companion-is-an-accounts-not-the-instances).

**The key never comes back out**, the way the relay's password does not. `key_set` is what the
form needs, and saving with an empty `api_key` keeps the stored one — which is what lets
somebody change their model without retyping a credential the form was never given. `about`
does come back, because it is a text field somebody edits.

`default_model` is the model a blank field would actually ask — an administrator's
`/api/admin/companion` when one is set, and the compiled-in one otherwise — so the placeholder
cannot offer one model while the loop uses another.

`PUT /api/admin/companion` is not checked against OpenRouter and could not be: the instance has
no key of its own. An empty `model` clears it. It exists because slugs are retired, and without
it the day the compiled-in model goes every account that never chose one breaks at once.

`advice` is `{status, advised_at, attempted_at, error, stale}`, and is **absent until a key is
configured** — reporting on a loop that never runs would be reporting on nothing. `status` is
one of:

| | |
| --- | --- |
| `none` | never asked |
| `some` | it has an opinion about part of the open list |
| `all` | it has an opinion about all of it |
| `limited` | the last attempt hit the key's quota |
| `failed` | the last attempt failed for a reason somebody has to fix |

`limited` is separated from `failed` on purpose. A quota is not a mistake and wants nothing
done about it — the next pass is the wait — and showing it the way a rejected key is shown
sends somebody to check a key that is fine.

**`some` and `all` rather than a count**, which is this payload's one temptation: it would be
easy to send "5 of 7 reminders". [There are no counts](#there-are-no-counts) is an API rule and
not only an interface one, and a workload is exactly the number this product exists not to
show. The server compares the two numbers and sends the comparison. A test asserts no count
reaches the body.

`POST /api/companion/test` puts one real completion to **the values in the body**, reconciled
against the stored ones by the same rule a save follows: an empty `api_key` means the stored
key, an empty `model` the stored model or the default. The button sits inside the edit dialog,
so it has to mean the fields beside it — and trying stores nothing, so a key that does not work
is not left behind by having been tested. It reports which model actually answered, which is
not always the one asked for. A refusal is a `502` carrying the gateway's own words, on the
same argument as a refused mail send.

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
postRemindersByIdBin          POST   /api/reminders/{id}/bin
deleteDevicesById             DELETE /api/devices/{id}
postAuthInvitesByTokenAccept  POST   /api/auth/invites/{token}/accept
patchRhythm                   PATCH  /api/rhythm
```

**One module per root**, under `web/src/api/actions/` — `auth`, `reminders`, `rhythm`,
`devices`, `nudges`, `push`, `companion`. A single `actions.ts` holding all of them meant every component
importing from one file that knew about every endpoint in the product, and the file only ever
grows. There is no barrel re-exporting them: an import that names the root it came from says
where to go and looking for it.
