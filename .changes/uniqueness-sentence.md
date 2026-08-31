# fixed

The transform reference page said `int_fpe` and `string_fpe` preserve
uniqueness and left out `preserve`, contradicting the generated table two lines
above it. That sentence sits under the AF-MSK-007 example, so a reader whose
golden refresh had just failed on a unique index was being told to choose one of
the two transforms that would fail it again. The sentence is generated from the
registry now.
