# added

A run could only be read after it was over, and the video it recorded was
surfaced nowhere.

The runner is one job in and one document out, which is the right contract for
a verdict and the wrong one for watching: by the time the document is written
the run has finished, so there was no way to see an agent drive the application
as it happened. The browser recorded a video of every session and it reached
no screen at all.

There is now a live channel. The runner streams agent state, steps, and frames
over a local socket while the run is still going. A new command, af watch, runs
the workflows and draws them in the terminal, one pane per agent, switchable
with the number keys, the arrows, or tab, with honest states rather than a
spinner. The control-plane console gains the same multi-agent watch view,
streaming from the runner edge. A run targets a surface: web and terminal are
driven for real, and desktop and iOS are declared and refused loudly rather
than reported as a green run that tested nothing.

The frames stay on the machine that produced them. They reach the terminal and
the console straight from the runner edge and are never written to the control
plane, which holds counts, verdicts, and a reference to the durable recording,
never a body. A test fails if a frame's bytes ever reach the control-plane
shape.
