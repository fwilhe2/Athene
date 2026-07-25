package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/graphene"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// completion_ui.go implements the editor's autocomplete: a popover we drive
// ourselves rather than a GtkSourceCompletionProvider (which would mean
// implementing an async GObject interface).
//
// The design goals, in order:
//
//  1. Never block the GTK main thread. Every gopls round trip happens in a
//     goroutine and comes back through glib.IdleAdd, tagged with a generation
//     number so replies overtaken by further typing are dropped.
//  2. Keep the keyboard in the editor. The popover is non-autohiding and
//     nothing inside it is focusable, so the caret stays in the buffer and the
//     user can keep typing to narrow the list. A capture-phase key controller
//     on the view steals only the keys the popup owns (arrows, Enter, Tab, Esc).
//  3. Filter locally, refresh remotely. Each keystroke re-filters the candidate
//     set instantly with a fuzzy matcher; a debounced request then re-asks gopls
//     with the longer prefix, which is what surfaces unimported symbols and
//     candidates beyond gopls' result budget.

const (
	// complMaxRows caps how many rows we build per keystroke; the fuzzy ranking
	// means anything past this is noise anyway.
	complMaxRows = 100
	// complRefreshDelay is how long typing must pause before we re-ask gopls.
	complRefreshDelay = 220
	// complTriggerDelay lets the "." land in the buffer before we ask about it.
	complTriggerDelay = 40
)

// completionState is the live autocomplete popup: the candidates gopls last
// returned, the filtered view of them, and the widgets showing it.
type completionState struct {
	popover *gtk.Popover
	list    *gtk.ListBox
	scroll  *gtk.ScrolledWindow
	footer  *gtk.Label

	items   []CompletionItem // everything gopls returned for this session
	shown   []int            // indices into items, after prefix filtering
	matched int              // matches before the complMaxRows cap
	sel     int              // index into shown

	start *gtk.TextMark // start of the word being completed
	open  bool

	gen      uint64            // request generation; stale replies are dropped
	refresh  glib.SourceHandle // pending debounced re-request
	trigger  glib.SourceHandle // pending auto-trigger after a "."
	applying bool              // true while we edit the buffer ourselves
}

// setupCompletion wires the editor's key handling and buffer listeners, then
// kicks off gopls in the background.
func (a *App) setupCompletion() {
	key := gtk.NewEventControllerKey()
	key.SetPropagationPhase(gtk.PhaseCapture)
	key.ConnectKeyPressed(a.onCodeKeyPressed)
	a.codeView.AddController(key)

	// insert-text fires *before* the character reaches the buffer, so an
	// auto-trigger has to be deferred by a tick.
	a.codeBuf.ConnectInsertText(func(loc *gtk.TextIter, text string, _ int) {
		if a.compl.applying {
			return
		}
		if text == "." && a.selectorBefore(loc) {
			a.scheduleAutoTrigger()
		}
	})
	// Typing or moving the caret re-filters the open popup (or closes it).
	a.codeBuf.ConnectChanged(func() {
		if !a.compl.applying && a.compl.open {
			a.refilterCompletion(true)
		}
	})
	a.codeBuf.ConnectMarkSet(func(loc *gtk.TextIter, mark *gtk.TextMark) {
		if !a.compl.applying && a.compl.open && mark.Name() == "insert" {
			a.refilterCompletion(true)
		}
	})

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

	// Warm gopls up with a throwaway request so the first real completion is
	// not the one that pays for loading the gtk4 packages.
	a.postStatus("Loading code intelligence…")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	_, _ = client.Complete(ctx, 0, 0, triggerInvoked)
	cancel()

	a.lspReady.Store(true)
	a.postStatus("Code intelligence ready — completions pop up as you type (Ctrl+Space to force).")
}

// postStatus updates the status label from a background goroutine safely.
func (a *App) postStatus(s string) {
	glib.IdleAdd(func() { a.setStatus(s) })
}

// ------------------------------------------------------------------ key input

func (a *App) onCodeKeyPressed(keyval, keycode uint, state gdk.ModifierType) bool {
	if keyval == gdk.KEY_space && state&gdk.ControlMask != 0 {
		a.requestCompletion(triggerInvoked)
		return true // preempt GtkSourceView's own completion binding
	}
	if !a.compl.open {
		return false
	}
	switch keyval {
	case gdk.KEY_Escape:
		a.dismissCompletion()
		return true
	case gdk.KEY_Down:
		a.moveCompletionSel(1)
		return true
	case gdk.KEY_Up:
		a.moveCompletionSel(-1)
		return true
	case gdk.KEY_Page_Down:
		a.moveCompletionSel(10)
		return true
	case gdk.KEY_Page_Up:
		a.moveCompletionSel(-10)
		return true
	case gdk.KEY_Return, gdk.KEY_KP_Enter, gdk.KEY_Tab:
		a.acceptCompletion()
		return true
	}
	return false // everything else goes to the buffer, then re-filters
}

