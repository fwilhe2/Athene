package main

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// setupCompletion installs the Ctrl+Space handler on the editor and kicks off
// gopls in the background. Real, type-aware Go completion is served through a
// popover we build ourselves (rather than a GtkSourceCompletionProvider, which
// would mean implementing an async GObject interface).
func (a *App) setupCompletion() {
	key := gtk.NewEventControllerKey()
	key.SetPropagationPhase(gtk.PhaseCapture)
	key.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
		if keyval == gdk.KEY_space && state&gdk.ControlMask != 0 {
			a.triggerCompletion()
			return true // preempt GtkSourceView's own completion binding
		}
		return false
	})
	a.codeView.AddController(key)

	if a.win != nil {
		// F12 toggles Designer <-> Code.
		winKey := gtk.NewEventControllerKey()
		winKey.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
			if keyval == gdk.KEY_F12 {
				a.toggleView()
				return true
			}
			return false
		})
		a.win.AddController(winKey)

		a.win.ConnectCloseRequest(func() bool {
			if a.lsp != nil {
				a.lsp.Close()
			}
			return false
		})
	}

	// Make sure the project exists on disk so gopls has a real module to load.
	_ = writeProject(a.projectDir, a.form)
	go a.startCodeIntelligence()
}

func (a *App) startCodeIntelligence() {
	// Ensure external deps resolve (go.sum). Cached, so this is quick.
	_, _ = goModTidy(a.projectDir)

	client, err := StartGopls(a.projectDir)
	if err != nil {
		a.postStatus("Code intelligence unavailable: " + err.Error())
		return
	}
	if err := client.DidOpen(handlersPath(a.projectDir), readHandlers(a.projectDir)); err != nil {
		client.Close()
		a.postStatus("gopls didOpen failed: " + err.Error())
		return
	}
	a.lsp = client
	a.lspReady.Store(true)
	a.postStatus("Code intelligence ready — press Ctrl+Space in the editor.")
}

// postStatus updates the status label from a background goroutine safely.
func (a *App) postStatus(s string) {
	glib.IdleAdd(func() { a.setStatus(s) })
}

// triggerCompletion syncs the buffer to gopls, asks for completions at the
// cursor, and shows them in a popover.
func (a *App) triggerCompletion() {
	if !a.lspReady.Load() {
		a.setStatus("Code intelligence still starting…")
		return
	}
	start, end := a.codeBuf.Bounds()
	text := a.codeBuf.Text(start, end, false)
	if err := a.lsp.DidChange(text); err != nil {
		a.setStatus("sync failed: " + err.Error())
		return
	}
	iter := a.codeBuf.IterAtMark(a.codeBuf.Mark("insert"))
	line, ch := iter.Line(), iter.LineOffset()

	items, err := a.lsp.Complete(line, ch)
	if err != nil {
		a.setStatus("completion: " + err.Error())
		return
	}
	if len(items) == 0 {
		a.setStatus("No completions here.")
		return
	}
	a.showCompletionPopover(items, iter)
}

func (a *App) showCompletionPopover(items []CompletionItem, iter *gtk.TextIter) {
	if a.complPopover != nil {
		a.complPopover.Unparent()
		a.complPopover = nil
	}
	a.complItems = items

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionSingle)
	for _, it := range items {
		list.Append(completionRow(it))
	}
	list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		a.acceptCompletion(row.Index())
	})
	a.complList = list

	sw := gtk.NewScrolledWindow()
	sw.SetChild(list)
	sw.SetSizeRequest(460, 280)
	sw.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)

	pop := gtk.NewPopover()
	pop.SetParent(a.codeView)
	pop.SetChild(sw)
	pop.SetHasArrow(false)
	pop.SetAutohide(true)
	pop.SetPosition(gtk.PosBottom)
	a.complPopover = pop

	// Point the popover at the caret. IterLocation is in buffer coordinates;
	// convert to widget-window coordinates for the popover's parent.
	loc := a.codeView.IterLocation(iter)
	wx, wy := a.codeView.BufferToWindowCoords(gtk.TextWindowText, loc.X(), loc.Y())
	rect := gdk.NewRectangle(wx, wy, 1, loc.Height())
	pop.SetPointingTo(&rect)

	pop.Popup()
	if first := list.RowAtIndex(0); first != nil {
		list.SelectRow(first)
	}
	list.GrabFocus()
}

func (a *App) acceptCompletion(index int) {
	if index < 0 || index >= len(a.complItems) {
		return
	}
	it := a.complItems[index]
	buf := a.codeBuf
	if it.TextEdit != nil {
		s, _ := buf.IterAtLineOffset(it.TextEdit.Range.Start.Line, it.TextEdit.Range.Start.Character)
		e, _ := buf.IterAtLineOffset(it.TextEdit.Range.End.Line, it.TextEdit.Range.End.Character)
		buf.Delete(s, e)
		buf.Insert(s, it.TextEdit.NewText)
	} else {
		txt := it.InsertText
		if txt == "" {
			txt = it.Label
		}
		buf.InsertAtCursor(txt)
	}
	if a.complPopover != nil {
		a.complPopover.Popdown()
	}
	a.codeView.GrabFocus()
}

// completionRow renders one suggestion: label on the left, kind + signature
// dimmed on the right.
func completionRow(it CompletionItem) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 10)
	row.SetMarginStart(6)
	row.SetMarginEnd(6)

	name := gtk.NewLabel(it.Label)
	name.SetXAlign(0)
	row.Append(name)

	meta := it.kindName()
	if it.Detail != "" {
		if meta != "" {
			meta += "  "
		}
		meta += it.Detail
	}
	if meta != "" {
		d := gtk.NewLabel(meta)
		d.SetXAlign(1)
		d.SetHExpand(true)
		d.SetWrap(false)
		d.AddCSSClass("dim-label")
		row.Append(d)
	}
	return row
}
