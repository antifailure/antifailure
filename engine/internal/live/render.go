package live

import (
	"fmt"
	"strings"
)

// The terminal palette, mapped from the console's own tokens so the two
// surfaces read as one product: neon for the live mark, the verdict greens and
// reds, amber for a caution. ANSI SGR codes, applied only when color is on so a
// piped or NO_COLOR run stays plain text.
const (
	cReset = "\x1b[0m"
	cDim   = "\x1b[38;5;244m" // muted grey, the console's --color-dim
	cBold  = "\x1b[1m"
	cNeon  = "\x1b[38;5;40m"  // #33bf00, the live mark and only the mark
	cPass  = "\x1b[38;5;29m"  // #1e7a3a
	cFail  = "\x1b[38;5;160m" // #b3261e
	cWarn  = "\x1b[38;5;136m" // #8a5a00 amber
	cInk   = "\x1b[38;5;231m" // near-white ink on a dark terminal
)

// RenderOpts is everything the pure renderer needs that is not in the State.
type RenderOpts struct {
	// Focus is the index of the agent whose pane is shown, into State.Agents.
	// Clamped, so an out-of-range value renders the nearest real agent rather
	// than panicking.
	Focus int
	// Width is the terminal width; lines are truncated to it. Zero means no
	// truncation, which is what a test wants.
	Width int
	// Color turns the ANSI palette on. Off for a non-TTY or NO_COLOR run.
	Color bool
	// Elapsed is the run's wall clock so far, already formatted by the caller
	// (the renderer holds no clock, so its output is deterministic).
	Elapsed string
}

// colorize applies a code when color is on. A method on RenderOpts so every
// line in this file reads the same way.
func (o RenderOpts) c(code, text string) string {
	if !o.Color {
		return text
	}
	return code + text + cReset
}

// stateChip is the static, colored word for an agent's state. There is no
// pulse and no spinner: a live agent says LIVE in neon and a frame timestamp
// carries the motion, which is the house rule this product holds on every
// surface.
func stateChip(o RenderOpts, a Agent) string {
	switch a.State {
	case "pending":
		return o.c(cDim, "PENDING")
	case "connecting":
		return o.c(cWarn, "CONNECTING")
	case "live":
		return o.c(cNeon, "LIVE")
	case "ended":
		return o.c(verdictColor(a.Verdict), "ENDED "+verdictWord(a.Verdict))
	case "error":
		return o.c(cFail, "ERROR")
	default:
		return o.c(cDim, strings.ToUpper(a.State))
	}
}

func verdictColor(verdict string) string {
	switch verdict {
	case "pass":
		return cPass
	case "fail":
		return cFail
	case "flaky", "unverified", "blocked":
		return cWarn
	default:
		return cDim
	}
}

func verdictWord(verdict string) string {
	if verdict == "" {
		return "no verdict"
	}
	return verdict
}

// isPixelSurface reports whether an agent's surface renders as an image. A
// terminal surface's cast IS text, so its pane shows the cast directly; a
// pixel surface (web, desktop, ios) cannot be shown in a terminal, so its pane
// shows a live text status and the frame's own metadata instead.
func isPixelSurface(surface string) bool {
	return surface == "web" || surface == "desktop" || surface == "ios"
}

// clampFocus keeps the focus index inside the agent list.
func clampFocus(focus, n int) int {
	if n == 0 {
		return 0
	}
	if focus < 0 {
		return 0
	}
	if focus >= n {
		return n - 1
	}
	return focus
}

// SwitchKey maps a keypress to a new focus index. Pure, so the switcher's whole
// behaviour is one testable function: a number key selects that agent when it
// exists, the arrows and vi keys step, Tab wraps, and anything else leaves the
// focus where it was.
func SwitchKey(key string, current, n int) int {
	if n == 0 {
		return 0
	}
	switch key {
	case "up", "k", "left", "h":
		if current > 0 {
			return current - 1
		}
		return current
	case "down", "j", "right", "l":
		if current < n-1 {
			return current + 1
		}
		return current
	case "tab":
		return (current + 1) % n
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		idx := int(key[0] - '1')
		if idx < n {
			return idx
		}
	}
	return current
}

// Render draws the whole watch view: a title, the agent switcher rail, the
// focused agent's pane, and a footer of keys. Pure: the same State and options
// always produce the same string, which is what lets the switcher and the
// pane be tested without a terminal.
func Render(s State, opts RenderOpts) string {
	var b strings.Builder
	focus := clampFocus(opts.Focus, len(s.Agents))

	// Title.
	title := opts.c(cBold, "af watch")
	tally := runTally(s)
	head := title + "  " + opts.c(cDim, "run "+shorten(s.Run, 24))
	if opts.Elapsed != "" {
		head += opts.c(cDim, "  "+opts.Elapsed)
	}
	if tally != "" {
		head += "  " + tally
	}
	b.WriteString(trunc(head, opts.Width))
	b.WriteByte('\n')
	b.WriteString(trunc(opts.c(cDim, strings.Repeat("-", railWidth(opts.Width))), opts.Width))
	b.WriteByte('\n')

	if len(s.Agents) == 0 {
		b.WriteString(trunc(opts.c(cDim, "Connecting. No agents have reported yet."), opts.Width))
		b.WriteByte('\n')
		b.WriteString(footer(opts))
		return b.String()
	}

	// The switcher rail: every agent, numbered, with its static state chip. The
	// focused one is marked so a reader knows which pane is below.
	for i, a := range s.Agents {
		marker := "  "
		label := fmt.Sprintf("%d %s", i+1, agentLabel(a))
		if i == focus {
			marker = opts.c(cNeon, "> ")
			label = opts.c(cBold, label)
		}
		line := marker + label + "  " + stateChip(opts, a)
		b.WriteString(trunc(line, opts.Width))
		b.WriteByte('\n')
	}
	b.WriteString(trunc(opts.c(cDim, strings.Repeat("-", railWidth(opts.Width))), opts.Width))
	b.WriteByte('\n')

	// The focused pane.
	b.WriteString(pane(s.Agents[focus], opts))
	b.WriteString(footer(opts))
	return b.String()
}

