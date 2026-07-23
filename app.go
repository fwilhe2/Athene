package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	gtksource "libdb.so/gotk4-sourceview/pkg/gtksource/v5"
)

// App holds the whole IDE state.
type App struct {
	gtkApp *gtk.Application
	win    *gtk.ApplicationWindow

	form       *Form
	projectDir string

	notebook *gtk.Notebook
	canvas   *gtk.Fixed
	live     map[*Widget]gtk.Widgetter // model -> live design-time widget
	selected *Widget

	propBox  *gtk.Box
	codeView *gtksource.View
	codeBuf  *gtksource.Buffer
	console  *gtk.TextView
	status   *gtk.Label

	// gopls-backed completion
	lsp             *LSPClient
	lspReady        atomic.Bool
	complPopover    *gtk.Popover
	complList       *gtk.ListBox
	complItems      []CompletionItem

	// drag state
	dragTarget         *Widget
	dragStartX, dragStartY int
}

func NewApp(gtkApp *gtk.Application) *App {
	cwd, _ := os.Getwd()
	return &App{
		gtkApp:     gtkApp,
		projectDir: filepath.Join(cwd, "athene-app"),
		live:       map[*Widget]gtk.Widgetter{},
	}
}

func (a *App) formPath() string { return filepath.Join(a.projectDir, "form.json") }

// ---------------------------------------------------------------- UI assembly

func (a *App) build() {
	// Load an existing form if the project already has one.
	if f, err := LoadForm(a.formPath()); err == nil {
		a.form = f
	} else {
		a.form = NewForm()
	}

	a.installCSS()

	a.win = gtk.NewApplicationWindow(a.gtkApp)
	a.win.SetTitle("Athene")
	a.win.SetDefaultSize(1100, 760)

	root := gtk.NewBox(gtk.OrientationVertical, 0)
	root.Append(a.buildToolbar())

	body := gtk.NewBox(gtk.OrientationHorizontal, 6)
	body.SetVExpand(true)
	body.SetHExpand(true)
	body.Append(a.buildPalette())
	body.Append(a.buildCenter())
	body.Append(a.buildInspector())
	root.Append(body)

	a.status = gtk.NewLabel("Ready.")
	a.status.SetXAlign(0)
	a.status.SetMarginStart(8)
	a.status.SetMarginTop(2)
	a.status.SetMarginBottom(2)
	root.Append(a.status)

	a.win.SetChild(root)

	// Populate canvas with any loaded widgets.
	for _, w := range a.form.Widgets {
		a.addLive(w)
	}
	a.loadCode()
	a.win.SetVisible(true)
}

func (a *App) installCSS() {
	css := gtk.NewCSSProvider()
	css.LoadFromData(`
		.athene-canvas { background: #f4f4f5; }
		.athene-selected { outline: 2px solid #3584e4; outline-offset: -1px; }
		.athene-panel { background: #ececed; }
		.athene-code { font-family: monospace; background: #ddedff; padding: 4px 6px; border-radius: 4px; }
	`)
	display := gdk.DisplayGetDefault()
	gtk.StyleContextAddProviderForDisplay(display, css, 600)
}

func (a *App) buildToolbar() *gtk.Box {
	bar := gtk.NewBox(gtk.OrientationHorizontal, 4)
	bar.SetMarginStart(6)
	bar.SetMarginEnd(6)
	bar.SetMarginTop(6)
	bar.SetMarginBottom(6)

	add := func(label string, fn func()) {
		b := gtk.NewButtonWithLabel(label)
		b.ConnectClicked(fn)
		bar.Append(b)
	}
	add("New", a.onNew)
	add("Save", a.onSave)
	add("Delete", a.deleteSelected)
	sep := gtk.NewSeparator(gtk.OrientationVertical)
	bar.Append(sep)
	run := gtk.NewButtonWithLabel("▶ Run")
	run.AddCSSClass("suggested-action")
	run.ConnectClicked(a.onRun)
	bar.Append(run)
	return bar
}

func (a *App) buildPalette() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	box.SetSizeRequest(130, -1)
	box.AddCSSClass("athene-panel")
	box.SetMarginStart(4)
	box.SetMarginTop(4)
	box.SetMarginBottom(4)

	title := gtk.NewLabel("Palette")
	title.SetMarginTop(4)
	box.Append(title)

	for _, typ := range []string{"Button", "Label", "Entry", "Box"} {
		t := typ
		b := gtk.NewButtonWithLabel(t)
		b.ConnectClicked(func() { a.addWidget(t) })
		box.Append(b)
	}
	return box
}