// selectorBefore reports whether the "." about to be inserted at loc looks like
// a member selector rather than part of a number: we want `athutil.` to pop the
// list open but `3.14` to be left alone.
func (a *App) selectorBefore(loc *gtk.TextIter) bool {
	buf := a.codeBuf
	off := loc.Offset()
	if off == 0 {
		return false
	}
	end := off
	for off > 0 {
		probe := buf.IterAtOffset(off - 1)
		if !isIdentRune(rune(probe.Char())) {
			break
		}
		off--
	}
	if off == end {
		// No identifier: still a selector after a call or index expression.
		prev := rune(buf.IterAtOffset(end - 1).Char())
		return prev == ')' || prev == ']' || prev == '"'
	}
	return !unicode.IsDigit(rune(buf.IterAtOffset(off).Char()))
}

func (a *App) scheduleAutoTrigger() {
	if a.compl.trigger != 0 {
		glib.SourceRemove(a.compl.trigger)
	}
	a.compl.trigger = glib.TimeoutAdd(complTriggerDelay, func() bool {
		a.compl.trigger = 0
		a.requestCompletion(triggerCharacter)
		return false
	})
}

// ------------------------------------------------------------- gopls requests

// requestCompletion syncs the buffer to gopls and asks for completions at the
// caret. The round trip runs off the main thread; the reply is delivered via
// glib.IdleAdd and discarded if newer typing has superseded it.
func (a *App) requestCompletion(trigger int) {
	if !a.lspReady.Load() {
		if trigger == triggerInvoked {
			a.setStatus("Code intelligence still starting…")
		}
		return
	}
	start, end := a.codeBuf.Bounds()
	text := a.codeBuf.Text(start, end, false)
	iter := a.codeBuf.IterAtMark(a.codeBuf.Mark("insert"))
	line, runeCol := iter.Line(), iter.LineOffset()
	col := a.lsp.Column(a.bufferLine(line), runeCol)

	a.compl.gen++
	gen := a.compl.gen
	client := a.lsp
	timeout := 20 * time.Second
	if trigger == triggerCharacter {
		timeout = 8 * time.Second
	}

	go func() {
		if err := client.DidChange(text); err != nil {
			glib.IdleAdd(func() { a.completionFailed(gen, trigger, err) })
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		items, err := client.Complete(ctx, line, col, trigger)
		glib.IdleAdd(func() {
			if err != nil {
				a.completionFailed(gen, trigger, err)
				return
			}
			a.deliverCompletion(gen, trigger, items)
		})
	}()
}

func (a *App) completionFailed(gen uint64, trigger int, err error) {
	if gen != a.compl.gen {
		return
	}
	if trigger == triggerInvoked {
		a.setStatus("completion: " + err.Error())
	}
}

func (a *App) deliverCompletion(gen uint64, trigger int, items []CompletionItem) {
	if gen != a.compl.gen {
		return // superseded by newer typing
	}
	if len(items) == 0 {
		if a.compl.open {
			return // a refresh came back empty; keep what we are showing
		}
		if trigger == triggerInvoked {
			a.setStatus("No completions here.")
		}
		return
	}
	keep := a.selectedLabel()
	a.compl.items = items
	a.markCompletionStart()
	if !a.compl.open {
		a.openCompletion()
	}
	// schedule=false: this reply *is* the refresh, so re-arming the debounce
	// here would poll gopls forever while the popup stays open.
	if !a.refilterCompletion(false) {
		return
	}
	a.reselectLabel(keep)
}

// ------------------------------------------------------------------ filtering

// refilterCompletion re-runs the prefix filter against the current caret and
// updates (or closes) the popup. With schedule set it also arms the debounced
// re-request. It reports whether the popup is still open.
func (a *App) refilterCompletion(schedule bool) bool {
	prefix, ok := a.completionPrefix()
	if !ok {
		a.dismissCompletion()
		return false
	}
	a.applyFilter(prefix)
	if len(a.compl.shown) == 0 {
		a.dismissCompletion()
		return false
	}
	a.rebuildCompletionList()
	if schedule {
		a.scheduleRefresh()
	}
	return true
}

// completionPrefix returns the text between the start of the word being
// completed and the caret. ok is false when the caret has wandered outside the
// word, which ends the session.
func (a *App) completionPrefix() (string, bool) {
	if a.compl.start == nil {
		return "", false
	}
	buf := a.codeBuf
	start := buf.IterAtMark(a.compl.start)
	caret := buf.IterAtMark(buf.Mark("insert"))
	if caret.Offset() < start.Offset() {
		return "", false
	}
	prefix := buf.Text(start, caret, false)
	for _, r := range prefix {
		if !isIdentRune(r) {
			return "", false
		}
	}
	return prefix, true
}

// markCompletionStart parks a mark at the beginning of the identifier the caret
// sits in, which is both the filter anchor and the range accepting an item
// replaces.
func (a *App) markCompletionStart() {
	buf := a.codeBuf
	caret := buf.IterAtMark(buf.Mark("insert"))
	off := caret.Offset()
	for off > 0 && isIdentRune(rune(buf.IterAtOffset(off-1).Char())) {
		off--
	}
	at := buf.IterAtOffset(off)
	if a.compl.start == nil {
		a.compl.start = buf.CreateMark("", at, true)
	} else {
		buf.MoveMark(a.compl.start, at)
	}
}

// scheduleRefresh re-asks gopls once typing pauses. Local filtering is instant
// but can only narrow what we already have — gopls trims large result sets and
// only offers unimported symbols once there is a prefix to match.
func (a *App) scheduleRefresh() {
	if a.compl.refresh != 0 {
		glib.SourceRemove(a.compl.refresh)
	}
	a.compl.refresh = glib.TimeoutAdd(complRefreshDelay, func() bool {
		a.compl.refresh = 0
		if a.compl.open {
			a.requestCompletion(triggerInvoked)
		}
		return false
	})
}

type scoredItem struct {
	idx, score int
}

// applyFilter ranks the candidate set against prefix, best first, capped at
// complMaxRows.
func (a *App) applyFilter(prefix string) {
	items := a.compl.items
	a.compl.shown = a.compl.shown[:0]
	if prefix == "" {
		// No prefix: gopls' own relevance order is the best we know.
		a.compl.matched = len(items)
		for i := range items {
			if len(a.compl.shown) >= complMaxRows {
				break
			}
			a.compl.shown = append(a.compl.shown, i)
		}
		return
	}
	matches := make([]scoredItem, 0, len(items))
	for i, it := range items {
		if score, ok := it.bestScore(prefix); ok {
			matches = append(matches, scoredItem{i, score})
		}
	}
	// Stable, so gopls' relevance order breaks ties between equal scores.
	sort.SliceStable(matches, func(x, y int) bool { return matches[x].score > matches[y].score })
	a.compl.matched = len(matches)
	for _, m := range matches {
		if len(a.compl.shown) >= complMaxRows {
			break
		}
		a.compl.shown = append(a.compl.shown, m.idx)
	}
}

// ----------------------------------------------------------------- the popup

func (a *App) openCompletion() {
	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionSingle)
	list.SetActivateOnSingleClick(true)
	list.SetCanFocus(false) // the keyboard belongs to the editor
	list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		a.compl.sel = row.Index()
		a.acceptCompletion()
	})

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(list)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetCanFocus(false)
	// Grow to fit a short list, scroll once it gets long, rather than always
	// showing a half-empty box.
	scroll.SetMinContentWidth(540)
	scroll.SetMinContentHeight(60)
	scroll.SetMaxContentHeight(280)
	scroll.SetPropagateNaturalHeight(true)

	footer := gtk.NewLabel("")
	footer.SetXAlign(0)
	footer.SetMarginStart(8)
	footer.SetMarginEnd(8)
	footer.SetMarginBottom(4)
	footer.SetEllipsize(pango.EllipsizeEnd)
	footer.AddCSSClass("dim-label")

	box := gtk.NewBox(gtk.OrientationVertical, 2)
	box.Append(scroll)
	box.Append(footer)

	pop := gtk.NewPopover()
	pop.SetParent(a.codeView)
	pop.SetChild(box)
	pop.SetHasArrow(false)
	pop.SetPosition(gtk.PosBottom)
	// Autohide would take a keyboard grab; without it the caret stays in the
	// buffer and the user can keep typing to narrow the list.
	pop.SetAutohide(false)
	pop.SetCanFocus(false)

	a.compl.popover, a.compl.list, a.compl.scroll, a.compl.footer = pop, list, scroll, footer
	a.compl.sel = 0
	a.compl.open = true
	a.positionCompletion()
	pop.Popup()
}

