# fixed

`Caps.CopyOnWrite` says a branch shares storage with its golden, and therefore
that branch time does not grow with the database. Five providers declared a
value for it. Nothing in the repository could tell whether any of them was
telling the truth.

The database conformance suite never read the field at all. Its
`Capabilities_AreSelfConsistent` behaviour reads `Branching`,
`SupportedVersions`, `ExpectedBranchLatency`, `MaxConcurrentBranches` and
`Name`, and stops. The datastore suite beside it reads the same field twice,
and both reads are about self consistency: copy on write without branching,
copy on write without a golden. Neither asks whether the claim is true. So a
provider could declare copy on write alongside branching and a golden, copy
every byte on every branch, and pass both suites, which is exactly the defect
this repository keeps finding in its own instruments.

It is not a small field. Copy on write is the distinguishing commercial claim
of the whole cloud database wave, the sentence the product is sold on is a
sentence about seconds, and the number a buyer is given comes from it.

`CopyOnWrite_BranchTimeMatchesTheDeclaration` measures it, in the units the
claim is made in. Two goldens are built, one with a gibibyte of ballast the
suite writes through the mask callback every provider already calls, and both
are branched several times alternately, taking the fastest of each. A provider
declaring copy on write must not have grown; a provider declaring it false
must have. Those two requirements are complementary, so one of the two possible
declarations is refused on every run whatever the stopwatch says, and there is
no reading under which both pass.

The false side is enforced as hard as the true side. A provider that understates
its own branching is wrong in the same published table as one that invents it,
and a check that only fires in one direction cannot tell a correct declaration
from an unexamined one, because both look like the absence of a complaint.

`Caps.ExpectedBranchLatency` had the same shape of hole and it is closed in the
same commit. Its documentation has always said "the suite asserts against it, so
a provider that gets slower fails rather than quietly degrading", and what the
suite asserted was that the number is greater than zero. A provider could
declare eight seconds, take three minutes, and stay green.
`Branch_IsWithinTheDeclaredLatency` makes the sentence true.

Both behaviours ship with the broken fakes that prove they can say no, in the
same commit, on both sides of each boundary.
