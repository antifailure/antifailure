# fixed

`af chaos` now says what a crash fault established about torn pages. After the
crash, the durability proof reads the writers' table back in full by sequential
scan, where a page that fails its data checksum stops the read, and then asks
amcheck to verify that table's primary key index against every row. Both facts
were already in the JSON report, and the terminal printed neither: it printed a
warning when checksums were off and nothing at all when they were on, so a
check that ran and passed looked exactly like one that never ran.

Each crash fault's block now carries an `amcheck` line, in amcheck's own words
or the reason it did not run, and a `pages` line. That line claims a pass only
when the control file was read after the fault, data checksums are on and the
read back finished, and it says the claim covers the writers' table and no
other. Otherwise it says the pages were not checked and why: checksums off, the
control file unread, or the read back unfinished.

The `chaos.integrity.checksums_off` warning also stopped firing on a control
file that could not be parsed. It read the zero checksum version of an unread
file as "off", beside the finding saying the file could not be read.
