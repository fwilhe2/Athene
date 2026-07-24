# Athene examples

A handful of small, complete applications that show what a well-made Athene app
looks like. Each folder is the **source of truth** for one app — nothing more
than the two files you would hand-craft or design in the IDE:

- `form.json` — the design surface (widgets, positions, sizes, wired signals).
- `handlers.go` — your event handlers, using the bundled `athutil`/`athui`
  helpers for parsing, validation, formatting and message boxes.

Everything else (`app.gen.go`, `go.mod`, the `athutil`/`athui` copies, the
`Makefile`, the binary) is produced by `athene gen` and is git-ignored.

## The apps

| Folder | What it shows |
| ------ | ------------- |
| [`tip-calculator`](tip-calculator) | Multiple inputs → several results in a framed panel; input validation with an error dialog; grouped currency formatting. |
| [`temperature-converter`](temperature-converter) | A two-way converter driven by two buttons that read from and write back into the same entries. |
| [`bmi-calculator`](bmi-calculator) | Compute a number **and** classify it — a helper function maps the result to a category label. |
| [`counter`](counter) | The classic increment / decrement / reset demo. Shows how to keep state between clicks with a package-scope variable. |
| [`loan-calculator`](loan-calculator) | A little real math (`math.Pow` for the amortization formula) alongside the helper packages, with a three-line results panel. |

## Building an example

From the repository root, after building the IDE (`make build`):

```
./athene gen examples/tip-calculator/form.json examples/tip-calculator
./examples/tip-calculator/app
```

`athene gen` stamps the generated files next to `form.json`/`handlers.go`,
compiles the project, and reports `OK: built …/app`. Because `handlers.go`
already exists, the generator leaves it untouched and only wires the rest around
it — exactly what happens when you press **▶ Run** in the IDE.

> The first build compiles the gotk4 cgo bindings and can take several minutes;
> later builds are cached and fast.

## Opening one in the designer

The IDE is a single-project tool: it always loads `./athene-app/form.json`
relative to where you launch it, and there is no File → Open. So "opening" an
example means making it the working project — copy its two files into
`athene-app/`, then launch:

```
cp examples/tip-calculator/form.json   athene-app/form.json
cp examples/tip-calculator/handlers.go athene-app/handlers.go
make run
```

The form loads onto the canvas so you can inspect and edit the layout. Copying
`handlers.go` too means **▶ Run** compiles the real logic instead of empty
stubs (`handlers.go` is append-only — the IDE keeps whatever you hand it and
only adds stubs for any *missing* wired signal).

These forms round-trip cleanly because they use only the four built-in widget
types (Button, Label, Entry, Box) and the `clicked` signal.

> `athene-app/` is the one working project and already contains the default
> demo form — copying over it replaces that form, so back it up first if you
> want to keep it.
