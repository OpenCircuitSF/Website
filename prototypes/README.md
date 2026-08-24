# Prototypes

Proof-of-concept work that is **not** part of the shipped site. Nothing here is
built, embedded, or served by the Go binary — `web/` is the real SPA.

A prototype lives here when a technique needs settling before it is written
into `web/src/`, and when being able to open, poke and share it matters more
than being integrated.

## What's here

| Folder | Issue | What it demonstrates |
|---|---|---|
| `crt-live-screen/` | [`#0233`](../issues/0233.md) | A live terminal screen projectively warped onto the hero CRT photograph |

## Sharing one

**Each prototype folder is self-contained and zips cleanly.**

```sh
cd prototypes && zip -r crt-live-screen.zip crt-live-screen
```

The recipient needs no repository, no build step, and no server — they unzip it
and open `index.html`. Or send `index.html` on its own: every asset is inlined,
so the single file works alone.

## Why the assets are duplicated

Each prototype keeps its own copies under `assets/` rather than reaching into
`web/public/`. That is deliberate, and it is the reason the folder can be
zipped at all — a relative path climbing out of the folder breaks the moment it
leaves the repository, and under `file://` a browser will not reliably fetch
subresources from outside the page's own directory anyway.

Duplication drifts, so it is **checked rather than trusted**. Each prototype's
`build.py` compares `assets/` against the upstream files by SHA-256 and exits
non-zero if any differ:

```sh
python3 prototypes/crt-live-screen/build.py           # build, and report drift
python3 prototypes/crt-live-screen/build.py --sync     # copy upstream over, then build
```

Run outside the repository — after someone unzips it — there is nothing to
compare against, so the check reports that and carries on.

## Editing one

Edit `index.src.html`, then re-run `build.py` to regenerate `index.html`.
`index.html` carries a do-not-edit banner and is overwritten every build.

Both are committed on purpose: the source so changes are reviewable as diffs,
the generated file so it can be opened straight from a checkout or a zip
without anyone needing to run a build first.