// page indices in the center notebook
const (
	pageDesigner = 0
	pageCode     = 1
)

func (a *App) buildCenter() *gtk.Box {
	center := gtk.NewBox(gtk.OrientationVertical, 4)
	center.SetHExpand(true)
	center.SetVExpand(true)

	a.notebook = gtk.NewNotebook()
	a.notebook.SetHExpand(true)
	a.notebook.SetVExpand(true)

	// ===== Designer tab =====
	a.canvas = gtk.NewFixed()
	a.canvas.AddCSSClass("athene-canvas")
	a.canvas.SetSizeRequest(a.form.Width, a.form.Height)
	a.installCanvasGestures()

	canvasScroll := gtk.NewScrolledWindow()
	canvasScroll.SetChild(a.canvas)
	canvasScroll.SetVExpand(true)
	canvasScroll.SetHExpand(true)
	a.notebook.AppendPage(canvasScroll, gtk.NewLabel("Designer"))

	// ===== Code tab =====
	codePage := gtk.NewBox(gtk.OrientationVertical, 4)

	codeHeader := gtk.NewBox(gtk.OrientationHorizontal, 6)
	lbl := gtk.NewLabel("handlers.go")
	lbl.SetXAlign(0)
	lbl.SetHExpand(true)
	codeHeader.Append(lbl)
	saveCode := gtk.NewButtonWithLabel("Save code")
	saveCode.ConnectClicked(func() { a.saveCode(); a.setStatus("Saved handlers.go") })
	codeHeader.Append(saveCode)
	codePage.Append(codeHeader)

	a.codeBuf = gtksource.NewBuffer(nil)
	if lang := gtksource.LanguageManagerGetDefault().Language("go"); lang != nil {
		a.codeBuf.SetLanguage(lang)
	}
	a.codeBuf.SetHighlightSyntax(true)
	a.codeView = gtksource.NewViewWithBuffer(a.codeBuf)
	a.codeView.SetMonospace(true)
	a.codeView.SetShowLineNumbers(true)
	a.codeView.SetHighlightCurrentLine(true)
	a.codeView.SetAutoIndent(true)
	a.codeView.SetTabWidth(4)
	a.applyEditorScheme()
	a.setupCompletion()
	codeScroll := gtk.NewScrolledWindow()
	codeScroll.SetChild(a.codeView)
	codeScroll.SetVExpand(true)
	codePage.Append(codeScroll)

	a.notebook.AppendPage(codePage, gtk.NewLabel("Code"))
	center.Append(a.notebook)

	// --- build console (below the tabs, visible from either view) ---
	consoleLabel := gtk.NewLabel("Build output")
	consoleLabel.SetXAlign(0)
	consoleLabel.SetMarginStart(4)
	center.Append(consoleLabel)
	a.console = gtk.NewTextView()
	a.console.SetMonospace(true)
	a.console.SetEditable(false)
	consoleScroll := gtk.NewScrolledWindow()
	consoleScroll.SetChild(a.console)
	consoleScroll.SetSizeRequest(-1, 110)
	center.Append(consoleScroll)

	return center
}

// showDesigner / showCode / toggleView flip the center notebook, RAD-style.
func (a *App) showDesigner() {
	if a.notebook != nil {
		a.notebook.SetCurrentPage(pageDesigner)
	}
}

func (a *App) showCode() {
	if a.notebook != nil {
		a.notebook.SetCurrentPage(pageCode)
	}
}

func (a *App) toggleView() {
	if a.notebook == nil {
		return
	}
	if a.notebook.CurrentPage() == pageCode {
		a.showDesigner()
	} else {
		a.showCode()
	}
}

func (a *App) buildInspector() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	box.SetSizeRequest(260, -1)
	box.AddCSSClass("athene-panel")

	title := gtk.NewLabel("Object Inspector")
	title.SetMarginTop(4)
	box.Append(title)

	a.propBox = gtk.NewBox(gtk.OrientationVertical, 4)
	a.propBox.SetMarginStart(6)
	a.propBox.SetMarginEnd(6)
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(a.propBox)
	scroll.SetVExpand(true)
	box.Append(scroll)

	a.refreshInspector()
	return box
}

// ---------------------------------------------------------------- canvas / live

