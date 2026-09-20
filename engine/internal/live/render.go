package live

import (
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/termimg"
)

// The terminal palette, in two, because an ANSI colour is absolute and a
// terminal's background is not.
//
// The greens, reds and ambers are the console's own tokens, so the two surfaces
// read as one product. What could not come across is that the console picks its
// ink against a page it controls, and a terminal hands you somebody else's.
// Measured against the warm paper the launch film uses (#f7f4ee), the neon the
// live mark is drawn in reads 1.78 to 1: on that terminal the word LIVE is
// very nearly invisible. Against a dark terminal (#14171a) the same colour is
// 9.18 to 1, which is why nobody noticed.
//
// No colour in the 256 colour cube clears 4.5 to 1 on both, and there is no way
// to average out of it, so the terminal is asked which it is and gets the
// palette that suits it. A terminal that will not say keeps the dark palette,
// which is what it had before any of this, and the loss is a dim word rather
// than a missing one: every signal these colours carry is also carried by a
// word and by the weight of a rule.
type tone int

// The tones, named for what they mean rather than for what colour they are,
// which is what lets one call site serve two palettes.
const (
	toneDim tone = iota
	toneBold
	toneNeon
	tonePass
	toneFail
	toneWarn
)

// palette is the SGR code for each tone. Every entry below clears 4.5 to 1
// against its own background, and TestPaletteIsLegibleOnItsOwnBackground
// measures it rather than taking the comment's word for it.
type palette map[tone]string

// Two of these are not the codes this view used before, and the measurement
// below is why. The pass green was #00875f at 3.97 to 1 and the fail red was
// #d70000 at 3.33, both under the 4.5 an ordinary reader needs, on the DARK
// terminal they were chosen for. They are brighter approximations of the same
// console tokens rather than different colours: a verdict word nobody can read
// is not a verdict word.
var darkPalette = palette{
	toneDim:  "\x1b[38;5;244m", // #808080, 4.56 on #14171a
	toneBold: "\x1b[1m",
	toneNeon: "\x1b[38;5;40m",  // #00d700, 9.18
	tonePass: "\x1b[38;5;35m",  // #00af5f, 6.25
	toneFail: "\x1b[38;5;203m", // #ff5f5f, 6.04
	toneWarn: "\x1b[38;5;136m", // #af8700, 5.39
}

// The light palette is the same four hues walked down until they hold against
// paper. LIVE and a pass share one green: the colour says "this one is fine"
// and the word says which kind of fine, which is how the dark palette's two
// greens read anyway.
var lightPalette = palette{
	toneDim:  "\x1b[38;5;242m", // #6c6c6c, 4.78 on #f7f4ee
	toneBold: "\x1b[1m",
	toneNeon: "\x1b[38;5;22m",  // #005f00, 7.25
	tonePass: "\x1b[38;5;22m",  // #005f00, 7.25
	toneFail: "\x1b[38;5;124m", // #af0000, 6.78
	toneWarn: "\x1b[38;5;94m",  // #875f00, 5.22
}

// cReset ends a colour. One code for both palettes, because there is only one
// way to stop.
const cReset = "\x1b[0m"

// The grid's shape.
//
// minCellWidth is the narrowest a pane can be and still say something useful: a
// personality name, a workflow and a step have to fit. Below it the grid drops
// to one column rather than shrinking further, because four panes of eighteen
// columns each are four panes of nothing.
//
// maxColumns is a taste decision rather than a limit of the layout. Past four
// across, a browser frame in a pane is too small to read at any terminal size
// somebody actually uses, and the point of this view is that the pictures are
// legible.
const (
	minCellWidth = 32
	maxColumns   = 4
	// minCellHeight is heading, workflow, personality, and the current step.
	// A pane shorter than this has lost one of the four things it exists to
	// show, so the grid pages instead of squeezing.
	minCellHeight = 4
	// minImageRows is the shortest image worth drawing. One row of pixels in a
	// pane is a smear, and a smear is worse than the text it replaced.
	minImageRows = 3
	// columnGap is the blank column between panes.
	columnGap = 2
)

