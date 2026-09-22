# fixed

The run page cut off how to reproduce a failure. The verdicts table carried
the reproduction as a seventh column and needed 860px to hold it, so on any
laptop window from 640 to 1279 wide the table scrolled sideways inside its card
and the Reproduction column was cut off at the card's edge. On a wider window
it fitted and was a narrow box scrolling a long sentence, so the reproduction
was cut off inside its own box instead. The steps also printed as a JSON array,
in quotes between brackets.

The reproduction now sits on its own full width line under the verdict it
belongs to, and wraps, so every step is on screen at every width from 360 to
1600. The steps print as lines, one step per line. A passing workflow, which has
nothing to reproduce, no longer carries an empty line saying so.
