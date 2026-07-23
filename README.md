# Athene

**Athene** is a classic RAD form designer for GTK4 — drop widgets on a form,
wire up their events, and generate a native Go application you can build into a
single executable. It is written in Go using
[gotk4](https://github.com/diamondburned/gotk4).

The name is the Dutch spelling of Athens.

## Features

- Visual form designer: a palette (Button, Label, Entry, Box) with a
  drag-and-select canvas and an object inspector.
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