func (a *App) makeLive(w *Widget) gtk.Widgetter {
	var live gtk.Widgetter
	switch w.Type {
	case "Button":
		live = gtk.NewButtonWithLabel(w.Caption)
	case "Label":
		live = gtk.NewLabel(w.Caption)
	case "Entry":
		e := gtk.NewEntry()
		if w.Caption != "" {
			e.SetText(w.Caption)
		}
		live = e
	case "Box":
		live = gtk.NewFrame(w.Caption)
	default:
		live = gtk.NewLabel(w.Type)
	}
	return live
}

func (a *App) addLive(w *Widget) {
	live := a.makeLive(w)
	wid := gtk.BaseWidget(live)
	wid.SetSizeRequest(w.W, w.H)
	// Design-time widgets must not swallow pointer events: make them
	// non-targetable so the canvas gestures handle selection & dragging.
	wid.SetCanTarget(false)
	wid.SetCanFocus(false)
	a.canvas.Put(live, float64(w.X), float64(w.Y))
	a.live[w] = live
}

func (a *App) installCanvasGestures() {
	click := gtk.NewGestureClick()
	click.ConnectPressed(func(nPress int, x, y float64) {
		hit := a.hitTest(int(x), int(y))
		a.selectWidget(hit)
		if nPress == 2 && hit != nil {
			// RAD-style: double-click jumps to code. For a Button we also
			// create/open its click handler; for others we just flip to Code.
			if hit.Type == "Button" {
				a.openHandler(hit)
			} else {
				a.showCode()
			}
		}
	})
	a.canvas.AddController(click)

	drag := gtk.NewGestureDrag()
	drag.ConnectDragBegin(func(x, y float64) {
		hit := a.hitTest(int(x), int(y))
		a.dragTarget = hit
		if hit != nil {
			a.selectWidget(hit)
			a.dragStartX = hit.X
			a.dragStartY = hit.Y
		}
	})
	drag.ConnectDragUpdate(func(offX, offY float64) {
		if a.dragTarget == nil {
			return
		}
		nx := a.dragStartX + int(offX)
		ny := a.dragStartY + int(offY)
		if nx < 0 {
			nx = 0
		}
		if ny < 0 {
			ny = 0
		}
		a.dragTarget.X = nx
		a.dragTarget.Y = ny
		if live, ok := a.live[a.dragTarget]; ok {
			a.canvas.Move(live, float64(nx), float64(ny))
		}
	})
	drag.ConnectDragEnd(func(offX, offY float64) {
		if a.dragTarget != nil && a.dragTarget == a.selected {
			a.refreshInspector()
		}
		a.dragTarget = nil
	})
	a.canvas.AddController(drag)
}

// hitTest returns the topmost widget whose rectangle contains (x,y).
func (a *App) hitTest(x, y int) *Widget {
	for i := len(a.form.Widgets) - 1; i >= 0; i-- {
		w := a.form.Widgets[i]
		if x >= w.X && x < w.X+w.W && y >= w.Y && y < w.Y+w.H {
			return w
		}
	}
	return nil
}

func (a *App) selectWidget(w *Widget) {
	if a.selected == w {
		return
	}
	if a.selected != nil {
		if live, ok := a.live[a.selected]; ok {
			gtk.BaseWidget(live).RemoveCSSClass("athene-selected")
		}
	}
	a.selected = w
	if w != nil {
		if live, ok := a.live[w]; ok {
			gtk.BaseWidget(live).AddCSSClass("athene-selected")
		}
	}
	a.refreshInspector()
}

func (a *App) nextID(typ string) string {
	n := 1
	for {
		id := fmt.Sprintf("%s%d", typ, n)
		used := false
		for _, w := range a.form.Widgets {
			if w.ID == id {
				used = true
				break
			}
		}
		if !used {
			return id
		}
		n++
	}
}

func (a *App) addWidget(typ string) {
	w := &Widget{ID: a.nextID(typ), Type: typ}
	w.W, w.H = defaultSize(typ)
	// cascade so new widgets don't stack exactly on top of each other
	n := len(a.form.Widgets)
	w.X = 20 + (n*12)%180
	w.Y = 20 + (n*12)%140
	w.Caption = w.ID
	if typ == "Entry" {
		w.Caption = ""
	}
	a.form.Widgets = append(a.form.Widgets, w)
	a.addLive(w)
	a.selectWidget(w)
	a.setStatus("Added " + w.ID)
}

func (a *App) deleteSelected() {
	if a.selected == nil {
		return
	}
	w := a.selected
	if live, ok := a.live[w]; ok {
		a.canvas.Remove(live)
		delete(a.live, w)
	}
	for i, x := range a.form.Widgets {
		if x == w {
			a.form.Widgets = append(a.form.Widgets[:i], a.form.Widgets[i+1:]...)
			break
		}
	}
	a.selected = nil
	a.refreshInspector()
	a.setStatus("Deleted " + w.ID)
}

