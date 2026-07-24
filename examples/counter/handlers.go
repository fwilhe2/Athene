package main

// This file holds YOUR event handlers.
// athene never rewrites your code here — when you double-click a widget it
// only appends a new empty stub if one does not already exist yet.

import "atheneapp/athutil"

// count is the app's single piece of state. Package-scope variables are the
// idiomatic way to keep model data between clicks in an Athene app.
var count int

func refresh() {
	lblCount.SetText(athutil.Itoa(count))
}

func OnbtnIncClicked() {
	count++
	refresh()
}

func OnbtnDecClicked() {
	count--
	refresh()
}

func OnbtnResetClicked() {
	count = 0
	refresh()
}
