# Frontend

React 19, TanStack Query 5, Tailwind 4, TypeScript 7, Vite 8, Prettier. `@app/*` → `/src`.

The interface is a text field and a list. Most of what follows is about what is deliberately
absent from it.

## Layout

```
web/
  index.html, login.html      one shell per island
  public/                     sw.js, the manifest, icons — copied verbatim
  src/
    api/
      transport.ts            the only file that mentions fetch
      keys.ts                 every query key, in one object
      actions/                one module per API root
    components/               the shared primitives, below
    islands/
      app/                    the list, settings, the account, routing
      login/                  signing in, accepting an invitation
    push.ts                   subscribing, and what this browser can do
    main.css                  tokens, and the two rules that are not utilities
```

The app island has three routes — `/`, `/settings`, `/account` — and one masthead across all
of them: `settings`, `admin` when the account is one, and the person's own name behind their
initial. Where you are is drawn in the foreground colour rather than hidden, so the nav is the
same three items everywhere and one of them is lit. A nav that renames its items by screen is
a nav you have to read before you can use.

**islands, not apps.** An island is what these are — one shell, one entry, one audience, no
routing between them — and `apps/` said nothing except that it held more than one thing.

`main.css` rather than `index.css` for the same reason `main.tsx` is not `index.tsx`: an
`index` is a directory's default file, a convention from a time when the file name was the
URL. Nothing here resolves by directory.

## Two entries

| entry | audience | ships |
| --- | --- | --- |
| `login.html` | nobody yet | signing in, accepting an invitation |
| `index.html` | somebody with an account | the list, and settings |

`login.html` earns its place by being the only document an unauthenticated visitor loads: a
few kilobytes instead of the whole application, and an invitation link handed to somebody who
has never heard of this instance opens a page about accepting an invitation rather than the
shell of an app they cannot use.

The Go side decides which shell a navigation gets, from one prefix table in `api/spa.go`:
`/login` and `/invite` get the login shell, everything else gets the app.

An admin island will be a third entry when there is an admin screen.

## The URL is not a nicety

Settings is a route, `/settings`, driven by the History API in `islands/app/route.ts`.

It was `useState` first, and that was a bug rather than a simplification. **An installed web
app has no address bar and no back button of its own**, so the system back gesture is the only
way out of a screen — and a screen that is a `useState` rather than a route answers that
gesture by closing the application. On the device this product is aimed at, that is the
difference between a working app and one that feels broken.

Thirty lines and no router. Two routes with no parameters is less than the configuration a
router needs; when a third route arrives with a parameter in it, that is when the dependency
earns its place.

One detail worth keeping: **returning to a screen we pushed from calls `history.back()`, not
`pushState`.** Without it, opening and closing settings four times leaves eight entries for the
system gesture to walk through before it can leave the app. Entries this island pushed are
marked in `history.state`, so a *deep link* straight to `/settings` — which has nothing behind
it — pushes `/` instead of reversing out of btw entirely.

## Components against islands

`src/components/` holds the pieces with no opinion about what they are for: `Heading`,
`Section`, `Row`, `Field`, `Note`, `Check`, `Button`, `Select`, `TextField`, `Warning`,
`Dialog`, and the icons. `src/islands/` holds everything that knows what
btw is — `Reminders`, `ThisBrowser`, `Devices`, `RhythmPanel`, `Login`.

The test for the boundary is whether a piece would need rewriting for a different product.
`Field` would not. `RhythmPanel` is nothing but this product's opinions, and lives in the
island that shows it.

`Button` has three variants and choosing between them is the whole decision: `solid` for the
one thing a screen wants you to do, `quiet` for available but not urged, `link` for an action
that reads as a sentence. It is inverted rather than coloured — `bg-fg text-bg` is
dark-on-light in light mode and light-on-dark in dark mode, from one pair of classes.

A `Section` is for **rows**. A heading whose only content is one button is a `Heading` and
that button — a bordered card drawn around a single control is a box inside a box, and says
nothing except that somebody had a container to hand. For the same reason `quiet` is filled
rather than outlined: bordered, it drew a second rounded rectangle inside the one a Section
already draws.

