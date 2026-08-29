# Stack

Every dependency is a thing that can break, change under us, or need explaining to somebody
reading this in a year. Each one below earns its place.

## Backend

| what | version | why |
| --- | --- | --- |
| Go | 1.27 | Routing patterns with methods (`POST /api/reminders/{id}/done`) removed the last reason to take a router dependency |
| `net/http` | stdlib | See above. No chi, no gorilla, no gin |
| `log/slog` | stdlib | Structured logging without a dependency |
| `crypto/ecdh`, `crypto/hkdf`, `crypto/aes` | stdlib | Web Push message encryption, RFC 8291. `crypto/hkdf` has been in the standard library since 1.24, which is what removed the last reason to take a library for this — see [push.md](push.md) |
| `crypto/ecdsa` | stdlib | The VAPID signature, RFC 8292 |
| `time/tzdata` | stdlib | The zone database, compiled in. About 450KB, and the reason is in [nudges.md](nudges.md#the-timezone-is-real) |
| `github.com/spf13/cobra` | v1.10.2 | `serve`, `invite`, `healthcheck` — subcommands rather than flags, so `docker exec btw invite` reads as what it does |
| `modernc.org/sqlite` | v1.57.0 | Pure Go. No cgo means `CGO_ENABLED=0`, a static binary, and an Alpine image with no toolchain in it |
| `golang.org/x/crypto` | v0.55.0 | bcrypt, cost 12 |

Three direct dependencies. That is the whole list and it should stay short enough to read.

### Not used, deliberately

- **No Web Push library.** `SherClockHolmes/webpush-go` is the obvious one and is mostly the
  two hundred lines in `internal/webpush` plus a JWT library. The argument against it is not
  size: this is the one part of btw with no fallback behaviour, so a message that fails to
  encrypt is the product not working, and a bug in it would be a bug found by reading
  somebody else's code. It is checked against RFC 8291's own worked example. See
  [push.md](push.md).
- **No JWT library.** A VAPID token is two base64url segments and an ES256 signature.
- **No ORM, no query builder.** Every query is SQL in `internal/store`. The schema is eleven
  tables; an abstraction over it would be larger than it.
- **No migration framework.** An append-only list of Go `Migration` values tracked by
  `PRAGMA user_version`, each in its own file and its own transaction.
- **No job queue.** There was going to be one. What it would have done is in
  [entities.md](entities.md#there-is-no-queue).
- **No config file.** Three environment variables. See [deploy.md](deploy.md).
- **No CORS middleware.** The browser only ever talks to this origin. Its absence is
  load-bearing — see [api_design.md](api_design.md#there-is-no-cors-middleware).

## Frontend

| what | version | why |
| --- | --- | --- |
| React | 19 | — |
| TanStack Query | 5 | Every read is a server read. There is no client state worth a store |
| Tailwind | 4, via `@tailwindcss/vite` | — |
| TypeScript | 7 | `strict`, `noUncheckedIndexedAccess`, `verbatimModuleSyntax` |
| Vite | 8 | One config, two entries |
| Prettier | 3 | Formatting is not a discussion |

### Not used, deliberately

- **No router.** Two routes and no parameters, driven by the History API in
  `islands/app/route.ts` — about thirty lines, which is less than the configuration a router
  needs. When a third route arrives with a parameter in it, that is when the dependency
  earns its place. Using the URL *at all* is not optional, and why is in
  [frontend.md](frontend.md#the-url-is-not-a-nicety).
- **No state manager.** Query owns server state; `useState` owns the rest.
- **No component library.** The interface is a text field and a list.
- **No test runner, yet.** The behaviour worth testing is on the Go side and is tested
  there. When a component gets logic of its own, Vitest arrives with it — and so does the
  four-file API layer that exists to make components testable. See
  [frontend.md](frontend.md#the-api-layer).

## Runtime

Alpine, port 80, `/data` as a volume, published to `ghcr.io/reeywhaar/btw`. The frontend is
built by a Node 26 stage that does not depend on the Go stage. See [deploy.md](deploy.md).

## Keeping versions honest

Dependabot is not configured and does not need to be at this size. When a version here
moves, move it in this table too — a stack document that lags the lockfile is how somebody
ends up debugging against the wrong changelog.
