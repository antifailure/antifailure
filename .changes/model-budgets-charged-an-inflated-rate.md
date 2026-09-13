# fixed

A hosted model budget was charged at a rate the provider no longer charges, so
it ran out early.

The control plane's built-in price list charged `claude-sonnet-5` at 3 and 15
US dollars per million input and output tokens and `claude-opus-5` at 15 and 75.
Anthropic publishes 2 and 10 for Sonnet 5, with the rise to 3 and 15 planned for
September 1, 2026 cancelled, and 5 and 25 for Opus 5. A customer spending
through a brokered key was charged 50 percent too much on Sonnet 5 and three
times too much on Opus 5, so a monthly cap was reached at two thirds or one third
of what they had set.

Both are corrected. Every built-in price now names the provider page it was
read from and the day, and a test refuses a price with no source. Spend already
recorded this month is a running total with no per-call token counts behind it,
so it is not recalculated.
