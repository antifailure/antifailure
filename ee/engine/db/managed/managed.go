// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

// Package managed registers the managed cloud database providers this edition
// adds, and it is the ONE list of them.
//
// It exists because the list used to be three statements inside main(), and a
// list inside main() can be read by nothing but the binary. So the air gapped
// hook's refusal test had to name providers by hand, and it named aurora and
// cloudsql and not azurepg: azurepg's refusal in a sealed installation was held
// by the shape of an allow list and by no test that would notice it moving.
// main.go calls Register above cloudgate.Wrap, and that test calls it on a
// fresh registry, so a provider added here is refused under seal by a test
// nobody has to remember to edit, and one registered anywhere else is not
// wrapped by the licence gate either, which TestTheCloudGateWrapsLast reports.
package managed

import (
	"github.com/antifailure/antifailure/ee/engine/db/aurora"
	"github.com/antifailure/antifailure/ee/engine/db/azurepg"
	"github.com/antifailure/antifailure/ee/engine/db/cloudsql"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// Register adds every managed cloud database provider to r.
//
// Unconditional rather than gated on the licence. Selecting one is a manifest
// saying database.provider is aurora, and a build whose licence lapsed should
// refuse at the point of use with a sentence about the licence rather than
// disappear from the list of providers this build has.
//
// aurora clones an Amazon Aurora PostgreSQL cluster. cloudsql and azurepg are
// the other two clouds' managed Postgres, and they are not equivalent to aurora:
// cloudsql declares CopyOnWrite because a Cloud SQL fast clone is created from
// an Instant Snapshot, and azurepg declares it FALSE, because a point in time
// restore creates an independent server and replays logs after the snapshot.
func Register(r *extension.Registry) {
	aurora.Register(r)
	cloudsql.Register(r)
	azurepg.Register(r)
}
