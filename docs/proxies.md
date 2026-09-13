# Proxies

btw reaches one kind of thing on the open internet: the service an account's
[companion](companion.md) has a key for. Some networks cannot. A proxy is somewhere else to ask
from.

An administrator configures one at `/admin`. Nothing is on by default and nothing is inferred.

## Somewhere else is the whole requirement

**And it is easy to get wrong.** A proxy container on the same compose network goes out from the
same address, so a filter that blocks this instance blocks the proxy identically. It tests fine
against anything unrestricted and never once helps.

That is why the test press fetches **the gateways themselves** rather than an address somebody
types — every service, since they are separate hosts and are blocked separately. A proxy that
reaches everything except the one an account is on is one somebody would otherwise have called
working.

## Why the word is not "relay"

btw already has one: the SMTP relay, with a table, a settings block, four routes and a whole
[document](mail.md). A second would make "check the relay" stop being an instruction.

## One proxy, and no ladder

btw makes a handful of requests an hour in a loop nothing is waiting on. A chain to try in turn
would guard a cost that does not exist, and would turn a proxy that has quietly stopped working
into something the program routes around rather than something somebody fixes. Nothing is
remembered about what worked, for the same reason.

## On and off is not the same as gone

Switching a proxy off **keeps its address and its credential** — the difference between turning
something off to find out whether it was the problem and deleting it to find out. Only one of
those can be undone, since a token cannot be read back off the screen.

`enabled` is its own `PATCH` rather than a field on the save. They are different acts, and
folding the second into the first would make the only way to switch one off be sending its whole
settings back — the request most likely to come from something that read them stale.

**Saving switches it on.** Somebody who has just corrected an address is saying it should work
now, and a proxy left off holding fixed settings looks exactly like one still broken.

The test press **ignores whether it is switched on**: a proxy somebody switched off in order to
test it is the case that most wants answering.

## The two kinds

They work at different layers, which is why the form asks for different things.

|              | proxio                       | SOCKS5                            |
| ------------ | ---------------------------- | --------------------------------- |
| what changes | the request's URL            | the connection under it           |
| address      | `https://proxio.example.com` | `socks5://socks.example.com:1080` |
| credential   | one opaque token             | a username and a password         |
| client       | the ordinary one             | one per endpoint, kept for reuse  |

**proxio** takes the target as a percent-encoded `url` and its credential as `token`. The stored
address is the host only, so pasting the whole example URL works and the credential in it is
moved out of the address. Method, headers and body are inherited, which is what makes it usable
here: the gateway wants a `POST` with an `Authorization` header.

**SOCKS5** replaces the dialer, so a response already points at the gateway. `socks5h` is
accepted and behaves identically — the dialer sends the hostname rather than resolving locally,
so the scheme names the behaviour rather than selecting it. That is the useful one anyway: a
host blocked at DNS is only reached when the endpoint does the lookup.

Clients are cached per endpoint, keyed by **what the connection depends on** — kind, address,
username, password — and not by the row. A row key would go on using an old password after it
was corrected.

## Credentials

The token is write-only like the SMTP password, for the reasons in
[mail.md](mail.md#where-the-configuration-lives). An empty field on save means the stored one,
**but only while the kind and the address are unchanged**: carrying a proxio token to a socks
host would send a secret somewhere it was never meant for.

Three places would otherwise write one down, and each has a test.

- **The address.** `socks5://user:pass@host` is the natural way to write one down. What was
  pasted is taken apart and the address stored is the host alone.
- **The response's `Request.URL`.** A proxio response points at the relay, whose address ends
  `?url=…&token=…`. It is pointed back at what was asked for before it is returned.
- **The log.** `net/http` wraps every transport failure in a `*url.Error` printing what it
  dialled. The cause is kept and the address dropped.

## Where it lives

|                                   |                                                  |
| --------------------------------- | ------------------------------------------------ |
| `internal/proxy`                  | the two kinds, the dialing, and what is scrubbed |
| `internal/store/proxy.go`         | the row, and what is safe to show                |
| `internal/api/proxy.go`           | `/api/admin/proxy`, including the test           |
| `web/src/islands/admin/Proxy.tsx` | the screen                                       |

Only `internal/gateway` sends anything through it. Web Push goes to whatever endpoint a
browser handed us and mail goes to the relay, and neither is a host anybody is blocked from.
