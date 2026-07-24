package main

// This file holds YOUR event handlers.
// athene never rewrites your code here — when you double-click a widget it
// only appends a new empty stub if one does not already exist yet.

import (
	"atheneapp/athui"
	"atheneapp/athutil"
)

func OnbtnCalcClicked() {
	if !athutil.IsNumeric(entBill.Text()) {
		athui.Error(MainWindow, "Please enter a valid bill amount.")
		return
	}
	bill := athutil.Atof(entBill.Text())
	pct := athutil.Atof(entPct.Text())
	split := athutil.Atoi(entSplit.Text())
	if split < 1 {
		split = 1
	}

	tip := bill * pct / 100
	total := bill + tip

	lblTipVal.SetText(athutil.FormatGrouped(tip, 2))
	lblTotVal.SetText(athutil.FormatGrouped(total, 2))
	lblEachVal.SetText(athutil.FormatGrouped(total/float64(split), 2))
}
