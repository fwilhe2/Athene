package main

// This file holds YOUR event handlers.
// athene never rewrites your code here — when you double-click a widget it
// only appends a new empty stub if one does not already exist yet.

import (
	"atheneapp/athui"
	"atheneapp/athutil"
)

func OnbtnCalcClicked() {
	if !athutil.IsNumeric(entW.Text()) || !athutil.IsNumeric(entH.Text()) {
		athui.Error(MainWindow, "Enter weight and height as numbers.")
		return
	}
	weight := athutil.Atof(entW.Text())
	height := athutil.Atof(entH.Text()) / 100 // cm → m
	if height <= 0 {
		athui.Error(MainWindow, "Height must be greater than zero.")
		return
	}

	bmi := weight / (height * height)
	lblBmiVal.SetText(athutil.FormatFixed(bmi, 1))
	lblCatVal.SetText(bmiCategory(bmi))
}

func bmiCategory(bmi float64) string {
	switch {
	case bmi < 18.5:
		return "Underweight"
	case bmi < 25:
		return "Normal"
	case bmi < 30:
		return "Overweight"
	default:
		return "Obese"
	}
}