// RenderOpts is everything the pure renderer needs that is not in the State.
type RenderOpts struct {
	// Focus is the index of the agent the view is pointed at, into State.Agents.
	// Clamped, so an out-of-range value renders the nearest real agent rather
	// than panicking.
	Focus int
	// Width and Height are the terminal size in character cells. Zero width
	// means no truncation and zero height means no vertical limit, which is
	// what a test wants and what a non-terminal never reaches.
	Width, Height int
	// Color turns the ANSI palette on. Off for a non-TTY or NO_COLOR run.
	Color bool
	// Elapsed is the run's wall clock so far, already formatted by the caller
	// (the renderer holds no clock, so its output is deterministic).
	Elapsed string
	// Images is what the terminal will draw. Its zero value is a terminal that
	// draws nothing, which is the safe default: a view built with no opinion
	// renders text rather than escape sequences somebody's terminal would print.
	Images termimg.Capability
	// Solo gives the focused agent the whole view instead of one pane of the
	// grid, for looking closely at one agent without losing the others' state.
	Solo bool
	// Light picks the palette for a terminal painted in a light colour. False
	// is the dark palette, which is also what a terminal that would not say
	// gets: guessing light on a dark terminal is the worse mistake of the two,
	// because the dark palette's own author could see it.
	Light bool
}

// colorize applies a tone when color is on, from the palette the terminal's
// own background chose. A method on RenderOpts so every line in this file reads
// the same way and neither palette is named at a call site.
func (o RenderOpts) c(t tone, text string) string {
	if !o.Color {
		return text
	}
	p := darkPalette
	if o.Light {
		p = lightPalette
	}
	return p[t] + text + cReset
}

// stateChip is the static, colored word for an agent's state.
//
// There is no pulse, no spinner and no blinking mark anywhere in this view, and
// this is the place somebody would reach for one. A live agent says LIVE in
// neon and the picture under it changes once a second; that is the motion, and
// it is the run's own motion rather than decoration. A chip that throbbed while
// nothing happened would be the house rule's exact counterexample.
func stateChip(o RenderOpts, a Agent) string {
	switch a.State {
	case "pending":
		return o.c(toneDim, "PENDING")
	case "connecting":
		return o.c(toneWarn, "OPENING")
	case "live":
		return o.c(toneNeon, "LIVE")
	case "ended":
		return o.c(verdictColor(a.Verdict), strings.ToUpper(verdictWord(a.Verdict)))
	case "error":
		return o.c(toneFail, "ERROR")
	case "":
		return o.c(toneDim, "WAITING")
	default:
		return o.c(toneDim, strings.ToUpper(a.State))
	}
}

func verdictColor(verdict string) tone {
	switch verdict {
	case "pass":
		return tonePass
	case "fail":
		return toneFail
	case "flaky", "unverified", "blocked":
		return toneWarn
	default:
		return toneDim
	}
}

func verdictWord(verdict string) string {
	if verdict == "" {
		return "ended"
	}
	return verdict
}

// isPixelSurface reports whether an agent's surface produces frames. A terminal
// surface's cast IS text, so its pane shows the cast whether or not the
// terminal can draw pictures.
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

// frameBox is one image to be drawn, and where on the screen it goes.
//
// Collected while the text is laid out and emitted after all of it, which is
// the one ordering that works. Every image protocol here paints pixels over the
// cells it covers, and writing an ordinary space into one of those cells wipes
// the pixels under it. The layout puts blank lines where the pictures go, so if
// the pictures were written first the blanks would erase them on the very same
// frame. Written last, they land on top of whatever text was just painted.
type frameBox struct {
	// ID is stable per pane for the life of the run, so a terminal that keys
	// images by id replaces the pane's picture rather than accumulating one a
	// second until its store evicts something somebody is looking at.
	ID         int
	B64        string
	Row, Col   int // one based, absolute on the screen
	Cols, Rows int
}

