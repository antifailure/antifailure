// The enterprise half of runtime placement, which is one declaration and a
// paragraph explaining why it is only one.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Everything placement does lives in the engine, in
// engine/internal/env.(*Orchestrator).placement, and it is gated there through
// edition.Permits rather than through feature.Enabled. That looks like the
// wrong side of the boundary until you ask what would have to move for it to be
// on this one: the manifest's targets, the scheduler, the runtime constructors
// and every command that has to agree on where an environment went. A second
// copy of all of that in the enterprise module would be a second answer to
// where an environment is, and the failure newRuntime's comment is written
// against is exactly two answers to that question.
//
// So the licence crosses the boundary instead of the code. main attaches what
// this installation is permitted, as strings, and the engine reads them. The
// community binary attaches nothing and permits nothing, which is the direction
// the mistake has to fail in.
//
// This file is what stops that arrangement from being invisible. A feature
// enforced somewhere the enterprise module never mentions is a feature nobody
// auditing the enterprise module can find, and an audit that cannot find an
// enforcement site reports it as absent. See ee/engine/feature.
package main

import (
	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
)

func init() {
	feature.Declare(license.FeatureMultiRuntime,
		"engine/internal/env.(*Orchestrator).placement, gated through edition.Permits")
}
