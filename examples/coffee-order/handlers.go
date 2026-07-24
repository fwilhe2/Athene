package main

// This file holds YOUR event handlers.
// athene never rewrites your code here — when you double-click a widget it
// only appends a new empty stub if one does not already exist yet.

import "atheneapp/athutil"

const (
	cupPrice     = 3.50
	giftWrap     = 2.00
	expressExtra = 5.00
	freeShipping = 30.00
)

func OnbtnOrderClicked() {
	total := spinQty.Value() * cupPrice
	if chkWrap.Active() {
		total += giftWrap
	}
	if swExpress.State() {
		total += expressExtra
	}

	lblTotalVal.SetText("$" + athutil.FormatFixed(total, 2))
	progFree.SetFraction(athutil.Clampf(total/freeShipping, 0, 1))
}
