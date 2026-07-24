package main

// This file holds YOUR event handlers.
// athene never rewrites your code here — when you double-click a widget it
// only appends a new empty stub if one does not already exist yet.

import (
	"math"

	"atheneapp/athui"
	"atheneapp/athutil"
)

func OnbtnCalcClicked() {
	if !athutil.IsNumeric(entP.Text()) || !athutil.IsNumeric(entR.Text()) || !athutil.IsNumeric(entY.Text()) {
		athui.Error(MainWindow, "Enter loan amount, rate and term as numbers.")
		return
	}
	principal := athutil.Atof(entP.Text())
	months := athutil.Atof(entY.Text()) * 12
	monthlyRate := athutil.Atof(entR.Text()) / 100 / 12
	if months <= 0 {
		athui.Error(MainWindow, "Term must be greater than zero.")
		return
	}

	// Standard amortized-loan payment formula; the rate-free case is a plain
	// even split so a 0% loan still gives a sensible answer.
	var payment float64
	if monthlyRate == 0 {
		payment = principal / months
	} else {
		payment = principal * monthlyRate / (1 - math.Pow(1+monthlyRate, -months))
	}
	total := payment * months

	lblPayVal.SetText(athutil.FormatGrouped(payment, 2))
	lblTotVal.SetText(athutil.FormatGrouped(total, 2))
	lblIntVal.SetText(athutil.FormatGrouped(total-principal, 2))
}