// agentLabel is the persona and workflow a switcher row shows.
func agentLabel(a Agent) string {
	parts := []string{}
	if a.Persona != "" {
		parts = append(parts, a.Persona)
	}
	if a.Workflow != "" {
		parts = append(parts, a.Workflow)
	}
	if len(parts) == 0 {
		return a.ID
	}
	return strings.Join(parts, " / ")
}

// pane renders the focused agent's live surface. A pixel surface shows a text
// status and the frame's metadata, because a terminal cannot show the image
// itself; a terminal surface shows its cast directly.
func pane(a Agent, opts RenderOpts) string {
	var b strings.Builder
	b.WriteString(trunc(opts.c(cBold, agentLabel(a))+"  "+stateChip(opts, a), opts.Width))
	b.WriteByte('\n')

	last := lastStep(a)
	if last.URL != "" {
		b.WriteString(trunc(opts.c(cDim, "at ")+shorten(last.URL, 60), opts.Width))
		b.WriteByte('\n')
	}

	if isPixelSurface(a.Surface) {
		if a.Frame != nil {
			meta := fmt.Sprintf("live frame #%d  %dx%d", a.Frame.Seq, a.Frame.W, a.Frame.H)
			if a.Frame.At != "" {
				meta += "  " + a.Frame.At
			}
			b.WriteString(trunc(opts.c(cNeon, "[ ")+opts.c(cDim, meta)+opts.c(cNeon, " ]"), opts.Width))
			b.WriteByte('\n')
			b.WriteString(trunc(opts.c(cDim, "The image streams to the browser watch view; here is its cast."), opts.Width))
			b.WriteByte('\n')
		} else if a.State == "pending" {
			b.WriteString(trunc(opts.c(cDim, "Waiting to start."), opts.Width))
			b.WriteByte('\n')
		}
	}

	// The cast: the recent steps, newest last, which is the terminal-native
	// view and the fallback for a pixel surface alike.
	tail := recentSteps(a, 8)
	if len(tail) == 0 {
		b.WriteString(trunc(opts.c(cDim, "No steps yet."), opts.Width))
		b.WriteByte('\n')
	}
	for _, step := range tail {
		b.WriteString(trunc(opts.c(cDim, "  . ")+step.Text, opts.Width))
		b.WriteByte('\n')
	}
	return b.String()
}

// lastStep is the most recent step, or the zero Step when there are none.
func lastStep(a Agent) Step {
	if len(a.Steps) == 0 {
		return Step{}
	}
	return a.Steps[len(a.Steps)-1]
}

// recentSteps is the last n steps in order.
func recentSteps(a Agent, n int) []Step {
	if len(a.Steps) <= n {
		return a.Steps
	}
	return a.Steps[len(a.Steps)-n:]
}

// runTally is the colored counts once the run has finished, empty before.
func runTally(s State) string {
	if !s.Counts.Present {
		return ""
	}
	c := s.Counts
	parts := []string{
		fmt.Sprintf("%d passed", c.Passed),
		fmt.Sprintf("%d failed", c.Failed),
	}
	if c.Flaky > 0 {
		parts = append(parts, fmt.Sprintf("%d flaky", c.Flaky))
	}
	if c.Blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", c.Blocked))
	}
	if c.Unverified > 0 {
		parts = append(parts, fmt.Sprintf("%d unverified", c.Unverified))
	}
	return strings.Join(parts, ", ")
}

func footer(opts RenderOpts) string {
	keys := "1-9 switch   up/down move   tab next   q quit"
	return trunc(opts.c(cDim, keys), opts.Width)
}

// railWidth is how wide the divider rule is drawn.
func railWidth(width int) int {
	if width <= 0 || width > 72 {
		return 48
	}
	return width - 1
}

// trunc cuts a line to width runes, ignoring ANSI escapes when counting so a
// colored line is not cut short by its own invisible codes. Width zero means no
// truncation.
func trunc(s string, width int) string {
	if width <= 0 {
		return s
	}
	visible := 0
	inEscape := false
	var b strings.Builder
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			b.WriteRune(r)
			continue
		}
		if inEscape {
			b.WriteRune(r)
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if visible >= width {
			// Keep any pending reset so color does not bleed past the cut.
			return b.String() + cReset
		}
		b.WriteRune(r)
		visible++
	}
	return b.String()
}

// shorten trims a long string to n runes with an ellipsis, for a url or a run
// id that would otherwise blow the line width.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "."
}
