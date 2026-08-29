# Mail

btw does not deliver mail. It hands a message to a relay an operator already has — their mail
provider, or a sending service — and that relay does the rest. Everything here is about the
handing over.

One thing sends today: the code that proves a recovery address. The relay exists because that
does, and because the next thing that needs to send will need it too.

## Where the configuration lives

**In the database, set from the admin page, not in the environment.**

An operator setting up mail gets it wrong two or three times — a port, a username that turns
out to be an address, a From the relay will not accept — and each correction should be a form
field and a test send, not a redeploy. The environment is the right place for things that must
be true before the process starts. This is not one of them.

One row, enforced. `singleton` is `UNIQUE` and `CHECK`ed, so a second configuration is a
constraint violation rather than a quiet question about which one is live.

The password is **stored as written**. There is no vault here to seal it under, and a
reversible scramble would only make it look protected: whoever can read `main.db` can already
read the session table and every password hash in it. The file is the boundary either way, and
pretending otherwise is worse than saying so.

It is **never sent back out**. `GET /api/admin/relay` carries `password_set` and no password —
readable by anything that can read a response, for no gain, since the form does not need it to
save a change. Saving with an empty password keeps the stored one, which is what lets somebody
correct a port without retyping a credential the form was never given.

## What is not configurable

**Whether the connection is encrypted.** `starttls` upgrades a plain connection, `implicit` is
TLS from the first byte, and there is no third option. A password crossing the network in the
clear is not a choice somebody should be able to make by accident, so a relay that does not
offer `STARTTLS` is refused by name rather than fallen back from — and the refusal says to try
implicit TLS on 465, because that is usually what is wrong.

**Whether credentials are required.** Relays that want none do exist, and a blank password is
accepted only with a blank username. Accepting one without the other would make "this relay
needs no authentication" indistinguishable from "somebody left the field empty", and the second
is far likelier.

## Sending

`internal/mail` opens the socket; `internal/store` decides what the relay is. Nothing in the
store touches the network and nothing in `mail` touches the database.

**Nothing is queued.** A message goes out while the request that asked for it is still open, so
the caller learns whether the relay accepted it. That is the whole point of a test send, and it
is what any later caller wants too — a recovery code that failed silently is worse than one
that failed loudly. If btw ever sends enough mail for that to hurt, a queue is a change to that
file rather than to its callers.

A refusal is a **`502` carrying the relay's own words**. Not a `500`: everything on this side
worked and something upstream did not, and a `500` sends an operator through the wrong logs.
Not "sending failed" either — "the host was wrong", "the credentials were rejected" and "the
certificate did not verify" are three different afternoons.

`PLAIN` first, `LOGIN` when that is all the relay offers. `LOGIN` is the same credentials in a
sillier shape, and enough relays speak nothing else that refusing it would mean refusing to
send at all. Both refuse to run over an unencrypted connection or against a host that is not
the one configured.

Messages are composed here rather than with a library: it is a dozen headers and a
quoted-printable body. Addresses go through `net/mail`, because a display name needs quoting
when it holds a comma and encoding when it holds anything outside ASCII, and either one done by
hand produces a header that parses as a different address than the one meant.

### Tested against a relay, not a mock

`internal/mail` starts a real SMTP server on a loopback port and holds a real conversation with
it. A mock of `net/smtp` would assert that `net/smtp` was called; what is worth asserting is
that STARTTLS is demanded and the upgrade happens, that the password never appears among the
lines read before it, that a `550` comes back with the relay's own reply attached, and that a
comma in a display name is quoted rather than parsed as a second recipient.

## Recovery addresses

An account carries one address or none, and **only an address somebody has proved they can
read**. Adding one is two steps: a code goes to the address and has to come back. Until it
does, the account has no recovery address at all — not a provisional one — so a flow abandoned
anywhere leaves exactly what was there before.

Storing whatever was typed is worse than storing nothing. A typo points recovery at a
stranger's inbox, and the owner finds out at the one moment they cannot afford to. It is also
the shape of an attack: a borrowed session sets an address of its own and comes back for the
account later, once recovery exists.

**Two tables, not a `proved` column.** `user_recovery` holds proved addresses;
`recovery_pending` holds attempts. A nullable flag is one forgotten `WHERE` clause away from an
unproved address being treated as proved, and confirming is a move from one table to the other
— so the only address anything can recover through is one that was proved.

The code is eight characters of Crockford base32 — no `I`, `L`, `O` or `U`, because it is read
off one screen and typed into another. Forty bits is not a key and does not need to be: it is
bounded to five attempts, expires in fifteen minutes, and authorises nothing but the address it
went to. It is stored hashed, so the only way to know a code is to be the recipient — which is
what the tests rely on rather than work around. Typing it back is forgiving about case, spaces,
and the letters the alphabet leaves out.

**Attempts are replaced, never accumulated.** Starting again is what somebody does when the
mail did not arrive, and two live codes for one account is two chances at the same guess. Five
wrong answers throws the attempt away rather than locking anything: a lockout is a state
somebody has to wait out, and starting again is faster and no weaker.

**One address, one account, held by whoever proved it last.** A unique index enforces it, and
confirming takes the address off whichever account had it. Whoever can read that inbox today is
who recovery through it would actually reach — a work address gets reassigned, somebody moves
out of a shared one — and refusing instead would refuse the person who really can read it while
leaving the one who cannot on record. It concedes nothing: anybody able to prove control of
that inbox could already recover the account attached to it.

**A code that could not be sent leaves nothing waiting.** The pending row is written before the
send, because the code has to exist before it travels — but a send that fails deletes it again.
Otherwise the page says it is waiting on a code that never left, and the way out of that state
is the button somebody just watched fail. A failed *change* leaves the address that already
worked, which is why dropping an attempt is its own store method rather than a flag on the one
that forgets everything.

Starting is refused outright when no relay is configured, before anything is written.
`GET /api/auth/recovery` carries `mail_configured` so the page can say why: whether this
instance can send mail is not a secret from the people whose recovery depends on it, and a
button disabled without a reason sends somebody looking for it in the wrong place.

One refusal — "that code is wrong or has expired" — covers wrong, expired, exhausted and absent
alike. Which of the four it was tells a caller something about an account that may not be
theirs, and tells the owner nothing they could not learn by trying again.

## What this does not do yet

**There is no forgotten-password flow.** A proved address is what one would read, and building
the binding first is deliberate — but until that exists, an address on file is a promise with
nothing behind it. It is the next thing worth building here, and it needs its own decisions:
a link rather than a code, a short life, and a login page that answers the same way whatever
the address turns out to be.

**Invitations are not sent.** An administrator hands the link over. Sending one binds its
address as a proved recovery address for free — the proof being that the link went to that
inbox and nowhere else — which is worth having and is not built.
