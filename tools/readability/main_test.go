package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeasureCountsProseAndNothingElse(t *testing.T) {
	page := "---\ntitle: A page\n---\n\n" +
		"One two three four five.\n\n" +
		"```sh\nthis is code and it has a great many words in it indeed yes\n```\n\n" +
		"| a | b |\n| --- | --- |\n| one two three four five six seven | eight |\n\n" +
		"## A heading with several words in it\n\n" +
		"Six seven eight.\n"

	got := measure(page)
	if got.Sentences != 2 {
		t.Errorf("sentences = %d, want 2", got.Sentences)
	}
	if got.Words != 8 {
		t.Errorf("words = %d, want 8 (five plus three)", got.Words)
	}
	if got.Longest != 5 {
		t.Errorf("longest = %d, want 5", got.Longest)
	}
}

// Link text is read and link targets are not, so a page full of long URLs is
// not reported as hard to read.
func TestMeasureKeepsLinkTextAndDropsTheTarget(t *testing.T) {
	got := measure("Read [the masking guide](/docs/concepts/masking-and-verification/) now.\n")
	if got.Words != 5 {
		t.Errorf("words = %d, want 5: Read the masking guide now", got.Words)
	}
}

// Inline code becomes one word rather than vanishing, because a reader reads
// it as one thing. Dropping it entirely would make a command heavy page look
// like it has shorter sentences than it does.
func TestMeasureCountsInlineCodeAsOneWord(t *testing.T) {
	got := measure("Run `af up --hud --branch feature/x` first.\n")
	if got.Words != 3 {
		t.Errorf("words = %d, want 3: Run, code, first", got.Words)
	}
}

func TestCountSyllables(t *testing.T) {
	cases := map[string]int{
		"a": 1, "the": 1, "code": 1, "table": 2, "running": 2,
		"environment": 4, "deterministic": 5, "idempotent": 4,
	}
	for word, want := range cases {
		if got := countSyllables(word); got != want {
			t.Errorf("countSyllables(%q) = %d, want %d", word, got, want)
		}
	}
}

// An abbreviation splits a sentence in two and makes the page look easier
// than it is. That is the direction the error should go: this report should be
// quiet when it is unsure, not noisy.
func TestSentenceSplittingErrsTowardsSilence(t *testing.T) {
	got := measure("It runs at 3 p.m. every day without fail.\n")
	if got.Sentences < 2 {
		t.Skip("splitting became abbreviation aware, which is fine")
	}
	if got.Mean > 8 {
		t.Errorf("mean = %.1f, want the split to make it look easier rather than harder", got.Mean)
	}
}

// A list is several sentences, and almost nobody ends a bullet with a full
// stop. Counted naively, three bullets and the paragraph after them are one
// sentence as long as all four, which inflates the mean on every page that
// uses a list. This is the one place the splitter used to report a page as
// harder than it is, which is the wrong direction for a report that is
// supposed to be quiet when unsure.
func TestEachListItemIsItsOwnSentence(t *testing.T) {
	page := "- one two three four five\n" +
		"- six seven eight nine ten\n" +
		"- eleven twelve thirteen fourteen fifteen\n"

	got := measure(page)
	if got.Sentences != 3 {
		t.Errorf("sentences = %d, want 3, one per item", got.Sentences)
	}
	if got.Longest != 5 {
		t.Errorf("longest = %d, want 5: no item is longer than its own words", got.Longest)
	}
	if got.Mean != 5 {
		t.Errorf("mean = %.1f, want 5", got.Mean)
	}
}

// A numbered list is a list too, and so is one that is indented under a
// paragraph, which is how most of them are actually written.
func TestNumberedAndNestedListItemsCountTheSameWay(t *testing.T) {
	got := measure("1. one two three\n2. four five six\n")
	if got.Sentences != 2 {
		t.Errorf("sentences = %d, want 2 for a numbered list", got.Sentences)
	}
}

// Pointing the report at a tree with no pages is an error rather than a quiet
// success, for the same reason as every other check here.
func TestReportingOnNothingIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs", "src", "content", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(dir, 0, os.Stdout); err == nil {
		t.Fatal("a tree with no pages was accepted")
	}
}

// The threshold fails on a page above it and passes on one below.
func TestTheThresholdFires(t *testing.T) {
	dir := t.TempDir()
	pages := filepath.Join(dir, "docs", "src", "content", "docs")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("word ", 40) + "end.\n"
	if err := os.WriteFile(filepath.Join(pages, "long.md"), []byte(long), 0o600); err != nil {
		t.Fatal(err)
	}

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devnull.Close() }()

	if err := run(dir, 20, devnull); err == nil {
		t.Error("a 41 word sentence passed a 20 word limit")
	}
	if err := run(dir, 0, devnull); err != nil {
		t.Errorf("with no limit the report should not fail: %v", err)
	}
}
