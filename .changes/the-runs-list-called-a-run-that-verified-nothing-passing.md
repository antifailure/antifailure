# fixed

The runs list called a run that verified nothing passing.

A verdict is a judgement only when it is conclusive. Pass, fail, flaky and warn
are; blocked and unverified are not, because the work either never reached the
application or finished with nothing to evaluate. The list coloured a row green
and wrote "N passing" whenever no verdict was failing, and a run whose verdicts
are all blocked or unverified has nothing failing. So a run that proved nothing
drew as a pass, in green, on the surface a customer scans first, while the
detail view for the same run already said nothing was verified. The two
disagreed about the same run.

The list now reads the counts it needs to be exactly as honest as the detail:
the passes, the fails alone rather than fails and blocks together, and the
number of conclusive verdicts. A run that proved nothing reads "nothing
verified" in amber, a run that passed some but not all of what it proved reads
"N of M passed", and only a run that is genuinely all passes reads green. The
same correction reaches the operator surfaces, where a complete run that proved
nothing was standing as passed and a run of unverified verdicts counted every
one of them as passed.