// ---------------------------------------------------------------- inspector

func (a *App) refreshInspector() {
	// clear
	for {
		child := a.propBox.FirstChild()
		if child == nil {
			break
		}
		a.propBox.Remove(child)
	}

	if a.selected == nil {
		hint := gtk.NewLabel("Select a widget to edit its properties, or double-click a Button to write its click handler.")
		hint.SetWrap(true)
		hint.SetXAlign(0)
		a.propBox.Append(hint)
		return
	}

	w := a.selected
	head := gtk.NewLabel(w.Type + ": " + w.ID)
	head.SetXAlign(0)
	a.propBox.Append(head)

	a.addTextRow("Name", w.ID, func(v string) {
		if v != "" {
			w.ID = v
		}
	})
	captionLabel := "Caption"
	if w.Type == "Entry" {
		captionLabel = "Text"
	}
	a.addTextRow(captionLabel, w.Caption, func(v string) {
		w.Caption = v
		a.applyCaption(w)
	})
	a.addIntRow("X", w.X, func(v int) { w.X = v; a.moveLive(w) })
	a.addIntRow("Y", w.Y, func(v int) { w.Y = v; a.moveLive(w) })
	a.addIntRow("Width", w.W, func(v int) { w.W = v; a.resizeLive(w) })
	a.addIntRow("Height", w.H, func(v int) { w.H = v; a.resizeLive(w) })

	if w.Type == "Button" {
		sep := gtk.NewSeparator(gtk.OrientationHorizontal)
		a.propBox.Append(sep)
		evLabel := gtk.NewLabel("Events")
		evLabel.SetXAlign(0)
		a.propBox.Append(evLabel)
		row := gtk.NewBox(gtk.OrientationHorizontal, 6)
		name := ""
		if w.Signals != nil {
			name = w.Signals["clicked"]
		}
		l := gtk.NewLabel("clicked")
		l.SetSizeRequest(70, -1)
		l.SetXAlign(0)
		row.Append(l)
		btnLabel := "Create handler"
		if name != "" {
			btnLabel = name
		}
		edit := gtk.NewButtonWithLabel(btnLabel)
		edit.SetHExpand(true)
		edit.ConnectClicked(func() { a.openHandler(w) })
		row.Append(edit)
		a.propBox.Append(row)
	}

	// Code hint: the exact GTK call to set this widget's text from a handler.
	// GTK's setters aren't uniform (SetText vs SetLabel), so we spell it out.
	if hint := setterHint(w); hint != "" {
		a.propBox.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
		lbl := gtk.NewLabel("Set text from code")
		lbl.SetXAlign(0)
		a.propBox.Append(lbl)

		hintRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
		code := gtk.NewLabel(hint)
		code.SetXAlign(0)
		code.SetHExpand(true)
		code.SetWrap(true)
		code.SetSelectable(true)
		code.AddCSSClass("athene-code")
		hintRow.Append(code)

		copyBtn := gtk.NewButtonWithLabel("Copy")
		copyBtn.SetVAlign(gtk.AlignStart)
		copyBtn.ConnectClicked(func() {
			if a.win != nil {
				a.win.Clipboard().SetText(hint)
				a.setStatus("Copied: " + hint)
			}
		})
		hintRow.Append(copyBtn)
		a.propBox.Append(hintRow)
	}
}

// setterHint returns the exact Go call to set the given widget's text, using
// its current caption as a realistic example value.
func setterHint(w *Widget) string {
	switch w.Type {
	case "Label", "Entry":
		return fmt.Sprintf("%s.SetText(%q)", w.ID, w.Caption)
	case "Button", "Box":
		return fmt.Sprintf("%s.SetLabel(%q)", w.ID, w.Caption)
	}
	return ""
}

func (a *App) addTextRow(label, value string, onChange func(string)) {
	row := gtk.NewBox(gtk.OrientationHorizontal, 6)
	l := gtk.NewLabel(label)
	l.SetSizeRequest(70, -1)
	l.SetXAlign(0)
	row.Append(l)
	e := gtk.NewEntry()
	e.SetText(value)
	e.SetHExpand(true)
	e.ConnectChanged(func() { onChange(e.Text()) })
	row.Append(e)
	a.propBox.Append(row)
}

