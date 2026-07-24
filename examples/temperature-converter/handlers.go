package main

// This file holds YOUR event handlers.
// athene never rewrites your code here — when you double-click a widget it
// only appends a new empty stub if one does not already exist yet.

import (
	"atheneapp/athui"
	"atheneapp/athutil"
)

func OnbtnCtoFClicked() {
	if !athutil.IsNumeric(entC.Text()) {
		athui.Error(MainWindow, "Enter a temperature in Celsius.")
		return
	}
	f := athutil.Atof(entC.Text())*9/5 + 32
	entF.SetText(athutil.FormatFixed(f, 1))
}

func OnbtnFtoCClicked() {
	if !athutil.IsNumeric(entF.Text()) {
		athui.Error(MainWindow, "Enter a temperature in Fahrenheit.")
		return
	}
	c := (athutil.Atof(entF.Text()) - 32) * 5 / 9
	entC.SetText(athutil.FormatFixed(c, 1))
}
