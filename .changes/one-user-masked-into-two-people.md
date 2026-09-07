# changed

Masking held one guarantee above all the others: the same customer maps to the
same fake customer in every table and every refresh, so a foreign key still
joins after the data is rewritten. It held that guarantee inside one Postgres,
and it had never been asked to hold it anywhere else.

Every part of the pipeline that touches an engine was Postgres and said so
nowhere. The classifier matched a rule's `type:` against Postgres type names, so
a rule saying `type: text` could not match a ClickHouse `String`. Nothing then
decided what happened to that column, so it was copied unchanged, while the
Postgres column beside it holding the same address was masked. One person, two
answers, and no error anywhere: the Postgres store holds a fake address and the
analytics store holds the real one. A join across the two returns nothing or
returns somebody else, every report built on it is plausible, and the fidelity
report calls the environment faithful.

That is worse than the empty ClickHouse the previous change is about. An empty
store is visible within a minute of opening a chart. A store masked into a
different person is confidently wrong.

So there is now a dialect boundary, and it is deliberately narrow: an engine's
type names, its identifier quoting, its statement parameters, the read that
takes a chunk, and the rewrite that puts one back. Everything else was already
engine independent and stays that way. The transforms are pure functions of a
key that never reaches the database, computed in Go, so what a value masks to
never depended on which store it came out of.

Each engine's type names map onto the POSTGRES names rather than onto a third
vocabulary, so one `masking.yaml` covers every store and a rule anybody has
already written keeps meaning what it meant. A type an engine's dialect does not
recognise comes back unchanged, which lands it in the branch that reports a
column rather than deciding about it. An engine nobody has a dialect for is
refused at planning time rather than treated as Postgres, and a ClickHouse table
with no sorting key is refused for the same reason a half masked table is never
started: ClickHouse has no physical row identifier, so there is no statement
that means one row.

The verification scanner gains the same boundary and keeps its own copy of the
type table, because it must not import the thing it checks. A test requires the
two to agree, which is how the Postgres lists have been kept in step and is now
how the ClickHouse ones are.

**And the guarantee is now checked rather than argued.** `CrossStoreCheck` takes
two stores' plans, finds every identifier that appears in both, masks probe
values through each side, and reports the share that come out identical. A
column masked in one store and copied in the other is a leak; two columns masked
with different transforms, or under different links, are one identity becoming
two people. Anything below 100 percent is a bug, there is deliberately no way to
mark a pair exempt, and a report that compared nothing is not a pass.
