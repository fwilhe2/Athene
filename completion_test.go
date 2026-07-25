package main

import "testing"

// The completion ranking and the LSP column conversion are pure functions, so
// they are the one part of the autocomplete path that can be checked without a
// display or a running gopls.

func TestFuzzyScoreTierOrder(t *testing.T) {
	// One example per match tier, best first. The tier constants dominate the
	// per-candidate adjustments, so these stay comparable across patterns.
	tiers := []struct{ pattern, candidate, why string }{
		{"Set", "SetText", "exact prefix"},
		{"Set", "setText", "case-insensitive prefix"},
		{"set", "ShowErrorText", "camel-case initials"},
		{"set", "GetSetTextFoo", "substring"},
		{"set", "SxeXtxt", "loose subsequence"},
	}
	scores := make([]int, len(tiers))
	for i, c := range tiers {
		s, ok := fuzzyScore(c.pattern, c.candidate)
		if !ok {
			t.Fatalf("fuzzyScore(%q, %q) did not match (%s)", c.pattern, c.candidate, c.why)
		}
		scores[i] = s
	}
	for i := 0; i+1 < len(scores); i++ {
		if scores[i] <= scores[i+1] {
			t.Errorf("%s (%d) should outrank %s (%d)", tiers[i].why, scores[i], tiers[i+1].why, scores[i+1])
		}
	}
}

func TestFuzzyScorePrefersShorterCandidates(t *testing.T) {
	short, _ := fuzzyScore("Set", "SetText")
	long, _ := fuzzyScore("Set", "SetTextWithMnemonic")
	if short <= long {
		t.Errorf("SetText (%d) should outrank SetTextWithMnemonic (%d)", short, long)
	}
}

func TestFuzzyScoreRejectsNonSubsequence(t *testing.T) {
	if _, ok := fuzzyScore("zzz", "SetText"); ok {
		t.Error("zzz should not match SetText")
	}
	// Right letters, wrong order.
	if _, ok := fuzzyScore("txeS", "SetText"); ok {
		t.Error("txeS should not match SetText")
	}
	if _, ok := fuzzyScore("", "SetText"); !ok {
		t.Error("the empty pattern should match everything")
	}
}

func TestWordInitials(t *testing.T) {
	cases := map[string]string{
		"NewWithLabel": "nwl",
		"parseInt":     "pi",
		"MAX_LEN":      "ml",
		"fmt.Println":  "fp",
		"x":            "x",
		"":             "",
	}
	for in, want := range cases {
		if got := wordInitials(in); got != want {
			t.Errorf("wordInitials(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubsequenceGaps(t *testing.T) {
	if gaps, ok := subsequenceGaps("set", "settext"); !ok || gaps != 0 {
		t.Errorf("contiguous match: got gaps=%d ok=%v, want 0 true", gaps, ok)
	}
	// s@0, t@2 (a gap), t@3 (contiguous with the previous t).
	if gaps, ok := subsequenceGaps("stt", "settext"); !ok || gaps != 1 {
		t.Errorf("scattered match: got gaps=%d ok=%v, want 1 true", gaps, ok)
	}
	if _, ok := subsequenceGaps("sz", "settext"); ok {
		t.Error("sz should not be a subsequence of settext")
	}
}

func TestColumnConversionHandlesNonASCII(t *testing.T) {
	// A GtkTextIter counts characters; LSP counts UTF-8 bytes or UTF-16 code
	// units. Anything non-ASCII earlier on the line shifts the position, which
	// is what used to send gopls to the wrong place.
	line := `	// price — in € for 𝄞: athutil.`
	runeCol := len([]rune(line)) // caret at end of line

	utf8Client := &LSPClient{encoding: "utf-8"}
	if got, want := utf8Client.Column(line, runeCol), len(line); got != want {
		t.Errorf("utf-8 column = %d, want %d (byte length)", got, want)
	}

	utf16Client := &LSPClient{encoding: "utf-16"}
	got := utf16Client.Column(line, runeCol)
	// One surrogate pair (𝄞) counts as two code units, everything else as one.
	want := len([]rune(line)) + 1
	if got != want {
		t.Errorf("utf-16 column = %d, want %d", got, want)
	}
	if got != runeCol {
		t.Logf("confirmed: character offset %d != utf-16 column %d", runeCol, got)
	}

	// Round-tripping must land back on the original character offset.
	for _, c := range []*LSPClient{utf8Client, utf16Client} {
		if back := c.RuneColumn(line, c.Column(line, runeCol)); back != runeCol {
			t.Errorf("%s round trip = %d, want %d", c.encoding, back, runeCol)
		}
	}
}

func TestCompletionItemCallDetection(t *testing.T) {
	withArgs := CompletionItem{Kind: 2, Detail: "func(text string)"}
	if !withArgs.isCallable() || !withArgs.takesArgs() {
		t.Error("func(text string) should be callable and take args")
	}
	noArgs := CompletionItem{Kind: 2, Detail: "func() string"}
	if !noArgs.isCallable() || noArgs.takesArgs() {
		t.Error("func() string should be callable but take no args")
	}
	field := CompletionItem{Kind: 5, Detail: "gtk.Widget"}
	if field.isCallable() {
		t.Error("a field should not be callable")
	}
}

func TestBestScoreConsidersBothLabelAndFilterText(t *testing.T) {
	// An unimported symbol arrives as label "Println" / filterText "fmt.Println".
	// Both spellings must be matchable.
	it := CompletionItem{Label: "Println", FilterText: "fmt.Println"}
	viaLabel, ok := it.bestScore("Print")
	if !ok {
		t.Fatal(`"Print" should match label "Println"`)
	}
	if _, ok := it.bestScore("fmt.Pr"); !ok {
		t.Error(`"fmt.Pr" should match filterText "fmt.Println"`)
	}
	// The label is the exact-prefix hit, so it must win over the qualified form.
	plainLabel, _ := fuzzyScore("Print", "Println")
	if viaLabel != plainLabel {
		t.Errorf("bestScore = %d, want the label's score %d", viaLabel, plainLabel)
	}
	if _, ok := it.bestScore("zzz"); ok {
		t.Error(`"zzz" should match neither spelling`)
	}
}
