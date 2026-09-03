# added

The operator portal can export its audit chain, and say what it holds about a
named person.

`admin.audit.export` had been a declared permission, held by the owner and
security roles, with no route behind it. A permission that guards nothing looks
like a capability from every angle except the one that counts. It is a route
now: a file, in JSON or CSV, carrying each entry's previous and current hash so
somebody who does not trust the vendor can check it, and recording its own
departure in the chain it came from.

The verifier it calls had the same shape in the other direction. It was written,
tested, and called by nothing outside a test, so the chain's tamper evidence was
a property of the code rather than of the product. Verification now runs over
the range a file actually covers rather than the whole chain, because a slice
shipped with a verification of something larger verifies a different document
from the one in the reader's hands.

Data Governance answers what is held about one person, and refuses to answer the
parts this product cannot. The locations are read from the database catalog when
the question is asked, so a table added next month is in the answer, and each one
carries its foreign key's own on-delete behaviour, because that is the erasure
answer rather than a description of one. What is not built is named instead of
drawn: there is no per person erasure, organization erasure leaves the people in
it behind, both audit chains keep the actor's name on purpose because the name is
hashed into the entry, and there is no retention policy table so nothing expires
on a schedule.

Security Center is every standing credential on the installation as one list
rather than three, with how each organization signs in and who holds an operator
account beside it.
