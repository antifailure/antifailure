# fixed

Two render gates that claimed to cover the console and never read it.

`motioncheck` finds its applications on disk, and it used to include one only
when something was found there. In CI the console was installed and built after
both render gates ran, so an unbuilt directory was dropped from the run without
a word and the check reported success over the marketing site alone. Measured
on a built tree: 42 files read with the console hidden, 64 with it present,
exit zero in both cases. The animation ban is the rule this project reaches for
most often, and for the whole life of the gate it was enforced on one of the
two applications.

`classcheck` never claimed the console, and why it did not is the more useful
half. It exists because `cn` is a plain join rather than tailwind-merge, and
the console does not import `cn` anywhere, so it read as exempt. It is not. It
composes classes in template literals, and an interpolated string that already
carries a margin beside a margin written after it is the same defect with the
same cause: the cascade decides, not the order of the literal.

Both gates now read every Next application, find those applications by looking
for the config file that makes one rather than by holding a list, walk for
prerendered HTML at any depth rather than at three fixed ones, and refuse an
application that is not built instead of skipping it. The console build moved
above them in CI, which that refusal makes load bearing rather than incidental.

`classcheck` also judges more than colour now. It began with five colour
properties because the four defects that motivated it were colours, which was
narrower than the rule it enforces: a height written last and beaten by a
height written first is the same defect and just as invisible in review. The
widening was measured before it was made rather than after. Across both
applications, 61 files and 16661 elements, twenty one properties add exactly
one finding.

That finding is real and is fixed here. The baseline card in the hero film
asked for 58 percent of the height and rendered at full height, because the
component set `h-full` in its own class string and the call site passed
`h-[58%]` beside it. Every arbitrary value is emitted before every named
utility, so the component won and the frame it was drawing was wrong.