func (a *App) rebuildCompletionList() {
	if a.compl.list == nil {
		return
	}
	a.compl.list.RemoveAll()
	for _, idx := range a.compl.shown {
		a.compl.list.Append(completionRow(a.compl.items[idx]))
	}
	a.compl.footer.SetText(a.completionFooter())
	a.setCompletionSel(a.compl.sel)
	a.positionCompletion()
}

func (a *App) completionFooter() string {
	shown, matched := len(a.compl.shown), a.compl.matched
	var s string
	if matched == 1 {
		s = "1 match"
	} else {
		s = fmt.Sprintf("%d matches", matched)
	}
	if shown < matched {
		s += fmt.Sprintf(" (top %d shown)", shown)
	}
	return s + "  ·  ↑↓ select  ·  Enter/Tab insert  ·  Esc cancel"
}

// positionCompletion anchors the popover to the start of the word being
// completed, so it does not jitter sideways as the user types.
func (a *App) positionCompletion() {
	if a.compl.popover == nil || a.compl.start == nil {
		return
	}
	iter := a.codeBuf.IterAtMark(a.compl.start)
	loc := a.codeView.IterLocation(iter)
	wx, wy := a.codeView.BufferToWindowCoords(gtk.TextWindowText, loc.X(), loc.Y())
	rect := gdk.NewRectangle(wx, wy, 1, loc.Height())
	a.compl.popover.SetPointingTo(&rect)
}

