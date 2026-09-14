# fixed

The Aurora provider could not refresh a golden against real AWS. Its first live
run cloned the source and brought up a writer. It then connected with the
rotated master password one second after asking for it. Aurora accepts that
change before applying it, and the cluster reads available the whole time, so
Postgres refused the login and nothing was published. The provider now waits
until the new password actually logs in.

Teardown was broken as well. Five of the fault codes the provider recognised
carry a Fault suffix that RDS does not send, including the one AWS answers when
an instance is already being deleted. So cleaning up a writer that was already
going away failed instead of succeeding, and a missing instance was reported as
an error instead of as gone. A failed refresh also gave its cleanup five
minutes, and AWS took twelve. The codes now match what RDS sends, and the
cleanup waits as long as an instance is allowed to take to come up.
