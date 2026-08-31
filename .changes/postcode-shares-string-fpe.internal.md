# changed

`postcode` and `string_fpe` were twenty identical lines each, differing only in
the order of two mutually exclusive switch cases. Running both proved they
return the same bytes for every input. `postcode` calls the one implementation
now, so improving one of them cannot silently leave the other behind. Both names
and both descriptions are unchanged, and so is the output.
