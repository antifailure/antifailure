# fixed

The control plane read the wrong shape for a load result and stored a run that
sent twelve hundred requests as one that sent none.

The decoder read `sent`, `rate` and a nested `overall` object, which are the
names on the engine's internal Go type. The document that actually crosses the
wire is the projected result the engine writes, and it spells them `requests`,
`achieved_rate` and flat percentiles. Both spellings are accepted now.

It failed silently, which is why a green suite on each side of the seam missed
it: the request count falls back to zero because a load result must carry one,
so the count read as zero, every percentile read as absent, the decoder's own
"some of this could not be read" note never fired, and every per route
measurement decoded perfectly. A console would have drawn per route latency over
a run that appeared to have sent nothing.

Found by dumping the bytes the engine puts on the wire and running the decoder
over them, which is the only thing that could have found it. Neither suite
crossed the seam.