func (a *App) addIntRow(label string, value int, onChange func(int)) {
	row := gtk.NewBox(gtk.OrientationHorizontal, 6)
	l := gtk.NewLabel(label)
	l.SetSizeRequest(70, -1)
	l.SetXAlign(0)
	row.Append(l)
	e := gtk.NewEntry()
	e.SetText(strconv.Itoa(value))
	e.SetHExpand(true)
	e.ConnectChanged(func() {
		if n, err := strconv.Atoi(e.Text()); err == nil {
			onChange(n)
		}
	})
	row.Append(e)
	a.propBox.Append(row)
}

func (a *App) applyCaption(w *Widget) {
	live, ok := a.live[w]
	if !ok {
		return
	}
	switch v := live.(type) {
	case *gtk.Button:
		v.SetLabel(w.Caption)
	case *gtk.Label:
		v.SetText(w.Caption)
	case *gtk.Entry:
		v.SetText(w.Caption)
	case *gtk.Frame:
		v.SetLabel(w.Caption)
	}
}

func (a *App) moveLive(w *Widget) {
	if live, ok := a.live[w]; ok {
		a.canvas.Move(live, float64(w.X), float64(w.Y))
	}
}

func (a *App) resizeLive(w *Widget) {
	if live, ok := a.live[w]; ok {
		gtk.BaseWidget(live).SetSizeRequest(w.W, w.H)
	}
}

// ---------------------------------------------------------------- code / build

func (a *App) openHandler(w *Widget) {
	if w.Type != "Button" {
		return
	}
	if w.Signals == nil {
		w.Signals = map[string]string{}
	}
	fn := handlerName(w, "clicked")
	w.Signals["clicked"] = fn

	// Persist any edits already in the editor before we append a new stub.
	a.saveCode()
	if _, err := ensureHandlerStub(a.projectDir, fn); err != nil {
		a.setStatus("Error: " + err.Error())
		return
	}
	a.loadCode()
	a.scrollCodeToEnd()
	a.showCode()
	a.refreshInspector()
	a.setStatus("Editing " + fn + " in handlers.go")
}

func (a *App) loadCode() {
	a.codeBuf.SetText(readHandlers(a.projectDir))
}

func (a *App) saveCode() {
	start, end := a.codeBuf.Bounds()
	writeHandlers(a.projectDir, a.codeBuf.Text(start, end, false))
}

func (a *App) scrollCodeToEnd() {
	end := a.codeBuf.EndIter()
	mark := a.codeBuf.CreateMark("end", end, false)
	a.codeView.ScrollToMark(mark, 0, true, 0, 1)
}

// applyEditorScheme picks the first available syntax colour scheme from a
// dark-first preference list.
func (a *App) applyEditorScheme() {
	sm := gtksource.StyleSchemeManagerGetDefault()
	for _, id := range []string{"Adwaita-dark", "oblivion", "Adwaita", "classic"} {
		if sch := sm.Scheme(id); sch != nil {
			a.codeBuf.SetStyleScheme(sch)
			return
		}
	}
}

func (a *App) appendConsole(s string) {
	buf := a.console.Buffer()
	buf.SetText(s)
}

func (a *App) onNew() {
	a.form = NewForm()
	for w, live := range a.live {
		a.canvas.Remove(live)
		delete(a.live, w)
	}
	a.selected = nil
	a.canvas.SetSizeRequest(a.form.Width, a.form.Height)
	a.refreshInspector()
	a.setStatus("New form")
}

func (a *App) onSave() {
	if err := os.MkdirAll(a.projectDir, 0755); err != nil {
		a.setStatus("Save failed: " + err.Error())
		return
	}
	if err := a.form.Save(a.formPath()); err != nil {
		a.setStatus("Save failed: " + err.Error())
		return
	}
	a.saveCode()
	a.setStatus("Saved to " + a.formPath())
}

func (a *App) onRun() {
	a.onSave()
	a.saveCode()
	a.setStatus("Building… (first build compiles GTK bindings, be patient)")
	if err := writeProject(a.projectDir, a.form); err != nil {
		a.setStatus("Codegen failed: " + err.Error())
		return
	}
	out, err := buildApp(a.projectDir)
	a.appendConsole(out)
	if err != nil {
		a.setStatus("Build FAILED — see build output below")
		return
	}
	if err := runApp(a.projectDir); err != nil {
		a.setStatus("Launch failed: " + err.Error())
		return
	}
	a.setStatus("Built OK — your app is running.")
}

func (a *App) setStatus(s string) {
	if a.status != nil {
		a.status.SetText(s)
	}
}
