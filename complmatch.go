package main

import (
	"strings"
	"unicode"
)

// complmatch.go is the ranking half of autocomplete: pure functions that decide
// which of gopls' candidates survive the prefix the user has typed and in what
// order they appear. It is deliberately free of GTK and of the LSP client, which
// is what makes it the one part of the completion path covered by tests
// (completion_test.go).
//
// Why match locally at all, when gopls already filters? Because a keystroke must
// narrow the list instantly, and a round trip cannot. completion_ui.go re-asks
// gopls on a debounce to widen the candidate set again; this file keeps the
// visible list honest in between.

// Match tiers. The gap between them is wide enough that the per-candidate
// adjustments below can never promote a worse kind of match over a better one.
const (
	tierExactPrefix = 10000 // "Set" in SetText
	tierCasePrefix  = 9000  // "set" in SetText
	tierInitials    = 8000  // "st" in SetText, "nwl" in NewWithLabel
	tierSubstring   = 7000  // "ext" in SetText
	tierSubsequence = 5000  // "stt" in SetText
)

// fuzzyScore ranks candidate against pattern the way an editor should: exact
// prefixes win, then case-insensitive prefixes, then camel-case initials, then
// substrings, then loose subsequences. Within a tier, shorter candidates and
// earlier, tighter matches score higher. ok is false when the pattern's
// characters do not appear in candidate in order at all.
func fuzzyScore(pattern, candidate string) (int, bool) {
	if pattern == "" {
		return 0, true
	}
	lowPat, lowCand := strings.ToLower(pattern), strings.ToLower(candidate)
	switch {
	case strings.HasPrefix(candidate, pattern):
		return tierExactPrefix - len(candidate), true
	case strings.HasPrefix(lowCand, lowPat):
		return tierCasePrefix - len(candidate), true
	case strings.HasPrefix(wordInitials(candidate), lowPat):
		return tierInitials - len(candidate), true
	}
	if i := strings.Index(lowCand, lowPat); i >= 0 {
		return tierSubstring - i*10 - len(candidate), true
	}
	if gaps, ok := subsequenceGaps(lowPat, lowCand); ok {
		return tierSubsequence - gaps*20 - len(candidate), true
	}
	return 0, false
}

// wordInitials reduces an identifier to its word-start letters, lowercased:
// "NewWithLabel" -> "nwl", "parseInt" -> "pi", "MAX_LEN" -> "ml".
func wordInitials(s string) string {
	var out []rune
	var prev rune
	for i, r := range s {
		switch {
		case i == 0:
			out = append(out, unicode.ToLower(r))
		case r == '_' || r == '.':
			// separator; the next rune starts a word
		case prev == '_' || prev == '.':
			out = append(out, unicode.ToLower(r))
		case unicode.IsUpper(r) && !unicode.IsUpper(prev):
			out = append(out, unicode.ToLower(r))
		}
		prev = r
	}
	return string(out)
}

// subsequenceGaps reports whether pattern appears in candidate in order, and how
// many times the run of matches was interrupted — fewer gaps is a tighter, and
// so better, match.
func subsequenceGaps(pattern, candidate string) (int, bool) {
	pat := []rune(pattern)
	pi, gaps, last := 0, 0, -2
	for i, r := range []rune(candidate) {
		if pi >= len(pat) {
			break
		}
		if r == pat[pi] {
			if pi > 0 && i != last+1 {
				gaps++
			}
			last = i
			pi++
		}
	}
	return gaps, pi == len(pat)
}

// isIdentRune reports whether r can appear in a Go identifier, which is how the
// editor decides where the word being completed starts and when the user has
// typed their way out of it.
func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
