# fixed

`af version` reported the wrong edition in the enterprise binary. It printed a
package variable that no build has ever stamped, so it said `community edition`
while `af license status`, in that same binary and from the same startup, said
`enterprise`. The command an auditor runs to record what they are running was
the one that was wrong, and both were green in every test because a test of the
community command tree correctly attaches nothing.

The edition is not a build time string. It is what the running binary declares
about itself, which the enterprise entry point has always done and which
`af license status` has always read. There is one reader for it now and both
commands ask it.

`tools/ldcheck` could not have caught this, because it validated the linker
flags it was handed and a variable nobody stamps is not in that list. It now
also refuses a string variable declared in the same `var` group as a stamped
one and left unstamped. A variable that is not a release stamp belongs in its
own declaration, which the failure message says.
