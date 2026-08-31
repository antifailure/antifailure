# changed

The console was built one page at a time and the seams showed. Six full-window
screens carried four heading sizes and four different primary buttons between
them; four radii were in use where there is a scale of three; selects were
styled three ways at two heights. That is one vocabulary now, held in
`console/components/ui.tsx` and `console/app/globals.css` rather than repeated
per page.

The tertiary grey the console used for column headings, field hints, card
descriptions and placeholder text was 3.2:1 on the page background, so a good
deal of its prose sat under the readable threshold. The three greys are now
measured against every surface they appear on and the lightest of them is
4.6:1.

Every list was a table that scrolled sideways on a phone, which hid the two
columns a reader came for: the environments list showed an id, a repository and
half a branch, and put the state and the age behind a horizontal scroll. Below
`sm` those tables stack into one record per row with each field beside its
column heading, so nothing is off screen. Controls are 44px with 16px text at
that width, which also stops iOS zooming the page when a field takes focus.

The rows in the environments and runs lists opened on click and were invisible
to a keyboard: not focusable, Enter did nothing. Their first cell is a real link
now. Two screens loaded behind `animate-pulse`; both use a static placeholder
shaped like the content instead. An environment reported as `provisioning` was
toned the same neutral as one torn down, and is now marked as in progress. The
provider keys screen showed a refusal on the bare page background, which read as
a page that had half rendered, and now frames it like every other answer.