// Render draws the whole swarm: every agent at once, each with its personality,
// its state, its current step and its latest frame.
//
// Pure. The same State and options always produce the same string, which is
// what lets the layout, the switcher and the image placement all be tested
// without a terminal.
//
// The images are appended to the LAST line of the frame rather than given lines
// of their own. Two reasons, both load bearing. A line carrying an image escape
// has no printable width, so it would render as an empty line and push the
// layout down by one. And the renderer above this only repaints lines that
// changed, so putting every placement on the one line that changes on every
// frame is what makes the pictures redraw after any repaint that blanked them.
func Render(s State, opts RenderOpts) string {
	width := opts.Width
	if width <= 0 {
		width = 80
	}
	focus := clampFocus(opts.Focus, len(s.Agents))

	lines := []string{
		title(s, opts, width),
		opts.c(toneDim, strings.Repeat("─", width)),
	}
	// The header above, the footer below, and whatever is left for the panes.
	body := opts.Height - len(lines) - 1
	if opts.Height <= 0 {
		body = 0
	}

	if len(s.Agents) == 0 {
		lines = append(lines, opts.c(toneDim, "Connecting. No agents have reported yet."))
		return join(lines, width) + "\n" + footer(opts, width, 0, 0)
	}

	var boxes []frameBox
	var shown, total int
	if opts.Solo {
		var cellLines []string
		cellLines, boxes = soloPane(s.Agents[focus], focus, opts, width, body, len(lines)+1)
		lines = append(lines, cellLines...)
		shown, total = 1, len(s.Agents)
	} else {
		var gridLines []string
		gridLines, boxes, shown, total = grid(s.Agents, focus, opts, width, body, len(lines)+1)
		lines = append(lines, gridLines...)
	}

	out := join(lines, width) + "\n" + footer(opts, width, shown, total)
	return out + placements(opts, boxes)
}

