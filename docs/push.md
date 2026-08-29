# Push

btw does not deliver notifications. It hands an encrypted message to the push service the
browser named — Apple's, Google's, Mozilla's — and that service wakes the device. Everything
here is about the handing over.

Three RFCs: [8030](https://www.rfc-editor.org/rfc/rfc8030) for the protocol,
[8291](https://www.rfc-editor.org/rfc/rfc8291) for the message encryption,
[8292](https://www.rfc-editor.org/rfc/rfc8292) for identifying the application server. All of
it is `internal/webpush`, about two hundred lines against the standard library.

## Why there is no library

`SherClockHolmes/webpush-go` is the obvious dependency and is mostly these two hundred lines
plus a JWT library. The argument against taking it is not size.

This is the one part of btw with no fallback behaviour. A message that fails to encrypt is the
product not working — silently, because a push that never arrives looks exactly like a push
nobody sent — and a bug in it is a bug found by reading somebody else's code. Two hundred lines
of standard-library crypto with the RFC's own test vector under them is a thing that can be
understood in an afternoon and then left alone.

Everything it needs is in the standard library now, which was not true a couple of years ago:
`crypto/ecdh` for the key agreement, `crypto/hkdf` (since Go 1.24) for the derivation,
`crypto/aes` and `crypto/cipher` for the record, `crypto/ecdsa` for the signature.

If this turns out to be three attempts and still wrong, the dependency is one import away and
this section is the record of why it was taken.

## The test vector is the point

`TestSealMatchesRFC8291` encrypts RFC 8291 §5's worked example with its own keys and salt and
compares the result byte for byte.

That test is the difference between *our code agrees with itself* and *our code agrees with
what a browser will try to decrypt*. A wrong `info` string in the key derivation round-trips
perfectly — encrypt and decrypt with the same mistake and the plaintext comes back — and
fails only against a real push service, as a `400` that reads like a malformed request. There
is no way to find that from inside.

Above it, `internal/nudge` drives the whole chain against a fake push service holding a real
subscription keypair, so the test that says a reminder arrives says it by decrypting one.

## VAPID

A P-256 keypair per instance, generated on first use and stored in `main.db`. See
[entities.md](entities.md#vapid) for why it is never rotated.

The `Authorization` header is RFC 8292's single-header form:

```
vapid t=<jwt>,k=<base64url public key>
```

The JWT is ES256 over `{aud, exp, sub}`.

- **`aud` is the push service's origin, not the endpoint.** A token scoped to one subscription
  would be a bearer token for it; scoped to the origin it is what it claims to be, an
  identification of the sender.
- **`exp` is twelve hours out.** The RFC's ceiling is twenty-four.
- **`sub` is `BTW_PUBLIC_URL`.** RFC 8292 allows an `https:` contact URI as well as a
  `mailto:`, so this needs no variable of its own — and an instance's own address is a better
  contact than an operator address that will be stale within a year.

**`ecdsa.Sign`, not `ecdsa.SignASN1`.** JWS wants `r` and `s` each left-padded to 32 bytes and
concatenated. `SignASN1` is the obvious call and produces a DER structure that every push
service rejects. A test asserts the signature is 64 bytes, because the failure is otherwise a
`400` that looks like a malformed request rather than a malformed signature.

## The message

RFC 8291 `aes128gcm`. ECDH against the subscription's `p256dh`, HKDF to a content key and a
nonce, one AES-GCM record, framed as:

```
salt(16) ‖ record size(4) ‖ key id length(1) ‖ ephemeral public key(65) ‖ ciphertext
```

with `Content-Encoding: aes128gcm`. The key id is the application server's ephemeral public
key, which is how the browser knows what to run the key agreement against. The two public keys
are bound into the derivation, so a message encrypted for one subscription cannot be replayed
at another.

The record's plaintext ends with `0x02`, RFC 8188's delimiter for the last record. `0x01` here
would be read as "more records follow" and rejected.

The body may not exceed **4,096 bytes** — an oversized payload is refused before it is sent,
because `413` is not retryable and the same bytes would only be refused again. That leaves a
little under four thousand for the text, which is far more than a reminder should be;
`reminders.text` is capped at 500 characters for reasons of lock screens rather than of
cryptography.

### The headers that make a nudge timely

| header | value | why |
| --- | --- | --- |
| `TTL` | `3600` | The one easiest to set to a day out of habit. A phone that has been off for two hours should not receive "btw, ring the dentist" at midnight; that nudge belonged to an afternoon that has passed. An hour, and then the push service drops it on our behalf |
| `Topic` | `btw` | A push service collapses undelivered messages sharing a topic, so a phone coming back from a flat battery gets the most recent nudge, once, rather than three at the door. At most 32 URL-safe characters |
| `Urgency` | `normal` | — |

## What a refusal means

Mapped once, in `webpush.classify`.

| status | reason | what happens |
| --- | --- | --- |
| `201` | — | stamp `last_ok_at`, clear the failure count |
| `404`, `410` | `gone` | **delete the device.** It is not coming back |
| `401`, `403` | `refused` | the push service rejected our identity; keep the device |
| `429`, `5xx` | `busy` | keep the device, record the failure |
| `413` | `too-large` | never retried — the same bytes would be sent again |
| `400` | `invalid` | almost always a malformed VAPID signature. A bug, not weather |
| — | `unreachable` | DNS, a refused connection, a timeout |

**Deleting on `410` rather than counting failures is the important one.** A browser that has
been reinstalled leaves an endpoint that will refuse forever, and a device list full of dead
rows is how somebody concludes the product is broken when one of their four entries is live.

Equally, a `429` must **not** delete. A test asserts a busy push service does not cost somebody
their device.

## The browser side

`web/public/sw.js`, served from the root so its scope covers both shells. It is deliberately
not part of the bundle: a worker with a content hash in its name is a worker the browser cannot
find at a stable address.

- **`push`** → `showNotification("btw", { body, tag: "btw", renotify: true, … })`. The sentence
  somebody wrote is the whole message; a title like "Reminder" above it is a word nobody needs
  to read twice. One tag for everything, so a second notification replaces the first on screen
  rather than stacking — it pairs with the `Topic` header, which does the same thing one hop
  earlier. Verified by delivering three pushes to a real worker and counting what is on
  screen: one, carrying the newest text and the newest nudge id.

  `renotify: true` is load-bearing twice over. It re-alerts rather than swapping the text
  silently, which is what a reminder wants — and because the spec refuses `renotify` without
  a `tag`, deleting the tag throws a `TypeError` instead of quietly stacking notifications
  again.
- **`notificationclick`** → `POST /api/nudges/{id}/{done|drop}`, then focus or open the app.
  Same-origin from a worker, so the session cookie rides along under `SameSite=Lax` and
  `Sec-Fetch-Site` says `same-origin` — which is why the CSRF guard needs no exception for it.
  A `401` means the session lapsed while the phone was in a pocket: fall through and open the
  app, which is the right thing to do about that rather than swallowing it.
- **`pushsubscriptionchange`** → re-subscribe and re-register. Support for this event is uneven,
  so it is the optimisation and **not** the mechanism. The app re-registers whatever
  subscription it holds on **every load**, which is what actually keeps a device alive: browsers
  rotate an endpoint without asking, and the alternative ending is somebody quietly never being
  nudged again.

A push that cannot be parsed still shows something. The subscription was granted under
`userVisibleOnly`, and a browser that catches us showing nothing may revoke it.

### The buttons cannot be load-bearing

The worker declares two actions, Done and Drop. Two, because `Notification.maxActions` is 2 on
every platform measured — so there was never room for a third, and any new button displaces an
existing one.

**They are not reliably drawn.** On iOS they have been observed not to appear at all; other
platforms hide them behind a long press rather than showing them inline. This is not something
a web app can fix, and it is not something to design around by pretending otherwise.

So the rule is: **every action a notification offers must also be reachable by tapping the
notification, and again in the list.** A plain tap with no action falls through to focusing or
opening the app, and the same Done and Drop sit on every row. The buttons are a shortcut for
the platforms that render them, never the only way to answer.

## One browser, one device

An endpoint identifies a **subscription**, not a browser, and browsers replace subscriptions
on their own — after a permission is re-granted, after site data is cleared, after a
`pushsubscriptionchange`. Registering upserts on the endpoint, so a rotated subscription
arrived as a *new row beside the old one*, both stayed live at the push service, and one
press of "send one now" sent two pushes. One browser, two notifications.

Neither the `Topic` header nor the notification tag can help with that. Both collapse
messages within one subscription, and this is two.

So a device also carries a **client id**: a UUID the app mints once and keeps in
localStorage. Registering deletes any other row for that account with the same client id, so
a rotated subscription replaces its row rather than joining it.

- **localStorage rather than a cookie**, because it is per browser profile, which is exactly
  the grain a push subscription has.
- **Losing it is harmless.** The next registration mints another; the stale row waits to be
  deleted when its endpoint answers `410`.
- **Empty is never matched.** Rows from before the column existed have no browser to belong
  to, and an unknown browser must not collapse another unknown browser's row.
- The **service worker asks a window** for the id when it re-subscribes on its own, since a
  worker has no localStorage. With no window open the answer is empty, which collapses
  nothing — the safe direction, because a wrong id would delete a device somebody is using.
- **Minting and reading are separate calls.** `clientID()` mints one if there is none, and is
  only ever reached while registering; `storedClientID()` reads. The settings screen uses the
  reader, because a page that quietly writes would give an unregistered browser an identity
  by being looked at.

### Rows the client id cannot fix

It only collapses rows it can recognise, so an account that accumulated duplicates *before*
the column existed still has them — every one of which receives its own copy of every nudge.
Deleting them automatically is not available: a row with no client id is indistinguishable
from a second browser that simply has not registered since, and guessing wrong would delete a
device somebody is using.

So the list says so instead. Each row this browser owns is marked, the rest are counted, and
the sentence names the consequence — *each one is a separate copy of the same reminder* —
because "you have three devices" is not information anybody acts on.

**Whether *this* browser is registered is a different question from whether any device is**,
and conflating them is a bug worth naming: a laptop that had never registered was told "this
browser will receive nudges" because a phone had, and was offered no button. The same fault
hid the button from a browser whose row predates the client id — which is precisely the
browser that needs to press it, since registering adopts its existing row rather than adding
one.

## What the push service learns

The body is encrypted to keys only the browser holds, so the push service sees a length and a
time and never a word.

It does learn *when* a person is nudged, and that is not encryptable. Worth one honest line
rather than a claim of end-to-end that overstates it.

## The origin is load-bearing

A push subscription is bound to the origin that created it. Moving btw between addresses — a
tunnel to a real domain, one host to another — silently kills every device already registered:
not erroring, just never delivering. They have to be added again.

Devices are per-origin, so carrying a database across is carrying a table of endpoints for a
host that no longer means anything.

Related, and the reason a checkout cannot test this over plain HTTP: no browser registers a
service worker against an insecure origin. `serve` warns at startup when `BTW_PUBLIC_URL` is
`http`, and says both consequences — the cookie ships without `Secure`, and nothing can
subscribe at all.
