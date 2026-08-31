package textwrap_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/textwrap"
)

// No line may exceed the width it was given, except where refusing to break a
// token is the more important rule. Both halves are asserted, because a
// wrapper that respected the width by cutting a URL in half would pass a test
// that only checked the width.
func TestWrap_KeepsEveryLineInsideTheWidth(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("word ", 60)
	for _, width := range []int{40, 60, 80, 120, 200} {
		got := textwrap.Wrap(long, 4, width)
		for _, line := range strings.Split(got, "\n") {
			require.LessOrEqualf(t, textwrap.Cells(line), width,
				"at width %d a line ran to %d: %q", width, textwrap.Cells(line), line)
		}
	}
}

// A URL with a newline in the middle of it is not ugly, it is wrong: somebody
// copies it and gets a 404 out of the message that was meant to help them.
func TestWrap_NeverBreaksAWord(t *testing.T) {
	t.Parallel()
	url := "https://antifailure.dev/docs/reference/errors#af-cpl-004"
	got := textwrap.Wrap("Read more at "+url+" and then try again", 2, 40)
	require.Contains(t, got, url, "the address must survive the wrap:\n%s", got)
}

// The same reasoning one level up: every next step in the error catalogue says
// what to run as 'af something', and a reader who copies the first line of a
// broken one pastes a usage error.
func TestWrap_NeverBreaksAQuotedCommand(t *testing.T) {
	t.Parallel()
	got := textwrap.Wrap(
		"Sign in with 'af login --control-plane https://app.antifailure.dev --scope providers.write' first",
		2, 60)
	require.Contains(t, got,
		"'af login --control-plane https://app.antifailure.dev --scope providers.write'",
		"the command must survive the wrap:\n%s", got)
}

// An apostrophe inside a word must not open a quoted run, or the rest of a
// sentence is glued into one unbreakable line.
func TestWrap_AnApostropheInsideAWordIsNotAQuote(t *testing.T) {
	t.Parallel()
	got := textwrap.Wrap(
		"The environment's own golden is what the branch it's asking about was made from, "+
			"and nothing else on this machine can answer that question for it", 0, 40)
	for _, line := range strings.Split(got, "\n") {
		require.LessOrEqual(t, textwrap.Cells(line), 40, "apostrophes glued the line:\n%s", got)
	}
}

// An unbalanced quote must not glue a paragraph together either.
func TestWrap_AnUnclosedQuoteDoesNotSwallowTheRest(t *testing.T) {
	t.Parallel()
	got := textwrap.Wrap("Run 'af up and then keep going with a great many further words "+
		"that would otherwise all end up on one very long line indeed", 0, 40)
	require.Greater(t, strings.Count(got, "\n"), 1, "the paragraph did not wrap at all:\n%s", got)
}

// Width is clamped rather than obeyed literally, so a caller that measured a
// pathological terminal, or passed nothing, still gets readable text.
func TestWrap_ClampsAnUnusableWidth(t *testing.T) {
	t.Parallel()
	for _, width := range []int{0, 1, 10, -5} {
		got := textwrap.Wrap(strings.Repeat("word ", 20), 0, width)
		for _, line := range strings.Split(got, "\n") {
			require.LessOrEqual(t, textwrap.Cells(line), textwrap.MinWidth)
		}
		require.Contains(t, got, "word word")
	}
}

// Continuation lines carry the indent; the first does not, because the caller
// has already written whatever sits in front of it.
func TestWrap_HangsUnderTheIndent(t *testing.T) {
	t.Parallel()
	got := textwrap.Wrap(strings.Repeat("word ", 20), 6, 40)
	lines := strings.Split(got, "\n")
	require.Greater(t, len(lines), 1)
	require.False(t, strings.HasPrefix(lines[0], " "), "the first line is not indented")
	for _, line := range lines[1:] {
		require.True(t, strings.HasPrefix(line, "      "), "continuation lost its indent: %q", line)
	}
}

// Escape sequences occupy no columns and multi byte characters may occupy two,
// which are two different wrong answers if you measure with len.
func TestCells_MeasuresDisplayColumnsNotBytes(t *testing.T) {
	t.Parallel()
	require.Equal(t, 8, textwrap.Cells("\x1b[32mverified\x1b[0m"),
		"a styled cell measured its escape sequence and misaligned every table it was in")
	require.Equal(t, 4, textwrap.Cells("café"))
	require.Equal(t, 0, textwrap.Cells(""))
}