// title is the one line that is always at the top.
func title(s State, opts RenderOpts, width int) string {
	left := opts.c(toneBold, "af watch")
	if s.Run != "" {
		left += "  " + opts.c(toneDim, "run "+shorten(s.Run, 28))
	}
	if opts.Elapsed != "" {
		left += "  " + opts.c(toneDim, opts.Elapsed)
	}
	if n := len(s.Agents); n > 0 {
		left += "  " + opts.c(toneDim, plural(n, "agent"))
	}

	right := runTally(s)
	gap := width - visible(left) - visible(right)
	if right == "" || gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// grid lays every agent out as a pane, reflowing to the width and paging when
// there are more agents than the height can hold.
//
// top is the screen row, one based, that the first pane row starts on, which is
// what turns a pane's own coordinates into the absolute position an image
// escape needs.
//
// Returns the lines, the images to draw, and how many of how many agents are on
// this page, so the footer can say when some are off it.
func grid(
	agents []Agent, focus int, opts RenderOpts, width, height, top int,
) (lines []string, boxes []frameBox, shown, total int) {
	cols := width / (minCellWidth + columnGap)
	if cols < 1 {
		cols = 1
	}
	if cols > maxColumns {
		cols = maxColumns
	}
	if cols > len(agents) {
		cols = len(agents)
	}
	cellWidth := (width - columnGap*(cols-1)) / cols

	// How many rows of panes fit, and therefore how many agents this page holds.
	// A height of zero means nobody said, which is a test or a pipe, so
	// everything is shown.
	rowsOfPanes := (len(agents) + cols - 1) / cols
	perPane := 0
	if height > 0 {
		if fits := height / minCellHeight; fits >= 1 && fits < rowsOfPanes {
			rowsOfPanes = fits
		}
		perPane = height / rowsOfPanes
	}

	page := len(agents)
	if height > 0 {
		page = rowsOfPanes * cols
	}
	// The page is the one holding the focused agent, so switching to an agent
	// off the bottom brings its page up rather than moving a marker nobody can
	// see.
	start := (focus / page) * page
	end := start + page
	if end > len(agents) {
		end = len(agents)
	}
	visibleAgents := agents[start:end]

	for row := 0; row*cols < len(visibleAgents); row++ {
		var panes [][]string
		for col := 0; col < cols; col++ {
			i := row*cols + col
			if i >= len(visibleAgents) {
				panes = append(panes, blankPane(cellWidth, perPane))
				continue
			}
			index := start + i
			paneTop := top + row*perPane
			paneLeft := 1 + col*(cellWidth+columnGap)
			paneLines, box := pane(
				visibleAgents[i], index, index == focus, opts,
				cellWidth, perPane, paneTop, paneLeft, 1,
			)
			if box != nil {
				boxes = append(boxes, *box)
			}
			panes = append(panes, paneLines)
		}
		lines = append(lines, beside(panes, cellWidth)...)
	}
	return lines, boxes, len(visibleAgents), len(agents)
}

// soloPane gives the focused agent the whole view.
func soloPane(
	a Agent, index int, opts RenderOpts, width, height, top int,
) ([]string, []frameBox) {
	lines, box := pane(a, index, true, opts, width, height, top, 1, 8)
	if box == nil {
		return lines, nil
	}
	return lines, []frameBox{*box}
}

// pane draws one agent.
//
// steps is how many of the agent's recent steps to list at the bottom: one in
// the grid, where the pane's job is the picture and the sentence under it, and
// a handful in solo, where the pane's job is the whole story.
//
// height of zero means unbounded, which a test and a pipe both want: every line
// the agent has to show, and no image, because there is no screen to place one
// on.
func pane(
	a Agent, index int, focused bool, opts RenderOpts,
	width, height, top, left, steps int,
) ([]string, *frameBox) {
	lines := []string{
		paneHeading(a, index, focused, opts, width),
		opts.c(toneDim, meta(a)),
		personalityLine(a, opts),
	}
	stepLines := castLines(a, opts, steps)

	// Whatever is left between the heading block and the steps is the picture,
	// or the text that stands in for it.
	middle := 0
	if height > 0 {
		middle = height - len(lines) - len(stepLines)
		if middle < 0 {
			// Not enough room for every step. The most recent one is the one
			// that matters, so the list is cut from the top.
			stepLines = stepLines[len(stepLines)+middle:]
			middle = 0
		}
	}

	var box *frameBox
	drawable := opts.Images.Protocol != termimg.NoImages && a.Frame != nil && a.Frame.B64 != ""
	// A pane can be too short for a picture even on a terminal that draws them,
	// which is the ordinary case once a dozen agents share a small window. That
	// is a third reason for an empty region and it gets its own sentence, for
	// the same reason the other two do: an unexplained empty region reads as a
	// pane that failed rather than as a pane that ran out of room.
	cramped := drawable && middle < minImageRows
	if drawable && middle >= minImageRows {
		box = &frameBox{
			ID: index + 1, B64: a.Frame.B64,
			Row: top + len(lines), Col: left,
			Cols: width, Rows: middle,
		}
		// Blank rows, so the picture has somewhere to land and nothing under it
		// gets painted over it.
		for i := 0; i < middle; i++ {
			lines = append(lines, "")
		}
	} else {
		// No picture here, so the room a picture would have taken goes to the
		// cast. This is not padding: a terminal surface produces no frames at
		// all, so its steps ARE its content, and leaving that room blank showed
		// seventeen empty lines and one sentence for the surface that needs the
		// space most. A pixel surface on a terminal that cannot draw gets the
		// same treatment, because there the cast is the only thing left.
		note := standIn(a, opts, width, 0, cramped)
		lines = append(lines, note...)
		if spare := middle - len(note); spare > 0 {
			stepLines = castLines(a, opts, len(stepLines)+spare)
		}
	}

	lines = append(lines, stepLines...)
	if height > 0 {
		for len(lines) < height {
			lines = append(lines, "")
		}
		lines = lines[:height]
	}
	return lines, box
}

// paneHeading is the agent's number, what it is, a rule, and its state.
//
// The personality is the heading because it is the thing this view exists to
// make visible: six panes whose headings read the same are one agent drawn six
// times, and six that read differently are a swarm. The workflow moves to the
// line below, where it is still there for somebody who needs it.
//
// The focused pane is marked with a heavier rule as well as with colour, so the
// mark survives NO_COLOR and a recording that lost its colour.
func paneHeading(a Agent, index int, focused bool, opts RenderOpts, width int) string {
	rule := "─"
	marker := "  "
	name := headingName(a)
	if focused {
		rule = "━"
		marker = opts.c(toneNeon, "▸ ")
		name = opts.c(toneBold, name)
	}
	left := fmt.Sprintf("%s%d %s", marker, index+1, name)
	right := stateChip(opts, a)

	fill := width - visible(left) - visible(right) - 2
	if fill < 1 {
		// Too narrow for the rule. The state still wins the space it needs,
		// because a pane that does not say whether its agent is running is a
		// pane that has lost its point.
		return truncVisible(left, width-visible(right)-1) + " " + right
	}
	return left + " " + opts.c(toneDim, strings.Repeat(rule, fill)) + " " + right
}

// headingName is what the pane calls its agent, in the order of how much it
// tells a reader.
func headingName(a Agent) string {
	switch {
	case a.Personality != "":
		return a.Personality
	case a.Persona != "":
		return a.Persona
	case a.Workflow != "":
		return a.Workflow
	}
	return a.ID
}

// meta is the workflow, the account and the surface, under the heading.
func meta(a Agent) string {
	parts := []string{}
	if a.Workflow != "" {
		parts = append(parts, a.Workflow)
	}
	if a.Persona != "" && a.Persona != a.Workflow {
		parts = append(parts, "as "+a.Persona)
	}
	if a.Surface != "" {
		parts = append(parts, a.Surface)
	}
	if len(parts) == 0 {
		return "no workflow named"
	}
	return strings.Join(parts, " · ")
}

// personalityLine is the behavioural profile in a few words.
//
// A run with no diversity block has no personality, and saying so is better
// than leaving the line blank: the blank reads as a pane that failed to load,
// and the sentence reads as a run that was configured this way.
func personalityLine(a Agent, opts RenderOpts) string {
	if a.Traits != "" {
		return opts.c(toneDim, a.Traits)
	}
	if a.Personality != "" {
		return opts.c(toneDim, "no behavioural profile")
	}
	return opts.c(toneDim, "no personality assigned")
}

// standIn is what a pane shows where the picture would be, and it is the
// deliberate fallback rather than an empty region.
//
// Four different things get here and they are not the same fact: an agent that
// has not started, an agent whose surface produces no frames at all, a frame
// that exists but that this terminal cannot draw, and a frame that this
// terminal could draw in a pane with room for one. The last two name the frame
// and its size, because that is the evidence that the run is producing pictures
// and only the display is not showing them.
func standIn(a Agent, opts RenderOpts, width, rows int, cramped bool) []string {
	var out []string
	switch {
	case a.State == "pending":
		out = append(out, opts.c(toneDim, "waiting to start"))
	case a.Frame != nil:
		at := fmt.Sprintf("frame %d · %dx%d", a.Frame.Seq, a.Frame.W, a.Frame.H)
		if a.Frame.At != "" {
			at += " · " + a.Frame.At
		}
		out = append(out, opts.c(toneDim, at))
		switch {
		case cramped:
			out = append(out, opts.c(toneDim, "this pane is too short for a picture"))
		case opts.Images.Protocol == termimg.NoImages:
			out = append(out, opts.c(toneDim, "this terminal draws no pictures"))
		}
	case isPixelSurface(a.Surface):
		out = append(out, opts.c(toneDim, "no frame yet"))
	}
	if last := lastStep(a); last.URL != "" {
		out = append(out, opts.c(toneDim, "at ")+shorten(last.URL, width-4))
	}
	if rows <= 0 {
		return out
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:rows]
}

// placements turns the collected boxes into the escape sequences that draw
// them, appended to the end of the frame.
//
// A box this terminal cannot draw is silently skipped rather than reported:
// every one of them was collected only because the capability said yes, so a
// failure here is a frame that arrived malformed, and one bad frame must not
// take the view down or print an error over somebody's dashboard. The pane
// under it already drew the text that says what the agent is doing.
func placements(opts RenderOpts, boxes []frameBox) string {
	var b strings.Builder
	for _, box := range boxes {
		esc, err := termimg.Place(
			opts.Images, box.ID, box.B64, box.Row, box.Col, box.Cols, box.Rows,
		)
		if err != nil {
			continue
		}
		b.WriteString(esc)
	}
	return b.String()
}

// blankPane is the empty space where a grid row has fewer agents than columns.
func blankPane(width, height int) []string {
	if height <= 0 {
		return nil
	}
	return make([]string, height)
}

// beside joins a row of panes column-wise, padding each to its width.
func beside(panes [][]string, width int) []string {
	tallest := 0
	for _, p := range panes {
		if len(p) > tallest {
			tallest = len(p)
		}
	}
	out := make([]string, 0, tallest)
	for i := 0; i < tallest; i++ {
		var row strings.Builder
		for c, p := range panes {
			if c > 0 {
				row.WriteString(strings.Repeat(" ", columnGap))
			}
			line := ""
			if i < len(p) {
				line = p[i]
			}
			row.WriteString(padVisible(truncVisible(line, width), width))
		}
		out = append(out, strings.TrimRight(row.String(), " "))
	}
	return out
}

// lastStep is the most recent step, or the zero Step when there are none.
func lastStep(a Agent) Step {
	if len(a.Steps) == 0 {
		return Step{}
	}
	return a.Steps[len(a.Steps)-1]
}

// castLines is the agent's last n steps, drawn. Never empty: a pane with no
// line at all where its steps go reads as one that failed to load rather than
// as one whose agent has not done anything yet.
func castLines(a Agent, opts RenderOpts, n int) []string {
	tail := recentSteps(a, n)
	if len(tail) == 0 {
		return []string{opts.c(toneDim, "no steps yet")}
	}
	out := make([]string, 0, len(tail))
	for _, s := range tail {
		out = append(out, opts.c(toneDim, "· ")+s.Text)
	}
	return out
}

// recentSteps is the last n steps in order.
func recentSteps(a Agent, n int) []Step {
	if n <= 0 || len(a.Steps) == 0 {
		return nil
	}
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

// footer is the keys on the left and what the terminal is doing about pictures
// on the right.
//
// The image state is on screen rather than hidden behind a flag, because "there
// is no picture" has several causes and a person looking at a text pane deserves
// to know which one they have: a terminal that draws nothing, a terminal that
// was never asked, or a deliberate switch.
func footer(opts RenderOpts, width, shown, total int) string {
	keys := "1-9 focus   tab next   f full screen   q quit"
	if opts.Solo {
		keys = "1-9 focus   tab next   f back to the swarm   q quit"
	}
	if total > shown && shown > 0 {
		keys += fmt.Sprintf("   showing %d of %d", shown, total)
	}
	left := opts.c(toneDim, keys)

	right := opts.c(toneDim, "images "+opts.Images.Protocol.String())
	if opts.Images.Protocol == termimg.NoImages {
		right = opts.c(toneDim, "no images: "+opts.Images.Why)
	}
	gap := width - visible(left) - visible(right)
	if gap < 2 {
		return truncVisible(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// join assembles the frame, truncating every line to the width so nothing wraps
// and pushes the layout down a row.
func join(lines []string, width int) string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, truncVisible(l, width))
	}
	return strings.Join(out, "\n")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// visible counts the printable cells in a string, ignoring ANSI escapes.
func visible(s string) int {
	n := 0
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		n++
	}
	return n
}

// padVisible pads a string to width printable cells.
func padVisible(s string, width int) string {
	if n := visible(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// truncVisible cuts a line to width printable cells, ignoring ANSI escapes when
// counting so a colored line is not cut short by its own invisible codes. Width
// zero means no truncation.
func truncVisible(s string, width int) string {
	if width <= 0 {
		return s
	}
	count := 0
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
		if count >= width {
			// Keep a reset so color does not bleed past the cut.
			return b.String() + cReset
		}
		b.WriteRune(r)
		count++
	}
	return b.String()
}

// shorten trims a long string to n runes with a trailing mark, for a url or a
// run id that would otherwise blow the line width.
func shorten(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
