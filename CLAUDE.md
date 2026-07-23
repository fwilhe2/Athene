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

There is no test suite. The `gen`/`lsp-test` subcommands (main.go) are the
practical way to exercise the codegen and LSP paths end to end.

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
- **athutil/** — a tiny stdlib-only helper package (`athutil.Atoi/Atof/Itoa/
  FormatFloat`) that generated apps import as `<module>/athutil`. It is the
  single source of truth; codegen embeds and copies it, overwriting the copy on
  every build (machine-owned, like `app.gen.go`).
- **lsp.go** — a minimal, synchronous JSON-RPC client for `gopls` (initialize,
  didOpen/didChange, completion only).
- **completion_ui.go** — Ctrl+Space handling and the custom completion popover;
  F12 toggles Designer↔Code.
- **main.go** — GUI entry point plus the two headless subcommands.

### Licensing model (Lazarus-style — don't break the exception)

Two tiers, deliberately:

- The **IDE** (everything except `athutil/` and generated output) is plain
  **LGPL-2.1** (`LICENSE`).
- **Code that ends up inside a user's app** — the emitted `app.gen.go` and the
  bundled `athutil` package — is **LGPL-2.1 + a linking exception**
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
- `Makefile` / `README.md` in generated projects are written once via
  `writeIfMissing` and then left alone.

Handler function names are derived by `handlerName` as `On<WidgetID><Event>`
(e.g. `OnButton1Clicked`). GTK setters are not uniform (`SetText` vs `SetLabel`);
`goType` and `setterHint` encode the per-type mapping — keep them in sync with the
widget types handled in `makeLive`/`applyCaption`/`generateApp` whenever adding a
new widget type.

### Adding a new widget type

A type must be added in several places: the palette list (`buildPalette`),
`defaultSize` (model.go), `makeLive` and `applyCaption` (app.go, design-time
rendering), and `goType` + `generateApp` + `setterHint` (codegen.go, generated
output). Missing one silently drops the widget from either the designer or the
generated app.

### Threading

The IDE runs on the GTK main thread. gopls is started in a background goroutine
(`startCodeIntelligence`); anything touching GTK from there must go through
`glib.IdleAdd` (see `postStatus`). The LSP client is deliberately synchronous under
a mutex — every request is issued and awaited from the main thread.

### Version pinning

`codegen.go`'s `gotkVersion` const must match the gotk4 version in the root
`go.mod` — generated projects pin it explicitly. Update both together.

## Runtime layout

The IDE writes its working project into `./athene-app/` (relative to CWD) — that
directory holds `form.json`, `app.gen.go`, `handlers.go`, and the built `app`
binary. It exists in the repo and is git-ignored.
