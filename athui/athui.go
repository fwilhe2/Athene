// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Copyright (C) 2026 Florian Wilhelm
//
// This file is part of the Athene runtime (athui). It is distributed under the
// GNU Lesser General Public License, version 2.1 or later, WITH the following
// linking exception:
//
//   As a special exception, the copyright holders give you permission to link
//   this file with independent modules to produce an executable, regardless of
//   the license terms of those independent modules, and to copy and distribute
//   the resulting executable under terms of your choice, provided that you also
//   meet, for each linked independent module, the terms and conditions of the
//   license of that module. An independent module is a module which is not
//   derived from or based on this file. If you modify this file, you may extend
//   this exception to your version of the file, but you are not obligated to do
//   so. If you do not wish to do so, delete this exception statement from your
//   version.
//
// In plain terms: applications you build with Athene may be licensed however
// you like. See LICENSE.exception in the Athene distribution for the full text.

// Package athui provides message-box helpers for Athene-designed applications.
// A copy is stamped into every generated project (import path "<yourmodule>/athui").
//
// The generated app exposes its main window as MainWindow, so the helpers take
// it directly:
//
//	athui.Info(MainWindow, "Saved.")
//	athui.Ask(MainWindow, "Delete this item?", func() { /* on Yes */ })
//
// These wrap gtk.MessageDialog. It is deprecated in GTK 4.10 in favour of
// gtk.AlertDialog, but this gotk4 release ships no Go constructor for
// AlertDialog, so MessageDialog remains the portable choice; it is fully
// functional.
package athui

import "github.com/diamondburned/gotk4/pkg/gtk/v4"

// Info shows a modal message box with a single OK button.
func Info(parent *gtk.ApplicationWindow, message string) {
	show(parent, gtk.MessageInfo, gtk.ButtonsOK, message, nil)
}

// Error shows a modal error message box with a single OK button.
func Error(parent *gtk.ApplicationWindow, message string) {
	show(parent, gtk.MessageError, gtk.ButtonsOK, message, nil)
}

// Ask shows a modal Yes/No question. onYes runs when the user chooses Yes.
func Ask(parent *gtk.ApplicationWindow, message string, onYes func()) {
	show(parent, gtk.MessageQuestion, gtk.ButtonsYesNo, message, onYes)
}

func show(parent *gtk.ApplicationWindow, typ gtk.MessageType, buttons gtk.ButtonsType, message string, onYes func()) {
	var win *gtk.Window
	if parent != nil {
		win = &parent.Window
	}
	d := gtk.NewMessageDialog(win, gtk.DialogModal|gtk.DialogDestroyWithParent, typ, buttons)
	d.SetMarkup(message)
	d.ConnectResponse(func(responseID int) {
		if onYes != nil && gtk.ResponseType(responseID) == gtk.ResponseYes {
			onYes()
		}
		d.Destroy()
	})
	d.Present()
}
