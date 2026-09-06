# Proxies

btw reaches exactly one thing on the open internet: the gateway an account's
[companion](companion.md) has a key for. Some networks cannot. A provider that blocks the host,
a machine behind a filter, a region the gateway refuses — the shape is always the same: the
gateway is fine and *we* are the problem. A proxy is somewhere else to ask from.

An administrator configures one at `/admin`. Nothing is on by default and nothing is inferred:
an instance with no proxy goes out directly, which is what almost every instance does.

## Somewhere else is the whole requirement

**And it is easy to get wrong.** A proxy container on the same compose network as btw goes out
from the same address, so a filter that blocks this instance blocks the proxy identically. It
tests fine against anything unrestricted and never once helps. A proxy has to be on a network
that can reach what this one cannot — a host somewhere else, a tunnel that comes out somewhere
else.

That is why the test press fetches **the gateway itself** rather than an address somebody types
or a service that echoes an IP. A proxy that reaches everything except OpenRouter is a proxy
somebody would otherwise have called working.

## Why the word is not "relay"

btw already has one. `relay` is the SMTP relay an operator hands mail to — a table, a settings
block, four routes and a whole [document](mail.md). A second thing called a relay would make
every sentence in either document ambiguous, and "check the relay" would stop being an
instruction.

## One proxy, and no ladder

There is no list, no priority and no falling back from one to the next.

btw makes a handful of requests an hour, to a single host, in a background loop that nothing is
waiting on. A chain to try in turn would be machinery guarding a cost that does not exist — and
worse, it would turn a proxy that has quietly stopped working into a thing the program routes
around rather than a thing somebody fixes. One proxy, switched on or off, and a test button to
say which it is.

Nothing is remembered about what worked either, for the same reason: there is one destination,
so there is nothing to learn about it that is not already on the screen.

## On and off is not the same as gone

Switching a proxy off **keeps its address and its credential**. That is the difference between
turning something off to find out whether it was the problem and deleting it to find out — and
only one of those can be undone, because a token is not something somebody can read back off
the screen and type again.

`enabled` is its own route, `PATCH`, rather than a field on the save. They are different acts:
saving says "this is what it should be", and switching off says "leave it exactly as it is and
stop using it". Folding the second into the first would mean the only way to switch one off was
to send its whole settings back, which is the request most likely to be sent by something that
read them stale.

**Saving switches it on.** Somebody who has just corrected an address or a token is telling us
the thing should work now, and making them press a second control to say so would leave a proxy
switched off holding settings that were fixed — which looks exactly like settings that are still
broken.

The test press **ignores whether it is switched on**, because the press means "would this work",
and a proxy somebody has just switched off in order to test it is the case that most wants
answering.

## The two kinds

They are not variations on each other — they work at different layers, which is why the form
asks for different things.

| | proxio | SOCKS5 |
| --- | --- | --- |
| what changes | the request's URL | the connection under it |
| address | `https://proxio.example.com` | `socks5://socks.example.com:1080` |
| credential | one opaque token | a username and a password |
| client | the ordinary one | one per endpoint, kept for reuse |

**proxio** takes the target as a percent-encoded `url` and its credential as `token`, fetches
it, and hands back what it got. The stored address is the host only — the path is this
program's business, so pasting the whole example URL still works and the credential in it is
moved out of the address rather than left on screen.

The method, the headers and the body are inherited, and that is what makes it usable here at
all: the gateway is reached with a `POST` carrying JSON and an `Authorization` header, and a
relay that only forwarded `GET`s would be no use for it.

**SOCKS5** replaces the dialer. The request is untouched, so a response coming back through one
already points at the gateway. `socks5h` is accepted and behaves identically: the underlying
dialer sends the hostname rather than resolving it locally, which is socks5h's semantics, so the
scheme names the behaviour rather than selecting it — and it is the useful one anyway, since a
host blocked at DNS rather than at the address is only reached when the endpoint does the
lookup.

Clients are cached per endpoint so connections can be reused, keyed by **what the connection
actually depends on** — kind, address, username, password — rather than by anything identifying
the row. A row key would be the tempting one and would be wrong exactly when it mattered:
correcting a password would go on using the old one out of the cache.

## Credentials

The token is write-only, exactly like the SMTP password, and for the reasons in
[mail.md](mail.md#where-the-configuration-lives). It is never sent to the browser, so an empty
field on save means "the one already stored" — **but only while the kind and the address are
unchanged**. A token belongs to an endpoint, and carrying a proxio token over to a socks host
would send a secret somewhere it was never meant for.

Three places would otherwise write one down, and all three are guarded.

- **The address itself.** `socks5://user:pass@host` is the natural way to write one of these
  down and would put the password on screen. What was pasted is taken apart, the parts go in
  their own columns, and the address stored is the host alone.
- **The response's `Request.URL`.** A response relayed through proxio points at the relay, whose
  address ends `?url=…&token=…`. Anything reading it to say where it ended up would print the
  credential. A relayed response is pointed back at what was asked for before it is returned.
- **The log.** `net/http` wraps every transport failure in a `*url.Error` that prints the
  address it dialled. Logged as it comes, one unreachable proxy writes the token into the log
  file on every attempt. The cause is kept and the address dropped.

A test asserts each of those, because all three are the kind of thing that works correctly right
up until the day something fails.

## Where it lives

| | |
| --- | --- |
| `internal/proxy` | the two kinds, the dialing, and what is scrubbed |
| `internal/store/proxy.go` | the row, and what is safe to show |
| `internal/api/proxy.go` | `/api/admin/proxy`, including the test |
| `web/src/islands/admin/Proxy.tsx` | the screen |

Only `internal/openrouter` sends anything through it, because it is the only thing in btw that
reaches out at all. Web Push goes to whatever endpoint a browser handed us and mail goes to the
relay, and neither is a host anybody is blocked from.
