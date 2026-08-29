# Documentation

How this project is built, so a decision made once does not have to be re-argued.

These are rules and references, not a plan — what gets built in what order is not settled
here. That lives in `private/plans/`.

They belong in the repository for a reason: a document explaining why a table is shaped the
way it is belongs beside the table, in the same history, reviewable in the same diff. That
is also what makes the rule below enforceable — a stale document is something a commit can
be seen not to have fixed.

| document | what it settles |
| --- | --- |
| [stack.md](stack.md) | Every dependency, its version, and why it is here |
| [conventions.md](conventions.md) | Naming, commits, comments, ids, time |
| [entities.md](entities.md) | The two databases, every table, every invariant |
| [nudges.md](nudges.md) | When somebody is nudged, and what with |
| [push.md](push.md) | Web Push: the encryption, the headers, and the iOS problem |
| [mail.md](mail.md) | The relay, and proving a recovery address |
| [api_design.md](api_design.md) | HTTP conventions and the full endpoint reference |
| [backend.md](backend.md) | Go package layout and the rules each package follows |
| [frontend.md](frontend.md) | Islands, the API layer, routing, theming |
| [deploy.md](deploy.md) | Image, environment, volumes, CI |

btw is small — about 4,000 lines of Go under `internal/` and 1,700 lines of test beside
them — and most of these documents are short because most of the program is. Where one runs
long it is because the thing it describes was got wrong first.

## What btw is, in three sentences

Some things you mean to do have no *when*. btw holds those, and a few times a day, at hours
nobody picked, puts one of them on your phone. The arrival is where the thought gets
decided: you do it, you drop it, or you ignore it — and ignoring it is free.

Everything else follows from refusing to ask *when?*. There are no due dates, no overdue, no
counts, and no review screen, because every one of those is a way of asking the question
again.

## When these disagree with the code

The code is right and the document is stale. Fix the document in the same commit that made
it stale — a reference nobody trusts is worse than no reference, because it costs a reader
the time to find out.

That is the rule and it will be broken, which is what [meta.txt](meta.txt) is for. It
records the commit each of these was last checked against, so "what has happened since
anybody read this" is `git log <hash>..HEAD` rather than a re-read of everything. Update the
line when you update the document.
