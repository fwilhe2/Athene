// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Copyright (C) 2026 Florian Wilhelm
//
// This file is part of the Athene runtime (athutil). It is distributed under
// the GNU Lesser General Public License, version 2.1 or later, WITH the
// following linking exception:
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

// Package athutil provides small helpers for Athene-designed applications:
// forgiving parsing of gtk.Entry text and compact number formatting for
// gtk.Label. It has no dependencies beyond the standard library, so a copy is
// stamped into every generated project (import path "<yourmodule>/athutil").
package athutil

import (
	"strconv"
	"strings"
)

// Atoi parses s as an integer, returning 0 when s is not a valid number.
// Leading/trailing whitespace is ignored, so reading an Entry needs no
// ceremony: n := athutil.Atoi(entry.Text()).
func Atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// Atof parses s as a float64, returning 0 when s is not a valid number.
func Atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// ParseInt is like Atoi but also reports whether s was a valid integer, so a
// handler can show its own message on bad input.
func ParseInt(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil
}

// ParseFloat is like Atof but also reports whether s was a valid number.
func ParseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

// Itoa formats an integer as text, ready to hand to Label.SetText.
func Itoa(n int) string { return strconv.Itoa(n) }

// FormatFloat renders f compactly, dropping trailing zeros: 3.5, 42, 0.001.
func FormatFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
