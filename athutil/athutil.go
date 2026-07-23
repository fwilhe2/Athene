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
	"math"
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

// FormatFixed renders f with exactly places decimals: FormatFixed(3.14159, 2) = "3.14".
func FormatFixed(f float64, places int) string {
	return strconv.FormatFloat(f, 'f', places, 64)
}

// --- validation ---

// IsBlank reports whether s is empty or only whitespace.
func IsBlank(s string) bool { return strings.TrimSpace(s) == "" }

// IsNumeric reports whether s parses as a number (integer or decimal).
func IsNumeric(s string) bool {
	_, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil
}

// --- math ---

// Clamp constrains n to the inclusive range [lo, hi].
func Clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// Clampf constrains f to the inclusive range [lo, hi].
func Clampf(f, lo, hi float64) float64 {
	if f < lo {
		return lo
	}
	if f > hi {
		return hi
	}
	return f
}

// Round rounds f to the given number of decimal places (Round(3.14159, 2) = 3.14).
func Round(f float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(f*p) / p
}

// --- thousands grouping ---

// FormatInt renders n with thousands separators: FormatInt(1234567) = "1,234,567".
func FormatInt(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	s = groupDigits(s)
	if neg {
		return "-" + s
	}
	return s
}

// FormatGrouped renders f with the given number of decimals and thousands
// separators on the integer part: FormatGrouped(1234.5, 2) = "1,234.50".
func FormatGrouped(f float64, places int) string {
	s := strconv.FormatFloat(f, 'f', places, 64)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}
	out := groupDigits(intPart) + frac
	if neg {
		return "-" + out
	}
	return out
}

// groupDigits inserts commas every three digits into a run of digits, from the
// right. It expects a non-negative, sign-free integer string.
func groupDigits(digits string) string {
	var b strings.Builder
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(digits[i])
	}
	return b.String()
}
