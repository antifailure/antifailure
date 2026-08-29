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
//	2  A provider that fails during a branch leaves nothing         engine/conformance/db.go   Claimed by a test that
//	   behind, or the inventory reports it.                         provider docs              proves almost nothing: it
//	                                                                                           cancels BEFORE calling
//	                                                                                           Branch, so no provider
//	                                                                                           ever creates anything and
//	                                                                                           every provider passes.
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
// The fault must be injected at the layer the claim is about, and at the moment
// the claim is about. Scenario 3 is about a crash, so it leaves resources the
// label sweep cannot see rather than ones it can. Scenario 1 is about a
// network, so it takes a real HTTP server away rather than returning an error
// from a fake. Scenario 2 is about an interruption during creation, so it
// cancels a second and a half in rather than before the call: a context that
// was dead on arrival is refused by every client library, which is why the
// conformance behaviour it corresponds to passes for every provider that will
// ever exist while proving nothing about the case anybody worries about.
//
// A retry must be able to retry. This is the third rule and it was learned
// late. refreshGolden below retries a busy daemon three times, and for its
// first night it did not: all three attempts shared one deadline, so the first
// spent the budget and the other two failed instantly on a context that was
// already dead. The loop was three lines of comment describing behaviour that
// could not happen. Worse, the spent deadline then poisoned the assertions
// after it, and the test failed saying the provider could not enumerate
// itself, which is a sentence about the wrong component. Each attempt now has
// its own budget, and a blown budget is reported as a statement about the
// machine rather than about the code under test.
//
// A note on where these run. Each of the Docker-backed tests below stands up a
// real Postgres and a real golden, and on a machine already running a dozen
// containers a golden refresh can take minutes or fail its readiness window
// outright. That is a failure rather than a skip, deliberately: a precondition
// check that machine load can make false is a way for the suite to pass.
package chaos
