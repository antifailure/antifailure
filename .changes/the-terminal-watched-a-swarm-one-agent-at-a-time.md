# fixed

`af watch` carried a picture of every agent and drew none of them.

The live channel has streamed a base64 JPEG per agent since it was built, once a
second, straight from the browser to the terminal. The terminal view threw all of
it away. Its own comment said why, and the sentence it printed said it to the
reader too: "The image streams to the browser watch view; here is its cast."
Underneath that it showed one agent at a time behind a switcher rail, so the
other agents in the run were a list of names and a state word. A run of six
agents with six different personalities looked, in the terminal, like one agent
with five things queued behind it.

The view is now the swarm. Every agent is on screen at once, in a grid that
reflows from four columns to one and pages rather than squashing when there are
more agents than rows. Each pane is headed by the PERSONALITY driving that agent,
because that is the thing that tells four panes running one workflow apart, with
the workflow, the account and the surface under it and the behavioural profile in
a few words under that. The personality reaches the pane because it now travels
on the wire as its own field; it used to be parenthesised into the workflow name,
which a layout cannot lay out. Below that is the agent's most recent frame, drawn
as a real picture.

The picture needs a terminal that draws inline images, and the terminal is asked
rather than guessed at. TERM is set by a shell, survives ssh onto a machine with
a different terminal on the other end, and is rewritten by every multiplexer, so
it is not evidence. One round trip at startup asks kitty its own graphics
question, asks for the cell size in pixels, and asks for the device attributes,
and the answers decide between the iTerm2 protocol, the kitty protocol, sixel and
nothing. A terminal that draws nothing gets the same panes with the frame's own
number and size in place of the picture, which is the evidence that the run is
producing pictures and only the display is not showing them.

Three reasons for an empty region are three different facts and each has its own
sentence: this terminal draws no pictures, this pane is too short for one, and
pictures were switched off. The footer says which one, always, including when
there IS a picture. A terminal that speaks sixel and will not report its cell
size is treated as one that draws nothing, because a sixel is sized in pixels and
a guessed size scrolls every pane under it.

Nothing pulses. A live agent says LIVE in a static neon word and the picture
under it changes once a second, which is the run's own motion rather than
decoration.

One bug found on the way and fixed here: the capability query could never have
worked on macOS. `os.File.SetReadDeadline` refuses a terminal there with "file
type does not support deadline", so the read failed before a byte arrived and
every macOS terminal, including the ones that draw pictures, came back
undetectable. It was caught because a capability carries the reason it reached
its verdict, and the reason said so out loud.

The colours are measured now, and there are two sets of them. An ANSI colour is
absolute and a terminal's background is not, so the same round trip asks the
terminal what colour it is painted and the view takes the palette that holds
against it. That question was worth asking: against the warm paper the launch
film's terminal uses, the neon the live mark is drawn in reads 1.78 to 1, which
is a word nobody can see, and against a dark terminal the same colour is 9.18 to
1, which is why it went unnoticed. No colour in the 256 colour cube clears the
threshold on both.

Measuring that also found two colours failing on the DARK terminal they were
already chosen for: the pass green at 3.97 to 1 and the fail red at 3.33. Both
are now brighter approximations of the same console tokens, and a test computes
the ratio for every colour against its own background rather than trusting the
comment beside it. A terminal that will not say what colour it is keeps the dark
palette, and nothing is lost either way: every signal these colours carry is
also carried by a word and by the weight of a rule.

A pane with no picture in it gives that room to the agent's steps rather than
leaving it blank. A terminal surface produces no frames at all, so its steps are
its whole content, and a pane that kept the space empty showed seventeen blank
lines and one sentence for the surface that needed the space most.

One more defect, found by running the finished thing against a real
environment rather than by reading it. `af watch` had TWO writers on one
terminal: building the orchestrator makes a status line that rewrites itself to
the same stream once a second, and the dashboard then takes the alternate
screen on that writer. Six "elapsed, on this step" lines were painted straight
over the panes in a six second run. The lifecycle already had the seam for
this, carrying a comment naming this exact case, and `af up --live` has passed
it since the dashboard was built; af watch never did. It does now, when there
is a screen to protect and not when the output is a pipe. The verdict still
prints, because it goes out after the program has given the terminal back.

The capability query is split by platform because the type system requires it:
the syscalls it uses take a different type on Windows, so one file cannot serve
both and the package does not compile there if it tries. Windows takes the
deadline path, which is shared rather than written twice, so the arm only it
runs is exercised by every test run on every machine.