A button sized to its content is what makes it read as one. Sign out was a `link` variant,
which rendered as bare text adrift in a bordered card and looked like a button that did not
work. Widening the press target to fill the row does not fix that — a full-width control with
its label hard against the left edge still reads as a stretched box rather than a button. Either
it hugs its content or its label is centred; the two are what make a shape look pressable, and
`quiet` gives the first.

## Account against settings

Two pages, split on subject rather than on convenience. **Settings** is how nudges behave —
this browser, the devices, the rhythm. **Account** is the account itself: who you are signed
in as, the password, the recovery address, signing out.

Sign out and the recovery address started on settings and moved. They are what somebody
arrives looking for rather than what they stumble into while adjusting something else, and a
page called Settings that also holds the way out of the product is a page with two subjects.

Changing a password ends every other session and keeps this one — that is what people mean by
it, and signing them out of the tab they are typing in would be a strange way to confirm it
worked. The token is re-minted rather than restored, so the credential rotates at the moment
somebody is worried enough about it to be there.

## The API layer

`transport.ts` is the only file in the frontend that mentions `fetch`. Under
`api/actions/` there is **one module per API root** — `auth`, `reminders`, `rhythm`,
`devices`, `nudges`, `push` — each holding one named function per endpoint, mechanically
named from the route (see [api_design.md](api_design.md#client-naming)) so a call site and a
handler find each other by grep.

It was one `actions.ts`. Every component then imported from a single file that knew about
every endpoint in the product, and that file only ever grows. There is no barrel re-exporting
the modules either: an import that names the root it came from says where to go looking.

The fuller shape this wants to grow into is four: `transport`, a `dispatcher` that carries an
`AbortSignal` and throws, a curried `request`, and a `provider` that injects the dispatcher
through context. All of that exists to let a test render a subtree against a recorded
transport. Until there is such a test it is four files doing one file's work, so it arrives
with the first component test and not before.

`credentials: "same-origin"` is set explicitly and there is no other origin. A request that
needed CORS would be a bug rather than a feature — see
[api_design.md](api_design.md#there-is-no-cors-middleware).

A refusal is thrown as `ApiError` carrying the server's own sentence, and that sentence is what
gets rendered. The server writes it for the person who will read it; re-wording it in the
client would mean maintaining two vocabularies for one failure.

### Queries

Every query key lives in one `qk` object, hierarchically arranged so prefix invalidation is
correct by construction. `invalidateQueries({ queryKey: ["reminders"] })` catches both the live
and the finished list without either knowing about the other.

Every read is a server read. There is no client state worth a store: Query owns what came from
the server, `useState` owns the rest.

## The app is one screen

A text field at the top and a list under it. Typing a sentence and pressing return is the
entire path to a reminder existing — no dialog, no second step, no required field beyond the
sentence.

Each row carries **Done** and **Drop**. Both end the reminder; the two words exist because
they are two different acts. See [conventions.md](conventions.md#done-and-drop).

Interface copy avoids "later" as a label, because doing nothing already means later and a
button that repeats the default teaches somebody the default is not enough.

### What the list does not have

No count, anywhere. No sort control. No sections for overdue or upcoming, because neither
exists. No badge on the tab. Finished reminders are behind a link with no number next to it.

Order is by creation, newest first, and that is a display detail rather than a priority — the
order things *arrive* in is decided server-side and has nothing to do with where they sit on
this screen. Making the list orderable would be reintroducing the ranking the product is trying
not to have.

## What is about this browser, and what is about the account

Two facts that look alike and are not. *Can this browser receive a nudge* is about the thing
you are holding. *Is anything registered at all* is about the account, and is the same answer
from every screen.

Settings has a section each. **This browser** carries the install gate, the "no Push API"
notice, the blocked-permission notice and the enable button. **Devices** carries the list,
`forget`, and `Send one now` — and it is shown whenever there is at least one device,
whatever the browser in front of you can do.

They were one block, with the device list and the test button nested inside the "permission is
granted" branch. Opening btw on a laptop that cannot receive push therefore hid the phone that
can, along with the only button able to reach it: the state of the browser in front of you
deciding what you were allowed to know about a device somewhere else. `Send one now` sends to
every device on the account, which is exactly what makes it useful from a browser that can
receive nothing itself.

## A row whose text can wrap

The sentence in a reminder row is itself a button, and it is the way into the editor — because
it is the thing somebody is looking at. Its own button rather than a click on the row, so it
does not swallow the marks beside it or nest one control inside another. A description, when
there is one, shows underneath on a single truncated line.

Aligning it went through two answers. Against *text* buttons the row aligned on the
**baseline**: the sentence is 16px and a 14px label inside padding and a border does not line
up by box, and centring fails because a wrapped reminder pushes its first line above the
buttons and its second below — baseline uses the first line's, so any height starts level.

Against **icon** buttons there is no text to align to, and baseline drifted. So the row aligns
tops and the sentence carries a little padding of its own, which puts its first line level with
the marks and still lets it wrap downward.

## A section with nothing to do in it is not shown

Recovery disappears when the instance has no relay **and** the account has no address. Both
halves matter: configuring a relay is an administrator's job, and explaining that on
everybody's settings is an explanation aimed at somebody who is not reading it.

An address already on file keeps the section even when the relay goes away, so it can still be
seen and forgotten — what it cannot do then is change, and the warning says so.

The general rule is the one in **Saying why nothing will arrive** below, and this is its other
half: say why when there is something the reader can do, and show nothing when there is not.

## Saying why nothing will arrive

A standing bar sits above the list when **nothing will arrive anywhere**. It is not
dismissible: an app that looks like it is working and silently never nudges anybody is the
failure the whole enable flow exists to prevent, and a banner somebody can dismiss is a failure
somebody dismisses.

Anywhere, not here. One registered device silences it, whatever this browser can do — a laptop
that cannot receive push is an ordinary thing to be sitting at, not a fault worth a permanent
notice on every visit.

When there is nothing, it names the **actual reason**, and only offers the tap when there is
something on the other end of it:

| state | bar | tappable |
| --- | --- | --- |
| no Push API | This browser cannot receive nudges — nothing will arrive here | **no** |
| iOS in a tab | Add btw to your Home Screen to get nudges → | yes |
| permission denied | Notifications are blocked for this site → | yes |
| never asked | Nudges are off — nothing will arrive. Turn them on → | yes |
| granted, nothing registered | This browser is not registered. Register it → | yes |

It said "Turn them on →" for every one of these first, including the ones where there is
nothing to turn on — so somebody on a browser without push was invited to tap through to a
screen whose answer was "this browser cannot". **A call to action that leads to a dead end is
worse than a plain statement**, because it spends somebody's attention before telling them the
thing they needed to know.

The dead-end case is drawn as a `<p>` in muted colours rather than a `<button>` in accent.
Drawing it in the same accent as the actionable one is what made it look tappable.

`denied` stays tappable on purpose: a permission refused once cannot be asked for again in
code, but the settings screen says where the browser's own switch lives, so there *is*
something there.

## The install gate

Safari delivers Web Push only to a web app added to the Home Screen. Offering a button that
cannot work there is how somebody taps Enable, sees nothing happen, and never comes back — the
likeliest way this product fails on the device it is for.

**What decides is a capability test, never a user-agent string:**

```ts
"serviceWorker" in navigator && "PushManager" in window && "Notification" in window
```

In an iOS Safari tab `window.PushManager` is not restricted, it is *absent*, and it appears
once the app is launched from the Home Screen. So one test covers both "too old to help" and
"would work if installed", and it will keep being right on whatever ships next without anybody
editing a regex.

The user agent is consulted **only to choose which instructions to draw** — Share → Add to Home
Screen on iOS, the address-bar control elsewhere. iPadOS 13 and later report a Mac user agent,
so touch points are what separate an iPad from a laptop.

The rest of the app stays usable behind the gate. Writing a reminder from a laptop browser that
will never receive one is a legitimate thing to do, and blocking it would block the cheapest way
to get something out of your head.

Permission is never requested on load. It is a button, on a screen that has already explained
what will arrive.

## Keeping a device alive

`push.ts` re-registers whatever subscription this browser holds **on every load**. Browsers
rotate an endpoint without asking, and support for `pushsubscriptionchange` is uneven — so this
is the mechanism and the event is the optimisation. It costs one request and repairs the case
that otherwise ends in somebody quietly never being nudged again.

It never throws. A browser that refuses to re-register is a browser that will stop receiving
nudges, which the standing bar already says.

## Theming

Semantic tokens, not colours. Components name what a colour is *for* — `bg-bg`, `text-fg`,
`text-muted`, `text-faint`, `border-line`, `bg-surface` — and `main.css` is the only file that
says what those are.

Light is the base on `:root`; dark is an override inside `@media (prefers-color-scheme: dark)`.
The alternative was a `dark:` variant on every className in the application, half of which
somebody eventually forgets.

**A caution is not a refusal.** `--color-warn` is its own token — amber, `#b45309` on the
light ground and `#f59e0b` on the dark, 4.8:1 and 9.2:1. The accent means *this was rejected*
everywhere it appears, and spending it on "this browser cannot receive nudges" would make it
mean less where it matters.

`Avatar` draws a person as their own initial rather than as a pictogram of a person. The
generic head-and-shoulders glyph is the obvious choice and the wrong one: at nav size a
stroked figure is a few spindly curves that read as clip art, and it says "a person" next to
text already naming which one.

It is **SVG rather than a letter centred in a rounded `<span>`**, which is the obvious way and
is off by about a third of a pixel. A line box carries room for descenders and a capital has
none, so centring the *box* leaves the ink riding high — an amount that looks like nothing in
the markup and like a mistake on screen. Measured at 20px: 0.38px up, which is a whole device
pixel on a retina display and is exactly what got noticed. `dy=".35em"` from the geometric
centre is the long-standing way to centre a capital on its own cap height, does not depend on
the element's line box, and brings it to 0.06px.

The nav around it is centred rather than baseline-aligned for the same family of reason: a
baseline row aligns text boxes, and a disc has no meaningful text baseline. Every item there is
the same size, so centring is what baseline alignment was trying to be.

`Warning` colours the mark and not the sentence. A whole paragraph set in warning colour
shouts, and what is being said is usually mild; the icon is enough to catch an eye scanning
the page. Icons are `1em` and `currentColor` and `aria-hidden` — sized to the text beside
them, and silent, because the sentence already says it.

**The accent flips with everything else.** The dark theme's coral is 2.6:1 on a light
background — unreadable, and it is the colour used for refusals, which are the words that most
need reading. Every pairing passes AA in both themes; the weakest is 4.59:1.

`color-scheme` flips too, in a block of its own. Without it a light page renders a dark-mode
`<select>`, and the first paint flashes the wrong colour before CSS applies. Both HTML entries
carry two `theme-color` metas for the same reason — one value means the browser chrome
disagrees with the page in one of the two modes.

The inverted button is `bg-fg text-bg`, which is self-correcting: dark-on-light in light mode,
light-on-dark in dark mode, from one pair of classes.

## Form controls are 16px — and only the ones that need to be

One rule in `main.css`: `input`, `select` and `textarea` are held at 16px, because anything
under it makes iOS Safari zoom the page on focus, and a page that jumps when you tap the one
field it has is a page nobody wants to type in twice.

**`button` was in that list and should not have been.** A button cannot be typed into, so it
never triggers the zoom — and including it silently overrode `text-sm` on every button in the
application. It surfaced as a 16px `<button>` beside a 14px `<a>` in the masthead, but it was
every button on every screen, and nothing at the call site said why.

A rule that broad is not a safety net; it is an override with no visible cause. A blanket
element rule should cover exactly the elements that need it — and `TextField` carries no text
size at all for the same reason, since a `text-sm` there would read as if it applied.
