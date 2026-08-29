# Deploy

One image, one port, one volume.

```
ghcr.io/reeywhaar/btw:latest
```

## Environment

| variable | default | meaning |
| --- | --- | --- |
| `BTW_PUBLIC_URL` | *required* | The address you open in a browser, e.g. `https://btw.example.com` |
| `BTW_DATA_DIR` | `/data` | Where `main.db` and `derived.db` live |
| `BTW_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `BTW_WEB_DIR` | `/srv/web` | The built bundle. Set in the image, rarely anywhere else |

There is no config file. Three variables do not need one.

### `BTW_PUBLIC_URL` does more work here than it looks

It **has to be told and cannot be inferred.** `Host` and `X-Forwarded-Host` are both
client-supplied, and an invitation link built from a header a stranger controls is an
invitation link a stranger controls. Startup fails without it rather than guessing.

It then decides three separate things:

1. Whether the session cookie carries `Secure`.
2. The VAPID `sub` claim — the contact URI push services are given. See
   [push.md](push.md#vapid).
3. The origin every push subscription is bound to.

The third is the one that surprises people. **Moving btw to a different address silently kills
every device already registered** — not erroring, just never delivering — and they have to be
added again. A device row is an endpoint issued to one origin; carried across, it is an
endpoint for a host that no longer means anything. `GET /api/devices` shows the failure count
climbing, which is otherwise the only sign.

### It must be https in practice

No browser registers a service worker against an insecure origin, so on plain `http` nothing
can subscribe and no nudge can ever arrive. The whole product is off.

`serve` warns at startup and names both consequences rather than only the cookie one:

```
level=WARN msg="public url is http, so the session cookie ships without Secure and
                no browser will register a service worker" url=http://localhost:3014
```

`http://localhost` is a secure context by definition, so a checkout can be poked at over
localhost — but a phone cannot reach localhost, which is the device this is for. Anything past
that needs a real certificate.

## Port

`:80`, inside the container, not configurable. Remap it with `-p`. A port number inside a
container is not a thing an operator should have to think about twice.

## Volume

`/data`, holding both SQLite databases and their WAL sidecars.

```sh
docker run -d --name btw \
  -e BTW_PUBLIC_URL=https://btw.example.com \
  -v btw-data:/data \
  -p 8080:80 \
  ghcr.io/reeywhaar/btw:latest
```

Mount it. Without it every account disappears when the container is replaced, and the way back
in is the first-run invitation link printed to a log that is also gone.

### What is worth backing up

**`main.db`.** Accounts, reminders, devices, and the VAPID keypair. That last one is the reason
the asymmetry is worth caring about: losing it invalidates every subscription on the instance
at once, and no amount of re-registering repairs it.

`derived.db` is sessions, the day's plan and the nudge log, and is designed to be deletable.
Losing it signs everybody out and forgets what was sent; every reminder still knows when it was
last raised, because that lives in `main.db`. A test asserts exactly that.

Back it up with `sqlite3 main.db ".backup out.db"` or a filesystem snapshot, **not `cp`** — a
plain copy of a WAL database while it is being written is a copy of an inconsistent moment.

## First run

If no administrator exists, `serve` creates an invitation and prints the link:

```
level=INFO msg="no administrator yet; open this link to make one" expires_at=...
https://btw.example.com/invite/3GoBUAL71DmgHpZZcxxJENg7zgn2-HOVktzyJOmS6VI
```

No default password exists at any point, so there is no credential for somebody to forget to
change. The link is single-use and expires in seven days.

`docker exec btw invite` prints another when the first scrolls out of a log — which is the way
back in, because the token is stored hashed and a lost link is reissued rather than recovered.

## The image

Multi-stage, and the two stages do not depend on each other.

- **Node 26 stage**, pinned to `--platform=$BUILDPLATFORM`. The bundle is
  architecture-independent, so there is no reason to run npm under QEMU. The lockfile is copied
  first, on its own layer, so a source edit does not reinstall the world.

  Pinned to the major that is run here rather than to the current LTS. This stage produces
  static JavaScript and CSS, so the runtime characteristics that make LTS worth choosing do not
  apply — what does apply is that a bundle which builds on a laptop and fails in CI is an
  afternoon nobody gets back.

- **Both HTML entries and `sw.js` are asserted non-empty after the build.** An empty bundle is
  otherwise invisible until somebody loads the page and gets the placeholder, which looks like a
  server problem rather than a build one.

- **Go stage** cross-compiles, `CGO_ENABLED=0`, `-trimpath`, version stamped via `-ldflags`.
  `modernc.org/sqlite` is pure Go, which is what keeps this static.

- **`COPY` path by path**, not a `.dockerignore`. An allowlist cannot accidentally admit
  `web/node_modules` or a local `data/`.

- **Alpine runtime with `ca-certificates`** — push services are reached over HTTPS, so the
  certificate store is not optional. `VOLUME /data`. `HEALTHCHECK` runs `btw healthcheck`, so
  the image needs no HTTP client and a wedged process fails it.

## CI

`test` → `publish` → `notify`.

`test` runs gofmt, `go vet`, `go test ./...` — **deliberately with no frontend build
present**, which is what keeps the two stages independent — then `npm ci`, `format:check`,
`typecheck` and the bundle build. There is no `npm test` yet: the behaviour worth testing is
on the Go side.

`publish` builds `linux/amd64,linux/arm64` and pushes to GHCR with the built-in
`GITHUB_TOKEN`. No secret to provision, which is the whole reason the image lives there.
`provenance: false` keeps the package listing to one real platform instead of adding an
`unknown/unknown` entry beside it.

**The smoke tests are steps on `publish`, not a job of their own.** A fresh GHCR package is
private, so a separate job would have to sign in again to pull the image it just pushed —
and would fail confusingly if it did not.

They check two different things, in two steps, so a failure names which:

- **The image**: `btw version` runs, `/healthz` answers, a first run prints an invitation
  link, and both shells carry `<div id="root">` rather than the "has not been built"
  placeholder. That last one is the failure that otherwise reaches users looking exactly like
  a successful publish.
- **The push chain**: `sw.js` is served from the root and contains its `notificationclick`
  handler, the manifest declares `display: standalone`, and `/api/push/key` returns 87
  characters — an uncompressed P-256 point. Without any of those, btw serves a list that will
  never nudge anybody, which looks like a working release from every other angle.

`notify` is a job of its own rather than steps on `publish`, because a failing `test`
*skips* `publish` rather than failing it, and a step in a job that never starts cannot report
that it never started. It reads both results off `needs` and says which half broke.

`concurrency` with `cancel-in-progress` means a newer commit on main wins the race for
`latest` rather than queueing behind an older one that would overwrite it.

Publishing is currently switched off — the `push` trigger is commented out and
`workflow_dispatch` is what remains, so a publish is a deliberate act.
