# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Athene is a classic RAD form designer for GTK4, written in Go. You drop widgets on
a canvas, wire up their events, and it generates a standalone native Go/GTK4
application. The IDE itself is a GTK4 app built with
[gotk4](https://github.com/diamondburned/gotk4) plus GtkSourceView 5 for the code
editor, and it shells out to `gopls` for autocomplete.

## Commands

```
make build     # compile the IDE to ./athene
make run       # build, then launch
make gopls     # install the gopls language server (one time; powers Ctrl+Space)
make tidy      # go mod tidy
make clean     # remove the binary
```

Two headless subcommands exist mainly for CI/testing without opening the GUI —
use these to validate codegen and the LSP layer:

```
./athene gen <form.json> <outdir>            # generate + build an app from a form
./athene lsp-test <projectdir> <line> <char> # print gopls completions at a position (0-based)
```

`go test .` covers only the completion ranking and the LSP column arithmetic
(complmatch.go, `LSPClient.Column`) — the parts with real logic and no GTK
dependency. Everything else is exercised through the `gen`/`lsp-test`
subcommands (main.go), which are the practical way to drive the codegen and LSP
paths end to end. `ATHENE_LSP_DEBUG=1` surfaces gopls' stderr and a trace of its
requests, which is where rejected settings and failed package loads show up.

**Build note:** the first `go build` compiles the gotk4 + GtkSourceView cgo
bindings and takes several minutes; subsequent builds are cached. `GOFLAGS=-mod=mod`
is required (baked into the Makefile targets) because go.sum is not committed as
fully tidy. Requires `gcc`, `pkg-config`, `libgtk-4-dev`, and `libgtksourceview-5-dev`.

## Architecture

Single `package main`, one file per concern:

- **model.go** — `Form` and `Widget`, the JSON-serialized design surface. A Widget
  has absolute X/Y/W/H, a `Type` (Button/Label/Entry/Box), a `Caption`, and a
  `Signals` map (event name → handler func name). This is the only persisted state.
- **app.go** — the `App` struct is the entire IDE: three-pane GTK layout (palette /
  canvas+code notebook / inspector), drag-to-move on a `gtk.Fixed` canvas via manual
  hit-testing, and the object inspector. Design-time widgets are made
  non-targetable (`SetCanTarget(false)`) so the canvas gestures own all pointer input.
  The `live` map connects each model `*Widget` to its live GTK widget.
- **codegen.go** — turns a `Form` into a buildable Go project, then compiles it.
  Also `//go:embed`s **athutil/athutil.go** and stamps it into each generated
  project (see Licensing below).
- **athutil/** and **athui/** — the runtime helper packages generated apps
  import as `<module>/athutil` and `<module>/athui`. `athutil` is stdlib-only
  (parsing, validation, math, number formatting); `athui` wraps `gtk` dialogs
  (`Info`/`Error`/`Ask`, taking the app's `*gtk.ApplicationWindow`). Each is the
  single source of truth; codegen `//go:embed`s and stamps a copy into every
  build (machine-owned, like `app.gen.go`). Note `athui` uses the deprecated-in-
  4.10 `gtk.MessageDialog` because this gotk4 release ships no `AlertDialog`
  constructor.
- **athcontainer/** — `Containerfile` + `build-in-container.sh`, the container
  build environment dropped into each generated project so `make container-build`
  works with no GTK4 `-dev` packages on the host. Like `athutil`/`athui` these are
  `//go:embed`ed from this directory (the single source of truth), but unlike them
  they are stamped with `writeIfMissing` — a user's edits to their project's copy
  survive. Neither file is Go source, so neither carries `genLicenseHeader`: they
  wrap the build instead of being linked into the app, so the LGPL linking
  exception does not apply. The base is `golang:1.24-trixie`: a cgo binary needs
  at least the glibc/GTK it was built against, so an older base would widen the
  set of machines the output runs on — but bookworm's GLib 2.74 cannot compile
  gotk4 (missing `g_path_buf_*`, `g_log_writer_syslog`, …; GLib ≥ 2.80 is
  required), which makes trixie the oldest Debian that works. The image also
  needs `libgirepository1.0-dev`: gotk4's `core/gerror` has a cgo pkg-config line
  for `gobject-introspection-1.0`, so it is a compile-time dependency of the
  bindings, not just of regenerating them. Cost of a build lives almost entirely
  in the first (cold) gotk4 compile, which the shared `~/.cache/athene-build`
  exists to pay once; everything else is trimmed to match — the image is rebuilt
  only when it is missing or the `Containerfile` is newer than
  `$CACHE_ROOT/image-*.stamp` (`--rebuild` forces it), its output is shown only
  if the build fails, and `-buildvcs=false` keeps `go build` from shelling out to
  git in a checkout the container's uid does not own.
- **lsp.go** — a minimal, synchronous JSON-RPC client for `gopls` (initialize,
  didOpen/didChange, completion only).
- **completion_ui.go** — Ctrl+Space handling and the custom completion popover;
  F12 toggles Designer↔Code.
- **lsp.go** — a minimal JSON-RPC client for `gopls` (initialize,
  didOpen/didChange, completion only). A single reader goroutine owns gopls'
  stdout and demultiplexes responses to per-request channels, so `request` is
  cancellable and never serializes one caller behind another. See Threading.
- **completion_ui.go** — the autocomplete popover: triggering, the popup, and
  applying an accepted item. F12 toggles Designer↔Code.
- **complmatch.go** — the pure ranking half of completion (`fuzzyScore` and
  friends). No GTK, no LSP, so it is unit-tested.
- **main.go** — GUI entry point plus the two headless subcommands.

### Licensing model (Lazarus-style — don't break the exception)

Two tiers, deliberately:

- The **IDE** (everything except `athutil/` and generated output) is plain
  **LGPL-2.1** (`LICENSE`).
- **Code that ends up inside a user's app** — the emitted `app.gen.go` and the
  bundled `athutil`/`athui` packages — is **LGPL-2.1 + a linking exception**
  (`LICENSE.exception`), so people can ship proprietary apps built with Athene.

Practical rules when touching codegen: any Go source Athene *writes into a
generated project* must carry the exception header. `app.gen.go` gets
`genLicenseHeader` (in codegen.go); `athutil.go` carries the full exception in
its own header. Don't make generated apps import LGPL-without-exception code
(e.g. don't have them pull in an Athene package that lacks the exception) — that
would defeat the whole point.

### The two-file codegen contract (most important invariant)

Generated projects split machine-owned and human-owned code, and nothing should
break this split:

- `app.gen.go` is **fully generated and overwritten on every build** (`generateApp`).
  Widgets are declared at package scope so handlers can reference them by name
  (e.g. `Label1.SetText("hi")`). The main window is `MainWindow`.
- `handlers.go` is **the user's code and is only ever appended to, never rewritten**
  (`ensureHandlerStub` — it appends a stub only if `func <name>(` is not already
  present). `writeProject` guarantees a stub exists for every wired signal so the
  generated code always compiles.
- `Makefile`, `README.md`, `Containerfile` and `build-in-container.sh` in
  generated projects are written once via `writeIfMissing` and then left alone.

Handler function names are derived by `handlerName` as `On<WidgetID><Event>`
(e.g. `OnButton1Clicked`). GTK setters are not uniform (`SetText` vs `SetLabel`);
`goType` and `setterHint` encode the per-type mapping — keep them in sync with the
widget types handled in `makeLive`/`applyCaption`/`generateApp` whenever adding a
new widget type.

### Adding a new widget type

A type must be added in several places: the palette list (`buildPalette`),
`defaultSize` (model.go), `makeLive` and `applyCaption` (app.go, design-time
rendering), `setterHint`/`getterHint` (app.go, inspector code hints), and
`goType` + `generateApp` (codegen.go, generated output). Missing one silently
drops the widget from either the designer or the generated app.

The eight types today are Button, Label, Entry, Box (a `gtk.Frame`),
CheckButton, SpinButton, Switch, and ProgressBar. For non-text widgets the
"caption" field is repurposed — it seeds a SpinButton's value / ProgressBar's
fraction (parsed as a float) and is ignored for Switch — so `makeLive`,
`applyCaption`, `generateApp` and the inspector caption row all special-case
those.

**Widgets with an event.** Only signals whose gotk4 `Connect*` handler is a
plain `func()` can be wired, because the generated stub is always zero-argument.
Those types are registered in `widgetEvent` (type → signal name) and
`connectMethod` (signal → `Connect*` method) in codegen.go; `handlerName`/
`eventSuffix` turn the signal into an identifier-safe suffix
(`value-changed` → `OnspinValueChanged`). The inspector event row, `openHandler`
and `generateApp`'s signal wiring all read `widgetEvent`, so adding a row there
is all it takes. Switch's `state-set` takes a `bool`, so it is deliberately
*not* wireable — read its `.State()` from another widget's handler instead.

### Threading

The IDE runs on the GTK main thread. gopls is started in a background goroutine
(`startCodeIntelligence`); anything touching GTK from there must go through
`glib.IdleAdd` (see `postStatus`).

**No LSP call may happen on the main thread.** `LSPClient.request` blocks until
gopls answers, and a cold completion can take seconds — doing that inline freezes
the whole IDE. `requestCompletion` is the pattern to copy: read what you need from
the buffer on the main thread, do `DidChange` + `Complete` in a goroutine, then
come back through `glib.IdleAdd`. Replies are tagged with `compl.gen`, which every
new request and every dismissal bumps, so a reply the user has already typed past
is dropped instead of clobbering the popup.

### How autocomplete hangs together

Worth knowing before changing any of it, because the pieces constrain each other:

- **The popover must not take a grab.** It is `SetAutohide(false)` with every
  widget inside it non-focusable, which is what lets the caret stay in the buffer
  so typing narrows the list. Making it autohiding, or letting a row take focus,
  breaks type-to-filter. Dismissal is therefore explicit: Escape, the caret
  leaving the word, an empty match set, or `loadCode`.
- **Keys are stolen in the capture phase** on `codeView`, and only the ones the
  popup owns (arrows, PageUp/Down, Enter, Tab, Escape). Everything else must fall
  through to the buffer or typing stops working.
- **Two-stage filtering.** Each keystroke re-ranks the candidates locally
  (complmatch.go) for instant feedback; `scheduleRefresh` then re-asks gopls after
  a pause, because gopls trims its result set and only offers unimported symbols
  once there is a prefix. `deliverCompletion` passes `schedule=false` to
  `refilterCompletion` — arming the debounce from a reply would poll gopls forever.
- **LSP columns are not character offsets.** GtkTextIter counts characters; LSP
  counts UTF-8 bytes or (with gopls today) UTF-16 code units. Always convert via
  `LSPClient.Column`/`RuneColumn`; one non-ASCII character earlier on the line is
  enough to send gopls to the wrong place.
- **Accepting an item ignores the server's edit range** and replaces
  start-of-word-to-caret instead, since the server's range is stale by the time
  the user has typed more. The `newText` is still used (`CompletionItem.insertion`),
  because it can be qualified where the label is not, and
  `additionalTextEdits` are applied for the auto-import.
- **`goplsSettings` keys must exist in `gopls api-json`.** gopls rejects unknown
  settings, and it does retire them — `completeUnimported` and `deepCompletion`
  were dropped in 0.21. Note the settings only reach gopls at all because the
  client advertises `workspace.configuration`.

### Version pinning

`codegen.go`'s `gotkVersion` const must match the gotk4 version in the root
`go.mod` — generated projects pin it explicitly. Update both together.

gotk4 is held at **0.3.x** on purpose, and dependabot is told to ignore 0.4.x
(`.github/dependabot.yml`). 0.4.0 regenerated the bindings against GLib 2.86 and
emits unguarded calls to symbols added there (`g_get_monotonic_time_ns`,
`g_source_dup_context`, `g_markup_parse_context_get_offset`, …), so `glib/v2`
itself fails to compile — no amount of adapting Athene's own API usage helps.
That rules it out on all three build environments: Debian trixie has GLib 2.84,
`ubuntu-latest` has 2.80, and the `golang:1.24-trixie` container base has 2.84
(there is no official `golang` image on a newer Debian). Lift the pin once the
container base and the CI runner both reach GLib ≥ 2.86 — and bump
`libdb.so/gotk4-sourceview/pkg` at the same time, since its pinned 2024 revision
predates the 0.4 API.

## Runtime layout

The IDE writes its working project into `./athene-app/` (relative to CWD) — that
directory holds `form.json`, `app.gen.go`, `handlers.go`, and the built `app`
binary. It exists in the repo and is git-ignored.
