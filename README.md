# Athene

**Athene** is a classic RAD form designer for GTK4 — drop widgets on a form,
wire up their events, and generate a native Go application you can build into a
single executable. It is written in Go using
[gotk4](https://github.com/diamondburned/gotk4).

The name is the Dutch spelling of Athens.

## Features

- Visual form designer: a palette (Button, Label, Entry, Box) with a
  drag-and-select canvas and an object inspector.
- Resize the form by dragging its bottom-right corner, or set exact
  Width/Height in the inspector (shown when nothing is selected).
- Multi-select: rubber-band a box over empty space, or Ctrl/Shift+click to
  toggle widgets, then drag them as a group or delete them all with **Delete**.
- Split Designer / Code view (toggle with **F12**); double-click a Button to
  generate and jump to its click handler.
- Built-in code editor (GtkSourceView 5) with Go syntax highlighting and real
  **gopls**-powered autocomplete (Ctrl+Space).
- One-click **▶ Run**: generates the project, compiles it with `go build`, and
  launches the resulting native binary.
- Clean codegen model: `app.gen.go` is machine-owned (overwritten every build);
  `handlers.go` holds your code and is only ever appended to.

## Prerequisites

- Go toolchain, 1.24 or newer
- A C compiler (`gcc`) and `pkg-config` (gotk4 uses cgo)
- GTK4 development libraries
- GtkSourceView 5 development libraries (for the code editor)
- `gopls` (for autocomplete) — installed via `make gopls`

### Debian / Ubuntu

    sudo apt update
    sudo apt install golang gcc pkg-config libgtk-4-dev libgtksourceview-5-dev libgirepository1.0-dev

### Fedora

    sudo dnf install golang gcc pkgconf-pkg-config gtk4-devel gtksourceview5-devel gobject-introspection-devel

## Build & run

    make build     # compile the IDE to ./athene
    make run       # build, then launch
    make gopls     # install the gopls language server (one time)
    make tidy      # resolve dependencies / refresh go.sum
    make clean     # remove the built binary

Or without make:

    GOFLAGS=-mod=mod go build -o athene .
    ./athene

> Note: the first build compiles the gotk4 and GtkSourceView cgo bindings and
> can take many minutes. Subsequent builds are cached and fast.

## Command-line usage

Besides the GUI, the `athene` binary has two headless subcommands, useful for
CI and testing:

    athene gen <form.json> <outdir>          # generate + build an app from a form
    athene lsp-test <projectdir> <line> <char>   # print gopls completions at a position

## Generated projects

Each generated application is a self-contained Go module with its own `Makefile`
and `README.md` describing how to build it. A generated app depends only on
GTK4 (not GtkSourceView), so its prerequisites are a subset of Athene's.

Generated projects also bundle two small helper packages for your handlers:

- **athutil** (`atheneapp/athutil`) — stdlib-only: forgiving `Entry` parsing
  (`Atoi`, `Atof`, `ParseInt`), validation (`IsBlank`, `IsNumeric`), math
  (`Clamp`, `Round`), and number formatting (`Itoa`, `FormatFloat`,
  `FormatFixed`, `FormatInt`/`FormatGrouped` with thousands separators).
- **athui** (`atheneapp/athui`) — message boxes over the main window:
  `athui.Info(MainWindow, "Saved.")`, `athui.Error(...)`, and
  `athui.Ask(MainWindow, "Delete?", func() { /* on Yes */ })`.

## Writing handlers

Your code lives in `handlers.go`. Widgets are package-scope variables named after
their IDs (`entry1`, `labelResult`, …), so you read and write them directly — use
`.Text()` to read a `Label`/`Entry` and `.SetText(...)` to write it. The two
helper packages keep handlers short.

**Adder with validation and grouped output.** Bad input pops an error box
instead of silently reading `0`:

```go
package main

import (
	"atheneapp/athui"
	"atheneapp/athutil"
)

func OnButton1Clicked() {
	if !athutil.IsNumeric(entry1.Text()) || !athutil.IsNumeric(entry2.Text()) {
		athui.Error(MainWindow, "Please enter two numbers.")
		return
	}
	sum := athutil.Atof(entry1.Text()) + athutil.Atof(entry2.Text())
	labelResult.SetText(athutil.FormatGrouped(sum, 2)) // e.g. "1,234.50"
}
```

**Temperature converter** (°C → °F), rounded to one decimal:

```go
func OnConvertClicked() {
	celsius := athutil.Atof(entryCelsius.Text())
	fahrenheit := athutil.Round(celsius*9/5+32, 1)
	labelFahrenheit.SetText(athutil.FormatFloat(fahrenheit) + " °F")
}
```

**Confirm before clearing** — `athui.Ask` runs the callback only on *Yes*:

```go
func OnResetClicked() {
	athui.Ask(MainWindow, "Clear all fields?", func() {
		entry1.SetText("")
		entry2.SetText("")
		labelResult.SetText("")
	})
}
```

Everything in `athutil`/`athui` is optional — you always have the full
[gotk4](https://github.com/diamondburned/gotk4) API in a handler if you need more.

## Examples

The [`examples/`](examples) directory holds a handful of small, complete apps —
a tip calculator, a temperature converter, a BMI calculator, a counter and a
loan calculator. Each is just a `form.json` plus a `handlers.go`; build any of
them with `./athene gen examples/<name>/form.json examples/<name>`. See
[`examples/README.md`](examples/README.md) for the full list.

## License

Athene follows the Lazarus model:

- The **Athene IDE** — the form designer, code editor, code generator and LSP
  client — is licensed under the **LGPL v2.1** (see `LICENSE`).
- The parts that end up *inside your app* — the generated `app.gen.go` and the
  bundled `athutil`/`athui` packages — are LGPL v2.1 **with a linking exception**
  (see `LICENSE.exception`).

That exception means **applications you build with Athene may be licensed however
you like, including proprietary**. Merely using Athene to design and generate an
app does not make your app a derivative of the IDE — just as compiling with GCC
does not make your program GPL. Your only obligation is to honour the LGPL for
the covered files themselves (e.g. share any changes you make to `athutil.go`).

> This is a summary, not legal advice; the authoritative terms are in `LICENSE`
> and `LICENSE.exception`.
