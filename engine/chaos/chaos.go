// Package chaos proves the failure paths the product already claims.
//
// Everything in here is a claim made somewhere else in the repository, in
// prose, as though it were settled: an error message's next_step, a package
// doc, a line in STATUS.md. A claim in prose is a claim nobody has run, and the
// four in this suite were between them one third true.
//
// The scenarios, and what each was found to be:
//
//	#  Claim                                                       Where it is claimed        Found
//	-- ----------------------------------------------------------- -------------------------- -------------------------
//	1  The control plane can go away mid run; events are buffered   AF-CPL-003's next_step     False, then fixed.
//	   and sent when it returns.                                    STATUS.md                  Nothing had ever attached
//	                                                                                           the sink, and the buffer
//	                                                                                           did not outlive a process.
//	2  A provider that fails during a branch leaves nothing         engine/conformance/db.go   True. The conformance
//	   behind, or the inventory reports it.                         provider docs              suite already proves it,
//	                                                                                           and this suite proves the
//	                                                                                           suite can fail.
//	3  A killed engine reconciles through the journal.              internal/journal doc       False, then fixed.
//	                                                                STATUS.md                  Replay, NewRegistry and
//	                                                                                           Commit had zero callers.
//	4  A teardown interrupted by an unreachable provider reports    AF-RUN-030's next_step     True in part. The message
//	   AF-RUN-030, and a second run finishes it.                                               was right; what finished
//	                                                                                           the job was the label
//	                                                                                           sweep, not the journal it
//	                                                                                           names.
//
// The suite is separate from engine/conformance on purpose. Conformance asks
// whether an implementation meets a contract, and every provider runs it. Chaos
// asks whether a sentence somebody wrote is true, which is a question about the
// whole assembled system and is asked once.
//
// Two rules hold for every test here, and they are what stop this becoming
// theatre.
//
// A chaos test must be able to fail. Each one names the negative control that
// was run against it: the change that makes it go red. A test asserting that a
// resource is gone passes trivially on a machine where the resource was never
// created, and a test asserting that events arrived passes trivially against a
// server that accepts anything. Both were real drafts of tests in this file.
//
// The fault must be injected at the layer the claim is about. Scenario 3 is
// about a crash, so it kills the process's work rather than calling a cleanup
// function and pretending. Scenario 1 is about a network, so it takes a real
// HTTP server away rather than returning an error from a fake.
package chaos