func (a *App) moveCompletionSel(delta int) {
	a.setCompletionSel(a.compl.sel + delta)
}

func (a *App) setCompletionSel(i int) {
	n := len(a.compl.shown)
	if n == 0 || a.compl.list == nil {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	a.compl.sel = i
	row := a.compl.list.RowAtIndex(i)
	if row == nil {
		return
	}
	a.compl.list.SelectRow(row)
	// Allocation is only valid once the popover has laid out, so scroll on idle.
	glib.IdleAdd(func() { a.scrollCompletionToSel() })
}

func (a *App) scrollCompletionToSel() {
	if a.compl.list == nil || a.compl.scroll == nil {
		return
	}
	row := a.compl.list.RowAtIndex(a.compl.sel)
	if row == nil {
		return
	}
	origin := graphene.NewPointAlloc()
	origin.Init(0, 0)
	at, ok := row.ComputePoint(a.compl.list, origin)
	if !ok {
		return
	}
	y, h := float64(at.Y()), float64(row.Height())
	adj := a.compl.scroll.VAdjustment()
	switch {
	case y < adj.Value():
		adj.SetValue(y)
	case y+h > adj.Value()+adj.PageSize():
		adj.SetValue(y + h - adj.PageSize())
	}
}

func (a *App) selectedLabel() string {
	if !a.compl.open || a.compl.sel < 0 || a.compl.sel >= len(a.compl.shown) {
		return ""
	}
	return a.compl.items[a.compl.shown[a.compl.sel]].Label
}

// reselectLabel keeps the highlight on the same suggestion after a refresh
// reshuffles the candidate set under it.
func (a *App) reselectLabel(label string) {
	if label == "" {
		return
	}
	for i, idx := range a.compl.shown {
		if a.compl.items[idx].Label == label {
			a.setCompletionSel(i)
			return
		}
	}
}

// ------------------------------------------------------------------ accepting

func (a *App) acceptCompletion() {
	if !a.compl.open || a.compl.sel < 0 || a.compl.sel >= len(a.compl.shown) {
		return
	}
	it := a.compl.items[a.compl.shown[a.compl.sel]]
	buf := a.codeBuf

	start := buf.IterAtMark(a.compl.start)
	caret := buf.IterAtMark(buf.Mark("insert"))
	if caret.Offset() < start.Offset() {
		a.dismissCompletion()
		return
	}

	text := it.insertion()
	// Functions and methods are almost always being called; add the parens and
	// put the caret where the arguments go. gopls only offers call completions
	// itself when the client supports snippets, which we deliberately do not.
	caretBack := 0
	if it.isCallable() && !strings.Contains(text, "(") {
		text += "()"
		if it.takesArgs() {
			caretBack = 1
		}
	}

	endMark := buf.CreateMark("", caret, false)
	a.compl.applying = true
	// gopls sends auto-imports as edits outside the range we are replacing;
	// marks keep our own range valid while they shift offsets around.
	imported := a.applyAdditionalEdits(it.AdditEdits)
	s := buf.IterAtMark(a.compl.start)
	e := buf.IterAtMark(endMark)
	buf.Delete(s, e)
	buf.Insert(s, text)
	buf.DeleteMark(endMark)
	if caretBack > 0 {
		cur := buf.IterAtMark(buf.Mark("insert"))
		buf.PlaceCursor(buf.IterAtOffset(cur.Offset() - caretBack))
	}
	a.compl.applying = false

	a.dismissCompletion()
	a.codeView.GrabFocus()
	if !imported {
		a.setStatus("Inserted " + it.Label + ", but its import could not be added — add it by hand.")
	}
}

// applyAdditionalEdits applies a completion's additionalTextEdits — in practice
// the import an unimported symbol needs. Every position is resolved to a mark
// before anything is written, so one edit cannot invalidate the next and a
// position we fail to map aborts the whole set rather than leaving half an
// import behind. Reports whether the edits were applied.
func (a *App) applyAdditionalEdits(edits []lspTextEdit) bool {
	if len(edits) == 0 {
		return true
	}
	buf := a.codeBuf
	type pendingEdit struct {
		start, end *gtk.TextMark
		text       string
	}
	pending := make([]pendingEdit, 0, len(edits))
	ok := true
	for _, ed := range edits {
		s, okStart := a.iterAtLSP(ed.Range.Start)
		e, okEnd := a.iterAtLSP(ed.Range.End)
		if !okStart || !okEnd {
			ok = false
			break
		}
		pending = append(pending, pendingEdit{
			start: buf.CreateMark("", s, true),
			end:   buf.CreateMark("", e, false),
			text:  ed.NewText,
		})
	}
	if ok {
		for _, p := range pending {
			s, e := buf.IterAtMark(p.start), buf.IterAtMark(p.end)
			buf.Delete(s, e)
			buf.Insert(s, p.text)
		}
	}
	for _, p := range pending {
		buf.DeleteMark(p.start)
		buf.DeleteMark(p.end)
	}
	return ok
}

func (a *App) iterAtLSP(p lspPosition) (*gtk.TextIter, bool) {
	col := a.lsp.RuneColumn(a.bufferLine(p.Line), p.Character)
	return a.codeBuf.IterAtLineOffset(p.Line, col)
}

// bufferLine returns the text of a zero-based buffer line, which is what turns
// a GtkTextIter's character offset into an LSP column and back.
func (a *App) bufferLine(line int) string {
	start, ok := a.codeBuf.IterAtLine(line)
	if !ok {
		return ""
	}
	end := a.codeBuf.IterAtOffset(start.Offset())
	// On an empty line the iter is already at the paragraph delimiter, and
	// ForwardToLineEnd would skip to the *next* line's end.
	if !end.EndsLine() {
		end.ForwardToLineEnd()
	}
	return a.codeBuf.Text(start, end, false)
}

// dismissCompletion tears the popup down and invalidates any reply still in
// flight. Safe to call when nothing is open.
func (a *App) dismissCompletion() {
	if a.compl.refresh != 0 {
		glib.SourceRemove(a.compl.refresh)
		a.compl.refresh = 0
	}
	if a.compl.trigger != 0 {
		glib.SourceRemove(a.compl.trigger)
		a.compl.trigger = 0
	}
	a.compl.gen++ // any in-flight reply is now stale
	if a.compl.popover != nil {
		a.compl.popover.Popdown()
		a.compl.popover.Unparent()
	}
	if a.compl.start != nil {
		a.codeBuf.DeleteMark(a.compl.start)
	}
	a.compl = completionState{gen: a.compl.gen}
}

// completionRow renders one suggestion: name, kind, then the signature dimmed
// and ellipsized on the right.
func completionRow(it CompletionItem) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	box.SetMarginStart(6)
	box.SetMarginEnd(6)

	name := gtk.NewLabel(it.Label)
	name.SetXAlign(0)
	if it.Deprecated {
		name.AddCSSClass("dim-label")
	}
	box.Append(name)

	if kind := it.kindName(); kind != "" {
		k := gtk.NewLabel(kind)
		k.SetXAlign(0)
		k.SetWidthChars(9)
		k.AddCSSClass("dim-label")
		box.Append(k)
	}
	if it.Detail != "" {
		d := gtk.NewLabel(it.Detail)
		d.SetXAlign(1)
		d.SetHExpand(true)
		d.SetEllipsize(pango.EllipsizeEnd)
		d.SetMaxWidthChars(56)
		d.AddCSSClass("dim-label")
		box.Append(d)
	}

	row := gtk.NewListBoxRow()
	row.SetChild(box)
	row.SetCanFocus(false)
	return row
}
