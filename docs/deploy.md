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
| `BTW_BACKUP_URL` | — | A backup agent to post archives to. Unset, btw takes none |
| `BTW_BACKUP_MODE` | `relaxed` | `main`, `relaxed`, `all` — see [Backups](#backups) |

There is no config file. A handful of variables do not need one.

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

There is no second port. Backups leave through an outgoing request — see below.

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

`derived.db` is sessions, the nudge waiting to go out and the nudge log, and is designed to be
deletable. Losing it signs everybody out and forgets what was sent; every reminder still knows
when it was last raised, because that lives in `main.db`. A test asserts exactly that.

Back it up with `sqlite3 main.db ".backup out.db"`, a filesystem snapshot, or the endpoint
below — **not `cp`**. A plain copy of a WAL database while it is being written is a copy of an
inconsistent moment.

## Backups

Set `BTW_BACKUP_URL` to a backup agent and btw sends it a gzipped tar of its databases when
there is something new to send. Leave it unset and btw takes no backups at all.

```
btw ──POST archive──▶ agent ──▶ wherever the agent was told
     (no credential)   (holds the token, names the file, prunes old ones)
```

Each copy is made with `VACUUM INTO`, so what leaves is a consistent database that opens on
its own — the reason btw builds the archive rather than something running `tar` over the
volume. A WAL database is three files with the committed state spread across them, and a
file-level copy of a running instance is a copy of a moment that never existed.

### When one goes out

**When `main.db` has changed, and not otherwise.** btw looks every five minutes, hashes a
fresh snapshot of `main.db`, and sends only if it differs from the copy the agent last
accepted. An instance nobody has touched since Tuesday sends nothing, and a week of that costs
one upload, not two thousand.

Deciding this inside btw is the whole reason the archive is pushed rather than served. A
container fetching on a loop from outside cannot know whether anything has been written; it
can only ask on a timer and take whatever it gets.

The five minutes are also a throttle. Writing down six things in one minute is one archive
holding all six, five minutes later, rather than six archives — nothing here reacts to a
write, so there is no burst it can be made to keep up with. The first pass is immediate,
because a process that has just started may be on a volume nobody has a copy of yet.

Neither number is a setting. What an operator chooses is the promise they want, and that is
the mode.

### Modes

| `BTW_BACKUP_MODE` | Carries | Sends when |
| --- | --- | --- |
| `main` | `main.db` | `main.db` changed |
| `relaxed` *(default)* | both | `main.db` changed |
| `all` | both | `main.db` changed, **or** half an hour has passed |

`main.db` is what somebody typed — accounts, reminders, devices, rhythms — plus the VAPID
keypair, whose loss invalidates every push subscription on the instance at once and cannot be
repaired by anybody re-registering. It is the file that has to survive.

`derived.db` is sessions, the nudge waiting to go out, and the nudge log. It rides along in
`relaxed` and `all` but never decides that a copy is due, and that costs less than it sounds:
sending a nudge writes to *both* — the reminder's `last_nudged_at` in `main.db`, the log row in
`derived.db` — so the record of what went out travels with a change `main.db` has already
noticed. What moves in `derived.db` alone is somebody signing in and the scheduler picking a
moment, and losing those costs a sign-in and one rescheduled nudge.

**`all` is the only mode that sends when nothing has changed**, and the reason to want it is
upstream of the archive. An agent told how often to expect one can report a btw that has
stopped backing up; with copies arriving only when somebody adds a reminder, it cannot tell a
broken instance from a quiet one, and that alarm is useless on exactly the instances that are
quiet for weeks. Pair it with the agent's own staleness check.

### What btw does not know

**btw holds no credential, no provider, no bucket and no retention policy.** It decides when to
back up and what goes in the archive; everything after that belongs to the agent.

That is the point of the arrangement rather than a gap in it. A btw container that is
compromised cannot read, overwrite or delete a single existing backup, because it has nothing
to authenticate with and nothing to point at. It can only hand over one more archive.

```yaml
services:
  btw:
    image: ghcr.io/reeywhaar/btw:latest
    environment:
      BTW_PUBLIC_URL: https://btw.example.com
      BTW_BACKUP_URL: http://backup:8080/backup
      BTW_BACKUP_MODE: all
    volumes: [btw-data:/data]
    ports: ["8080:80"]

  backup:
    # The agent: it holds the token and decides where archives end up.
    image: ghcr.io/reeywhaar/backio-agent:latest
    environment:
      # With mode `all`, an hour of silence means something is wrong rather than quiet.
      BACKUP_EXPECT_EVERY: 1h
      # …destination and credential, which are the agent's business
```

### Restoring

Stop the container, extract the archive into the data directory, start it again.

The archive carries every password hash on the instance and the VAPID private key, so it is
worth the same care as the volume itself — the agent's `BACKUP_PASSWORD` is worth setting if
archives are going anywhere you do not own. Members are written mode `0600` and there are no
directory entries, so extracting cannot re-`chmod` a data directory that already exists.

Restoring `main.db` alone is a complete restore. Everybody signs in again, and the first nudge
after it is scheduled afresh.

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
